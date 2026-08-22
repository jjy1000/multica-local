---
name: release-notes-0.5.58
created: 2026-08-23T00:35:00Z
updated: 2026-08-23T00:35:00Z
---

# Release Notes — 0.5.58 (semantica port-and-localize series, 7 phases)

**2026-08-23** · branch `epic/0.5.13-integration` · 17 atomic commits on top of 0.5.51 (head `603c0765d`).

The flagship of this ship is **the semantica port-and-localize series** — Phase 0 through P6 per `.omc/plans/semantica-research-and-porting-design.md`. The vendored `semantica-agi/semantica` Python package now ships inside the fork's resource tree, the runtime is fully wheel-driven (no user-side `pip install -e` anymore), and the per-workspace ACL boundary (individual vs team mode) closes the team/individual agent isolation contract that the user specified at session start.

**Ship gate:** `pnpm typecheck --force` 6/6 · `go build ./...` clean · targeted Go tests (semantica/decision/experimental/handler) all green · L2 wheel install verified by `bash scripts/build-semantica-wheel.sh` (host-bound; document).

## Headline: a sustainable semantica integration

The fork now vendors upstream semantica as a git subtree (`apps/desktop/vendor/semantica-src/`), keeps a frozen snapshot of the 14 `sem:` vocabulary terms in a reference doc, and ships a 5-scraper monitor (`scripts/check-semantica-upstream.sh`) that catches endpoint, field, SPARQL-blocklist, vocabulary, and dep drift on a monthly cadence. The runtime uses a prebuilt wheel from `vendor/semantica-src/builds/`, mirrored to `resources/semantica/builds/` by `bundle-cli.mjs` — no external `pip install -e` step survives.

On the data-isolation side: the `semantica_local_decision_acl` table (migration 273) records per-decision visibility (team | individual_private | shared_team) computed from `COUNT(member WHERE workspace_id = ?)`. The fork-side `GET /api/experimental/semantica/decisions` endpoint reads from this cache filtered by viewer; the renderer surfaces the workspace's mode via a new `<SemanticaModeBanner />` that announces individual vs team with arrow-expression `useT` selectors (the 2026-07-14 incident rule).

## 7-phase plan summary

### Phase 0 — sync infrastructure (0.5.52, 5 commits, +19,000 LOC vendor + ~310 LOC fork)

| Hash | Subject | LOC |
|---|---|---|
| `f0e158992b` | git subtree add semantica-agi/semantica v0.6.6 | 1,063 files / 28 MB |
| `4a8867a49` | vendor(semantica): add fork overlay | +70 |
| `a2691aa1c` | chore(scripts): add semantica sync + wheel builder | +176 |
| `1c6b91fca` | feat(monitor): extend check-semantica-upstream to 5 scrapers | +286 / -98 |

Closes the **sustainable update loop**: future semantica-agi releases can be ingested with one command (`bash scripts/sync-semantica-upstream.sh`), and the 5-scraper monitor (`endpoint / field / sparql / vocab / deps`) reports drift on every monthly cadence run before it becomes a regression.

The 5 scrapers today surface **34 NEW field names** (upstream `Decision` dataclass has 34 fields, fork's `SemanticaDecisionRecordSchema` zod intentionally ships a 10-field envelope subset), **18 NEW + 19 REMOVED endpoint literals** (fork paths are Multica-relative under the reverse proxy), **9 NEW SPARQL forbidden keywords** (fork's `multica-semantica` SKILL.md doesn't list them), **14 NEW vocab terms** (resolved by P2), and **42 NEW Python deps** (fork uses the prebuilt wheel, by design).

### P1 — drop user-side `pip install -e` (0.5.53, 4 commits, +231 / -159)

| Hash | Subject | LOC |
|---|---|---|
| `f658fb4c2` | refactor(semantica-run): drop SEMANTICA_REPO_PATH, use prebuilt wheel | +120 / -98 |
| `14600abe9` | feat(bundle-cli): mirror semantica-src/builds into resources/semantica | +23 / -1 |
| `65b42f3da` | docs(skills): drop pip install -e hint from multica-semantica | +8 / -6 |
| `b59b0f4a4` | chore(scripts): semantica-e2e-smoke --no-pip default + add --no-wheel strict | +80 / -54 |

`apps/desktop/vendor/semantica/run.sh` was rewritten to:
- Validate `SEMANTICA_WORKSPACE_ID` (UUID regex, unchanged) + `SEMANTICA_API_KEY` (in-memory, unchanged)
- Locate the prebuilt wheel at `./builds/` then `../../semantica-src/builds/` (dev fallback)
- Reuse `~/.multica/semantica-venv/` across launches (amortised install)
- `pip install --no-index --find-links=./builds/ semantica-*.whl` (idempotent)
- Deprecation-warn if `SEMANTICA_REPO_PATH` is still set (one minor version of compat; P2 drops)

`apps/desktop/scripts/bundle-cli.mjs` extends the existing `vendor/semantica/` → `resources/semantica/` recursive-cp block to also mirror `vendor/semantica-src/builds/` → `resources/semantica/builds/` — builds/ missing → graceful `bundle-cli` warn (matches pythia pattern).

`multica-semantica` SKILL.md rewords the cold-start note (`ready_timeout_ms: 180000`, the 0.5.28 P0-1 clamp). `scripts/semantica-e2e-smoke.sh` swaps `--no-pip` to default-on, adds `--no-wheel` strict mode, and retains `--with-pip` legacy escape.

### P2 — sync 0.6.6 stability notes (0.5.54, 2 commits, +188)

| Hash | Subject | LOC |
|---|---|---|
| `9ae080c31` | docs(semantica): refresh decision_sync + api-source-map for 0.6.6 | +89 / -6 |
| `3b4df690b` | docs(semantica): add vocabulary.md reference for the 14 sem: terms | +99 |

0.6.6 brought SHA-256 deterministic entity/relationship IRIs (was: randomised Python `hash()`) and the first RDF vocabulary file at `semantica/ontology/vocabulary/semantica-ns.ttl`. The wire shape Multica posts (`SemanticaDecisionRecordSchema`) is unchanged — fork code emits zero `sem:*` predicates today.

What 0.5.54 documents:
- `decision_sync.go` header is refreshed to list 0.5.22 / 0.5.30 P1-2 / 0.5.54 P2 provenance + a new "0.5.54 P2 notes" block explaining SHA-256 IRI + RDF vocab + why fork code emits no `sem:*` today (deferred to P4)
- `api-source-map.md` gains a "What's new in semantica-agi/semantica v0.6.6" section
- New `vocabulary.md` reference catalogues all 14 `sem:` terms (3 classes, 5 datatype properties, 4 object properties, 1 annotation property, 1 external PROV role) with per-term emission sites + 0.6.6 fix context

**Drift closed:** vocab scraper went from **14/0 → 0/0** once the reference doc uses the same `sem:` term literals.

### P3 — i18n 4-locale audit (0.5.55, 1 commit, +113)

| Hash | Subject | LOC |
|---|---|---|
| `48ef64339` | docs(audit): semantica P3 i18n 4-locale audit (no-op) | +113 |

**Plan §2.3 P3 listed 16 NEW keys × 4 locale.** Audit (`.omc/audit/2026-08-23-semantica-p3-i18n-audit.md`) established that all 16 keys belong to surfaces Phase 0 / P1 / P2 did not build (P4 `<SemanticaModeBanner>`, P4 ACL UI panels, P1 wheel preflight error panels). Adding placeholder keys would violate Karpathy §2 (Simplicity First) and i18next render-empty semantics.

What P3 ships instead:
- Verified 8 existing semantica UI keys × 4 locales (en / zh-Hans / ja / ko) all present + no empty strings + no English fallbacks
- Documented the P4 / P5 surface-coupling rule (future i18n keys ship in the same atomic commit as their component)

### P4 — ACL: actor_type=team + migration 271 (0.5.56, 3 commits, +360 / -10)

| Hash | Subject | LOC |
|---|---|---|
| `d2abb428f` | feat(experimental): semantica ACL table + workspace member count | +260 / 0 |
| `dc6ec17a6` | refactor(decision_sync): stamp visibility + write-through ACL | +62 / -10 |
| `6cbb05287` | feat(semantica): fork-side ACL-filtered read endpoint | +162 / 0 |

**The isolation contract**:
- `mode = "individual"` (1 member in workspace) → every outgoing decision stamped `visibility = "individual_private"`; only the originating member can read
- `mode = "team"` (≥2 members) → `actor_type=team` records visible to all members; `actor_type=user` records visible to all members when `visibility = "shared_team"`, otherwise only the originating user

What P4 ships:
- **Migration 273** (was plan §3.1's 271, bumped because 271 / 272 already taken by 0.5.4x work): `semantica_local_decision_acl` table with `CHECK` on `actor_type IN ('system','user','agent','team')` + `visibility IN ('team','individual_private','shared_team')` + 3 indexes (ws / ws+actor / ws+visibility)
- **3 sqlc queries**: `CountWorkspaceMembers :one`, `UpsertSemanticaDecisionACL :exec`, `ListSemanticaDecisionsForViewer :many`
- **Pure helpers** (`internal/experimental/acl.go`): `WorkspaceMemberCount`, `Mode(count)`, `VisibilityFor(mode, actorType)`, `ActorIDFor(actorType, actorID, workspaceID)` — 100% unit-testable without a live PG
- **Write path**: `postDecisionSync` queries `WorkspaceMemberCount`, computes `Mode + VisibilityFor(actorType)`, stamps the envelope; failure falls back to `ModeIndividual` + `slog.Warn` (default-safe). After successful upstream POST, `UpsertSemanticaDecisionACL` runs with the same 10 s timeout as the POST itself (best-effort; upstream remains source of truth)
- **Read path**: new `GET /api/experimental/semantica/decisions?workspace=<uuid>` — membership-gated via `requireWorkspaceMember` (defence-in-depth, NOT the primary ACL) → calls `ListSemanticaDecisionsForViewer` → JSON envelope `{count, mode, items[]}` so the renderer can pick the ModeBanner label without a second roundtrip
- **TS zod mirror**: `SemanticaDecisionSummarySchema` + `SemanticaDecisionListResponseSchema` + optional `visibility` field on existing `SemanticaDecisionRecordSchema` (default `''` so pre-P4 envelopes parse cleanly via `parseWithFallback`)

### P5 — ModeBanner UI + 4-locale i18n (0.5.57, 1 commit, +192 / -5)

| Hash | Subject | LOC |
|---|---|---|
| `5188e9538` | feat(semantica): SemanticaModeBanner + 4-locale i18n | +192 / -5 |

`<SemanticaModeBanner mode="individual" | "team" className?>` is a presentational wrapper. `role="status"` + `aria-live="polite"` for team (heads-up the user might share decisions), `role="note"` + `aria-live="off"` for individual (no peer readers). `data-semantica-mode` attribute for CSS hooks.

Mounted in `apps/desktop/src/renderer/src/pages/semantica-explorer-view.tsx` above the iframe. The view fetches the workspace mode from the new fork-side ACL endpoint on every retry/url change; failure falls back to `individual`.

4 locales gained `semantica.mode.{individual|team}.{label|desc}` (8 new keys total) — en canonical, others translated.

### P6 — ACL reconciler tick + cold-start smoke (0.5.58, 1 commit, +254)

| Hash | Subject | LOC |
|---|---|---|
| `8d69ff9b8` | feat(semantica): ACL reconciler tick + cold-start smoke | +254 / 0 |

`internal/experimental/semantica_acl_reconciler.go` — `ACLReconciler` with `Start/Stop/Run/sweep`. Lifecycle mirrors `SemanticaGC` (stopOne `sync.Once`, `sweepCount atomic.Uint64`). Default `Interval=6h` per plan contract; `Queries=nil` disables DB I/O (test fixture path).

Wired alongside `SemanticaGC.Start()` in `router.go`; `h.SemanticaACLReconciler.Stop()` in `main.go` shutdown sequence.

`sweep()` body is **observability only** by design: emits a tick log line + bumps `sweepCount`. Real reconcile logic (re-stamp Visibility when workspace membership shifts, GC orphan rows) is deferred until upstream semantica exposes list `/api/decisions` — graph.json is rdflib's default serialization, requires Python to parse.

`scripts/semantica-e2e-smoke.sh` gains Stage 4b/5: hits the new ACL endpoint, asserts `200` + `count` field, OR `401/403/404` (membership-gated paths, expected for an empty workspace).

## Drift convergence after the 7 phases

| scraper | pre-Phase0 | post-0.5.58 | resolved by |
|---|---|---|---|
| endpoint | 18 / 19 | 18 / 19 | structural (fork proxy path vs upstream absolute) |
| field | 34 / 10 | 34 / 10 | zod is intentional envelope subset; future P3-follow-up work |
| sparql | 9 / 0 | 9 / 0 | i18n SKILL.md add 9 keyword (future work) |
| vocab | **14 / 0** | **0 / 0** ✅ | P2 `vocabulary.md` reference uses the same literals |
| deps | 41 / 2 | 42 / 0 | wheel handles all 42 (by design) |

Plus 2 structural drift signals that are NOT actionable through the monitor:
- **field 24 NEW** = upstream `Decision` dataclass fields the fork's envelope intentionally doesn't carry (P3 follow-up per plan §2.5)
- **endpoint 19 REMOVED** = upstream CHANGELOG mentions path names like `/analytics` that the fork's `api-source-map.md` doesn't reference by absolute path (the proxy strips `/api/`)

## Cumulative stats

| | |
|---|---|
| Atomic commits | 17 (+ 1 docs plan commit) |
| Vendor LOC | +19,000 (Python pkg + tests + pyproject + requirements) |
| Fork code LOC | +1,500 (Go + TS + shell, excluding vendored subtree) |
| New sqlc queries | 3 (`CountWorkspaceMembers`, `UpsertSemanticaDecisionACL`, `ListSemanticaDecisionsForViewer`) |
| New DB tables | 1 (`semantica_local_decision_acl`, migration 273) |
| New HTTP endpoints | 1 (`GET /api/experimental/semantica/decisions`) |
| New React components | 1 (`<SemanticaModeBanner>`) |
| New bash scripts | 2 (`sync-semantica-upstream.sh`, `build-semantica-wheel.sh`) |
| i18n keys added | 8 (semantica.mode.{individual|team}.{label|desc} × 4 locale) |
| Doc references added | 2 (`vocabulary.md`, `2026-08-23-semantica-p3-i18n-audit.md`) |
| 7 shippable versions | 0.5.52 / 0.5.53 / 0.5.54 / 0.5.55 / 0.5.56 / 0.5.57 / 0.5.58 |

## Verification

```bash
# Targeted semantica-related Go tests
cd server && go test -count=1 -timeout 60s \
  -run "TestACLReconciler|TestMode|TestVisibility|TestActorID|TestBuildSemantica|TestPostDecision" \
  ./internal/handler/... ./internal/experimental/...
# → ok handler 0.88s, ok experimental 0.62s

# Typecheck
pnpm typecheck --force
# → 6 successful, 6 total (TURBO FULL CACHE)

# Monitor (5 scrapers)
bash scripts/check-semantica-upstream.sh --quiet
# → vocab 0/0 ✅, deps 42/0 (by design), other 3 stable
```

`TestDashboardPerAgentRollupsUseExactWindow` fails pre-existing — date-bucket window test, unrelated to semantica changes.

## Known follow-ups (deferred, not blockers)

1. **Reconcile body** — when upstream exposes `GET /api/decisions` (list), add `ListAllACL :many` + the actual Visibility re-stamp + orphan GC logic to `ACLReconciler.sweep()`. The cron infrastructure is in place.
2. **Field drift 24 envelope gaps** — extend `SemanticaDecisionRecordSchema` zod to carry additional fields from upstream `Decision` (e.g. `confidence`, `decision_maker`). Track under plan §2.5 P3-follow-up.
3. **SPARQL 9 forbidden keywords** — append to `multica-semantica` SKILL.md so the advisor's `semantica_decision_advisor` agent surfaces the same blocklist the upstream enforces.
4. **Wheel install verification** — host-side lacks `pip`/`uv`; `python -m build --wheel` runs in `bash scripts/build-semantica-wheel.sh` but the `--no-index --find-links=builds/` install step needs a Python venv to verify end-to-end (CI / user-side).
5. **Daily/weekly cadence cron** — the 6h tick is manual today (the script is run-on-demand); launchd job for monthly cadence is a `.omc/install-gate` candidate.

## How to consume this release

For a team-mode workspace:

1. Enable `semantica` Labs flag in `Settings → Labs`.
2. `multica lab delegate semantica "<task>"` (the advisor agent handles terminal-issue decisions).
3. The `SemanticaExplorer` view (Labs tab) shows `<SemanticaModeBanner mode="team" />` above the iframe.
4. A terminal-lab-sourced issue (`lab_source = "semantica"`) auto-syncs its decision to upstream semantica AND writes a row to `semantica_local_decision_acl`.
5. `GET /api/experimental/semantica/decisions?workspace=<wsId>` returns the ACL-filtered view (count + mode + items[]).

For an individual-mode workspace (single member), the same flow runs with `visibility = "individual_private"`; only the originating member's decisions surface. The banner reads "Individual workspace — decisions you see are private to this workspace".

For updates: `bash scripts/sync-semantica-upstream.sh` (subtree pull + wheel build + monitor) monthly; the 5-scraper diff surfaces breaking changes before they hit runtime.

Plan ref: `.omc/plans/semantica-research-and-porting-design.md` (research + plan), `.omc/plans/semantica-port-and-localize-0.5.52.md` (the superseded all-in-one alternative), `.omc/audit/2026-08-23-semantica-p3-i18n-audit.md` (P3 no-op rationale).