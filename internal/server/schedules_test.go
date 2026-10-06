package server_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// fakeClock is the server's clock, moved forward by tests so schedules
// come due without waiting.
type fakeClock struct{ off atomic.Int64 }

func (c *fakeClock) Now() time.Time          { return time.Now().Add(time.Duration(c.off.Load())) }
func (c *fakeClock) Advance(d time.Duration) { c.off.Add(int64(d)) }

// startSchedServer starts a server on st with a fast scheduler running.
func startSchedServer(t *testing.T, st *store.Store, clock *fakeClock) string {
	t.Helper()
	key, _ := keyringKey()
	srv, err := server.New(server.Config{
		Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: clock.Now, SchedulerInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.StartScheduler(context.Background())
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		hs.Close()
	})
	return hs.URL
}

func okTarget(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	t.Cleanup(ts.Close)
	return ts
}

// schedFixture is a project with one target and one scenario.
type schedFixture struct {
	pid, tid, sid string
}

func newSchedFixture(t *testing.T, c *client, targetURL, duration string) schedFixture {
	t.Helper()
	var proj, tgt, sc map[string]any
	if code := c.do("POST", "/projects", map[string]string{"name": "Sched"}, &proj); code != 201 {
		t.Fatalf("project: %d", code)
	}
	pid := proj["id"].(string)
	if code := c.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "local", "baseURL": targetURL}, &tgt); code != 201 {
		t.Fatalf("target: %d", code)
	}
	yaml := "metadata: {name: tick}\njourneys:\n  - name: ping\n    steps:\n      - get: /\nload: {mode: rate, rate: 5/s, duration: " + duration + "}\n"
	if code := c.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": yaml}, &sc); code != 201 {
		t.Fatalf("scenario: %d", code)
	}
	return schedFixture{pid: pid, tid: tgt["id"].(string), sid: sc["id"].(string)}
}

// member creates a user with a role and returns a signed-in client.
func member(t *testing.T, owner *client, base, email, role string) *client {
	t.Helper()
	if code := owner.do("POST", "/users", map[string]string{"email": email, "name": role, "role": role, "password": "a long password 1"}, nil); code != 201 {
		t.Fatalf("create %s: %d", role, code)
	}
	c := newClient(t, base)
	if code := c.do("POST", "/auth/login", map[string]string{"email": email, "password": "a long password 1"}, nil); code != 200 {
		t.Fatalf("%s login: %d", role, code)
	}
	return c
}

func eventually(t *testing.T, timeout time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestSchedulesAPI(t *testing.T) {
	base := startServer(t)
	owner := newClient(t, base)
	setup(t, owner)
	f := newSchedFixture(t, owner, okTarget(t).URL, "1s")
	viewer := member(t, owner, base, "viewer@acme.test", "viewer")
	runner := member(t, owner, base, "runner@acme.test", "runner")
	editor := member(t, owner, base, "editor@acme.test", "editor")

	body := map[string]any{"name": "nightly", "scenarioId": f.sid, "targetId": f.tid, "cron": "0 2 * * *", "timezone": "Europe/London",
		"overrides": map[string]any{"duration": "2s"}, "env": map[string]string{"STAGE": "ci"}, "note": "regression"}
	path := "/projects/" + f.pid + "/schedules"

	// Bad input is refused before anything is saved.
	var e errBody
	for _, bad := range []map[string]any{
		{"cron": "61 * * * *"},
		{"cron": "0 0 30 2 *"},
		{"timezone": "Mars/Base"},
		{"name": "  "},
		{"overrides": map[string]any{"duration": "banana"}},
		{"targetId": f.sid},
	} {
		b := map[string]any{}
		for k, v := range body {
			b[k] = v
		}
		for k, v := range bad {
			b[k] = v
		}
		if code := editor.do("POST", path, b, &e); code != 422 {
			t.Errorf("%v: %d %+v", bad, code, e)
		}
	}

	// Only editors and above create schedules.
	for _, c := range []*client{viewer, runner} {
		if code := c.do("POST", path, body, nil); code != 403 {
			t.Errorf("create by lower role: %d", code)
		}
	}
	var sch map[string]any
	if code := editor.do("POST", path, body, &sch); code != 201 {
		t.Fatalf("create: %d", code)
	}
	id := sch["id"].(string)
	if sch["ownerEmail"] != "editor@acme.test" || sch["enabled"] != true || sch["timezone"] != "Europe/London" || sch["nextRunAt"] == nil {
		t.Errorf("created: %v", sch)
	}
	next, _ := time.Parse(time.RFC3339, sch["nextRunAt"].(string))
	london, _ := time.LoadLocation("Europe/London")
	if h, m, _ := next.In(london).Clock(); h != 2 || m != 0 || time.Until(next) > 25*time.Hour {
		t.Errorf("next run %s is not the next 02:00 in London", next)
	}
	if code := editor.do("POST", path, body, &e); code != 409 {
		t.Errorf("duplicate name: %d", code)
	}

	// Everyone can read.
	var list []map[string]any
	if code := viewer.do("GET", path, nil, &list); code != 200 || len(list) != 1 || list[0]["scenarioName"] != "tick" {
		t.Errorf("viewer list: %d %v", code, list)
	}
	if code := viewer.do("GET", "/schedules/"+id, nil, &sch); code != 200 || sch["env"].(map[string]any)["STAGE"] != "ci" {
		t.Errorf("viewer get: %d %v", code, sch)
	}
	for _, req := range []struct{ method, path string }{
		{"PATCH", "/schedules/" + id}, {"DELETE", "/schedules/" + id}, {"POST", "/schedules/" + id + "/run"},
	} {
		if code := viewer.do(req.method, req.path, map[string]any{"enabled": false}, nil); code != 403 {
			t.Errorf("viewer %s %s: %d", req.method, req.path, code)
		}
	}
	if code := runner.do("PATCH", "/schedules/"+id, map[string]any{"enabled": false}, nil); code != 403 {
		t.Errorf("runner patch: %d", code)
	}

	// Disabling clears the next run; enabling sets it again. Saving makes
	// the caller the owner.
	var off map[string]any
	if code := owner.do("PATCH", "/schedules/"+id, map[string]any{"enabled": false}, &off); code != 200 || off["nextRunAt"] != nil || off["enabled"] != false {
		t.Errorf("disable: %d %v", code, off)
	}
	if off["ownerEmail"] != "owner@acme.test" {
		t.Errorf("owner after save: %v", off["ownerEmail"])
	}
	sch = nil
	if code := editor.do("PATCH", "/schedules/"+id, map[string]any{"enabled": true, "cron": "@hourly"}, &sch); code != 200 || sch["nextRunAt"] == nil || sch["cron"] != "@hourly" {
		t.Errorf("enable: %d %v", code, sch)
	}
	if code := editor.do("PATCH", "/schedules/"+id, map[string]any{"cron": "nope"}, &e); code != 422 {
		t.Errorf("bad edit: %d", code)
	}

	// A runner can start it by hand, once at a time; the schedule's next
	// firing is unchanged.
	var run map[string]any
	if code := runner.do("POST", "/schedules/"+id+"/run", nil, &run); code != 201 || run["note"] != "scheduled: nightly" {
		t.Fatalf("run now: %d %v", code, run)
	}
	if code := runner.do("POST", "/schedules/"+id+"/run", nil, &e); code != 409 {
		t.Errorf("second run while active: %d", code)
	}
	var after map[string]any
	editor.do("GET", "/schedules/"+id, nil, &after)
	if after["lastRunId"] != run["id"] || after["nextRunAt"] != sch["nextRunAt"] || after["lastRunStatus"] == nil {
		t.Errorf("after run now: %v", after)
	}
	eventually(t, 20*time.Second, "the run to finish", func() bool {
		var r map[string]any
		owner.do("GET", "/runs/"+run["id"].(string), nil, &r)
		return r["status"] == "completed"
	})
	editor.do("GET", "/schedules/"+id, nil, &after)
	if after["lastRunStatus"] != "completed" || after["lastRunVerdict"] != "no-targets" {
		t.Errorf("last run: %v %v", after["lastRunStatus"], after["lastRunVerdict"])
	}

	// Previews.
	var pv map[string]any
	q := "/schedules/preview?cron=" + url.QueryEscape("0 9 * * *") + "&timezone=Asia/Kolkata&count=4"
	if code := viewer.do("GET", q, nil, &pv); code != 200 || pv["timezone"] != "Asia/Kolkata" || len(pv["next"].([]any)) != 4 {
		t.Fatalf("preview: %d %v", code, pv)
	}
	first := pv["next"].([]any)[0].(string)
	if !strings.HasSuffix(first, "T09:00:00+05:30") {
		t.Errorf("preview time %s", first)
	}
	if code := viewer.do("GET", "/schedules/preview?cron=bad", nil, &e); code != 422 || !strings.Contains(e.Error.Message, "cron") {
		t.Errorf("bad preview: %d %+v", code, e)
	}

	var audit []map[string]any
	owner.do("GET", "/audit?limit=100", nil, &audit)
	seen := map[string]bool{}
	for _, a := range audit {
		seen[a["action"].(string)] = true
	}
	for _, want := range []string{"schedule.create", "schedule.disable", "schedule.update", "run.start"} {
		if !seen[want] {
			t.Errorf("audit lacks %s", want)
		}
	}

	if code := editor.do("DELETE", "/schedules/"+id, nil, nil); code != 204 {
		t.Errorf("delete: %d", code)
	}
	if code := viewer.do("GET", "/schedules/"+id, nil, nil); code != 404 {
		t.Errorf("deleted schedule: %d", code)
	}
}

func countRuns(t *testing.T, c *client, pid string) []map[string]any {
	t.Helper()
	var runs []map[string]any
	if code := c.do("GET", "/projects/"+pid+"/runs", nil, &runs); code != 200 {
		t.Fatalf("runs: %d", code)
	}
	return runs
}

// TestSchedulerFiresOnce runs two servers on one database, as an active
// replica and one racing it would, both ticking every 20ms. A schedule
// that missed ten firings starts one run; a firing while that run is
// active is skipped; the next one after it finishes starts again.
func TestSchedulerFiresOnce(t *testing.T) {
	dbURL := storetest.URL(t)
	ctx := context.Background()
	open := func() *store.Store {
		st, err := store.Open(ctx, dbURL)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(st.Close)
		return st
	}
	a := open()
	if err := a.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), false); err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{}
	baseA := startSchedServer(t, a, clock)
	startSchedServer(t, open(), clock)

	c := newClient(t, baseA)
	setup(t, c)
	f := newSchedFixture(t, c, okTarget(t).URL, "4s")
	var sch map[string]any
	if code := c.do("POST", "/projects/"+f.pid+"/schedules", map[string]any{"name": "minutely", "scenarioId": f.sid, "targetId": f.tid, "cron": "* * * * *"}, &sch); code != 201 {
		t.Fatalf("create: %d", code)
	}
	id := sch["id"].(string)
	time.Sleep(150 * time.Millisecond)
	if n := len(countRuns(t, c, f.pid)); n != 0 {
		t.Fatalf("fired before it was due: %d runs", n)
	}

	clock.Advance(10 * time.Minute)
	eventually(t, 5*time.Second, "the first scheduled run", func() bool { return len(countRuns(t, c, f.pid)) > 0 })
	time.Sleep(200 * time.Millisecond) // both schedulers tick several more times
	runs := countRuns(t, c, f.pid)
	if len(runs) != 1 {
		t.Fatalf("missed firings started %d runs, want 1", len(runs))
	}
	if runs[0]["note"] != "scheduled: minutely" {
		t.Errorf("note %v", runs[0]["note"])
	}
	c.do("GET", "/schedules/"+id, nil, &sch)
	if sch["lastRunId"] != runs[0]["id"] || sch["lastSkipReason"] != "" || sch["lastFiredAt"] == nil {
		t.Errorf("after firing: %v", sch)
	}
	next, _ := time.Parse(time.RFC3339, sch["nextRunAt"].(string))
	if d := next.Sub(clock.Now()); d <= 0 || d > time.Minute {
		t.Errorf("next run %s is not within the minute after now", next)
	}

	// Due again while the run is still going: skipped, with the reason.
	clock.Advance(time.Minute)
	eventually(t, 3*time.Second, "the skip", func() bool {
		c.do("GET", "/schedules/"+id, nil, &sch)
		return strings.Contains(sch["lastSkipReason"].(string), "still active")
	})
	if n := len(countRuns(t, c, f.pid)); n != 1 {
		t.Fatalf("fired while the previous run was active: %d runs", n)
	}

	eventually(t, 20*time.Second, "the first run to finish", func() bool {
		var r map[string]any
		c.do("GET", "/runs/"+runs[0]["id"].(string), nil, &r)
		return r["status"] == "completed"
	})
	clock.Advance(time.Minute)
	eventually(t, 5*time.Second, "the second scheduled run", func() bool { return len(countRuns(t, c, f.pid)) == 2 })
	c.do("GET", "/schedules/"+id, nil, &sch)
	if sch["lastSkipReason"] != "" {
		t.Errorf("skip reason not cleared: %v", sch["lastSkipReason"])
	}

	var audit []map[string]any
	c.do("GET", "/audit?limit=100", nil, &audit)
	starts := 0
	for _, a := range audit {
		if a["action"] == "run.start" {
			starts++
			if a["actor"] != "owner@acme.test (schedule:minutely)" {
				t.Errorf("scheduled run actor %v", a["actor"])
			}
		}
	}
	if starts != 2 {
		t.Errorf("%d run.start entries, want 2", starts)
	}
}

// TestSchedulerOwnerLosesRunnerRole: a schedule whose owner can no longer
// start runs is skipped and audited, until an editor takes it over.
func TestSchedulerOwnerLosesRunnerRole(t *testing.T) {
	clock := &fakeClock{}
	base := startSchedServer(t, storetest.Open(t), clock)
	owner := newClient(t, base)
	setup(t, owner)
	f := newSchedFixture(t, owner, okTarget(t).URL, "1s")
	editor := member(t, owner, base, "editor@acme.test", "editor")
	var sch map[string]any
	if code := editor.do("POST", "/projects/"+f.pid+"/schedules", map[string]any{"name": "hourly", "scenarioId": f.sid, "targetId": f.tid, "cron": "@hourly"}, &sch); code != 201 {
		t.Fatalf("create: %d", code)
	}
	id := sch["id"].(string)

	var users []map[string]any
	owner.do("GET", "/users", nil, &users)
	for _, u := range users {
		if u["email"] == "editor@acme.test" {
			if code := owner.do("PATCH", "/users/"+u["id"].(string), map[string]string{"role": "viewer"}, nil); code != 200 {
				t.Fatalf("demote: %d", code)
			}
		}
	}
	clock.Advance(61 * time.Minute)
	eventually(t, 3*time.Second, "the skip", func() bool {
		owner.do("GET", "/schedules/"+id, nil, &sch)
		return strings.Contains(sch["lastSkipReason"].(string), "cannot start runs")
	})
	if n := len(countRuns(t, owner, f.pid)); n != 0 {
		t.Fatalf("started %d runs as a viewer", n)
	}
	var audit []map[string]any
	owner.do("GET", "/audit?limit=100", nil, &audit)
	found := false
	for _, a := range audit {
		if a["action"] == "schedule.skip" && a["actor"] == "scheduler" && a["subject"] == "hourly" {
			found = true
		}
	}
	if !found {
		t.Error("the skip was not audited")
	}

	// Saving it makes the owner the schedule's owner; it fires again.
	if code := owner.do("PATCH", "/schedules/"+id, map[string]any{"enabled": true}, &sch); code != 200 || sch["ownerEmail"] != "owner@acme.test" {
		t.Fatalf("take over: %d %v", code, sch)
	}
	clock.Advance(61 * time.Minute)
	eventually(t, 5*time.Second, "the run", func() bool { return len(countRuns(t, owner, f.pid)) == 1 })
}
