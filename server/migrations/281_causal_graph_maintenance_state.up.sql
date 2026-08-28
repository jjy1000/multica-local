-- 281_causal_graph_maintenance_state.up.sql
-- 0.5.84 P0 fix batch: DB-anchored maintenance ticker (causal_graph).
--
-- The maintenance ticker (server/internal/service/causal_graph/maintenance.go)
-- previously used a bare time.NewTicker(m.cfg.Interval) with NO DB anchor —
-- every daily desktop restart reset the ticker to "now", so under
-- restart-heavy sessions the 24h cadence could elapse >72h between actual
-- sweeps and stale nodes accumulated indefinitely. The 0.5.83 audit
-- (`6 confirmed bugs`, bug #4) flagged this as the "30-day stale TTL hides
-- old chains" gap.
--
-- Fix: a singleton row carries the last sweep timestamp. Start() reads it
-- and, if (now - last_sweep) >= Interval, runs sweepOnce immediately before
-- resuming the ticker; every sweep() call also updates the timestamp so a
-- daily restart mid-cycle does not double-sweep. Forward-only additive —
-- no existing tables / columns touched.

CREATE TABLE IF NOT EXISTS causal_graph_maintenance_state (
    id            BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    last_sweep_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch'::timestamptz,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed the singleton row (id = TRUE is the conventional singleton key).
INSERT INTO causal_graph_maintenance_state (id, last_sweep_at, updated_at)
VALUES (TRUE, 'epoch'::timestamptz, NOW())
ON CONFLICT (id) DO NOTHING;
