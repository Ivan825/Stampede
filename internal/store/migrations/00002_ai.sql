-- +goose Up

-- AI journey generation is optional and bring-your-own-key. Provider keys
-- use the same envelope encryption as secrets and are never returned.
CREATE TABLE ai_providers (
    id                uuid PRIMARY KEY,
    org_id            uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    name              text NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('anthropic', 'openai', 'gemini', 'ollama', 'openai-compatible')),
    model             text NOT NULL,
    base_url          text NOT NULL DEFAULT '',
    ciphertext        bytea,
    wrapped_key       bytea,
    key_id            text,
    monthly_token_cap bigint NOT NULL CHECK (monthly_token_cap > 0),
    created_by        uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);

-- Generation jobs. Inputs (descriptions, specs, HAR files, logs) are kept in
-- memory only; the row records their kinds and sizes. Results hold redacted
-- dry-run traces. Jobs do not survive a restart: unfinished ones are marked
-- failed when the server starts.
CREATE TABLE ai_jobs (
    id                   uuid PRIMARY KEY,
    org_id               uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    project_id           uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    provider_id          uuid REFERENCES ai_providers (id) ON DELETE SET NULL,
    provider_kind        text NOT NULL,
    model                text NOT NULL,
    status               text NOT NULL CHECK (status IN ('queued', 'running', 'succeeded', 'needs_review', 'failed')),
    stage                text NOT NULL DEFAULT '',
    round                integer NOT NULL DEFAULT 0,
    target_id            uuid REFERENCES targets (id) ON DELETE SET NULL,
    scenario_id          uuid REFERENCES scenarios (id) ON DELETE SET NULL,
    dry_run              boolean NOT NULL,
    inputs               jsonb NOT NULL DEFAULT '{}',
    yaml                 text NOT NULL DEFAULT '',
    diff                 text NOT NULL DEFAULT '',
    result               jsonb NOT NULL DEFAULT '{}',
    error                text NOT NULL DEFAULT '',
    input_tokens         bigint NOT NULL DEFAULT 0,
    output_tokens        bigint NOT NULL DEFAULT 0,
    created_by           uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    finished_at          timestamptz,
    approved_at          timestamptz,
    approved_scenario_id uuid REFERENCES scenarios (id) ON DELETE SET NULL,
    approved_version     integer
);
CREATE INDEX ai_jobs_project_created_idx ON ai_jobs (project_id, created_at DESC);
CREATE INDEX ai_jobs_org_created_idx ON ai_jobs (org_id, created_at);
CREATE INDEX ai_jobs_active_idx ON ai_jobs (status) WHERE status IN ('queued', 'running');

-- +goose Down
DROP TABLE IF EXISTS ai_jobs, ai_providers;
