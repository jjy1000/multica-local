# VENDOR-NOTICE — apps/desktop/vendor/timesfm-src/

- **Upstream**: google/timesfm (`https://github.com/google-research/timesfm`)
- **Snapshot**: local OSS tree `开发项目参考/timesfm-master` pinned 2026-08-27
  (package version 2.0.2 per upstream `pyproject.toml`; model generation
  TimesFM **2.5, 200M params**)
- **License**: Apache-2.0 — full text in [`LICENSE`](LICENSE) (Copyright
  2025 Google LLC). This notice does not replace the license.
- **Scope**: torch backend ONLY. Vendored from upstream `src/timesfm/`:
  `__init__.py`, `configs.py`, `timesfm_2p5/{timesfm_2p5_base,timesfm_2p5_torch}.py`,
  `torch/*` (dense / transformer / normalization / util), `utils/xreg_lib.py`.
  The **flax stack is NOT vendored** (`flax/`, `timesfm_2p5/timesfm_2p5_flax.py`)
  — upstream's `__init__.py` already wraps both backend imports in
  `try/except ImportError`, so `import timesfm` stays clean with flax
  absent; the torch branch is untouched.
- **Weights are NOT bundled.** The user manually seeds
  `~/.multica/models/timesfm/model.safetensors` (google/timesfm-2.5-200m-pytorch
  snapshot, ~800 MB). The loopback service only ever calls
  `from_pretrained(<local dir>)` with `HF_HUB_OFFLINE=1` /
  `TRANSFORMERS_OFFLINE=1` set in-process — no Hugging Face network
  traffic, ever. Without seeded weights the service serves the Tier-0
  seasonal-naive fallback (`provenance: "seasonal_naive"`).
- **Dependencies** come from `wheelhouse/` (offline install via
  `run.sh` → `pip install --no-index --find-links`). Rebuild the
  wheelhouse with `bash scripts/build-timesfm-wheelhouse.sh`
  (networked, run on a dev mac, NOT in-app). The wheelhouse directory is
  gitignored — it is reproducible from `requirements-timesfm.txt`.
- **Local modifications vs upstream**: none to the vendored `.py` files.
  Multica-authored additions in this directory: `run.sh`,
  `run_loopback.py`, `requirements-timesfm.txt`, this notice.
- **Sync upstream**: copy the files listed under Scope from a fresh
  upstream checkout, re-verify the import-guard in `__init__.py`, bump
  the Snapshot date, and re-run `scripts/build-timesfm-wheelhouse.sh`
  if pins move.
