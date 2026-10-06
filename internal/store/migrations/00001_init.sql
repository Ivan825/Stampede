-- +goose Up

CREATE TABLE orgs (
    id         uuid PRIMARY KEY,
    name       text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    email         text NOT NULL,
    name          text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE memberships (
    org_id     uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'runner', 'viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

-- Sessions and API tokens store only a SHA-256 of the secret.
CREATE TABLE sessions (
    token_hash   bytea PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    org_id       uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY,
    org_id       uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL,
    prefix       text NOT NULL,
    token_hash   bytea NOT NULL UNIQUE,
    role         text NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'runner', 'viewer')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at   timestamptz
);
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id);

CREATE TABLE projects (
    id          uuid PRIMARY KEY,
    org_id      uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name        text NOT NULL,
    slug        text NOT NULL,
    description text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);

CREATE TABLE targets (
    id                  uuid PRIMARY KEY,
    project_id          uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name                text NOT NULL,
    base_url            text NOT NULL,
    host                text NOT NULL,
    private             boolean NOT NULL,
    verification_token  text NOT NULL,
    verified_at         timestamptz,
    verification_method text,
    allow_hosts         text[] NOT NULL DEFAULT '{}',
    max_rate            double precision,
    max_vus             integer,
    max_duration_s      integer,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX targets_project_idx ON targets (project_id);

-- Secret values use envelope encryption: a random data key encrypts the
-- value, and the server's master key encrypts the data key.
CREATE TABLE secrets (
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        text NOT NULL,
    ciphertext  bytea NOT NULL,
    wrapped_key bytea NOT NULL,
    key_id      text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, name)
);

CREATE TABLE scenarios (
    id          uuid PRIMARY KEY,
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    tags        text[] NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);

CREATE TABLE scenario_versions (
    scenario_id uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    version     integer NOT NULL,
    yaml        text NOT NULL,
    message     text NOT NULL DEFAULT '',
    plan        jsonb,
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scenario_id, version)
);

CREATE TABLE runs (
    id               uuid PRIMARY KEY,
    project_id       uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    scenario_id      uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    scenario_version integer NOT NULL,
    target_id        uuid NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    status           text NOT NULL CHECK (status IN ('scheduling', 'starting', 'running', 'stopping', 'analyzing', 'completed', 'aborted', 'failed')),
    verdict          text,
    stop_reason      text,
    error            text,
    overrides        jsonb NOT NULL DEFAULT '{}',
    plan             jsonb,
    env              jsonb NOT NULL DEFAULT '{}',
    workers          integer NOT NULL DEFAULT 0,
    note             text NOT NULL DEFAULT '',
    summary          jsonb,
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    ended_at         timestamptz
);
CREATE INDEX runs_project_created_idx ON runs (project_id, created_at DESC);
CREATE INDEX runs_active_idx ON runs (status) WHERE status IN ('scheduling', 'starting', 'running', 'stopping', 'analyzing');

-- One row per run per interval. The snapshot column holds the lossless
-- merged metrics (histograms included); the other columns are a lossy
-- summary for charts and SQL.
CREATE TABLE run_metrics (
    run_id      uuid NOT NULL,
    ts          timestamptz NOT NULL,
    interval    integer NOT NULL,
    requests    bigint NOT NULL,
    failed      bigint NOT NULL,
    rps         double precision NOT NULL,
    error_rate  double precision NOT NULL,
    p50         double precision NOT NULL,
    p95         double precision NOT NULL,
    p99         double precision NOT NULL,
    vus         integer NOT NULL,
    planned     double precision NOT NULL,
    dropped     bigint NOT NULL,
    iterations  bigint NOT NULL,
    sched_lag   double precision NOT NULL,
    snapshot    bytea NOT NULL,
    PRIMARY KEY (run_id, interval, ts)
);

CREATE TABLE run_events (
    id      bigserial PRIMARY KEY,
    run_id  uuid NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    at      timestamptz NOT NULL DEFAULT now(),
    type    text NOT NULL,
    message text NOT NULL,
    worker  text NOT NULL DEFAULT '',
    details jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX run_events_run_idx ON run_events (run_id, id);

CREATE TABLE reports (
    run_id     uuid PRIMARY KEY REFERENCES runs (id) ON DELETE CASCADE,
    report     jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id       bigserial PRIMARY KEY,
    org_id   uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    at       timestamptz NOT NULL DEFAULT now(),
    actor    text NOT NULL,
    actor_id uuid,
    action   text NOT NULL,
    subject  text NOT NULL DEFAULT '',
    details  jsonb NOT NULL DEFAULT '{}',
    ip       text NOT NULL DEFAULT ''
);
CREATE INDEX audit_org_at_idx ON audit_log (org_id, at DESC);

-- TimescaleDB is used when available (the Compose stack and Helm chart
-- ship it); plain PostgreSQL works too, without compression.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb') THEN
        CREATE EXTENSION IF NOT EXISTS timescaledb;
        PERFORM create_hypertable('run_metrics', 'ts', chunk_time_interval => interval '1 day', if_not_exists => true);
        EXECUTE 'ALTER TABLE run_metrics SET (timescaledb.compress, timescaledb.compress_segmentby = ''run_id'')';
        PERFORM add_compression_policy('run_metrics', interval '7 days', if_not_exists => true);
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS audit_log, reports, run_events, run_metrics, runs, scenario_versions, scenarios,
    secrets, targets, projects, api_tokens, sessions, memberships, users, orgs CASCADE;
