# Semantica REST API — Quick reference

Reference for `multica-semantica-decision-advisor`. Compact map of every verb to
the exact REST endpoint and its required fields.

- **Base:** `$MULTICA_API_URL/experimental/semantica/api/`
- **Auth:** `Authorization: Bearer $MULTICA_API_TOKEN` (the token every agent
  subprocess already carries). Writes add
  `-H "Content-Type: application/json"`.
- **Embedded header:** the Multica reverse proxy injects
  `X-Multica-Embedded: 1` automatically — do not set or strip it.
- **Flag gate:** if the `semantica` Labs flag is off the upstream server is not
  running and every path returns `502 Bad Gateway`. Check the flag first
  (`multica experimental flags list | grep semantica`).

All paths below are relative to the base above. `{...}` denotes a path or query
parameter. Query filters for list endpoints are optional unless marked required.

## Knowledge Graph + Analytics

| Verb | Method | Path | Required fields |
|---|---|---|---|
| Graph stats | GET | `/analytics` | — |
| Validation report | GET | `/analytics/validation` | — |
| Node neighborhood | GET | `/graph/{node_id}/neighborhood` | `node_id` |
| Shortest path | GET | `/graph/{node_id}/path?to={other}` | `node_id`, `to` |

## Decisions

| Verb | Method | Path | Required fields |
|---|---|---|---|
| List decisions | GET | `/decisions` | — (filters: `category`, `tags`, `status`, `q`, `limit`) |
| Decision detail | GET | `/decisions/{decision_id}` | `decision_id` |
| Causal chain | GET | `/decisions/{decision_id}/chain?direction=upstream\|downstream&depth=N` | `decision_id` (`direction` optional, default upstream) |
| Precedents | GET | `/decisions/{decision_id}/precedents` | `decision_id` |
| Compliance check | GET | `/decisions/{decision_id}/compliance` | `decision_id` |
| Causal distance | GET | `/decisions/causal-distance?from={a}&to={b}` | `from`, `to` |
| Record decision | POST | `/decisions` | `category`, `scenario`, `reasoning`, `outcome`, `confidence` (optional: `entities[]`, `decision_maker`, `tags[]`, `provenance`) |

Record-decision body shape:

```json
{
  "category": "loan_approval",
  "scenario": "First-time homebuyer, income 80k",
  "reasoning": "Good credit score, low DTI ratio",
  "outcome": "approved",
  "confidence": 0.95,
  "entities": ["customer_123", "property_456"],
  "decision_maker": "semantica_decision_advisor"
}
```

### DecisionRecord shape (multica → semantica POST /api/decisions)

0.5.30 P1-2 — synthesizer Round 7. The Multica-side `decision_sync.go`
fires this envelope when a Multica issue reaches a terminal status
(done / cancelled / closed). The wire shape is mirrored exactly
between Go (`semanticaDecision` struct) and TS
(`packages/core/api/schemas.ts::SemanticaDecisionRecordSchema`); keep
them in sync.

```json
{
  "id": "multica_<issue_uuid>",
  "title": "<issue.title>",
  "description": "<issue.description, ≤2000 runes>",
  "status": "done | cancelled | closed",
  "outcome": "Multica issue reached terminal status \"<status>\"",
  "tags": ["multica", "lab:semantica"],
  "provenance": {
    "source": "multica",
    "issue_id": "<issue_uuid>",
    "workspace_id": "<workspace_uuid>",
    "actor_type": "system | user | agent",
    "actor_id": "<uuid-or-empty>",
    "occurred_at": "<RFC3339 UTC>"
  }
}
```

Field rules (from the Go struct tags):

- `id`: prefix `multica_` + issue UUID. Idempotent on the Semantica side
  via upsert-on-id — repeated terminal-status fires collapse to one
  record.
- `description`: truncated to `semanticaDecisionDescriptionMax` (2000 runes)
  upstream. Keeps the payload under the typical 10 KB upstream ceiling.
- `tags`: stable set, used as a Semantica query filter.
- `provenance.actor_id`: empty when the system fires the sync (no actor
  UUID); populated for user/agent-driven syncs.

## Ontology

| Verb | Method | Path | Required fields |
|---|---|---|---|
| List ontologies | GET | `/ontology/registry` | — |
| Term search | GET | `/ontology/search?q={term}&limit=N` | `q` |
| Entity detail | GET | `/ontology/entity/{uri}` | `uri` (URL-encoded) |
| Load from URL | POST | `/ontology/load` | `url` (optional: `format`) |
| Create ontology | POST | `/ontology/create` | `source` (text / data) |
| Ontology health | GET | `/ontology/health` | — |
| Suggest alignments | POST | `/ontology/suggest-alignments` | `source_uri`, `target_uri` |
| List alignments | GET | `/ontology/alignments?uri={uri}` | `uri` |
| Save alignment | POST | `/ontology/alignments` | `source_uri`, `target_uri`, `relation` |
| SHACL generate | POST | `/ontology/shacl/generate` | `uri`, `source` |
| SHACL list shapes | GET | `/ontology/shacl/shapes` | — |
| SHACL validate | POST | `/ontology/shacl/validate` | `uri`, `graph_id` |
| SKOS schemes | GET | `/ontology/skos/schemes` | — |
| SKOS search | POST | `/ontology/skos/search` | `query`, optional `scheme` |
| SKOS concept | GET | `/ontology/skos/concept/{uri}` | `uri` |
| Draft versions | GET | `/ontology/drafts/{uri}` | `uri` |
| Save draft | POST | `/ontology/draft` | `uri`, `payload` |

## SPARQL + Import/Export

| Verb | Method | Path | Required fields |
|---|---|---|---|
| Run SPARQL | POST | `/sparql` | `query` (SELECT/CONSTRUCT/ASK only — updates rejected) |
| Export graph | POST | `/export` | `format` (json / graphml / turtle / nt / jsonld / parquet) |
| Import graph | POST | `/import` | multipart file upload |

## Provenance + Auxiliary

| Verb | Method | Path | Required fields |
|---|---|---|---|
| Provenance trace | GET | `/provenance/{entity_id}` | `entity_id` |
| Temporal state at | GET | `/temporal/at/{timestamp}` | `timestamp` |
| Temporal window | GET | `/temporal/between/{start}/{end}` | `start`, `end` |
| Enrichment suggestions | GET | `/enrich/{entity_id}` | `entity_id` |
| Resolve entities | POST | `/enrich/resolve` | `entities[]` |
| List vocabularies | GET | `/vocabulary/schemes` | — |
| Concepts in scheme | GET | `/vocabulary/concepts?scheme={uri}` | `scheme` |
| List annotations | GET | `/annotations` | — |
| Attach annotation | POST | `/annotations` | `entity_id`, `text` |
| Remove annotation | DELETE | `/annotations/{annotation_id}` | `annotation_id` |

## Response envelope (specialist output)

Every specialist answer is one JSON object:

```json
{
  "ok": true,
  "data": {
    "result": {"summary": "...", "details": {}, "precedents": [], "provenance": []},
    "confidence": 0.0,
    "next_steps": ["..."]
  },
  "error": null
}
```

On failure: `"ok": false`, `"data": null`, `"error": "<diagnostic>"`.

## Notes

- **Decision node names are case-sensitive** — some legacy paths write
  `"Decision"` (capitalized); filter both when completeness matters.
- **Graph persistence is opt-in** — in-memory unless `$SEMANTICA_KG_PATH` is
  set; the default is
  `${MULTICA_RESOURCES_DIR:-~/.multica}/semantica-graph.json`.
- **SPARQL Update is rejected** — the safety net 400s bodies containing
  INSERT/DELETE/DROP/LOAD/CLEAR/CREATE/COPY/MOVE/ADD. Use the dedicated write
  endpoints (`/decisions`, `/ontology/load`, `/import`, `/alignments`).
- **Auth is anonymous by default** — `SEMANTICA_ALLOW_ANONYMOUS=true` skips
  `X-API-Key`; set `SEMANTICA_REQUIRE_AUTH=1` to require it.
- **Cold-start takes 30–90 s** — numpy/scipy/networkx/rdflib import at boot;
  the desktop `ready_timeout_ms: 180000` accommodates it (0.5.28 P0-1 clamp;
  was 120000 pre-P0). First `/api/health` 502 within 2 min → check
  `~/.multica/profiles/*/daemon.log`.
- **X-Multica-Embedded is set by two paths** — the Multica reverse proxy
  adds it to every request that flows through `/experimental/semantica/*`
  (handler/experimental_proxy.go::reverseProxyTo), AND the
  Multica-side terminal-issue sync (internal/handler/decision_sync.go)
  sets it on outbound POSTs from the daemon. The Semantica allow-list
  logic sees both call paths identically — the duplicate is
  intentional belt-and-braces.

## What's new in semantica-agi/semantica v0.6.6 (0.5.54 P2 sync)

The fork now vendors upstream at `apps/desktop/vendor/semantica-src/`
via `git subtree` (Phase 0). The 0.6.6 release landed two changes that
matter to Multica integrators; the **wire shape** that the fork posts
(`SemanticaDecisionRecordSchema`) is **unchanged**.

### SHA-256 deterministic IRIs (was: randomised `hash()`)

Pre-0.6.6 the upstream exporter minted entity/relationship IRIs from
Python's builtin `hash()`, which is randomised per process
(`PYTHONHASHSEED`). The same entity produced a different IRI on
every run, so exports were not diff/join-able across restarts and
could not be matched against an earlier provenance record. 0.6.6
fixed this by:

- Minting IRIs with **SHA-256** in the declared
  `https://semantica.dev/ns#` namespace.
- Resolving endpoints the same way `serialize_to_turtle` does
  (catches a Qodo-reviewer-found edge case where the temporal
  fallback hashed the wrong source field).
- Storing `sem:confidence` with `xsd:decimal` consistently across
  the N-Triples + Turtle serializers (was `xsd:float` in one path,
  which contradicted the other).

**Fork-side impact**: zero. Multica's wire envelope uses
`id: "multica_<issue_uuid>"` which is already SHA-stable on the
fork side; the upstream change is invisible to fork code but
makes exports diff-able in the IF explorer view.

### RDF vocabulary: `semantica-ns.ttl` (NEW file in 0.6.6)

0.6.6 published its first RDF vocabulary at
`semantica/ontology/vocabulary/semantica-ns.ttl`. The 14 terms
declared are the predicates that the exporters actually emit —
nothing invented for completeness. See the new
[`vocabulary.md`](./vocabulary.md) reference for the per-term
emission sites and the deprecation status.

**Fork-side impact**: zero today. Multica's `decision_sync.go`
emits `tags: ["multica", "lab:semantica"]` but no `sem:*` predicates.
P2 defers first-class vocabulary emission to **P4 (ACL)** — that's
when `actor_type=team` is added to the DecisionRecord envelope and
the per-actor subgraph view needs to label its results with
`sem:KnowledgeGraph` / `sem:Entity` so the explorer can filter
correctly.

### How to re-sync upstream

```bash
bash scripts/sync-semantica-upstream.sh   # subtree pull + wheel rebuild + 5-scraper diff
```

If the wire schema changes upstream, the `field` scraper in
`scripts/check-semantica-upstream.sh` (P0 deliverable) catches it
before the build breaks.
