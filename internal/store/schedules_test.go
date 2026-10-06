package store_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

// TestClaimScheduleOnce races many claimers, on separate connection pools
// as two server replicas would be, for one due schedule: exactly one wins.
func TestClaimScheduleOnce(t *testing.T) {
	url := storetest.URL(t)
	ctx := context.Background()
	a := openMigrated(t, url)
	b, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	org, project, scenario, target := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	must(t, a.CreateOrg(ctx, db.CreateOrgParams{ID: org, Name: "Acme"}))
	must(t, a.CreateProject(ctx, db.CreateProjectParams{ID: project, OrgID: org, Name: "P", Slug: "p"}))
	must(t, a.CreateScenario(ctx, db.CreateScenarioParams{ID: scenario, ProjectID: project, Name: "s", Tags: []string{}}))
	must(t, a.CreateTarget(ctx, db.CreateTargetParams{ID: target, ProjectID: project, Name: "t", BaseUrl: "http://127.0.0.1", Host: "127.0.0.1", Private: true, VerificationToken: "x", AllowHosts: []string{}}))

	now := time.Now().UTC().Truncate(time.Second)
	due := now.Add(-time.Minute)
	id := uuid.New()
	must(t, a.CreateSchedule(ctx, db.CreateScheduleParams{
		ID: id, ProjectID: project, Name: "nightly", ScenarioID: scenario, TargetID: target,
		Cron: "* * * * *", Timezone: "UTC", Overrides: []byte("{}"), Env: []byte("{}"), Enabled: true, NextRunAt: &due,
	}))

	rows, err := a.ListDueSchedules(ctx, now)
	if err != nil || len(rows) != 1 || !rows[0].NextRunAt.Equal(due) {
		t.Fatalf("due: %v %v", rows, err)
	}
	next := now.Add(time.Minute)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 16 {
		st := a
		if i%2 == 1 {
			st = b
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.ClaimSchedule(ctx, db.ClaimScheduleParams{ID: id, Due: *rows[0].NextRunAt, Next: &next, Now: now})
			switch {
			case err == nil:
				wins.Add(1)
			case !store.IsNotFound(err):
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := wins.Load(); n != 1 {
		t.Fatalf("%d claimers won, want 1", n)
	}
	if rows, _ := a.ListDueSchedules(ctx, now); len(rows) != 0 {
		t.Errorf("still due after the claim: %v", rows)
	}

	// A disabled schedule is never due, and a schedule not yet due cannot
	// be claimed.
	must(t, a.UpdateSchedule(ctx, db.UpdateScheduleParams{ID: id, Name: "nightly", ScenarioID: scenario, TargetID: target, Cron: "* * * * *", Timezone: "UTC",
		Overrides: []byte("{}"), Env: []byte("{}"), Enabled: false, NextRunAt: &due}))
	if rows, _ := a.ListDueSchedules(ctx, now); len(rows) != 0 {
		t.Errorf("disabled schedule is due: %v", rows)
	}
	if _, err := a.ClaimSchedule(ctx, db.ClaimScheduleParams{ID: id, Due: due, Next: &next, Now: now}); !store.IsNotFound(err) {
		t.Errorf("claimed a disabled schedule: %v", err)
	}
	must(t, a.UpdateSchedule(ctx, db.UpdateScheduleParams{ID: id, Name: "nightly", ScenarioID: scenario, TargetID: target, Cron: "* * * * *", Timezone: "UTC",
		Overrides: []byte("{}"), Env: []byte("{}"), Enabled: true, NextRunAt: &next}))
	if _, err := a.ClaimSchedule(ctx, db.ClaimScheduleParams{ID: id, Due: next, Next: &next, Now: now}); !store.IsNotFound(err) {
		t.Errorf("claimed before it was due: %v", err)
	}
}

func openMigrated(t *testing.T, url string) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), false); err != nil {
		t.Fatal(err)
	}
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
