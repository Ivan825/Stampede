-- name: CreateScenario :exec
INSERT INTO scenarios (id, project_id, name, description, tags) VALUES ($1, $2, $3, $4, $5);

-- name: GetScenario :one
SELECT s.* FROM scenarios s JOIN projects p ON p.id = s.project_id
WHERE s.id = $1 AND p.org_id = $2;

-- name: ListScenarios :many
SELECT * FROM scenarios WHERE project_id = $1 ORDER BY updated_at DESC;

-- name: UpdateScenarioMeta :exec
UPDATE scenarios SET name = $2, description = $3, tags = $4, updated_at = now() WHERE id = $1;

-- name: DeleteScenario :execrows
DELETE FROM scenarios s USING projects p
WHERE s.id = $1 AND p.id = s.project_id AND p.org_id = $2;

-- name: NextScenarioVersion :one
SELECT COALESCE(max(version), 0) + 1 AS next FROM scenario_versions WHERE scenario_id = $1;

-- name: CreateScenarioVersion :exec
INSERT INTO scenario_versions (scenario_id, version, yaml, message, plan, created_by)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetScenarioVersion :one
SELECT * FROM scenario_versions WHERE scenario_id = $1 AND version = $2;

-- name: GetLatestScenarioVersion :one
SELECT * FROM scenario_versions WHERE scenario_id = $1 ORDER BY version DESC LIMIT 1;

-- name: ListScenarioVersions :many
SELECT * FROM scenario_versions WHERE scenario_id = $1 ORDER BY version DESC;
