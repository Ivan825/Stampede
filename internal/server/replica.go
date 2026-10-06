package server

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Ivan825/Stampede/internal/store/db"
	"github.com/Ivan825/Stampede/internal/version"
)

// Replicas. Every server process registers itself in the replicas table and
// refreshes its row every few seconds. Runs and AI jobs record the replica
// that owns them. Work whose owner has not been seen for ReplicaStale is
// settled as interrupted by whichever replica notices first, so several
// replicas can serve at once and a crashed one's runs do not stay
// "running" for ever. Stop and kill requests for a run owned by another
// replica travel over the control channel (Postgres LISTEN/NOTIFY).

const controlChannel = "stampede_control"

type controlMsg struct {
	Op   string    `json:"op"` // stop, kill, kill_all
	Run  uuid.UUID `json:"run,omitempty"`
	Org  uuid.UUID `json:"org,omitempty"`
	By   string    `json:"by,omitempty"`
	From uuid.UUID `json:"from"`
}

type replica struct {
	id     uuid.UUID
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ReplicaID identifies this server process among its replicas.
func (s *Server) ReplicaID() uuid.UUID { return s.replica.id }

// startReplica registers this process and starts its heartbeat, reaper and
// control listener. Recover calls it.
func (s *Server) startReplica(ctx context.Context) error {
	if err := s.st.UpsertReplica(ctx, db.UpsertReplicaParams{ID: s.replica.id, Addr: s.cfg.ReplicaAddr, Version: version.Version}); err != nil {
		return err
	}
	rctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.replica.cancel = cancel
	s.replica.wg.Add(2)
	go func() {
		defer s.replica.wg.Done()
		t := time.NewTicker(s.cfg.ReplicaHeartbeat)
		defer t.Stop()
		for {
			select {
			case <-rctx.Done():
				return
			case <-t.C:
			}
			if err := s.st.TouchReplica(rctx, s.replica.id); err != nil && rctx.Err() == nil {
				s.log.Warn("replica heartbeat failed", "error", err)
			}
			s.settleOrphans(rctx)
		}
	}()
	go func() {
		defer s.replica.wg.Done()
		s.st.Listen(rctx, controlChannel, s.onControl, nil)
	}()
	return nil
}

func (s *Server) stopReplica() {
	if s.replica.cancel == nil {
		return
	}
	s.replica.cancel()
	s.replica.wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Leaving cleanly lets other replicas settle our work straight away.
	_ = s.st.DeleteReplica(ctx, s.replica.id)
}

// settleOrphans fails runs and AI jobs whose owner stopped heartbeating.
func (s *Server) settleOrphans(ctx context.Context) {
	cutoff := s.cfg.Now().Add(-s.cfg.ReplicaStale)
	ids, err := s.st.ListOrphanedRuns(ctx, cutoff)
	if err != nil {
		return
	}
	msg := "the server running this run stopped while it was in progress"
	for _, id := range ids {
		if s.runs.get(id) != nil {
			continue // ours, even if our own heartbeat lagged
		}
		if err := s.st.FinishRun(ctx, db.FinishRunParams{ID: id, Status: statusFailed, Error: &msg}); err == nil {
			s.log.Warn("settled a run whose server went away", "run", id)
		}
	}
	if n, err := s.st.FailOrphanedAIJobs(ctx, cutoff); err == nil && n > 0 {
		s.log.Warn("settled AI jobs whose server went away", "count", n)
	}
	_ = s.st.DeleteStaleReplicas(ctx, s.cfg.Now().Add(-24*time.Hour))
}

func (s *Server) publishControl(ctx context.Context, m controlMsg) error {
	m.From = s.replica.id
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return s.st.Notify(ctx, controlChannel, string(b))
}

func (s *Server) onControl(payload string) {
	var m controlMsg
	if json.Unmarshal([]byte(payload), &m) != nil || m.From == s.replica.id {
		return
	}
	switch m.Op {
	case "stop":
		if s.runs.stop(m.Run) {
			if a := s.runs.get(m.Run); a != nil {
				s.runs.setStatus(context.Background(), a, statusStopping)
			}
		}
	case "kill":
		s.runs.kill(m.Run, m.By)
	case "kill_all":
		s.runs.killOrg(m.Org, m.By)
	}
}

// errRunNotActive means the run is not running anywhere.
var errRunNotActive = errors.New("the run is not active")

// controlRun stops or kills a run wherever it runs: here directly, or on
// its owning replica through the control channel.
func (s *Server) controlRun(ctx context.Context, run db.GetRunRow, op, by string) error {
	switch op {
	case "stop":
		if s.runs.stop(run.ID) {
			if a := s.runs.get(run.ID); a != nil {
				s.runs.setStatus(ctx, a, statusStopping)
			}
			return nil
		}
	case "kill":
		if s.runs.kill(run.ID, by) {
			return nil
		}
	}
	if isTerminalStatus(run.Status) {
		return errRunNotActive
	}
	return s.publishControl(ctx, controlMsg{Op: op, Run: run.ID, By: by})
}
