# Copyright 2026 Multica (fork addition — not part of upstream timesfm).
# Apache-2.0, matching the vendored upstream code in ./timesfm.
"""TimesFM lab loopback service (0.5.82 WL2).

FastAPI wrapper around the vendored TimesFM 2.5 torch stack, spawned by
apps/desktop/vendor/timesfm-src/run.sh as:

    python -m uvicorn run_loopback:app --host 127.0.0.1 --port <port>

Endpoints (the Go server reverse-proxies /experimental/timesfm here and
its handler forwards /forecast):

    POST /forecast  {series: number[][], horizon: int, dates?: string[][]}
                 → {series: [{point: [...], quantiles: {lower_90, lower_80,
                     median, upper_80, upper_90}, provenance, dates?}],
                    provenance: "model"|"seasonal_naive"|"mixed",
                    model_present: bool, horizon: int}
    GET  /health → {status: "ok"}
    GET  /info   → {model_present, ram_ok, variant}

Fork laws honoured here:

- NO network, ever. HF_HUB_OFFLINE=1 + TRANSFORMERS_OFFLINE=1 are set in
  os.environ BEFORE torch/timesfm are imported, and the model is only
  ever loaded via local-dir from_pretrained(
  ~/.multica/models/timesfm) containing model.safetensors. No HF repo id
  is ever constructed.
- Never fails closed: if the weights are absent / RAM is below the 2 GB
  floor / torch import fails, /forecast still answers using the Tier-0
  seasonal-naive fallback forecaster with provenance="seasonal_naive".
- torch_compile=False on load (macOS CPU first-compile latency, report
  §1.3 / R6).
- XReg covariates are de-scoped from v1 (report §1.5).

Forecast logic wraps (not reimplements) the upstream model API exactly
as timesfm-forecasting/scripts/forecast_csv.py does: model.compile(
ForecastConfig(max_context=1024, max_horizon=256, ...)) then
model.forecast(horizon=H, inputs=[...]) → (point [n,H], quantiles
[n,H,10]) with the fixed decode indices 1/2/5/8/9 = 10th/20th/50th/80th/
90th percentiles.
"""
from __future__ import annotations

import logging
import math
import os
import subprocess
import sys
import threading
from pathlib import Path

# Fork law: offline env MUST be set before torch / timesfm import.
os.environ.setdefault("HF_HUB_OFFLINE", "1")
os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")
os.environ.setdefault("HF_HUB_DISABLE_TELEMETRY", "1")

_HERE = Path(__file__).resolve().parent
if str(_HERE) not in sys.path:
    sys.path.insert(0, str(_HERE))

from fastapi import Body, FastAPI  # noqa: E402

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("timesfm.run_loopback")

WEIGHTS_DIR = Path(os.environ.get("TIMESFM_WEIGHTS_DIR", "")) if os.environ.get("TIMESFM_WEIGHTS_DIR") else Path.home() / ".multica" / "models" / "timesfm"
WEIGHTS_FILENAME = "model.safetensors"
VARIANT = "timesfm-2.5-200m-torch"

# Model RAM floor (report §1.4: v2.5 min 2 GB / rec 4 GB). Below the
# floor we politely refuse to LOAD the model (never to serve — the
# seasonal-naive fallback keeps /forecast green).
MIN_MODEL_RAM_GB = 2.0

MAX_HORIZON = 256  # ForecastConfig max_horizon
MAX_SERIES = 64  # per-request series cap; issue-scale requests are 1-3

app = FastAPI(title="TimesFM Forecast Lab", version="0.1.0")

_state_lock = threading.Lock()
_model = None          # compiled TimesFM_2p5_200M_torch, or None
_model_failed = False  # a load attempt already failed this process


def model_present() -> bool:
    return (WEIGHTS_DIR / WEIGHTS_FILENAME).is_file()


def total_ram_gb() -> float:
    """Total physical RAM. sysctl hw.memsize on darwin (primary target);
    /proc/meminfo fallback for linux dev boxes; psutil if available."""
    try:
        import psutil  # optional

        return psutil.virtual_memory().total / (1024**3)
    except Exception:  # noqa: BLE001
        pass
    try:
        if sys.platform == "darwin":
            out = subprocess.run(
                ["sysctl", "-n", "hw.memsize"], capture_output=True, text=True, timeout=5
            )
            return int(out.stdout.strip()) / (1024**3)
        if Path("/proc/meminfo").is_file():
            for line in Path("/proc/meminfo").read_text().splitlines():
                if line.startswith("MemTotal:"):
                    return int(line.split()[1]) / (1024**2)
    except Exception:  # noqa: BLE001
        pass
    # Unknown — assume enough rather than block the model on a probe bug.
    return 64.0


def ram_ok() -> bool:
    return total_ram_gb() >= MIN_MODEL_RAM_GB


def _mean_abs_delta(values: list[float]) -> float:
    if len(values) < 2:
        return 0.0
    total = sum(abs(values[i] - values[i - 1]) for i in range(1, len(values)))
    return total / (len(values) - 1)


def seasonal_naive_forecast(series: list[float], horizon: int) -> tuple[list[float], dict[str, list[float]]]:
    """Tier-0 fallback (always available). Season lengths tried in order
    of domain plausibility (weekly=7, monthly=12, hourly-daily=24,
    weekly-years=52); the first season with >= 2 full cycles of history
    wins, else the forecaster degrades to last-value persistence. The
    quantile band is an honest empirical estimate: ±z·(mean absolute
    step delta of recent history), z = 1.2816 / 0.8416 for the 90 / 80
    bands, median = the point path."""
    n = len(series)
    season = 1
    for m in (7, 12, 24, 52):
        if n >= 2 * m:
            season = m
            break
    point = [float(series[n - season + (i % season)]) for i in range(horizon)]
    recent = series[-min(n, 64):]
    delta = _mean_abs_delta(recent) if n > 1 else 0.0
    band: dict[str, list[float]] = {"median": list(point)}
    for key, z in (("lower_90", -1.2816), ("lower_80", -0.8416),
                   ("upper_80", 0.8416), ("upper_90", 1.2816)):
        band[key] = [v + z * delta for v in point]
    return point, band


def _get_model():
    """Load + compile the model once per process. Returns None when the
    weights are absent, RAM is under the floor, or the torch stack is
    broken — callers fall back to seasonal-naive. Never raises."""
    global _model, _model_failed
    if _model is not None or _model_failed:
        return _model
    with _state_lock:
        if _model is not None or _model_failed:
            return _model
        if not model_present():
            log.info("weights not seeded at %s — serving seasonal_naive", WEIGHTS_DIR)
            _model_failed = True
            return None
        if not ram_ok():
            log.warning("RAM below %.1f GB floor — refusing to load the model (seasonal_naive stays available)", MIN_MODEL_RAM_GB)
            _model_failed = True
            return None
        try:
            import numpy as np  # noqa: F401 (ensures the stack is importable before timesfm)
            import timesfm

            torch_mod = __import__("torch")
            torch_mod.set_float32_matmul_precision("high")
            log.info("loading TimesFM 2.5 from local dir %s (offline)", WEIGHTS_DIR)
            # Local-dir from_pretrained ONLY (no HF repo id anywhere).
            # Default torch_compile=False — see module docstring (R6).
            model = timesfm.TimesFM_2p5_200M_torch.from_pretrained(str(WEIGHTS_DIR))
            model.compile(
                timesfm.ForecastConfig(
                    max_context=1024,
                    max_horizon=MAX_HORIZON,
                    normalize_inputs=True,
                    use_continuous_quantile_head=True,
                    force_flip_invariance=True,
                    infer_is_positive=True,
                    fix_quantile_crossing=True,
                    per_core_batch_size=32,
                )
            )
            _model = model
            log.info("TimesFM 2.5 ready")
        except Exception as exc:  # noqa: BLE001 — never fail closed
            log.error("model load failed (%s) — serving seasonal_naive", exc)
            _model_failed = True
            _model = None
        return _model


def _validate_payload(payload: dict) -> tuple[list[list[float]], int, list[list[str]] | None]:
    series = payload.get("series")
    if not isinstance(series, list) or not series:
        raise ValueError("`series` must be a non-empty array of number arrays")
    if len(series) > MAX_SERIES:
        raise ValueError(f"`series` supports at most {MAX_SERIES} arrays per request")
    cleaned: list[list[float]] = []
    for i, s in enumerate(series):
        if not isinstance(s, list) or not s:
            raise ValueError(f"series[{i}] must be a non-empty number array")
        vals: list[float] = []
        for j, v in enumerate(s):
            if isinstance(v, bool) or not isinstance(v, (int, float)):
                raise ValueError(f"series[{i}][{j}] is not a number")
            f = float(v)
            if math.isnan(f):
                continue  # NaN-tolerant: the model strips leading NaNs and
            # interpolates interior gaps; dropping NaNs here keeps the
            # JSON round-trip strict (JSON has no NaN literal) while
            # matching the upstream NaN-tolerance contract.
            if math.isinf(f):
                raise ValueError(f"series[{i}][{j}] is infinite")
            vals.append(f)
        if len(vals) < 2:
            raise ValueError(f"series[{i}] needs at least 2 finite points")
        cleaned.append(vals)
    horizon = payload.get("horizon")
    if isinstance(horizon, bool) or not isinstance(horizon, int) or horizon < 1:
        raise ValueError("`horizon` must be a positive integer")
    horizon = min(horizon, MAX_HORIZON)
    dates = payload.get("dates")
    if dates is not None:
        if not isinstance(dates, list) or len(dates) != len(cleaned) or not all(
            isinstance(d, list) for d in dates
        ):
            raise ValueError("`dates` must be an array of string arrays parallel to `series`")
        dates = [[str(x) for x in d] for d in dates]
    return cleaned, horizon, dates


@app.get("/health")
async def health():
    return {"status": "ok", "service": "timesfm"}


@app.get("/info")
async def info():
    present = model_present()
    return {
        "model_present": present,
        "ram_ok": ram_ok(),
        "variant": VARIANT if present else "seasonal_naive",
        "weights_dir": str(WEIGHTS_DIR),
        "min_ram_gb": MIN_MODEL_RAM_GB,
    }


@app.post("/forecast")
async def forecast(payload: dict = Body(...)):
    try:
        series, horizon, dates = _validate_payload(payload or {})
    except ValueError as exc:
        from fastapi import HTTPException

        raise HTTPException(status_code=400, detail=str(exc)) from exc

    model = _get_model()
    out_series: list[dict] = []
    provenances: set[str] = set()
    if model is not None:
        import numpy as np

        inputs = [np.asarray(s, dtype=np.float32) for s in series]
        point, quantiles = model.forecast(horizon=horizon, inputs=inputs)
        for i in range(len(series)):
            provenances.add("model")
            entry: dict = {
                "point": [float(x) for x in point[i]],
                "quantiles": {
                    "lower_90": [float(x) for x in quantiles[i, :, 1]],
                    "lower_80": [float(x) for x in quantiles[i, :, 2]],
                    "median": [float(x) for x in quantiles[i, :, 5]],
                    "upper_80": [float(x) for x in quantiles[i, :, 8]],
                    "upper_90": [float(x) for x in quantiles[i, :, 9]],
                },
                "provenance": "model",
            }
            if dates is not None:
                entry["dates"] = dates[i]
            out_series.append(entry)
    else:
        for i, s in enumerate(series):
            pts, band = seasonal_naive_forecast(s, horizon)
            provenances.add("seasonal_naive")
            entry = {"point": pts, "quantiles": band, "provenance": "seasonal_naive"}
            if dates is not None:
                entry["dates"] = dates[i]
            out_series.append(entry)

    if len(provenances) == 1:
        provenance = provenances.pop()
    else:
        provenance = "mixed"
    return {
        "series": out_series,
        "provenance": provenance,
        "model_present": model is not None,
        "horizon": horizon,
    }
