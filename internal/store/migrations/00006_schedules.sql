-- +goose Up
-- Schedules start runs on a cron expression. The scheduler on the active
-- replica claims a due schedule by moving next_run_at forward in a single
-- conditional UPDATE, so a firing is never started twice.
CREATE TABLE schedules (
    id               uuid PRIMARY KEY,
    project_id       uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name             text NOT NULL,
    scenario_id      uuid NOT NULL REFERENCES scenarios (id) ON DELETE CASCADE,
    target_id        uuid NOT NULL REFERENCES targets (id) ON DELETE CASCADE,
    cron             text NOT NULL,
    timezone         text NOT NULL DEFAULT 'UTC',
    overrides        jsonb NOT NULL DEFAULT '{}',
    env              jsonb NOT NULL DEFAULT '{}',
    workers          integer NOT NULL DEFAULT 0,
    enabled          boolean NOT NULL DEFAULT true,
    note             text NOT NULL DEFAULT '',
    -- Runs start as this user: whoever created or last edited the schedule.
    owner_id         uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    -- NULL while disabled.
    next_run_at      timestamptz,
    last_fired_at    timestamptz,
    last_run_id      uuid REFERENCES runs (id) ON DELETE SET NULL,
    -- Why the last firing did not start a run; empty when it did.
    last_skip_reason text NOT NULL DEFAULT '',
    UNIQUE (project_id, name)
);
CREATE INDEX schedules_due_idx ON schedules (next_run_at) WHERE enabled;

-- +goose Down
DROP TABLE schedules;
