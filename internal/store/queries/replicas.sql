-- name: UpsertReplica :exec
INSERT INTO replicas (id, addr, version) VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET last_seen = now(), addr = EXCLUDED.addr, version = EXCLUDED.version;

-- name: TouchReplica :exec
UPDATE replicas SET last_seen = now() WHERE id = $1;

-- name: DeleteReplica :exec
DELETE FROM replicas WHERE id = $1;

-- name: ListReplicas :many
SELECT * FROM replicas ORDER BY started_at;

-- name: DeleteStaleReplicas :exec
DELETE FROM replicas WHERE last_seen < $1;

-- name: SetRunOwner :exec
UPDATE runs SET owner_replica = $2 WHERE id = $1;

-- name: GetRunOwner :one
SELECT owner_replica FROM runs WHERE id = $1;

-- name: ListOrphanedRuns :many
-- Unfinished runs whose owner has no fresh heartbeat (or no owner at all).
SELECT r.id FROM runs r
WHERE r.status IN ('scheduling', 'starting', 'running', 'stopping', 'analyzing')
  AND (r.owner_replica IS NULL OR NOT EXISTS (
        SELECT 1 FROM replicas p WHERE p.id = r.owner_replica AND p.last_seen >= $1));

-- name: SetAIJobOwner :exec
UPDATE ai_jobs SET owner_replica = $2 WHERE id = $1;

-- name: FailOrphanedAIJobs :execrows
UPDATE ai_jobs j
SET status = 'failed', error = 'interrupted: the server stopped before the job finished', finished_at = now()
WHERE j.status IN ('queued', 'running')
  AND (j.owner_replica IS NULL OR NOT EXISTS (
        SELECT 1 FROM replicas p WHERE p.id = j.owner_replica AND p.last_seen >= $1));
