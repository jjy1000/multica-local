-- 273_semantica_local_decision_acl.up.sql
-- 0.5.56 P4 (semantica port-and-localize): per-decision ACL index for
-- fork-side filtering. Storage remains per-workspace
-- (~/.multica/workspaces/<wsId>/semantica-graph.json, 0.5.29 P0-2);
-- this table is the FORK-SIDE cache that the new
-- GET /api/experimental/semantica/decisions endpoint reads from so the
-- renderer can show team-shared vs individual-private decisions without
-- round-tripping the upstream subprocess.
--
-- Why a fork-side index and not just rely on upstream semantica:
--   - The upstream subprocess stores decisions in semantica.decisions,
--     filtered only by X-API-Key (single shared secret per workspace).
--   - Per-actor_type ACL ("team sees all team, individual sees own") is a
--     Multica-fork concept that doesn't exist in semantica-agi upstream.
--   - Caching the (decision_id, workspace_id, actor_type, actor_id,
--     visibility) tuple here lets the fork filter at query time
--     without re-reading graph.json + a per-decision check.
--
-- Mode detection (not stored here — computed at write time):
--   - mode = "team"        when COUNT(member WHERE workspace_id = ?) >= 2
--   - mode = "individual"  when COUNT(...) = 1
--   - The actor_type='team' envelope is used for shared records
--     written BY THE SYSTEM on first bind (see install_semantica.go).
--
-- actor_type CHECK matches the existing fork convention
-- (server/internal/handler/decision_sync.go:125 — 'system' | 'user' |
-- 'agent'), extended with 'team' for P4.
CREATE TABLE semantica_local_decision_acl (
  decision_id   TEXT PRIMARY KEY,         -- upstream semantica.decisions id; == "multica_<issue_uuid>"
  workspace_id  UUID NOT NULL,
  actor_type    TEXT NOT NULL CHECK (actor_type IN ('system','user','agent','team')),
  actor_id      TEXT NOT NULL,             -- UUID-as-text; for actor_type='team' the literal workspace_id is used
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  visibility    TEXT NOT NULL CHECK (visibility IN ('team','individual_private','shared_team'))
);

CREATE INDEX semantica_local_acl_ws ON semantica_local_decision_acl (workspace_id);
CREATE INDEX semantica_local_acl_actor ON semantica_local_decision_acl (workspace_id, actor_type, actor_id);
CREATE INDEX semantica_local_acl_visibility ON semantica_local_decision_acl (workspace_id, visibility);
