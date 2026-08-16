---
name: multica-semantica
description: "Use the Semantica Explorer REST API for ontology management, decision recording, SPARQL queries, graph analytics, provenance tracing, and entity enrichment. The `semantica` Labs flag must be enabled for these endpoints to be reachable. Skip if the user is not asking about a knowledge graph, ontology, decision record, SPARQL query, or causal/provenance analysis."
---

# Semantica Explorer REST API (0.5.22+)

Bridges Multica agents to the **Semantica Explorer** local FastAPI
server. When the `semantica` Labs flag is enabled, the desktop
spawns `apps/desktop/vendor/semantica/run.sh`, which runs the
Semantica Explorer on a free loopback port. Multica's generic
subprocess path registers the loopback URL and mounts a same-origin
reverse proxy at `/experimental/semantica/*` → upstream `/api/...`.

This skill teaches agents how to call the proxied REST API from
inside the agent subprocess (with `MULTICA_API_URL` + Bearer token).

## When to use

Quick decision matrix:

| User intent | Endpoint |
|---|---|
| "List loaded ontologies" | `GET /api/ontology/registry` |
| "Search ontology terms" | `GET /api/ontology/search?q=<term>` |
| "Inspect ontology entity X" | `GET /api/ontology/entity/<uri>` |
| "Load ontology from URL" | `POST /api/ontology/load {url}` |
| "What did we decide about X?" | `GET /api/decisions` + filter |
| "Why did we decide X?" | `GET /api/decisions/{id}/chain` |
| "Past decisions like scenario X" | `GET /api/decisions/{id}/precedents` |
| "Run a SPARQL query" | `POST /api/sparql {query}` |
| "Where did this node come from?" | `GET /api/provenance/{entity_id}` |
| "Graph stats / centrality / communities" | `GET /api/analytics` |
| "Export the graph" | `POST /api/export {format, scope}` |
| "Import a graph file" | `POST /api/import` |
| "Temporal graph query" | `GET /api/temporal/<...>` |
| "Entity enrichment / dedup" | `GET /api/enrich/<entity_id>` |
| "SKOS vocabulary lookup" | `GET /api/vocabulary/<...>` |

If the `semantica` Labs flag is **off**, the upstream server is not
running and these endpoints return `502 Bad Gateway` — surface the
diagnostic to the user (the flag must be enabled in Labs settings).

## Hard rule — flag check before any call

Before issuing any HTTP verb, **check the flag** (mirrors the
`multica-llm-wiki` Skill pattern):

```sh
multica experimental flags list 2>/dev/null | grep -E '^semantica\s+(true|enabled)'
```

If the flag is `false` (or missing), **refuse**:

> Semantica 集成未启用。请在 Settings → Labs 打开「Semantica
> Knowledge Graph」开关。

Do NOT fall back to direct file-system reads of the Semantica graph
file — that's the on-disk execution path the labs flag is designed
to keep out of.

## How to call the API

All endpoints live behind the Multica reverse proxy at
`$MULTICA_API_URL/experimental/semantica/api/...`. The proxy
forwards to the upstream Semantica server with these rewrites:

- strips `Cookie` + `Authorization` (Multica session credentials)
- forwards `X-API-Key` (Semantica auth header — only if
  `SEMANTICA_REQUIRE_AUTH=1` was set at subprocess startup)
- sets `X-Multica-Embedded: 1` (so Semantica suppresses "open in browser")

The agent calls them via `curl` (or `httpie` if available). Read
verbs use `GET`; writes use `POST`.

### Read example — list loaded ontologies

```sh
curl -sS \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "X-Workspace-ID: $WORKSPACE_ID" \
  "$MULTICA_API_URL/experimental/semantica/api/ontology/registry"
```

### Read example — search ontology terms

```sh
curl -sS \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  "$MULTICA_API_URL/experimental/semantica/api/ontology/search?q=protein&limit=20"
```

### Read example — decision chain (causal)

```sh
curl -sS \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  "$MULTICA_API_URL/experimental/semantica/api/decisions/$DECISION_ID/chain?direction=upstream&depth=5"
```

### Write example — record a decision

```sh
curl -sS -X POST \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "category": "loan_approval",
    "scenario": "First-time homebuyer, income 80k",
    "reasoning": "Good credit score, low DTI ratio",
    "outcome": "approved",
    "confidence": 0.95,
    "entities": ["customer_123", "property_456"]
  }' \
  "$MULTICA_API_URL/experimental/semantica/api/decisions"
```

### Write example — SPARQL query

```sh
curl -sS -X POST \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/sparql-query" \
  --data-binary @query.rq \
  "$MULTICA_API_URL/experimental/semantica/api/sparql"
```

### Write example — load ontology from URL

```sh
curl -sS -X POST \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"url": "https://example.com/ontology.owl", "format": "turtle"}' \
  "$MULTICA_API_URL/experimental/semantica/api/ontology/load"
```

## Endpoint reference

All paths are relative to `$MULTICA_API_URL/experimental/semantica/`.
The Semantica upstream also serves interactive OpenAPI docs at
`$MULTICA_API_URL/experimental/semantica/docs` — fetch that URL
once to discover all available endpoints.

### Ontology (`/api/ontology/*`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/registry` | List loaded ontologies |
| GET | `/search?q=<term>` | Search ontology terms |
| GET | `/entity/<uri>` | Entity detail (classes, properties, individuals) |
| POST | `/load` | Load ontology from source URL |
| POST | `/create` | Create a new ontology from scratch / text / data |
| GET | `/health` | Ontology health check (schema/syntax validation) |
| POST | `/shacl/generate` | Generate SHACL shapes from data |
| GET | `/shacl/shapes` | List generated SHACL shapes |
| POST | `/shacl/validate` | Validate a graph against SHACL shapes |
| GET | `/alignments?uri=<uri>` | List ontology alignments |
| POST | `/alignments` | Save an alignment between two entities |
| POST | `/suggest-alignments` | Suggest alignments between two ontologies |
| GET | `/skos/schemes` | List SKOS concept schemes |
| POST | `/skos/search` | Search within SKOS concept schemes |
| GET | `/skos/concept/<uri>` | SKOS concept detail |
| GET | `/drafts/<uri>` | List ontology draft versions |
| POST | `/draft` | Save draft (visual editor changes) |

### Decisions (`/api/decisions/*`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/` (list) | List recorded decisions |
| GET | `/{decision_id}` | Decision detail |
| GET | `/{decision_id}/chain` | Causal chain (upstream or downstream) |
| GET | `/{decision_id}/precedents` | Past decisions similar to this one |
| GET | `/{decision_id}/compliance` | Policy compliance check |
| GET | `/causal-distance` | Causal distance between two nodes |
| POST | `/` (record) | Record a new decision (see write example above) |

### Graph + Analytics (`/api/graph/*`, `/api/analytics/*`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/analytics` | Centrality, density, communities, growth-rate |
| GET | `/api/analytics/validation` | Validation report |
| GET | `/api/graph/<node_id>/neighborhood` | Local neighborhood |
| GET | `/api/graph/<node_id>/path?to=<other>` | Shortest path between two nodes |

### SPARQL (`/api/sparql`)

| Method | Path | Purpose |
|---|---|---|
| POST | `/` | Execute a SPARQL query (SELECT/CONSTRUCT/ASK). Updates (INSERT/DELETE/DROP/LOAD/CLEAR/CREATE/COPY/MOVE/ADD) are rejected — use `/api/import` or specific write endpoints for mutations. |

### Provenance (`/api/provenance/*`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/<entity_id>` | Source lineage for an entity — text spans, decision ids, tool calls |

### Temporal (`/api/temporal/*`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/at/<timestamp>` | Graph state at a point in time |
| GET | `/between/<start>/<end>` | Edges/nodes that changed in a window |
| GET | `/state` | Current temporal index |

### Enrichment (`/api/enrich/*`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/<entity_id>` | Enrichment suggestions for an entity |
| POST | `/resolve` | Run entity resolution across a list |

### Vocabulary (`/api/vocabulary/*`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/schemes` | List SKOS vocabularies |
| GET | `/concepts?scheme=<uri>` | Concepts in a scheme |

### Export / Import (`/api/export`, `/api/import`)

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/export` | Export graph (JSON / GraphML / RDF Turtle / N-Triples / JSON-LD / Parquet) |
| POST | `/api/import` | Import a graph file (multipart upload) |

### Annotations (`/api/annotations`)

| Method | Path | Purpose |
|---|---|---|
| GET | `/` | List annotations on entities |
| POST | `/` | Attach an annotation |
| DELETE | `/{annotation_id}` | Remove an annotation |

## Critical API notes

- **Cold-start takes 30-90 seconds.** The Semantica Explorer
  imports numpy / scipy / networkx / rdflib at startup, so the
  subprocess is slow to bind its first `/api/health` response.
  The desktop manager's `ready_timeout_ms: 120000` accommodates
  this; the first run after `pip install -e` is the slowest (subsequent
  starts benefit from filesystem cache and Python module cache).
  If `/api/health` returns 502 within 2 minutes, check the
  desktop main process log (`~/.multica/profiles/*/daemon.log`)
  for the `semantica` subprocess output.
- **Decision node names are case-sensitive.** `POST /api/decisions`
  writes nodes with `node_type="decision"` (lowercase); some legacy
  paths write `"Decision"` (capitalized). When listing, filter both
  with separate calls if completeness matters.
- **Graph persistence is opt-in.** The graph is in-memory by
  default; it is persisted to `$SEMANTICA_KG_PATH` only when the
  Semantica Explorer subprocess is started with that env var set.
  The run.sh writes to
  `${MULTICA_RESOURCES_DIR:-~/.multica}/semantica-graph.json`.
- **SPARQL Update is rejected.** `/api/sparql` accepts SELECT /
  CONSTRUCT / ASK only. For writes use dedicated endpoints
  (`/api/decisions`, `/api/ontology/load`, `/api/import`,
  `/api/alignments`, etc.) — the safety net scans the request body
  for INSERT/DELETE/DROP/LOAD/CLEAR/CREATE/COPY/MOVE/ADD keywords
  and 400s the call.
- **Auth is anonymous by default.** The fork's single-user
  `SEMANTICA_ALLOW_ANONYMOUS=true` mode skips X-API-Key checks. To
  opt back in, set `SEMANTICA_REQUIRE_AUTH=1` before the flag is
  enabled; the subprocess will write a random 32-byte hex key to
  `$SEMANTICA_KG_PATH.api-key` and require it as `X-API-Key` on
  every call (the Multica proxy passes the header through).

## Phase status

**Phase 1 (this commit):** the `semantica` flag shows up in Labs;
the `multica-semantica` skill is loaded into every agent's context
via `server/internal/service/builtin_skills/embed.FS` (same pattern
as `multica-llm-wiki`); `run.sh` starts the Semantica Explorer
HTTP server; Multica's reverse proxy exposes every endpoint at
`/experimental/semantica/*`. Agents call the API via `curl` using
the `MULTICA_API_URL` + Bearer token they already carry.

**Phase 2 (separate change, not in this commit):** if you want
agents to skip the manual curl step and call Semantica tools as
first-class MCP tools (`mcp__semantica__<verb>`), wire
`manifest.capabilities.mcp_servers` into `agent.mcp_config` at
claim time. Today this skill teaches the curl pattern, which is
fully functional but verbose.

## When to delegate to the specialist agent

Phase 2 adds a dedicated `semantica_decision_advisor` leader agent
(bundled skill `multica-semantica-decision-advisor`). For **multi-step
structured analysis** — precedent search, causal-chain tracing, ontology
lookup, or decision recording that returns a JSON envelope for the
caller to post-process — prefer delegating instead of a raw curl chain:

```sh
multica lab delegate semantica "find precedents for issue #1234" --output plain
```

The `lab delegate` verb creates an issue with `lab_source=semantica`,
waits for the leader agent run to finish, and returns the parsed
`result.output` — a JSON object shaped `{"ok": bool, "data": ..., "error": ...}`
(see `multica-semantica-decision-advisor/SKILL.md` "Output format").

**When NOT to delegate — keep using curl:**

- A single read of one endpoint (e.g. `GET /api/ontology/registry`) —
  the round-trip through an agent run is slower than a direct call.
- When you are already inside an agent subprocess with
  `MULTICA_API_URL` + `MULTICA_API_TOKEN` in scope, the curl recipes
  in this file are the shortest path.
- When the `semantica` flag is off — delegation returns 400 ("flag is
  disabled") and curl returns 502; both fail, so surface the flag
  first.

## Decision sync behavior

When an issue with `lab_source=semantica` reaches a terminal status
(`done` / `closed` / `cancelled`), Multica fire-and-forget POSTs a
decision record to Semantica's `POST /api/decisions` (wired by
`server/cmd/server/decision_sync_listeners.go`). The payload maps
`issue.id` → `id` (`multica_<uuid>`), `issue.title` → `title`,
`issue.description` (truncated 2 KB) → `description`, and stamps
`provenance.source = "multica"` + `provenance.issue_id` so Semantica
can trace the record back to the Multica issue.

This is **best-effort**: a failed POST (Semantica down, flag off, 10 s
timeout) is logged and does NOT block or roll back the issue workflow.
Semantica dedupes on `decision.id`, so the agent-side explicit
`POST /api/decisions` recipe above and the listener writing the same
issue never double-record.

## Where to look

- Catalog flag: `semantica` (`server/internal/experimental/catalog.go`)
- Manifest: `apps/desktop/resources/experiments/semantica/manifest.json`
- Desktop starter: `apps/desktop/vendor/semantica/run.sh`
- Multica reverse proxy:
  `server/internal/handler/experimental_proxy.go::mountExperimentalProxy`
  + `reverseProxyTo` (strips Cookie/Authorization, passes X-API-Key)
- Runtime wiring: `apps/desktop/src/main/experimental/manager-factory.ts`
  → generic `subprocess` dispatch →
  `apps/desktop/src/main/experimental/subprocess-manager.ts` →
  `on_ready: registerExperimentalUpstream` (the manager hardcodes
  these handlers — the manifest's `on_ready` / `on_stop` fields
  are decorative)
- Persistent graph: `$SEMANTICA_KG_PATH` (default
  `${MULTICA_RESOURCES_DIR:-~/.multica}/semantica-graph.json`)
- Install handler (leader agent + visibility): `server/internal/handler/install_semantica.go`
- Decision sync listener: `server/cmd/server/decision_sync_listeners.go`