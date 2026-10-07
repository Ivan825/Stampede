-- +goose Up
-- Hard caps per organisation and per project, set by admins. A run must
-- fit within every cap that applies: the server's, the organisation's,
-- the project's and the target's. NULL means no cap at that level.
CREATE TABLE org_caps (
    org_id         uuid PRIMARY KEY REFERENCES orgs (id) ON DELETE CASCADE,
    max_rate       double precision CHECK (max_rate > 0),
    max_vus        integer CHECK (max_vus > 0),
    max_duration_s integer CHECK (max_duration_s > 0),
    updated_by     uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

-- Project settings: caps, and whether every run must first pass a dry run
-- (each journey once with one user) before load starts.
CREATE TABLE project_settings (
    project_id      uuid PRIMARY KEY REFERENCES projects (id) ON DELETE CASCADE,
    max_rate        double precision CHECK (max_rate > 0),
    max_vus         integer CHECK (max_vus > 0),
    max_duration_s  integer CHECK (max_duration_s > 0),
    require_dry_run boolean NOT NULL DEFAULT false,
    updated_by      uuid REFERENCES users (id) ON DELETE SET NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE project_settings, org_caps;
