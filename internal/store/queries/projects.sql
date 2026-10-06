-- name: CreateProject :exec
INSERT INTO projects (id, org_id, name, slug, description) VALUES ($1, $2, $3, $4, $5);

-- name: ListProjects :many
SELECT * FROM projects WHERE org_id = $1 ORDER BY name;

-- name: GetProject :one
SELECT * FROM projects WHERE id = $1 AND org_id = $2;

-- name: UpdateProject :exec
UPDATE projects SET name = $3, slug = $4, description = $5 WHERE id = $1 AND org_id = $2;

-- name: DeleteProject :execrows
DELETE FROM projects WHERE id = $1 AND org_id = $2;

-- name: CreateTarget :exec
INSERT INTO targets (id, project_id, name, base_url, host, private, verification_token, allow_hosts, max_rate, max_vus, max_duration_s)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: ListTargets :many
SELECT * FROM targets WHERE project_id = $1 ORDER BY name;

-- name: GetTarget :one
SELECT t.* FROM targets t JOIN projects p ON p.id = t.project_id
WHERE t.id = $1 AND p.org_id = $2;

-- name: UpdateTarget :exec
UPDATE targets SET name = $2, base_url = $3, host = $4, private = $5, allow_hosts = $6,
    max_rate = $7, max_vus = $8, max_duration_s = $9,
    verified_at = CASE WHEN host = $4 THEN verified_at END,
    verification_method = CASE WHEN host = $4 THEN verification_method END
WHERE id = $1;

-- name: SetTargetVerified :exec
UPDATE targets SET verified_at = now(), verification_method = $2 WHERE id = $1;

-- name: DeleteTarget :execrows
DELETE FROM targets t USING projects p
WHERE t.id = $1 AND p.id = t.project_id AND p.org_id = $2;

-- name: PutSecret :one
INSERT INTO secrets (project_id, name, ciphertext, wrapped_key, key_id)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (project_id, name) DO UPDATE
SET ciphertext = EXCLUDED.ciphertext, wrapped_key = EXCLUDED.wrapped_key, key_id = EXCLUDED.key_id, updated_at = now()
RETURNING updated_at;

-- name: ListSecretNames :many
SELECT name, updated_at FROM secrets WHERE project_id = $1 ORDER BY name;

-- name: ListSecrets :many
SELECT * FROM secrets WHERE project_id = $1;

-- name: DeleteSecret :execrows
DELETE FROM secrets WHERE project_id = $1 AND name = $2;
