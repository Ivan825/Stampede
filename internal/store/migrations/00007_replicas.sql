-- +goose Up
-- Server replicas announce themselves here. Runs and AI jobs record the
-- replica that owns them, so a replica can tell work whose owner died
-- (heartbeat too old) from work another live replica is running.
CREATE TABLE replicas (
    id         uuid PRIMARY KEY,
    addr       text NOT NULL DEFAULT '',
    version    text NOT NULL DEFAULT '',
    started_at timestamptz NOT NULL DEFAULT now(),
    last_seen  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE runs ADD COLUMN owner_replica uuid;
ALTER TABLE ai_jobs ADD COLUMN owner_replica uuid;

-- +goose Down
ALTER TABLE ai_jobs DROP COLUMN owner_replica;
ALTER TABLE runs DROP COLUMN owner_replica;
DROP TABLE replicas;
