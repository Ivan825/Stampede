-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CreateOrg :exec
INSERT INTO orgs (id, name) VALUES ($1, $2);

-- name: GetOrg :one
SELECT * FROM orgs WHERE id = $1;

-- name: FirstOrg :one
SELECT * FROM orgs ORDER BY created_at, id LIMIT 1;

-- name: CreateUser :exec
INSERT INTO users (id, email, name, password_hash) VALUES ($1, $2, $3, $4);

-- name: GetUserByEmail :one
SELECT * FROM users WHERE lower(email) = lower(@email);

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: UpdateUserName :exec
UPDATE users SET name = $2 WHERE id = $1;

-- name: UpdateUserPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: TouchUserLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: CreateMembership :exec
INSERT INTO memberships (org_id, user_id, role) VALUES ($1, $2, $3);

-- name: UpdateMembershipRole :exec
UPDATE memberships SET role = $3 WHERE org_id = $1 AND user_id = $2;

-- name: GetMembershipForUser :one
SELECT m.org_id, m.role, o.name AS org_name
FROM memberships m JOIN orgs o ON o.id = m.org_id
WHERE m.user_id = $1
ORDER BY m.created_at
LIMIT 1;

-- name: GetMember :one
SELECT u.id, u.email, u.name, u.created_at, u.last_login_at, m.role
FROM memberships m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1 AND u.id = $2;

-- name: ListMembers :many
SELECT u.id, u.email, u.name, u.created_at, u.last_login_at, m.role
FROM memberships m JOIN users u ON u.id = m.user_id
WHERE m.org_id = $1
ORDER BY u.created_at;

-- name: CountOwners :one
SELECT count(*) FROM memberships WHERE org_id = $1 AND role = 'owner';

-- name: CreateSession :exec
INSERT INTO sessions (token_hash, user_id, org_id, expires_at, ip, user_agent)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetSession :one
SELECT s.user_id, s.org_id, s.expires_at, u.email, u.name, m.role, o.name AS org_name
FROM sessions s
JOIN users u ON u.id = s.user_id
JOIN memberships m ON m.user_id = s.user_id AND m.org_id = s.org_id
JOIN orgs o ON o.id = s.org_id
WHERE s.token_hash = $1 AND s.expires_at > now();

-- name: DeleteSession :exec
DELETE FROM sessions WHERE token_hash = $1;

-- name: DeleteUserSessions :exec
DELETE FROM sessions WHERE user_id = $1;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions WHERE expires_at <= now();

-- name: CreateToken :exec
INSERT INTO api_tokens (id, org_id, user_id, name, prefix, token_hash, role, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListTokens :many
SELECT * FROM api_tokens WHERE user_id = $1 ORDER BY created_at DESC;

-- name: DeleteToken :execrows
DELETE FROM api_tokens WHERE id = $1 AND user_id = $2;

-- name: GetTokenByHash :one
SELECT t.id, t.user_id, t.org_id, t.name, t.role AS token_role, t.expires_at,
       u.email, u.name AS user_name, m.role AS user_role, o.name AS org_name
FROM api_tokens t
JOIN users u ON u.id = t.user_id
JOIN memberships m ON m.user_id = t.user_id AND m.org_id = t.org_id
JOIN orgs o ON o.id = t.org_id
WHERE t.token_hash = $1 AND (t.expires_at IS NULL OR t.expires_at > now());

-- name: TouchToken :exec
UPDATE api_tokens SET last_used_at = now() WHERE id = $1;

-- name: InsertAudit :exec
INSERT INTO audit_log (org_id, actor, actor_id, action, subject, details, ip)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAudit :many
SELECT * FROM audit_log
WHERE org_id = $1 AND at < @before
ORDER BY at DESC
LIMIT $2;
