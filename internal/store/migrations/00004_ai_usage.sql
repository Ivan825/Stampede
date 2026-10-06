-- +goose Up
-- AI calls outside generation jobs (report narratives). Their tokens count
-- towards the monthly cap together with ai_jobs.
CREATE TABLE ai_usage (
    id            uuid PRIMARY KEY,
    org_id        uuid NOT NULL REFERENCES orgs (id) ON DELETE CASCADE,
    provider_id   uuid REFERENCES ai_providers (id) ON DELETE SET NULL,
    purpose       text NOT NULL,
    run_id        uuid REFERENCES runs (id) ON DELETE SET NULL,
    input_tokens  bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    created_by    uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ai_usage_org_created ON ai_usage (org_id, created_at);

-- +goose Down
DROP TABLE ai_usage;
