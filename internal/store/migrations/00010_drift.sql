-- +goose Up
-- Schedules of kind 'drift' do not start load: on their cron they dry-run
-- the scenario's journeys against the target (one user, each journey
-- once) and, when spec_url is set, compare the scenario with the API's
-- current OpenAPI document. Each check is recorded in drift_results.
ALTER TABLE schedules ADD COLUMN kind text NOT NULL DEFAULT 'run' CHECK (kind IN ('run', 'drift'));
ALTER TABLE schedules ADD COLUMN spec_url text NOT NULL DEFAULT '';

CREATE TABLE drift_results (
    id               uuid PRIMARY KEY,
    project_id       uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    schedule_id      uuid REFERENCES schedules (id) ON DELETE SET NULL,
    scenario_id      uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    scenario_version integer NOT NULL,
    target_id        uuid NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    status           text NOT NULL CHECK (status IN ('ok', 'drifted', 'error')),
    error            text NOT NULL DEFAULT '',
    -- Journeys that failed their dry run or call endpoints the API no
    -- longer has.
    broken           text[] NOT NULL DEFAULT '{}',
    -- Redacted dry-run traces, the spec diff and unmatched requests.
    result           jsonb NOT NULL DEFAULT '{}',
    -- The endpoints of the spec fetched for this check, which the next
    -- check of the same schedule diffs against.
    spec             jsonb,
    -- The AI repair job started from this result, if any.
    repair_job_id    uuid REFERENCES ai_jobs (id) ON DELETE SET NULL,
    created_by       uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX drift_results_project_idx ON drift_results (project_id, created_at DESC);
CREATE INDEX drift_results_schedule_idx ON drift_results (schedule_id, created_at DESC);

ALTER TABLE schedules ADD COLUMN last_drift_id uuid REFERENCES drift_results (id) ON DELETE SET NULL;

-- +goose Down
-- Without the kind column a drift schedule would start load: remove them.
DELETE FROM schedules WHERE kind = 'drift';
ALTER TABLE schedules DROP COLUMN last_drift_id;
DROP TABLE drift_results;
ALTER TABLE schedules DROP COLUMN spec_url;
ALTER TABLE schedules DROP COLUMN kind;
