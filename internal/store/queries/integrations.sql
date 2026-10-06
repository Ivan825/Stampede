-- name: CreateIntegration :exec
INSERT INTO integrations (id, org_id, name, kind, url, ciphertext, wrapped_key, key_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetIntegration :one
SELECT * FROM integrations WHERE id = $1 AND org_id = $2;

-- name: GetIntegrationByName :one
SELECT * FROM integrations WHERE org_id = $1 AND name = $2;

-- name: ListIntegrations :many
SELECT * FROM integrations WHERE org_id = $1 ORDER BY name;

-- name: DeleteIntegration :execrows
DELETE FROM integrations WHERE id = $1 AND org_id = $2;

-- name: CreateNotificationChannel :exec
INSERT INTO notification_channels (id, org_id, name, kind, events, allow_private, url_hint,
    url_ciphertext, url_wrapped_key, secret_ciphertext, secret_wrapped_key, key_id, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: GetNotificationChannel :one
SELECT * FROM notification_channels WHERE id = $1 AND org_id = $2;

-- name: ListNotificationChannels :many
SELECT * FROM notification_channels WHERE org_id = $1 ORDER BY name;

-- name: ListNotificationChannelsForEvent :many
SELECT * FROM notification_channels WHERE org_id = $1 AND sqlc.arg(event)::text = ANY (events) ORDER BY name;

-- name: DeleteNotificationChannel :execrows
DELETE FROM notification_channels WHERE id = $1 AND org_id = $2;

-- name: InsertNotificationDelivery :exec
INSERT INTO notification_deliveries (channel_id, delivery_id, event, run_id, attempt, ok, status_code, error, duration_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: PruneNotificationDeliveries :exec
DELETE FROM notification_deliveries AS old
WHERE old.channel_id = sqlc.arg(channel_id)::uuid
  AND old.id < (
    SELECT COALESCE(MIN(newest.id), 0) FROM (
      SELECT d.id FROM notification_deliveries AS d
      WHERE d.channel_id = sqlc.arg(channel_id)::uuid
      ORDER BY d.id DESC LIMIT sqlc.arg(keep)::int
    ) AS newest
  );

-- name: ListNotificationDeliveries :many
SELECT * FROM notification_deliveries WHERE channel_id = $1 ORDER BY id DESC LIMIT $2;
