-- name: GetOrgCaps :one
SELECT * FROM org_caps WHERE org_id = $1;

-- name: PutOrgCaps :exec
INSERT INTO org_caps (org_id, max_rate, max_vus, max_duration_s, updated_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (org_id) DO UPDATE
SET max_rate = EXCLUDED.max_rate, max_vus = EXCLUDED.max_vus, max_duration_s = EXCLUDED.max_duration_s,
    updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: GetProjectSettings :one
SELECT * FROM project_settings WHERE project_id = $1;

-- name: PutProjectSettings :exec
INSERT INTO project_settings (project_id, max_rate, max_vus, max_duration_s, require_dry_run, updated_by)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (project_id) DO UPDATE
SET max_rate = EXCLUDED.max_rate, max_vus = EXCLUDED.max_vus, max_duration_s = EXCLUDED.max_duration_s,
    require_dry_run = EXCLUDED.require_dry_run, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: GetProjectRole :one
SELECT role FROM project_roles WHERE project_id = $1 AND user_id = $2;

-- name: ListProjectRoles :many
SELECT pr.user_id, pr.role, pr.created_at, u.email, u.name, m.role AS org_role
FROM project_roles pr
JOIN users u ON u.id = pr.user_id
JOIN projects p ON p.id = pr.project_id
LEFT JOIN memberships m ON m.org_id = p.org_id AND m.user_id = pr.user_id
WHERE pr.project_id = $1
ORDER BY u.email;

-- name: SetProjectRole :exec
INSERT INTO project_roles (project_id, user_id, role, created_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (project_id, user_id) DO UPDATE
SET role = EXCLUDED.role, created_by = EXCLUDED.created_by, created_at = now();

-- name: DeleteProjectRole :execrows
DELETE FROM project_roles WHERE project_id = $1 AND user_id = $2;

-- name: ListUserProjectRoles :many
-- The caller's overrides across an organisation's projects.
SELECT pr.project_id, pr.role FROM project_roles pr JOIN projects p ON p.id = pr.project_id
WHERE p.org_id = $1 AND pr.user_id = $2;
