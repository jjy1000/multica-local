# WL2 — TimesFM Vendoring Feasibility Report (0.5.82 pre-work)

> Research pass, 2026-08-27. Static analysis of the local OSS tree at
> `/Users/jiangjianyan/jjy/开发项目参考/timesfm-master/` (below: **OSS**)
> against the two in-repo vendoring precedents (Pythia, Semantica).
> Repo claims carry `file:line` citations; OSS claims carry OSS paths.
> Feeds §4 Work Line 2 of `.omc/plans/0.5.81-0.5.83-labs-evolution-roadmap.md`
> (path in the main worktree; WL2 body at lines 216-240, ICP law 126-162).

---

## 1. TimesFM footprint assessment

### 1.1 Tree layout (OSS)

| Path | What it is |
|---|---|
| `OSS/src/timesfm/` | Current package **v2.0.2** (`pyproject.toml`): TimesFM **2.5, 200M params only** |
| `OSS/src/timesfm/configs.py` | `ForecastConfig` dataclass + model arch configs (105 ln) |
| `OSS/src/timesfm/timesfm_2p5/` | Base runner (423 ln) + **torch** impl (515 ln) + flax impl (614 ln) |
| `OSS/src/timesfm/torch/`, `flax/` | Framework-specific layers (dense/transformer/norm/util), ~1,160 ln each stack |
| `OSS/src/timesfm/utils/xreg_lib.py` | Covariate (exogenous regressor) linear-model lib, 520 ln |
| `OSS/v1/` | Archived 1.x generation: JAX/PAX primary (`timesfm_jax.py`, `patched_decoder.py`) + torch port + finetuning/peft — **not a candidate**, see §1.3 |
| `OSS/tests/`, `OSS/.github/` | Tests & CI (not needed at runtime) |
| `OSS/timesfm-forecasting/` | **Third-party skill pack** (author borealBytes, v1.0.0): `SKILL.md`, `scripts/check_system.py`, `scripts/forecast_csv.py`, `references/{api_reference,data_preparation,system_requirements}.md`, `examples/` |

Total runtime-relevant Python for the torch backend is small: ~3,300 LOC
across `src/timesfm/` including the flax stack we would NOT ship; a
torch-only trim is well under 1,500 LOC plus one FastAPI wrapper we write.

### 1.2 Dependency weight

Core declared deps (`OSS/pyproject.toml [project].dependencies`) are light:

- `numpy>=1.26.4`, `huggingface_hub[cli]>=0.23.0`, `safetensors>=0.5.3`

Backends are extras:

- `torch = ["torch>=2.0.0"]` ← the viable one
- `flax = ["flax","optax","einshape","orbax-checkpoint","jaxtyping","jax[cuda]"]`
- `xreg = ["jax[cuda]","scikit-learn"]`

Measured on THIS machine (the semantica venv already carries torch):

| Package | On disk here |
|---|---|
| torch 2.13.0 (darwin/arm64 build, no CUDA) | **492 MB** (`~/.multica/semantica-venv/lib/python3.12/site-packages/torch`) |
| numpy 2.5.2 | 25 MB |
| pandas 3.0.5 (only needed by `forecast_csv.py`) | 47 MB |

For comparison: pythia's live venv is **24 MB** total; semantica's is
**1.5 GB** (it already ships torch). Current installed app:
`/Applications/Multica.app` = 813 MB.

Practical order-of-magnitude: a timesfm venv without pandas ≈ **550 MB
installed**; wheelhouse download size for first-run provisioning is far
smaller compressed but must still be treated as "big dependency" — this is
20× the Pythia precedent and closer to Semantica's class.

### 1.3 JAX vs torch backend — CPU-only viability on darwin/arm64

- Torch backend is first-class for 2.5 (`src/timesfm/timesfm_2p5/timesfm_2p5_torch.py`);
  device selection is CUDA-or-CPU with no MPS branch
  (`if torch.cuda.is_available(): ... else: self.device = torch.device("cpu")`,
  lines 72-77). CPU-only inference is the designed fallback and works.
- The **JAX/PAX path must be rejected**: `jax[cuda]` extra is GPU-oriented,
  the orbax/flax stack adds little for us, and `v1/`'s decoder predates the
  refactor (`v1/src/timesfm/timesfm_jax.py`, `patched_decoder.py`). The
  upstream README itself moved API examples to torch.
- Note: `torch.compile` is applied by default in
  `_from_pretrained → load_checkpoint(torch_compile=instance.torch_compile)`
  (torch file lines 287-302); on macOS CPU the first compile can be slow.
  Our wrapper should call `load_checkpoint(path, torch_compile=False)`.

### 1.4 Model variants, weights sizes, download behavior — CRITICAL offline constraint

Variants pinned in `OSS/timesfm-forecasting/scripts/check_system.py`
(`MODEL_PROFILES`, lines ~36-70):

| Variant | Params | min / rec RAM | disk | HF repo |
|---|---|---|---|---|
| v2.5 (target) | 200M | 2 GB / 4 GB | ~800 MB weights (+2 GB headroom advised) | `google/timesfm-2.5-200m-pytorch` |
| v2.0 | 500M | 8 GB / 16 GB | 4 GB | `google/timesfm-2.0-500m-pytorch` |
| v1.0 | 200M | 4 GB / 8 GB | 2 GB | `google/timesfm-1.0-200m-pytorch` |

Download behavior (`src/timesfm/timesfm_2p5/timesfm_2p5_torch.py:304-364`):
`from_pretrained("google/timesfm-2.5-200m-pytorch")` goes through
`hf_hub_download` → caches under `~/.cache/huggingface/`. The SKILL.md is
explicit: "Model weights are **NOT stored in this repository** … download
on-demand from HuggingFace on first use."

**This violates the fork's network-in-app prohibition** if left stock. But
the same loader has an offline hatch built in:

- `_from_pretrained` checks `os.path.isdir(model_id)` FIRST and, when the
  id is a local directory, loads `<dir>/model.safetensors` directly via
  `safetensors.load_file` with zero network (lines 337-343);
  `load_checkpoint` accepts either a dir or a raw file path (79-84).

So the required fork policy is mechanical:

1. Never construct calls with an HF repo id. Always
   `from_pretrained(<weights_dir>)` pointing at a seeded local dir
   containing exactly `model.safetensors`.
2. Belt-and-braces env in the child process: `HF_HUB_OFFLINE=1`,
   `TRANSFORMERS_OFFLINE=1`.
3. Weights are **pre-seeded, never downloaded in-app**: either shipped as an
   optional payload or dropped manually into
   `~/.multica/models/timesfm/model.safetensors` (see §2 decision gate).

### 1.5 Inference entrypoints and I/O contract

Programmatic (`src/timesfm/…/timesfm_2p5_base.py:134-196` +
`configs.py:21-60`):

```python
model = timesfm.TimesFM_2p5_200M_torch.from_pretrained(weights_dir)
model.compile(timesfm.ForecastConfig(
    max_context=1024, max_horizon=256,          # context_limit 16384 hard cap
    normalize_inputs=True, use_continuous_quantile_head=True,
    force_flip_invariance=True, infer_is_positive=True,
    fix_quantile_crossing=True, per_core_batch_size=32))
point, quantiles = model.forecast(horizon=H, inputs=[np.ndarray, …])
# point:     float32 [num_series, H]
# quantiles: float32 [num_series, H, 10]
```

Key input-schema facts:

- Inputs are plain univariate float arrays; NaN-tolerant
  (`strip_leading_nans` + `linear_interpolation`, base file lines 33-81);
  shorter series front-padded with mask, longer truncated to max_context
  (lines 175-183).
- **No frequency input in 2.5.** v1 had a per-series freq code
  (`v1/src/timesfm/timesfm_base.py:259` forecast(..., freq), `freq_map`
  :53); 2.5 dropped it — periodicity only re-enters at presentation time
  via `pd.infer_freq` when labeling future dates
  (`forecast_csv.py` write_csv_output, lines ~157-166).
- Quantile heads are fixed to `[0.1 … 0.9]` plus point column
  (`TimesFM_2p5_200M_Definition.quantiles`, base :92-94; q-dim size 10 =
  9 quantiles + point, mean-ish decode index 5, torch :53-54).

High-level reference wrapper — **wrap, don't reimplement** (plan §WL2
amendment): `OSS/timesfm-forecasting/scripts/forecast_csv.py` already does
preflight → load → CSV column detection → forecast → CSV/JSON emit, with
output keys `{forecast, lower_90, lower_80, median, upper_80, upper_90}`
per series (:119-148) and CLI flags `--horizon --date-col --value-cols
--output --format --batch-size`. Our loopback service can shell/repurpose
this verbatim behind `POST /forecast`.

XReg covariate forecasting exists (`forecast_with_covariates`,
base :198-237) but requires recompiling with `return_backcast=True` and
pulls sklearn/JAX for some modes — **de-scope from lab v1**.

---

## 2. Embedding strategy options, ranked

### Precedent ground truth (what the repo actually does)

The two precedents converge more than their labels suggest — BOTH end in a
loopback HTTP service plus a Go reverse proxy; they differ only in how the
Python payload ships:

- **Pythia**: raw source vendored at `apps/desktop/vendor/pythia-src/engine/`
  (= AGENTS.md source-of-truth rule); `bundle-cli.mjs:295-348` mirrors it to
  `resources/pythia/` every bundle; `vendor/pythia-src/run.sh` probes five
  interpreters, bootstraps `~/.multica/pythia-venv` via uv as last resort,
  then `exec python -m uvicorn engine.server:app --host 127.0.0.1 --port $1`;
  `pythia-manager.ts` extends `BaseExperimentalManager`
  (`manager-template.ts`: pickFreePort :157 → spawn(bin, [...args, port]) :162
  → GET `/health` probe) and registers via `upstreamRegister`; server side
  auto-mounts `ProxyPrefix` per catalog entry
  (`server/internal/handler/experimental_proxy.go:190-233,246-269`).
- **Semantica**: vendored subtree `apps/desktop/vendor/semantica-src/`;
  wheel PRE-BUILT offline by `scripts/build-semantica-wheel.sh`
  (`pip wheel --no-deps` into `semantica-src/builds/`, 1.6 MB artifact on
  disk today); `resources/semantica/run.sh` locates the wheel, installs it
  into a persistent venv with `pip install --no-index --find-links builds/`
  (fully offline install path, run.sh section 4), then execs
  `python -m semantica.explorer … --port $1`; spawned by the GENERIC
  manifest-driven `resolveGenericSubprocessManager`
  (`apps/desktop/src/main/experimental/manager-factory.ts:260`,
  `subprocess-manager.ts`), proxied identically via catalog entry
  `catalog.go:380-403` (`RuntimeKind:"subprocess"`,
  `ProxyPrefix:"/experimental/semantica"`).

### Option (a) — vendored Python engine + REST proxy (mirrors semantica/plan sketch)

Plan §WL2 sketch itself: subtree pin → FastAPI wrapper at
`vendor/timesfm/run_loopback.py` exposing `POST /forecast | GET /health |
GET /info` → run.sh mirroring `pythia/run.sh` → bundle-cli cp-block →
catalog flag → manager/proxy → handlers gated `DefaultFor("timesfm")`.

Pros: exactly replayed twice in-repo; proxy mount is automatic once the
catalog entry carries ProxyPrefix+LoopbackService
(experimental_proxy.go:218-223 "no router edit"); renderer reaches the
service same-origin through Multica origin; health/status handling already
generic.

Cons: dependency weight must be solved (see offline wheelhouse below);
800 MB weights are the real cost driver, independent of transport choice.

### Option (b) — daemon subprocess bridging mirroring pythia, differently framed

Reading (b) charitably as alternatives NOT already covered by (a): a
long-lived daemon owned by the Go server, a compiled engine, or CGo/FFI
embedding of Python. All inferior:

- Reimplementing the patchwise decoder in Go is unrealistic (~500 LN of
  masked attention w/ KV-cache decode in torch semantics,
  timesfm_2p5_torch.py:115-219) and would fork model fidelity from upstream.
- CGo embedding drags libpython into electron-builder packaging, fights GIL
  and arm64 signing/notarization (electron-builder.yml `notarize: true`,
  line 77) — nothing in either precedent does this, and for good reason.
- A Go-owned daemon duplicates lifecycle state the desktop main process
  already owns (spawn/port/health/upstream register), splitting ownership
  across processes for no benefit.

Note that pythia IS option (a)'s architecture; "daemon subprocess bridging"
is precisely what both managers do. So (b) reduces to variants with strictly
worse packaging properties.

### Option (c) — rejection outright (no feasible offline runtime)

Only valid if neither torch nor JAX can be provisioned offline. Mitigated:
CPU-only darwin/arm64 torch works locally today (§1.3, measured §1.2), the
offline-wheel install pattern already ships (semantica run.sh §4
`--no-index --find-links`), and weight loading has a pure-local path
(§1.4). Rejection is not warranted IF the weight-bundling decision gate
below resolves; rejection WITH the seasonal-naive fallback alone would make
the lab hollow (a seasonal-naive forecaster doesn't need TimesFM at all).

### RECOMMENDATION

**Adopt option (a), hybrid-styled: source-vendor like Pythia, spawn generically
like Semantica, ship an offline wheelhouse.**

1. Vendor `OSS/src/timesfm/` (torch-stack-only trim) →
   `apps/desktop/vendor/timesfm-src/` next to `pythia-src`/`semantica-src`;
   add our own `run_loopback.py` FastAPI app in the vendor tree wrapping
   `forecast_csv.py` logic (wrap-not-reimplement, plan amendment).
   Source > wheel here because the package is tiny, needs no build step,
   and keeps the AGENTS.md "vendored engine source-of-truth" idiom;
   semantica needed a wheel because IT is a large installable package —
   TimesFM is not.
2. Extend `bundle-cli.mjs` after the pythia cp-block (:295-348): copy
   `timesfm-src/engine → resources/timesfm/` + run.sh + requirements.txt;
   mirror the wheelhouse dir with warn-if-missing semantics copied from the
   semantica builds block (:579-587). Wheelhouse pins
   torch/numpy/safetensors/huggingface_hub(+pandas optional) darwin-arm64
   wheels so run.sh installs fully offline into
   `~/.multica/timesfm-venv` (mirror of semantica persistent-venv amortize
   pattern). This satisfies the plan's "Offline wheel pre-build mandatory"
   amendment.
3. run.sh clones pythia's five-way interpreter probe; venv bootstrap uses
   ONLY `--no-index --find-links <bundled wheelhouse>` — never PyPI.
4. Spawn via `resolveGenericSubprocessManager` (manifest-driven), not a
   bespoke manager file; add its descriptor to `staticFlagDescriptors`
   (manager-factory.ts:70-93).
5. Weights tiers (decision gate, matches plan's synthetic-fallback
   amendment):
   - Tier 0 (always present): seasonal-naive synthetic forecaster so
     `/forecast` never fails closed and provenance can be labeled honestly
     (mirrors pythia envelope `source ∈ {oracle, synthetic,…}` labeled in
     migration 164's CHECK constraint).
   - Tier 1 (recommended initial ship): user/manual seed into
     `~/.multica/models/timesfm/model.safetensors` (+ companion doc in the
     skill); child env sets HF_HUB_OFFLINE=1 and loads via local-dir
     `from_pretrained` only. No in-app network, ever.
   - Tier 2 (optional later): bundle weights into a separate "extras" DMG —
     NOT into Multica.app resources — since +800 MB pushes the installed
     footprint from 813 MB toward ~1.7 GB. Precedent for shipping big
     binaries exists (Postgres.app under `resources/pg/`,
     electron-builder.yml:31-39) but weights shouldn't ride along by
     default.

---

## 3. Integration contract outline (Multica)

Issue-task-first, per the governing law (roadmap ICP-1..ICP-4, lines
126-162) and the existing flag/view plumbing:

- **Catalog flag** (`server/internal/experimental/catalog.go`, new entry
  modeled on `pythia_oracle` :225-254): `Key:"timesfm"`, `DefaultVal:false`,
  localized En/Zh title/description, `ManifestPath:"experiments/timesfm/manifest.json"`,
  `RuntimeKind:"subprocess"`, `ProxyPrefix:"/experimental/timesfm"`,
  `LoopbackService:"timesfm"`. `AutoDispatch:false` like pythia (:253) —
  CPU inference takes seconds-minutes; keep runs manual/retry-driven.
  No new router code: `MountExperimentalProxies` auto-mounts the prefix
  (experimental_proxy.go:218-223); handlers gate on
  `DefaultFor("timesfm")`.
- **Manifest** `apps/desktop/resources/experiments/timesfm/manifest.json`
  modeled on `resources/experiments/pythia_oracle/manifest.json`:
  `spec.entry_points.sidebar: []` — EMPTY per ICP-1 ("New labs may have an
  EMPTY sidebar entry when they are purely issue-driven; precedent planned
  for timesfm"). `capabilities.skills:["multica-timesfm"]`,
  `runtime.kind:"subprocess"`, `runtime.binary:"timesfm/run.sh"`,
  `health_path:"/health"`, `on_ready:"registerUpstream"`.
- **Route suffix / view**: add `timesfm: "timesfm-lab"` to
  `FLAG_ROUTE_SUFFIX` (`packages/views/issues/components/issue-labs-section.tsx:31-48`)
  and register desktop route path `"timesfm-lab"` under the experimental
  group (`apps/desktop/src/renderer/src/routes.tsx:165` pythia analog).
  Candidate view: minimal page listing the bound issue's last 10 forecasts
  (plan commit item) + a chart surface. Keep suffix distinct from the REST
  prefix exactly like semantica (bare `/experimental/semantica` reserved
  for the proxy, view at `semantica-explorer`,
  issue-labs-section.tsx:40-47 comment; catalog.go:402-403). Navigation
  inherits release-guard arming automatically via `useNavigation().push` /
  AppLink (ground-truth row, roadmap line 110).
- **Forecast job flow (issue-driven)**: agent assignee on an issue with
  lab_source=timesfm receives task instructions; the adapted skill teaches
  it to POST through the same-origin proxy:
  `POST /api/experimental/timesfm/forecast` body
  `{ series: number[], horizon: int, dates?: string[] }` — series values
  extracted from the issue context (metric tables in comments, attached
  CSV artifact). Handler passes through to loopback `POST /forecast`;
  response wraps forecast_csv.py output: per-series
  `{forecast[·], lower_90, median, upper_80, upper_90, …}` plus top-level
  `provenance: "model" | "seasonal_naive"` and
  `model_present: bool`.
- **Persistence**: migration (next free number; roadmap ground truth says
  max applied = 274) adds `timesfm_forecast_run` sibling of
  `164_pythia_forecast_run.up.sql`: `id UUID PK`, `workspace_id FK CASCADE`,
  `issue_id UUID NOT NULL REFERENCES issue(id) ON DELETE CASCADE`,
  `horizons INT`, `provenance TEXT CHECK IN ('model','seasonal_naive','mixed')`,
  `result JSONB` (quantile-band envelope array), `created_at TIMESTAMPTZ`,
  index `(issue_id, created_at DESC)`. Read path:
  `GET /api/experimental/timesfm-lab/forecast/issue/runs?issue=<id>`
  (pythia analog per roadmap ground truth line 123), enabling ICP-2
  record-listing without a live service and ICP-3 deep links
  `?issue=<id>&run=<runId>`.
- **Artifacts returned**: quantile-band JSON persisted in the table and
  rendered as a chart in the view; agent delivers results back as an issue
  comment (fork dispatch model — agents deliver via comments,
  `task.go` invariant cited in roadmap ground truth :109), with the comment
  referencing the run id so ExecutionLogSection / LabLastResultChip can
  jump OUT to the lab record (ICP-3/I-4 loop).
- **Lab agent seeding**: single leader install handler modeled on
  `install_semantica.go:140-146` / `install_pythia.go:124-133` (
  `upsertPythiaVisibility` shows the visibility-row + purge-on-delete
  contract to replicate — purge contract lives at `user_plugins.go:451,:538-544`).
- **Skill adaptation**: port `OSS/timesfm-forecasting/SKILL.md` (+ its
  `references/api_reference.md`, `references/data_preparation.md`) into
  `server/internal/service/builtin_skills/multica-timesfm/SKILL.md`,
  rewritten against `multica experimental …` / rawRequest conventions
  (embed point: `//go:embed builtin_skills`,
  `builtin_skills.go:16-17`), issued in en/zh-Hans/ja/ko per the roadmap
  amendment.

---

## 4. Risks & open questions

Packaging size / bundling
- R1 Wheelhouse size: torch arm64 wheel will dominate resources/; expected
  tens-of-MB compressed vs 492 MB installed. Measure a real `pip download`
  set before committing to an in-DMG wheelhouse; if too large, fall back to
  first-run seeding parity with weights (manual drop).
- R2 `asarUnpack: resources/**` (electron-builder.yml:29) covers new
  subdirs automatically, but add a documented explicit entry for timesfm
  (style of :41-58) so child_process spawns never hit asar paths. Also keep
  bundle-cli warn-if-missing parity (semantica block :579-587).


Arch coverage
- R4 package.mjs supports x64/arm64/universal (scripts/package.mjs:58-62).
  Wheelhouse and weights are arch-neutral (safetensors floats), but the
  torch WHEEL is arch-specific; an x64/Intel ship needs a second wheelhouse
  or the lab degrades to seasonal-naive with clear toast. Recommend
  arm64-first; degrade loudly elsewhere (preflight mirrors check_system.py).

Performance (GPU-less)
- R5 CPU decode cost scales with max_context×max_horizon and DOUBLES under
  `force_flip_invariance=True` (two decodes per batch,
  timesfm_2p5_torch.py:456-471). For issue-scale single-series requests
  (context ≤1024, horizon ≤256) expect seconds, fine for synchronous HTTP
  within the desktop proxy timeout; long horizons should flip the wrapper
  to async-job-with-run-row semantics early rather than hold the request.
- R6 `torch_compile=False` in our wrapper (macOS CPU first-compile latency,
  §1.3) unless profiling says otherwise.
- R7 RAM floor: rec 4 GB (min 2 GB) for the 200M model on top of Electron+
  embedded Postgres; run_loopback preflight should refuse politely below
  floor, exactly like check_system.py's verdict mechanism.

Offline integrity
- R8 Pin exact wheel versions AND a revision/tag pin note for the weights
  snapshot in the vendored requirements (reproducibility, no drift).
- R9 Weight redistribution terms: code is Apache-2.0 (`OSS/LICENSE`,
  pyproject license field); confirm google/timesfm HF repo licensing before
  any DMG-bundled weights tier. Manual-seed tier sidesteps redistribution
  entirely — another argument for Tier 1 first.
- R10 Where weights live: `~/.multica/models/timesfm/` vs inside profile
  backup scope (`scripts/backup.sh`) — decide whether weights join backups
  (probably exclude; they're re-seedable).

Open questions for the user / next session
- Q1 Accept shipping a ~100 MB-class wheelhouse inside the DMG? Or manual
  seed for deps too (numpy/safetensors often present via system python?
  unreliable — lean yes-to-wheelhouse).
- Q2 pandas: include (enables forecast_csv.py verbatim reuse, +47 MB
  installed) vs wrap raw library path numpy-only. Lean include — reuse beats
  reimplementing date inference.
- Q3 Route suffix confirmation: `timesfm-lab` (this report's candidate) vs
  `timesfm` clash-free alternative naming; FLAG_ROUTE_SUFFIX is
  single-sourced (issue-labs-section.tsx:29-49) so pick once.
- Q4 Does the 10-forecast history view render charts inline (desktop canvas
  / existing chart lib?) or table-first? Scope the view commit accordingly.
