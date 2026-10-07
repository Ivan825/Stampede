-- name: CreateDriftResult :exec
INSERT INTO drift_results (id, project_id, schedule_id, scenario_id, scenario_version, target_id, status, error, broken, result, spec, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

-- name: GetDriftResult :one
SELECT d.*, s.name AS scenario_name, t.base_url AS target_url, sc.name AS schedule_name
FROM drift_results d
JOIN projects p ON p.id = d.project_id
JOIN scenarios s ON s.id = d.scenario_id
JOIN targets t ON t.id = d.target_id
LEFT JOIN schedules sc ON sc.id = d.schedule_id
WHERE d.id = $1 AND p.org_id = $2;

-- name: ListDriftResults :many
SELECT d.id, d.project_id, d.schedule_id, d.scenario_id, d.scenario_version, d.target_id, d.status, d.error,
       d.broken, d.repair_job_id, d.created_at, s.name AS scenario_name, t.base_url AS target_url, sc.name AS schedule_name
FROM drift_results d
JOIN scenarios s ON s.id = d.scenario_id
JOIN targets t ON t.id = d.target_id
LEFT JOIN schedules sc ON sc.id = d.schedule_id
WHERE d.project_id = $1
  AND (sqlc.narg('schedule_id')::uuid IS NULL OR d.schedule_id = sqlc.narg('schedule_id'))
ORDER BY d.created_at DESC
LIMIT sqlc.arg('lim');

-- name: LastDriftSpec :one
-- The spec endpoints recorded by a schedule's latest check that fetched one.
SELECT spec FROM drift_results
WHERE schedule_id = $1 AND spec IS NOT NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: SetDriftRepairJob :exec
UPDATE drift_results SET repair_job_id = $2 WHERE id = $1;
