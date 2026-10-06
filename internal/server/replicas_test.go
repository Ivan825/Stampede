package server_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/server"
	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

func startReplica(t *testing.T, st *store.Store) (string, *server.Server) {
	t.Helper()
	key, _ := keyringKey()
	srv, err := server.New(server.Config{
		Store: st, Keyring: key, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ReplicaHeartbeat: 100 * time.Millisecond, ReplicaStale: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		hs.Close()
	})
	return hs.URL, srv
}

func waitStatus(t *testing.T, c *client, rid string, within time.Duration, ok func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var got map[string]any
		c.do("GET", "/runs/"+rid, nil, &got)
		s, _ := got["status"].(string)
		if ok(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s still %q after %s", rid, s, within)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestActiveReplicas(t *testing.T) {
	st := storetest.Open(t)
	baseA, _ := startReplica(t, st)
	baseB, _ := startReplica(t, st)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(5 * time.Millisecond)
		fmt.Fprint(w, "ok")
	}))
	t.Cleanup(target.Close)

	a := newClient(t, baseA)
	setup(t, a)
	b := newClient(t, baseB)
	if code := b.do("POST", "/auth/login", map[string]string{"email": "owner@acme.test", "password": "correct horse battery"}, nil); code != 200 {
		t.Fatalf("login on B: %d", code)
	}
	var proj, tgt, sc map[string]any
	a.do("POST", "/projects", map[string]string{"name": "Shop"}, &proj)
	pid := proj["id"].(string)
	a.do("POST", "/projects/"+pid+"/targets", map[string]any{"name": "t", "baseURL": target.URL}, &tgt)
	a.do("POST", "/projects/"+pid+"/scenarios", map[string]string{"yaml": `
metadata: {name: long}
target: {baseURL: "http://ignored.example"}
journeys: [{name: home, steps: [{get: /}]}]
load: {mode: rate, rate: 20/s, duration: 60s}`}, &sc)
	start := func() string {
		var run map[string]any
		if code := a.do("POST", "/projects/"+pid+"/runs", map[string]any{"scenarioId": sc["id"], "targetId": tgt["id"]}, &run); code != 201 {
			t.Fatalf("start on A: %d", code)
		}
		return run["id"].(string)
	}

	// A run started on A streams live through B.
	rid := start()
	waitStatus(t, b, rid, 10*time.Second, func(s string) bool { return s == "running" })
	req, _ := http.NewRequest("GET", baseB+"/api/v1/runs/"+rid+"/live", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := b.http.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	points := 0
	sc2 := bufio.NewScanner(resp.Body)
	for sc2.Scan() && points < 3 {
		if strings.HasPrefix(sc2.Text(), "event: point") {
			points++
		}
	}
	resp.Body.Close()
	if points < 3 {
		t.Fatalf("B streamed %d points of A's run", points)
	}

	// Stopping through B stops A's run long before its 60 seconds.
	if code := b.do("POST", "/runs/"+rid+"/stop", nil, nil); code != 202 {
		t.Fatalf("stop through B: %d", code)
	}
	if s := waitStatus(t, b, rid, 15*time.Second, func(s string) bool { return s == "completed" || s == "aborted" }); s != "completed" {
		t.Errorf("stopped run ended %q", s)
	}

	// The kill switch on B reaches a run on A.
	rid2 := start()
	waitStatus(t, b, rid2, 10*time.Second, func(s string) bool { return s == "running" })
	var killed map[string][]string
	if code := b.do("POST", "/runs/kill-all", nil, &killed); code != 200 || len(killed["killed"]) != 1 {
		t.Fatalf("kill-all through B: %d %v", code, killed)
	}
	waitStatus(t, b, rid2, 10*time.Second, func(s string) bool { return s == "aborted" })

	// A run whose replica vanished without a word is settled by a live one.
	var me map[string]any
	a.do("GET", "/me", nil, &me)
	uid := uuid.MustParse(me["id"].(string))
	ghost, orphan := uuid.New(), uuid.New()
	if err := st.CreateRun(context.Background(), db.CreateRunParams{
		ID: orphan, ProjectID: uuid.MustParse(pid), ScenarioID: uuid.MustParse(sc["id"].(string)), ScenarioVersion: 1,
		TargetID: uuid.MustParse(tgt["id"].(string)), Overrides: []byte("{}"), Plan: []byte("{}"), Env: []byte("{}"), CreatedBy: &uid,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRunOwner(context.Background(), db.SetRunOwnerParams{ID: orphan, OwnerReplica: &ghost}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, b, orphan.String(), 10*time.Second, func(s string) bool { return s == "failed" })
	var got map[string]any
	b.do("GET", "/runs/"+orphan.String(), nil, &got)
	if e, _ := got["error"].(string); !strings.Contains(e, "stopped while it was in progress") {
		t.Errorf("orphan error %q", e)
	}
}
