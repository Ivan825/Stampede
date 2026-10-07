-- +goose Up
-- Optional per-project role overrides. A member's organisation role
-- applies to every project unless a row here sets a different role for
-- one project, higher or lower. Owners keep the owner role everywhere.
CREATE TABLE project_roles (
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('admin', 'editor', 'runner', 'viewer')),
    created_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_roles_user_idx ON project_roles (user_id);

-- +goose Down
DROP TABLE project_roles;
