-- name: CreateRun :exec
INSERT INTO runs (id, project_id, scenario_id, scenario_version, target_id, status, overrides, plan, env, workers, note, created_by)
VALUES ($1, $2, $3, $4, $5, 'scheduling', $6, $7, $8, $9, $10, $11);

-- name: GetRun :one
SELECT r.*, s.name AS scenario_name, t.base_url AS target_url
FROM runs r
JOIN projects p ON p.id = r.project_id
JOIN scenarios s ON s.id = r.scenario_id
JOIN targets t ON t.id = r.target_id
WHERE r.id = $1 AND p.org_id = $2;

-- name: ListRuns :many
SELECT r.*, s.name AS scenario_name, t.base_url AS target_url
FROM runs r
JOIN scenarios s ON s.id = r.scenario_id
JOIN targets t ON t.id = r.target_id
WHERE r.project_id = $1
  AND (sqlc.narg('scenario_id')::uuid IS NULL OR r.scenario_id = sqlc.narg('scenario_id'))
  AND r.created_at < @before
ORDER BY r.created_at DESC
LIMIT $2;

-- name: ListActiveRuns :many
SELECT r.id FROM runs r JOIN projects p ON p.id = r.project_id
WHERE p.org_id = $1 AND r.status IN ('scheduling', 'starting', 'running', 'stopping');

-- name: ListUnfinishedRuns :many
SELECT id FROM runs WHERE status IN ('scheduling', 'starting', 'running', 'stopping', 'analyzing');

-- name: SetRunStatus :exec
UPDATE runs SET status = $2 WHERE id = $1;

-- name: MarkRunStarted :exec
UPDATE runs SET status = 'running', started_at = $2 WHERE id = $1;

-- name: FinishRun :exec
UPDATE runs SET status = $2, verdict = $3, stop_reason = $4, error = $5, summary = $6, ended_at = now()
WHERE id = $1;

-- name: InsertRunMetric :exec
INSERT INTO run_metrics (run_id, ts, interval, requests, failed, rps, error_rate, p50, p95, p99, vus, planned, dropped, iterations, sched_lag, snapshot)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT DO NOTHING;

-- name: ListRunPoints :many
SELECT interval, requests, failed, rps, error_rate, p50, p95, p99, vus, planned, dropped, iterations, sched_lag
FROM run_metrics WHERE run_id = $1 ORDER BY interval;

-- name: ListRunSnapshots :many
SELECT snapshot FROM run_metrics WHERE run_id = $1 ORDER BY interval;

-- name: InsertRunEvent :exec
INSERT INTO run_events (run_id, type, message, worker, details) VALUES ($1, $2, $3, $4, $5);

-- name: ListRunEvents :many
SELECT * FROM run_events WHERE run_id = $1 ORDER BY id;

-- name: SaveReport :exec
INSERT INTO reports (run_id, report) VALUES ($1, $2)
ON CONFLICT (run_id) DO UPDATE SET report = EXCLUDED.report, created_at = now();

-- name: GetReport :one
SELECT report FROM reports WHERE run_id = $1;

-- name: SetRunWorkers :exec
UPDATE runs SET workers = $2 WHERE id = $1;
