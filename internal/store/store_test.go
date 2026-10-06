package store_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/store"
	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/store/storetest"
)

func TestMigrateAndBasics(t *testing.T) {
	s := storetest.Open(t)
	ctx := context.Background()

	v, err := s.MigrationVersion(ctx)
	if err != nil || v < 1 {
		t.Fatalf("version %d, %v", v, err)
	}
	var hyper bool
	err = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_name = 'run_metrics')`).Scan(&hyper)
	if err != nil || !hyper {
		t.Errorf("run_metrics should be a hypertable: %v", err)
	}

	org, user := uuid.New(), uuid.New()
	err = s.InTx(ctx, func(q *db.Queries) error {
		if err := q.CreateOrg(ctx, db.CreateOrgParams{ID: org, Name: "Acme"}); err != nil {
			return err
		}
		if err := q.CreateUser(ctx, db.CreateUserParams{ID: user, Email: "Owner@Acme.test", Name: "Owner", PasswordHash: "x"}); err != nil {
			return err
		}
		return q.CreateMembership(ctx, db.CreateMembershipParams{OrgID: org, UserID: user, Role: "owner"})
	})
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.GetUserByEmail(ctx, "owner@acme.test")
	if err != nil || u.ID != user {
		t.Fatalf("case-insensitive email lookup failed: %v", err)
	}
	err = s.CreateUser(ctx, db.CreateUserParams{ID: uuid.New(), Email: "OWNER@acme.test", Name: "Dup", PasswordHash: "x"})
	if !store.IsUniqueViolation(err) {
		t.Errorf("duplicate email should violate uniqueness, got %v", err)
	}
	if _, err := s.GetProject(ctx, db.GetProjectParams{ID: uuid.New(), OrgID: org}); !store.IsNotFound(err) {
		t.Errorf("want not found, got %v", err)
	}

	ok, release, err := s.AdvisoryLock(ctx, 42)
	if err != nil || !ok {
		t.Fatalf("lock: %v", err)
	}
	ok2, _, _ := s.AdvisoryLock(ctx, 42)
	if ok2 {
		t.Error("second lock holder should be refused")
	}
	release()
}
