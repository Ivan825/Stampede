-- name: CreateAIProvider :exec
INSERT INTO ai_providers (id, org_id, name, kind, model, base_url, ciphertext, wrapped_key, key_id, monthly_token_cap, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: UpdateAIProvider :exec
UPDATE ai_providers
SET kind = $3, model = $4, base_url = $5, ciphertext = $6, wrapped_key = $7, key_id = $8,
    monthly_token_cap = $9, updated_at = now()
WHERE id = $1 AND org_id = $2;

-- name: GetAIProvider :one
SELECT * FROM ai_providers WHERE id = $1 AND org_id = $2;

-- name: GetAIProviderByName :one
SELECT * FROM ai_providers WHERE org_id = $1 AND name = $2;

-- name: ListAIProviders :many
SELECT * FROM ai_providers WHERE org_id = $1 ORDER BY name;

-- name: DeleteAIProvider :execrows
DELETE FROM ai_providers WHERE id = $1 AND org_id = $2;

-- name: CreateAIJob :exec
INSERT INTO ai_jobs (id, org_id, project_id, provider_id, provider_kind, model, status, target_id, scenario_id, dry_run, inputs, created_by)
VALUES ($1, $2, $3, $4, $5, $6, 'queued', $7, $8, $9, $10, $11);

-- name: StartAIJob :exec
UPDATE ai_jobs SET status = 'running', started_at = now() WHERE id = $1 AND status = 'queued';

-- name: UpdateAIJobProgress :exec
UPDATE ai_jobs SET stage = $2, round = $3, input_tokens = $4, output_tokens = $5
WHERE id = $1 AND status = 'running';

-- name: FinishAIJob :exec
UPDATE ai_jobs
SET status = $2, stage = $3, round = $4, yaml = $5, diff = $6, result = $7, error = $8,
    input_tokens = $9, output_tokens = $10, finished_at = now()
WHERE id = $1;

-- name: GetAIJob :one
SELECT * FROM ai_jobs WHERE id = $1 AND org_id = $2;

-- name: ListAIJobs :many
SELECT id, project_id, status, stage, provider_kind, model, input_tokens, output_tokens, dry_run, error,
       created_by, created_at, finished_at, approved_at
FROM ai_jobs
WHERE project_id = $1
ORDER BY created_at DESC
LIMIT $2;

-- name: FailInterruptedAIJobs :execrows
UPDATE ai_jobs
SET status = 'failed', error = 'interrupted: the server stopped before the job finished', finished_at = now()
WHERE status IN ('queued', 'running');

-- name: AITokensSince :one
SELECT COALESCE(SUM(input_tokens + output_tokens), 0)::bigint AS tokens
FROM ai_jobs
WHERE org_id = $1 AND created_at >= $2;

-- name: ClaimAIJobApproval :execrows
UPDATE ai_jobs SET approved_at = now()
WHERE id = $1 AND approved_at IS NULL AND status IN ('succeeded', 'needs_review');

-- name: ReleaseAIJobApproval :exec
UPDATE ai_jobs SET approved_at = NULL WHERE id = $1 AND approved_scenario_id IS NULL;

-- name: SetAIJobApproved :exec
UPDATE ai_jobs SET approved_scenario_id = $2, approved_version = $3 WHERE id = $1;
