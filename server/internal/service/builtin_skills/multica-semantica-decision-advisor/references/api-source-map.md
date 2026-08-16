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

## Ontology

| Verb | Method | Path | Required fields |
|---|---|---|---|
| List ontologies | GET | `/ontology/registry` | — |
| Term search | GET | `/ontology/search?q={term}&limit=N` | `q` |
| Entity detail | GET | `/ontology/entity/{uri}` | `uri` (URL-encoded) |
| Load from URL | POST | `/ontology/load` | `url` (optional: `format`) |
| Create ontology | POST | `/ontology/create` | `source` (text / data) |
| Ontology health | GET | `/ontology/health` | — |
| List alignments | GET | `/ontology/alignments?uri={uri}` | `uri` |
| Save alignment | POST | `/ontology/alignments` | `source_uri`, `target_uri`, `relation` |
| Draft versions | GET | `/ontology/drafts/{uri}` | `uri` |

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
  the desktop `ready_timeout_ms: 120000` accommodates it. First `/api/health`
  502 within 2 min → check `~/.multica/profiles/*/daemon.log`.
- **X-Multica-Embedded is set by two paths** — the Multica reverse proxy
  adds it to every request that flows through `/experimental/semantica/*`
  (handler/experimental_proxy.go::reverseProxyTo), AND the
  Multica-side terminal-issue sync (internal/handler/decision_sync.go)
  sets it on outbound POSTs from the daemon. The Semantica allow-list
  logic sees both call paths identically — the duplicate is
  intentional belt-and-braces.
