-- +goose Up

-- Integrations connect server runs to the target's own telemetry. An admin
-- configures them by name per organisation and scenarios refer to them by
-- name, so the server never fetches a URL written in a scenario.
--   prometheus: url is the Prometheus base URL; the optional bearer token
--               is sealed with the master key.
--   traces:     url is a link template containing {traceId}; nothing is
--               fetched.
CREATE TABLE integrations (
    id          uuid PRIMARY KEY,
    org_id      uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('prometheus', 'traces')),
    url         text NOT NULL,
    ciphertext  bytea,
    wrapped_key bytea,
    key_id      text,
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

-- Notification channels receive run events. The destination URL (which
-- for Slack and Discord is itself the credential) and the webhook signing
-- secret are sealed with the master key; url_hint keeps only the host for
-- display.
CREATE TABLE notification_channels (
    id                 uuid PRIMARY KEY,
    org_id             uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name               text NOT NULL,
    kind               text NOT NULL CHECK (kind IN ('webhook', 'slack', 'discord')),
    events             text[] NOT NULL,
    allow_private      boolean NOT NULL DEFAULT false,
    url_hint           text NOT NULL,
    url_ciphertext     bytea NOT NULL,
    url_wrapped_key    bytea NOT NULL,
    secret_ciphertext  bytea,
    secret_wrapped_key bytea,
    key_id             text NOT NULL,
    created_by         uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

-- One row per delivery attempt; only the most recent attempts per channel
-- are kept.
CREATE TABLE notification_deliveries (
    id          bigserial PRIMARY KEY,
    channel_id  uuid NOT NULL REFERENCES notification_channels (id) ON DELETE CASCADE,
    delivery_id uuid NOT NULL,
    event       text NOT NULL,
    run_id      uuid,
    attempt     integer NOT NULL,
    ok          boolean NOT NULL,
    status_code integer NOT NULL DEFAULT 0,
    error       text NOT NULL DEFAULT '',
    duration_ms integer NOT NULL DEFAULT 0,
    at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notification_deliveries_channel_idx ON notification_deliveries (channel_id, id DESC);

-- +goose Down
DROP TABLE IF EXISTS notification_deliveries, notification_channels, integrations;
