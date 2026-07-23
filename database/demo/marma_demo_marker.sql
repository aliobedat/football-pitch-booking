-- WO-DEMO-02: manual, Demo-database-ONLY marker.
--
-- Install this ONCE, by hand, against the dedicated Demo Neon database only.
-- NEVER apply this to Production. It is deliberately NOT part of
-- migrations/, database/schema.sql, or application startup — backend/cmd/demo-reset
-- refuses to run (guard 4) unless it finds exactly this row, so the marker is
-- what actually proves a DATABASE_URL points at Demo before any DELETE runs.
--
--   psql "$DEMO_DATABASE_URL" -f database/demo/marma_demo_marker.sql
--
-- Idempotent: safe to re-run.

CREATE TABLE IF NOT EXISTS marma_demo_marker (
    marker text PRIMARY KEY
);

INSERT INTO marma_demo_marker (marker)
VALUES ('MARMA_DEMO_DATABASE_ONLY')
ON CONFLICT (marker) DO NOTHING;
