-- +goose Up
-- Real-time rollups: with TimescaleDB, reading run_metrics_10s and
-- run_metrics_1m also aggregates the per-second rows not yet materialised,
-- so a run's rollups are complete while it runs and right after it ends,
-- not only once the refresh policy has caught up. Plain PostgreSQL views
-- are always computed when read.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        IF EXISTS (SELECT 1 FROM timescaledb_information.continuous_aggregates WHERE view_name = 'run_metrics_10s') THEN
            ALTER MATERIALIZED VIEW run_metrics_10s SET (timescaledb.materialized_only = false);
        END IF;
        IF EXISTS (SELECT 1 FROM timescaledb_information.continuous_aggregates WHERE view_name = 'run_metrics_1m') THEN
            ALTER MATERIALIZED VIEW run_metrics_1m SET (timescaledb.materialized_only = false);
        END IF;
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        IF EXISTS (SELECT 1 FROM timescaledb_information.continuous_aggregates WHERE view_name = 'run_metrics_10s') THEN
            ALTER MATERIALIZED VIEW run_metrics_10s SET (timescaledb.materialized_only = true);
        END IF;
        IF EXISTS (SELECT 1 FROM timescaledb_information.continuous_aggregates WHERE view_name = 'run_metrics_1m') THEN
            ALTER MATERIALIZED VIEW run_metrics_1m SET (timescaledb.materialized_only = true);
        END IF;
    END IF;
END
$$;
-- +goose StatementEnd
