# Semantica RDF Vocabulary (`https://semantica.dev/ns#`)

> Reference companion to [`api-source-map.md`](./api-source-map.md).
> Source of record: `apps/desktop/vendor/semantica-src/semantica/ontology/
> vocabulary/semantica-ns.ttl` (vendored via `git subtree` from
> `semantica-agi/semantica` v0.6.6; 0.5.54 P2 sync).

## What this is

The Semantica 0.6.6 release published its first RDF vocabulary — a
closed-world declaration of the 14 `sem:*` terms the exporters actually
emit in `https://semantica.dev/ns#`. Every term below appears in output
the package produces; nothing is invented for completeness. The
vocabulary file ships inside the package
(`from semantica.ontology.vocabulary import vocabulary_turtle`) so it
loads without a network round trip.

**Namespace IRI:** `https://semantica.dev/ns#`
**Prefix:** `sem:`
**Vocabulary IRI:** `https://semantica.dev/ns`
**Ontology version:** `0.1.0-draft` (created `2026-08-19`)

## The 14 declared terms

### Classes (3)

| Term | Type | Used for |
|---|---|---|
| `sem:Entity` | `owl:Class` | Default type for an extracted entity when the source carries no type of its own. Emitted by `serialize_to_turtle` as the fallback for `entity.get("type")`. |
| `sem:Relationship` | `owl:Class` | A reified relationship, as emitted in the JSON-LD export when the relationship carries its own metadata (confidence, source span, etc.). |
| `sem:KnowledgeGraph` | `owl:Class` | The document-level type of a JSON-LD export: the `@type` of the top-level object in `_convert_kg_to_jsonld` (`export/json_exporter.py`). |

### Datatype properties (5)

| Term | Range | Used for |
|---|---|---|
| `sem:text` | `xsd:string` | The surface text of an extracted entity. Carries the same payload as the inline `text` key in the JSON-LD export. |
| `sem:confidence` | `xsd:decimal` | Extractor confidence in the assertion, on the unit interval. (0.6.6 fix: N-Triples and Turtle serializers now agree on `xsd:decimal`; pre-0.6.6 the N-Triples path used `xsd:float`, which made the two exports un-joinable.) |
| `sem:type` | `xsd:string` | The relationship type as a label, as emitted in the JSON-LD `predicates` map when the extractor carries a relationship type. |
| `sem:exportedAt` | `xsd:dateTime` | ISO-8601 timestamp of the export. Emitted by `_convert_kg_to_jsonld` in `export/json_exporter.py`. |
| `sem:format` | `xsd:string` | The wire format identifier of the export (`"application/ld+json"`, `"text/turtle"`, etc.). |
| `sem:openEndedInterval` | `xsd:string` | A sentinel value emitted in place of a temporal end-point when the interval has not closed (matches `time:openEnded` in OWL-Time). |

### Object properties (4)

| Term | Range | Used for |
|---|---|---|
| `sem:source` | `sem:Entity` | The subject entity of a reified relationship. |
| `sem:target` | `sem:Entity` | The object entity of a reified relationship. |
| `sem:entities` | `sem:Entity` | Inverse of the `entities` collection on a `sem:KnowledgeGraph` document. |
| `sem:relationships` | `sem:Relationship` | Inverse of the `relationships` collection on a `sem:KnowledgeGraph` document. |
| `sem:related_to` | `sem:Relationship` | The default predicate for a relationship whose type the source does not specify (matches `skos:related` semantics). |

### Annotation properties (1)

| Term | Used for |
|---|---|
| `sem:metadata` | Free-form metadata carried through from extraction. An annotation property — does not contribute to logical inference. |

### External roles (1)

| Term | From | Used for |
|---|---|---|
| `sem:role_generator` | `prov:Role` (W3C PROV-O) | Marks the extractor / agent that produced an entity or relationship; included in the JSON-LD `@context` so downstream consumers can attribute provenance. |

**Note:** `sem:role_generator` is declared in the same `.ttl` file
(line 151, per the `vocab scraper` regex match) but is typed as a
`prov:Role`, not a standalone class — it lives in the W3C PROV
namespace and is *referenced* here. The fork should treat it as a
PROV term, not as a fork-defined sem:* term.

## Multica fork-side adoption status

| Surface | Used today? | Where it lands |
|---|---|---|
| `decision_sync.go` POST envelope (`tags: ["multica", "lab:semantica"]`) | no `sem:*` predicates | stays as-is for 0.5.54 |
| `packages/core/api/schemas.ts::SemanticaDecisionRecordSchema` (TS zod) | no `sem:*` predicates | stays as-is |
| Explorer iframe view (`semantica-explorer-view.tsx`) | reads whatever upstream exports | stays as-is |
| Per-actor subgraph view (P5) | not built yet | P5/P6 will surface `sem:KnowledgeGraph` + `sem:Entity` for per-team / per-individual filtering |

**Verdict (P2 / 0.5.54):** No fork-side vocabulary emission. The
wire envelope already carries everything the fork needs; emitting
`sem:*` predicates is a P4 (ACL) deliverable when
`actor_type=team` lands and the explorer needs to label team-scoped
vs individual-scoped subgraphs.

## How to update this reference when upstream changes

```bash
# After a `git subtree pull` brings in a new semantica-ns.ttl:
bash scripts/check-semantica-upstream.sh --scraper=vocab
# The vocab scraper reports NEW (upstream-only) and REMOVED
# (fork-only) terms. Re-run this file's 'The 14 declared terms'
# table from the new .ttl + bump the count in the heading.
```

The vocab scraper in `scripts/check-semantica-upstream.sh` (Phase 0
deliverable) catches 14/0 / 15/0 / 14/1 etc. drift on every monthly
cadence run before it becomes a regression.
