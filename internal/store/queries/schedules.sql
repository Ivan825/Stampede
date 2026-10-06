-- name: CreateSchedule :exec
INSERT INTO schedules (id, project_id, name, scenario_id, target_id, cron, timezone, overrides, env, workers, enabled, note, owner_id, next_run_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: GetSchedule :one
SELECT sc.*, p.org_id, s.name AS scenario_name, t.name AS target_name,
       u.email AS owner_email, r.status AS last_run_status, r.verdict AS last_run_verdict, r.created_at AS last_run_at
FROM schedules sc
JOIN projects p ON p.id = sc.project_id
JOIN scenarios s ON s.id = sc.scenario_id
JOIN targets t ON t.id = sc.target_id
LEFT JOIN users u ON u.id = sc.owner_id
LEFT JOIN runs r ON r.id = sc.last_run_id
WHERE sc.id = $1 AND p.org_id = $2;

-- name: ListSchedules :many
SELECT sc.*, p.org_id, s.name AS scenario_name, t.name AS target_name,
       u.email AS owner_email, r.status AS last_run_status, r.verdict AS last_run_verdict, r.created_at AS last_run_at
FROM schedules sc
JOIN projects p ON p.id = sc.project_id
JOIN scenarios s ON s.id = sc.scenario_id
JOIN targets t ON t.id = sc.target_id
LEFT JOIN users u ON u.id = sc.owner_id
LEFT JOIN runs r ON r.id = sc.last_run_id
WHERE sc.project_id = $1
ORDER BY sc.name;

-- name: UpdateSchedule :exec
UPDATE schedules
SET name = $2, scenario_id = $3, target_id = $4, cron = $5, timezone = $6, overrides = $7, env = $8,
    workers = $9, enabled = $10, note = $11, owner_id = $12, next_run_at = $13, updated_at = now()
WHERE id = $1;

-- name: DeleteSchedule :execrows
DELETE FROM schedules WHERE id = $1;

-- name: ListDueSchedules :many
SELECT id, cron, timezone, next_run_at FROM schedules
WHERE enabled AND next_run_at <= @now::timestamptz
ORDER BY next_run_at
LIMIT 100;

-- name: ClaimSchedule :one
-- Moves a due schedule to its next firing. Only one caller can match the
-- old next_run_at, so concurrent claimers fire it once.
UPDATE schedules SET next_run_at = sqlc.narg('next'), last_fired_at = @now::timestamptz
WHERE id = @id AND enabled AND next_run_at = @due::timestamptz AND next_run_at <= @now::timestamptz
RETURNING id;

-- name: RecordScheduleRun :exec
UPDATE schedules SET last_run_id = $2, last_fired_at = $3, last_skip_reason = '' WHERE id = $1;

-- name: RecordScheduleSkip :exec
UPDATE schedules SET last_skip_reason = $2 WHERE id = $1;
