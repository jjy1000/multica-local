# Semantica upstream watch — long-cadence tracking

> **Source-of-record:** this file. Created 2026-08-17 (Synthesizer Round 7 plan item C).
> **Companion script:** `scripts/check-semantica-upstream.sh` (re-runnable delta diff).
> **Hand-off target:** `.omc/issues/semantica-upstream-delta-<date>.md` (created on every delta).
> **Cadence:** monthly (user-chosen; manual or launchd — see "Cadence options" below).

## Why this exists

Semantica is a vendored Python service (`apps/desktop/vendor/semantica/`). The
fork integrates it via the `semantica` Labs flag + a Multica reverse proxy
mount at `/experimental/semantica/api/*`. The fork-local schema (decision
records, ontology alignment shapes, graph envelopes) is mirrored 1:1 between
Go (`server/internal/handler/decision_sync.go::semanticaDecision`) and TS
(`packages/core/api/schemas.ts::SemanticaDecisionRecordSchema`). When upstream
adds endpoints, renames fields, or ships a new ontology vocabulary, the fork
silently misses the new surface until a regression breaks — pre-0.5.30 this
went undetected for ≥1 cycle (R6 confirmed `vendor/semantica/` lacks the
referenced `decisions.py`).

This file is the **planning + cadence** layer; `scripts/check-semantica-upstream.sh`
is the **mechanical diff** layer. Together they close the gap before it becomes
a regression.

## Sources (in priority order)

1. **`semantica-agi/semantica` CHANGELOG.md** — primary signal. Fetched via
   `scripts/check-semantica-upstream.sh`. Endpoint-shape deltas (additions,
   deprecations, breaking field changes) are usually called out in the
   `### Added` / `### Changed` / `### Removed` sections.
2. **`semantica-agi/semantica` explorer openapi** — secondary signal. The
   `explorer/openapi.yaml` (or `.json`) is the canonical endpoint inventory.
   Fetched on demand by the reviewer; not auto-clobbered by the script (CHANGELOG
   covers the "what's new" surface; openapi is for completeness audit when a
   delta is detected).
3. **Fork-local baseline:** `server/internal/service/builtin_skills/multica-semantica-decision-advisor/references/api-source-map.md`
   is the source-of-record for which endpoints the fork has wired through.
4. **Fork-local mirror:** `packages/core/api/schemas.ts::SemanticaDecisionRecordSchema`
   + `SemanticaDecisionProvenanceSchema` for the schema-sync half. Any new
   upstream field needs a zod counterpart (mirroring the 0.5.30 P1-2 fix).

## What to watch

The fork-local baseline covers ~50 endpoints across 6 surfaces. Watch
upstream for changes in:

- **`/api/ontology/*`** — SHACL shape additions (new validators), SKOS scheme
  vocabulary, alignment relation types, draft versioning semantics. Highest
  churn surface; expect 1-2 changes/month.
- **`/api/decisions/*`** — new required fields on `POST /api/decisions`,
  new decision-status enum values (`pending_review`, `overridden`, etc.),
  `provenance` envelope field additions. Mirror Go + TS schema on every
  field add (1:1 contract per 0.5.30 P1-2).
- **`/api/graph/*`** — graph envelope shape changes (`nodes` / `edges` /
  `metadata` schema drift), new analytics endpoints, neighborhood vs
  shortest-path return-type changes. Low churn but high blast-radius.
- **`/api/sparql`** — query language version bumps (new SPARQL 1.2 features),
  the safety-net blocklist (`INSERT/DELETE/DROP/LOAD/CLEAR/CREATE/COPY/MOVE/ADD`)
  may need widening if upstream accepts new keywords.
- **Deprecated endpoints** — upstream typically marks `### Deprecated`
  sections 1-2 releases ahead of removal. Plan a 2-cycle migration window.
- **Vector model changes** — embedding dimension, model id, the
  `${MULTICA_RESOURCES_DIR}/semantica-graph.json` schema version field.
  Any dimension change forces a re-embed of every existing decision
  (otherwise cosine similarity returns NaN silently).

## Delta workflow

On every cadence tick (monthly by default):

1. **Run the mechanical diff.** `bash scripts/check-semantica-upstream.sh`
   prints `NEW ENDPOINTS` (upstream-only) + `REMOVED` (fork-only) + a summary.
   Exit 0 = no diff (skip steps 2-4). Exit 1 = network failure (retry next
   tick + open a P2 ticket if persistent).
2. **Read upstream CHANGELOG.** Manually scan the most recent release section
   even if the script reports no endpoint-level diff — the script only
   catches `/api/*` literal patterns, not field-adds or enum-extensions.
3. **Diff against fork-local baselines.** Compare the new endpoints against
   `api-source-map.md`. For each NEW endpoint, classify:
   - **Add-only** (new surface, no fork change needed): note in hand-off,
     skip the PR.
   - **Wire-through** (must be exposed via the Multica proxy mount): file
     `.omc/issues/semantica-upstream-delta-<date>.md` and ship.
   - **Schema-mirror** (POST/PATCH new field): update `semanticaDecision`
     Go struct + zod schema in lock-step, plus the api-source-map.md row.
4. **File the hand-off.** Write `.omc/issues/semantica-upstream-delta-<date>.md`
   (template below). Do NOT auto-open a PR — the user reviews + decides
   which deltas are in-scope vs deferred.
5. **Update the baseline.** If a delta is shipped, commit the updated
   `api-source-map.md` row + the new endpoint verb in the same PR. The next
   run of the script then sees the delta as in-sync.

## Cadence options

### Option A — launchd monthly plist (hands-off)

Suggested calendar: 1st of each month at 09:00 local. Replace `$USER` and
adjust `WorkingDirectory`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.multica.semantica-upstream-watch</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>/Users/$USER/jjy/multica-exploration-dev/scripts/check-semantica-upstream.sh</string>
    <string>--notify</string>
  </array>
  <key>StartCalendarInterval</key>
  <dict>
    <key>Day</key><integer>1</integer>
    <key>Hour</key><integer>9</integer>
    <key>Minute</key><integer>0</integer>
  </dict>
  <key>WorkingDirectory</key>
  <string>/Users/$USER/jjy/multica-exploration-dev</string>
  <key>StandardOutPath</key>
  <string>/tmp/multica-semantica-watch.log</string>
  <key>StandardErrorPath</key>
  <string>/tmp/multica-semantica-watch.err.log</string>
</dict>
</plist>
```

Install with `launchctl load -w ~/Library/LaunchAgents/com.multica.semantica-upstream-watch.plist`.
The `--notify` flag (script-supported) writes a macOS notification on diff.

### Option B — manual checklist (low-friction)

Run once per month before the version-bump cycle:

```
[ ] bash scripts/check-semantica-upstream.sh
[ ] Read the upstream CHANGELOG section referenced in script output
[ ] Diff against server/internal/service/builtin_skills/multica-semantica-decision-advisor/references/api-source-map.md
[ ] If delta: file .omc/issues/semantica-upstream-delta-<date>.md
[ ] If no delta: archive the script output to .omc/semantica-watch-<YYYY-MM>.log
```

## Hand-off template — `.omc/issues/semantica-upstream-delta-<date>.md`

```markdown
# Semantica upstream delta — <YYYY-MM-DD>

> Source: bash scripts/check-semantica-upstream.sh + manual CHANGELOG review.

## Summary

- **Upstream version surveyed:** <tag-or-commit>
- **NEW endpoints (upstream-only):** N
- **REMOVED endpoints (fork-only):** N
- **Field-adds / enum-extensions:** N (manual review only)

## NEW endpoints

| Method | Path | Required fields | Fork-local impact |
|---|---|---|---|
| POST | /api/ontology/shacl/lint | uri, ruleset | add proxy mount line |

## REMOVED endpoints (upstream deprecated)

| Method | Path | Fork replacement | Migration window |
|---|---|---|---|

## Schema drift (manual review)

- `/api/decisions` POST: new field `urgency` (enum low/medium/high)
  - Go: add `Urgency *string` to `semanticaDecision`
  - TS: extend `SemanticaDecisionRecordSchema` zod schema
  - Doc: update api-source-map.md row 38

## Verdict

[ ] Ship (file PR)
[ ] Defer to <version>
[ ] Out-of-scope (note why)
```

## Lessons (forward-looking)

- **The script is a tripwire, not a substitute for reading.** Mechanical
  `/api/*` literal-matching catches endpoint additions but misses field
  adds, enum-extensions, and deprecation timelines. The reviewer must
  read the CHANGELOG section even when the script reports "no diff".
- **Schema-mirror is a 1:1 contract.** 0.5.30 P1-2 closed the
  `decision_sync.go:25-27` dead-reference drift. Any future field add
  MUST touch Go + zod + api-source-map in one PR — partial mirrors
  reintroduce the same silent-drift class.
- **The `.omc/issues/` directory is created on first delta.** No need
  to pre-create; the hand-off template names the path explicitly so
  `git` reports the new dir without surprise.