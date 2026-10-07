-- +goose Up
-- 10-second and 1-minute rollups of the per-second run metrics, for long
-- runs and for keeping history after per-second rows expire
-- (stampede server --metrics-retention).
--
-- With TimescaleDB they are continuous aggregates, refreshed every minute
-- and kept when per-second rows are dropped. On plain PostgreSQL they are
-- ordinary views with the same columns, computed from the per-second rows
-- when read (so they hold only what those rows still hold).
--
-- error_rate is not stored: divide failed by requests. Latency columns are
-- the mean (p50) or the worst (p95, p99) of the per-second values in the
-- bucket; exact quantiles stay in the run's report.
-- +goose StatementBegin
DO $$
DECLARE
    body text := $q$
        SELECT run_id, %s AS bucket,
               sum(requests)::bigint AS requests, sum(failed)::bigint AS failed,
               avg(rps) AS rps, avg(p50) AS p50, max(p95) AS p95, max(p99) AS p99,
               max(vus) AS vus, avg(planned) AS planned, sum(dropped)::bigint AS dropped,
               sum(iterations)::bigint AS iterations, max(sched_lag) AS sched_lag, count(*) AS samples
        FROM run_metrics
        GROUP BY run_id, bucket $q$;
    continuous boolean := false;
BEGIN
    -- Nested checks: the TimescaleDB catalog exists only with the extension.
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        IF EXISTS (SELECT 1 FROM timescaledb_information.hypertables WHERE hypertable_name = 'run_metrics') THEN
            continuous := true;
        END IF;
    END IF;
    IF continuous THEN
        EXECUTE 'CREATE MATERIALIZED VIEW run_metrics_10s WITH (timescaledb.continuous) AS '
            || format(body, 'time_bucket(INTERVAL ''10 seconds'', ts)') || ' WITH NO DATA';
        EXECUTE 'CREATE MATERIALIZED VIEW run_metrics_1m WITH (timescaledb.continuous) AS '
            || format(body, 'time_bucket(INTERVAL ''1 minute'', ts)') || ' WITH NO DATA';
        PERFORM add_continuous_aggregate_policy('run_metrics_10s',
            start_offset => INTERVAL '1 day', end_offset => INTERVAL '10 seconds', schedule_interval => INTERVAL '1 minute');
        PERFORM add_continuous_aggregate_policy('run_metrics_1m',
            start_offset => INTERVAL '1 day', end_offset => INTERVAL '1 minute', schedule_interval => INTERVAL '1 minute');
    ELSE
        EXECUTE 'CREATE VIEW run_metrics_10s AS '
            || format(body, 'to_timestamp(floor(extract(epoch FROM ts) / 10) * 10)');
        EXECUTE 'CREATE VIEW run_metrics_1m AS '
            || format(body, 'to_timestamp(floor(extract(epoch FROM ts) / 60) * 60)');
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb') THEN
        IF EXISTS (SELECT 1 FROM timescaledb_information.continuous_aggregates WHERE view_name = 'run_metrics_1m') THEN
            DROP MATERIALIZED VIEW run_metrics_1m;
        END IF;
        IF EXISTS (SELECT 1 FROM timescaledb_information.continuous_aggregates WHERE view_name = 'run_metrics_10s') THEN
            DROP MATERIALIZED VIEW run_metrics_10s;
        END IF;
    END IF;
    DROP VIEW IF EXISTS run_metrics_1m;
    DROP VIEW IF EXISTS run_metrics_10s;
END
$$;
-- +goose StatementEnd
