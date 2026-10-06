-- +goose Up
-- Integrations can point at a stampede agent for fault injection.
ALTER TABLE integrations DROP CONSTRAINT integrations_kind_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_kind_check CHECK (kind IN ('prometheus', 'traces', 'agent'));

-- +goose Down
DELETE FROM integrations WHERE kind = 'agent';
ALTER TABLE integrations DROP CONSTRAINT integrations_kind_check;
ALTER TABLE integrations ADD CONSTRAINT integrations_kind_check CHECK (kind IN ('prometheus', 'traces'));
