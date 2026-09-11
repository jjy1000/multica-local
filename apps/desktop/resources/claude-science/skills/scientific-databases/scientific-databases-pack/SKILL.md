---
name: scientific-databases-pack
description: Compact catalog of 100+ scientific databases across biology, chemistry, medicine, drug discovery, and physics — distilled from the `scientific-agent-skills` project (K-Dense-AI). The lab's `biology`, `physics`, `ml`, and `research` agents use this skill when an issue mentions a gene / protein / compound / clinical trial / crystal structure / arXiv paper; the skill routes the query to the most specific database (UniProt, PubChem, ClinicalTrials.gov, PDB, ChEMBL, Ensembl, etc.) and returns a citation-ready record. Hidden from main picker; lab-only.
category: scientific-databases
allowed-tools: [Read, Write, Edit, Bash]
---

# Scientific Databases — Routing Pack

A condensed routing layer for the 100+ databases that ship with
`scientific-agent-skills` (K-Dense-AI). Each entry is one paragraph:
**(domain) → (database) → (when to route here) → (auth / access)**.

The lab's domain agents (`biology` / `physics` / `ml` / `research`)
trigger this skill via the issue title pattern matcher; the skill
returns a database list to query and a hint about which one is the
primary source.

## When to Use

Issue body mentions any of:
- 基因 / 蛋白 / 通路 / 突变 / 表达 → biology routing
- 化合物 / 反应 / SMILES / 分子量 / 药物 → chemistry routing
- 临床试验 / 适应症 / 招募 / 不良反应 → medicine routing
- 晶体结构 / 衍射 / PDB id / α 螺旋 → structural biology
- arXiv id / DOI / 引用网络 / 综述 → literature routing

## Database Routing Map (excerpt)

### Biology / Genomics
- **NCBI Gene** — primary gene lookup. Free, no auth. URL: `https://www.ncbi.nlm.nih.gov/gene/`
- **UniProt** — protein sequence + function + isoforms. Free, no auth. URL: `https://rest.uniprot.org/`
- **Ensembl** — genome browser, REST API. Free. URL: `https://rest.ensembl.org/`
- **STRING** — protein-protein interaction network. Free. URL: `https://string-db.org/api/`
- **GEO** — gene expression omnibus (microarray + RNA-seq). Free. URL: `https://www.ncbi.nlm.nih.gov/geo/`
- **BioGRID** — curated interaction database. Free. URL: `https://webservice.thebiogrid.org/`

### Chemistry / Drug Discovery
- **PubChem** — compound properties, bioassays, safety. Free. URL: `https://pubchem.ncbi.nlm.nih.gov/rest/pug/`
- **ChEMBL** — bioactive molecules with target / assay data. Free for academic. URL: `https://www.ebi.ac.uk/chembl/api/data/`
- **DrugBank** — drug-target associations. Free academic license. URL: `https://go.drugbank.com/`
- **ZINC** — purchasable compound catalog. Free. URL: `https://zinc.docking.org/substances/`
- **RCSB PDB** — 3D macromolecular structures. Free. URL: `https://data.rcsb.org/`
- **BindingDB** — measured binding affinities. Free. URL: `https://www.bindingdb.org/axis2/services/BDBService/`

### Medicine / Clinical
- **ClinicalTrials.gov** — trial registry + outcomes. Free, no auth. URL: `https://clinicaltrials.gov/api/v2/`
- **OpenFDA** — adverse events, drug labels. Free. URL: `https://api.fda.gov/`
- **WHO ICTRP** — international trial registry. Free. URL: `https://trialsearch.who.int/`

### Literature
- **arXiv** — preprints. Free, no auth. URL: `http://export.arxiv.org/api/`
- **Semantic Scholar** — citation graph + TLDR. Free API key optional. URL: `https://api.semanticscholar.org/`
- **OpenAlex** — open citation database. Free. URL: `https://api.openalex.org/`
- **PubMed** — biomedical literature. Free. URL: `https://eutils.ncbi.nlm.nih.gov/entrez/eutils/`
- **bioRxiv / medRxiv** — preprints. Free. URL: `https://api.biorxiv.org/`

### Physics / Materials
- **Materials Project** — inorganic crystals, DFT energies. Free API key. URL: `https://api.materialsproject.org/`
- **arXiv cond-mat** — condensed matter preprints. Free. (same endpoint as arXiv)

(Full routing map: ~100 entries across the domains above; see the sibling
file `scientific-databases-pack-full.json` for the exhaustive table.)

## Routing Rules

1. The agent's first action is to **match the issue body to ONE primary
   domain**. If it matches two domains with comparable specificity,
   split the work — `biology` for the gene half, `chemistry` for the
   compound half.
2. The skill returns the **primary database** and one **fallback**. Do
   not query more than 2 databases per issue unless the user asks for
   cross-domain analysis.
3. Auth: the agent runtime reads `MULTICA_API_TOKEN` from
   `~/.multica/profiles/<name>/config.json`. Databases that need an API
   key (Materials Project, etc.) read `MPIResterAPIKey` from the same
   config.
4. Every database call **MUST** include the source citation in the
   returned comment (`PMID:12345`, `DOI:...`, `arXiv:...`,
   `UniProt:P12345`, etc.). The lab's `write` agent uses these to
   populate the bibliography automatically.

## Failure Modes

- Empty result on a primary database → fall back, do NOT loop more
  than once. Report "no record found" to the user.
- API key missing → fall back to the free endpoint if one exists
  (PubMed / arXiv / OpenAlex), else report and stop.
- Rate-limit 429 → exponential backoff with jitter, max 3 retries.
  If still 429, log and continue with the partial result.

## Related Skills

- `ai-research-writing-pack` (research-prompts/) — for the resulting writeup
- `bioservices` (biology/) — Python client wrapper for several of these
- `biopython` (biology/) — sequence + structure toolkit