"""PYTHIA oracle API — Osiris world data in, future predictions out.

0.3.16+ (Multica fork): when MULTICA_AGENT_RUNTIME_URL is set, the engine
treats the Multica issue stream as its "world view" instead of an Osiris
server. /health and the lifespan boot respect that — we do not block on
an Osiris probe that would never resolve.
"""
from __future__ import annotations

import asyncio
import logging
import math
import os
import secrets
from contextlib import asynccontextmanager

from fastapi import Body, FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, StreamingResponse

from .config import CONFIG
from .state import STATE

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(levelname)s %(message)s")
log = logging.getLogger("pythia.server")


def _is_multica_mode() -> bool:
    return bool(os.environ.get("MULTICA_AGENT_RUNTIME_URL", "").strip())


@asynccontextmanager
async def lifespan(app: FastAPI):
    from .loop import ALERTS, BRIEF, LOOP, RESOLVE, SENSE
    from .pipeline import run_prediction
    LOOP.start()
    SENSE.start()   # keep live events fresh between forecasts
    RESOLVE.start()  # grade forecasts once their horizon expires
    ALERTS.start()   # evaluate user alert rules against the live world
    BRIEF.start()    # the daily Morning Brief, at the configured hour
    log.info("PYTHIA oracle up | %s | multica_mode=%s", CONFIG.summary(), _is_multica_mode())

    async def _boot():
        from .runtime import intake
        from .pipeline import hydrate_from_ledger, refresh_world
        hydrate_from_ledger()              # deck + rings light up with live forecasts instantly
        if _is_multica_mode():
            # No Osiris server to wait for — refresh immediately so the
            # renderer / agents get a populated world snapshot on first hit.
            await refresh_world()
            await run_prediction(trigger="boot")
            return
        # wait for Osiris to be reachable, then give its routes a moment to compile
        for _ in range(20):
            if await intake.health():
                break
            await asyncio.sleep(2)
        await asyncio.sleep(4)
        await refresh_world()              # populate live events immediately (for agents/chat)
        await run_prediction(trigger="boot")

    asyncio.create_task(_boot())
    yield


app = FastAPI(title="PYTHIA Oracle", version="0.2.0", lifespan=lifespan)
app.add_middleware(CORSMiddleware, allow_origins=["*"], allow_methods=["*"], allow_headers=["*"])

# ---------------------------------------------------------------------------
# Loopback token gate.
#
# The engine binds 127.0.0.1, which accepts every local user account, and
# the engine env carries the user's Multica JWT — a same-host process from
# another user session could otherwise drive forecasts (LLM spend) or read
# world state. When PYTHIA_ENGINE_TOKEN is set (the desktop main process
# generates one per spawn), every route except /health requires the
# X-API-Key header to match that token OR the MULTICA_API_TOKEN copy in
# this process env (agent CLIs / `multica pythia` inherit the JWT, not the
# manager-generated token). Empty env → open engine, so dev / standalone
# runs (`python3 -m uvicorn engine.server:app`) keep working unchanged.
# ---------------------------------------------------------------------------
_ENGINE_TOKEN = os.environ.get("PYTHIA_ENGINE_TOKEN", "").strip()
_ENGINE_JWT = os.environ.get("MULTICA_API_TOKEN", "").strip()


@app.middleware("http")
async def _loopback_token_guard(request, call_next):
    if _ENGINE_TOKEN and request.url.path != "/health":
        # compare_digest raises TypeError on str inputs with non-ASCII
        # chars; Starlette decodes header bytes as latin-1, so a raw
        # >=0x80 byte in the header would 500 instead of 401. Compare
        # bytes (no ASCII restriction) so bad keys fail closed as 401.
        key = request.headers.get("X-API-Key", "").encode("utf-8", "replace")
        ok = secrets.compare_digest(key, _ENGINE_TOKEN.encode())
        if not ok and _ENGINE_JWT:
            ok = secrets.compare_digest(key, _ENGINE_JWT.encode())
        if not ok:
            return JSONResponse({"error": "engine token required"}, status_code=401)
    return await call_next(request)


@app.get("/health")
async def health():
    return {"status": "ok", "service": "pythia-oracle", "config": CONFIG.summary()}


@app.get("/config")
async def config():
    return CONFIG.summary()


_links_cache: dict = {"ts": 0.0, "data": None}


@app.get("/links")
async def links():
    import time as _t
    now = _t.monotonic()
    if _links_cache["data"] and now - _links_cache["ts"] < 8:
        data = dict(_links_cache["data"])
    else:
        from .runtime import intake, oracle
        osiris_up, oracle_up = await asyncio.gather(intake.health(), oracle.health())
        data = {"engine": True, "osiris": bool(osiris_up), "oracle": bool(oracle_up)}
        _links_cache.update(ts=now, data=dict(data))
    from .runtime import oracle as _oracle
    data.update(model=_oracle.model, generating=STATE.generating,
                loop=STATE.loop_enabled, last_run_ms=STATE.last_run_ms,
                prediction_count=len(STATE.predictions))
    return data


@app.get("/models")
async def models():
    """Installed local models + the one currently in use."""
    from .runtime import oracle
    return {"models": await oracle.list_models(), "current": oracle.model}


@app.post("/model")
async def set_model(payload: dict = Body(...)):
    """Switch the oracle's model at runtime."""
    from .runtime import oracle
    name = (payload or {}).get("model", "").strip()
    if not name:
        raise HTTPException(400, "provide `model`")
    oracle.model = name
    STATE.publish("model", {"model": name})
    log.info("oracle model switched -> %s", name)
    return {"model": oracle.model}


@app.get("/swarm/models")
async def swarm_models_get():
    """Per-persona model overrides for the swarm council + the models available to pick from."""
    from .runtime import oracle
    from .swarm import PERSONAS
    return {
        "personas": [name for name, _ in PERSONAS],
        "overrides": STATE.swarm_models,          # persona -> model (only those overridden)
        "default_model": oracle.model,            # what a persona uses when not overridden
        "available": await oracle.list_models(),
    }


@app.post("/swarm/model")
async def swarm_model_set(payload: dict = Body(...)):
    """Set (or clear) the model for one swarm persona. Empty/blank model = use the main model."""
    from .swarm import PERSONAS
    persona = (payload or {}).get("persona", "").strip()
    model = (payload or {}).get("model", "").strip()
    if persona not in {name for name, _ in PERSONAS}:
        raise HTTPException(400, "unknown persona")
    if model:
        STATE.swarm_models[persona] = model
    else:
        STATE.swarm_models.pop(persona, None)
    STATE.save_swarm_models()   # survive engine restarts
    log.info("swarm persona %s -> %s", persona, model or "(main)")
    return {"overrides": STATE.swarm_models}


@app.get("/predictions")
async def predictions(horizon: str | None = None, min_probability: float = 0.0):
    """Current forecasts, optionally filtered by `horizon` (24h|week|month|year)
    and `min_probability` (0..1)."""
    preds = [p for p in STATE.predictions
             if (not horizon or p.horizon == horizon) and p.probability >= min_probability]
    return {"predictions": [p.model_dump() for p in preds],
            "horizons": CONFIG.horizons,
            "world": STATE.world.model_dump() if STATE.world else None}


@app.post("/predict")
async def predict():
    """Run an oracle pass now (sense the world -> forecast)."""
    from .pipeline import run_prediction
    if STATE.generating:
        return {"status": "already running"}
    asyncio.create_task(run_prediction(trigger="manual"))
    return {"status": "started"}


@app.get("/agent/view")
async def agent_view():
    """One consolidated, machine-readable view of the world for external agents:
    the assembled brief, every live event (with coords), and current predictions.
    For a live feed, subscribe to GET /state/stream (SSE)."""
    from .runtime import oracle
    by_domain: dict[str, list] = {}
    for e in STATE.events:
        by_domain.setdefault(e.category, []).append({
            "title": e.title, "summary": e.summary, "source": e.source,
            "lat": e.lat, "lng": e.lng, "salience": e.salience, "ts": e.ts,
        })
    from .tickers import watch_from_predictions
    return {
        "generated_at": STATE.last_run_ms,
        "model": oracle.model,
        "summary": (STATE.world.text if STATE.world else ""),
        "domains": (STATE.world.domains if STATE.world else {}),
        "events_by_domain": by_domain,
        "event_count": len(STATE.events),
        "predictions": [p.model_dump() for p in STATE.predictions],
        # the user's watched markets are PRIORITY context, not background noise:
        # surface them (and the oracle's own forecast-linked picks) so any agent
        # reading this view treats them as things the user needs to know about.
        "market_watch": {
            "note": ("These symbols are explicitly watched by the user — treat developments "
                     "touching them as high-priority when reasoning or reporting."),
            "watchlist": list(STATE.watchlist),
            "pythia_watch": watch_from_predictions(STATE.predictions),
        },
        "live_stream": "/state/stream",
    }


@app.get("/agent/events")
async def agent_events(domain: str | None = None, source: str | None = None,
                       min_salience: float = 0.0, since: int = 0, limit: int = 0):
    """Every live world event, with optional filters so an agent gets exactly what it wants:
    `domain` (category), `source`, `min_salience` (0..1), `since` (epoch ms), `limit`.
    Returned most-salient first, with the list of available domains for discovery."""
    out = []
    for e in STATE.events:
        if domain and e.category != domain:
            continue
        if source and e.source != source:
            continue
        if e.salience < min_salience:
            continue
        if since and e.ts < since:
            continue
        out.append(e)
    out.sort(key=lambda e: e.salience, reverse=True)
    if limit > 0:
        out = out[:limit]
    return {"count": len(out), "events": [e.model_dump() for e in out],
            "domains_available": sorted({e.category for e in STATE.events})}


@app.get("/scorecard")
async def scorecard():
    """PYTHIA's track record. Every forecast is persisted when made; an LLM judge
    grades it against the archived world once its horizon expires. Returns overall
    Brier score, hit rate, per-horizon + per-persona accuracy, calibration bins,
    and the most recent resolutions."""
    from .runtime import ledger
    return ledger.scorecard()


@app.post("/scorecard/resolve")
async def scorecard_resolve():
    """Run a resolution pass now (grade any due forecasts) instead of waiting
    for the hourly loop."""
    from .loop import resolve_due
    if STATE.generating:
        return {"status": "busy — oracle pass in progress"}
    judged = await resolve_due()
    return {"status": "ok", "judged": judged}


@app.get("/world")
async def world():
    if not STATE.world:
        raise HTTPException(404, "no world brief yet — run /predict")
    return STATE.world.model_dump()


@app.get("/runs")
async def runs():
    return {"runs": [r.model_dump() for r in list(STATE.runs.values())[-20:]]}


@app.get("/state")
async def state():
    return STATE.snapshot()


@app.get("/state/stream")
async def stream():
    async def gen():
        q = STATE.subscribe()
        try:
            yield STATE.sse({"kind": "snapshot", "payload": STATE.snapshot()})
            while True:
                try:
                    msg = await asyncio.wait_for(q.get(), timeout=15)
                    yield STATE.sse(msg)
                except asyncio.TimeoutError:
                    yield ": ping\n\n"
        finally:
            STATE.unsubscribe(q)

    return StreamingResponse(gen(), media_type="text/event-stream",
                             headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"})


@app.post("/chat")
async def chat(payload: dict = Body(...)):
    """Ask the oracle anything — it sees every live source + current predictions.
    Pass `persona` (e.g. "Strategist") to put one council specialist on the line,
    answered in its voice by its own configured model."""
    from .runtime import intake, oracle
    from .swarm import PERSONAS
    from .world_state import build_brief
    payload = payload or {}
    msg = payload.get("message", "").strip()
    if not msg:
        raise HTTPException(400, "provide `message`")
    persona = None
    model = None
    want = str(payload.get("persona") or "").strip()
    if want:
        match = next(((n, l) for n, l in PERSONAS if n.lower() == want.lower()), None)
        if match is None:
            raise HTTPException(400, f"unknown persona {want!r} — one of {[n for n, _ in PERSONAS]}")
        persona = match
        model = STATE.swarm_models.get(match[0]) or None
    brief = STATE.world
    if brief is None:
        try:
            brief = build_brief(await intake.fetch(limit=150))
            STATE.set_world(brief)
        except Exception:  # noqa: BLE001
            brief = None
    answer = await oracle.chat(msg, brief, STATE.predictions, payload.get("history", []),
                               persona=persona, model=model)
    return {"answer": answer, "persona": persona[0] if persona else None}


@app.post("/loop")
async def loop(payload: dict = Body(default={})):
    STATE.set_loop(bool(payload.get("enabled", not STATE.loop_enabled)))
    return {"loop_enabled": STATE.loop_enabled}


@app.get("/watch")
async def watch():
    """The market watch: the user's watchlist symbols + PYTHIA's Watch — tickers the
    oracle's own live forecasts touch, each carrying the forecast that flagged it.
    Prices come from the UI's keyless quote route; this endpoint is the *why*."""
    from .tickers import watch_from_predictions
    return {
        "watchlist": list(STATE.watchlist),
        "pythia_watch": watch_from_predictions(STATE.predictions),
    }


@app.post("/watchlist")
async def watchlist_add(payload: dict = Body(...)):
    """Add a symbol (Yahoo-style, e.g. AAPL, CL=F, BTC-USD) to the watchlist."""
    sym = str((payload or {}).get("symbol", "")).strip().upper()
    if not sym or len(sym) > 12 or not all(c.isalnum() or c in "^=-." for c in sym):
        raise HTTPException(400, "provide a `symbol` like AAPL, CL=F or BTC-USD")
    if sym not in STATE.watchlist:
        if len(STATE.watchlist) >= 40:
            raise HTTPException(400, "watchlist is full (40)")
        STATE.watchlist.append(sym)
        STATE.save_watchlist()
    return {"watchlist": STATE.watchlist}


@app.delete("/watchlist/{symbol}")
async def watchlist_remove(symbol: str):
    """Remove a symbol from the watchlist."""
    sym = symbol.strip().upper()
    STATE.watchlist = [s for s in STATE.watchlist if s != sym]
    STATE.save_watchlist()
    return {"watchlist": STATE.watchlist}


@app.get("/drift")
async def drift():
    """Probability drift for every live forecast: how each number moved across
    passes (similarity-matched through the ledger). {id: {points: [{ts,p}], delta}}
    — delta is the max-min swing over the tracked window."""
    from .runtime import ledger
    out = {}
    for p in STATE.predictions:
        pts = ledger.history_for(p.statement, p.horizon)
        if len(pts) >= 2:
            vals = [x["p"] for x in pts]
            out[p.id] = {"points": pts, "delta": round(max(vals) - min(vals), 2)}
    return {"drift": out}


@app.get("/alerts")
async def alerts_list():
    """The user's alert rules + the recent fired-alert feed size."""
    from . import alerts
    return {"rules": alerts.RULES, "kinds": list(alerts.KINDS), "feed_size": len(alerts.FEED)}


@app.post("/alerts")
async def alerts_upsert(payload: dict = Body(...)):
    """Create or update an alert rule. Kinds: event {keywords, domain?, min_salience},
    quake {min_magnitude}, market {symbol, move_percent}, vix {level},
    forecast {min_probability, horizon?, keywords?}. Include `id` to update."""
    from . import alerts
    try:
        return alerts.upsert_rule(payload or {})
    except (ValueError, TypeError) as e:
        raise HTTPException(400, str(e))


@app.delete("/alerts/{rule_id}")
async def alerts_delete(rule_id: str):
    from . import alerts
    if not alerts.remove_rule(rule_id):
        raise HTTPException(404, "no such rule")
    return {"status": "ok"}


@app.get("/alerts/feed")
async def alerts_feed(since: int = 0, limit: int = 50):
    """Fired alerts (and Morning Briefs), newest last — the UI polls this for
    browser notifications. `since` = only items with ts > since (epoch ms)."""
    from . import alerts
    items = [a for a in alerts.FEED if a["ts"] > since]
    return {"alerts": items[-max(1, min(200, limit)):], "now": __import__("time").time_ns() // 1_000_000}


@app.get("/brief")
async def brief_get():
    """The Morning Brief — latest text + schedule config + history of dates."""
    from . import brief
    return {"config": brief.get_config(), "latest": brief.latest(), "history": brief.history()}


@app.post("/brief/run")
async def brief_run():
    """Write the brief now instead of waiting for the schedule."""
    from . import brief
    try:
        return await brief.generate(trigger="manual")
    except Exception as e:  # noqa: BLE001
        raise HTTPException(503, f"brief generation failed: {e}")


@app.post("/brief/config")
async def brief_config(payload: dict = Body(...)):
    """Set the daily schedule: {"time": "07:30", "enabled": true}."""
    from . import brief
    try:
        return brief.set_config(payload.get("time"), payload.get("enabled"))
    except (ValueError, TypeError) as e:
        raise HTTPException(400, str(e))


@app.get("/personas")
async def personas():
    """The swarm's persona roster — name + the lens each judges through. Drives the
    what-if field's 'who deliberates' checkboxes."""
    from .swarm import PERSONAS
    return {"personas": [{"name": n, "lens": l} for n, l in PERSONAS]}


@app.post("/whatif")
async def whatif(payload: dict = Body(...)):
    """Counterfactual mode: 'assume X just happened' — the oracle forecasts the
    knock-on effects grounded in the live world, then (optionally) the chosen swarm
    personas deliberate on those knock-ons. Ephemeral: nothing is stored, nothing
    enters the track record. Returns {scenario, narrative, predictions, personas}."""
    from .config import CONFIG
    from .runtime import intake, oracle
    from .world_state import build_brief
    payload = payload or {}
    scenario = payload.get("scenario", "").strip()
    if not scenario:
        raise HTTPException(400, "provide `scenario`, e.g. {\"scenario\": \"the Strait of Hormuz closes tonight\"}")
    # which personas the user checked; omit/empty list = no council (single-shot)
    personas = payload.get("personas")
    if personas is not None and not isinstance(personas, list):
        personas = None
    brief = STATE.world
    if brief is None:
        try:
            brief = build_brief(await intake.fetch(limit=150))
            STATE.set_world(brief)
        except Exception:  # noqa: BLE001
            brief = None
    scen, narrative, preds = await oracle.what_if(scenario, brief)
    used = list(personas) if personas else []
    if used and CONFIG.swarm_enabled and preds:
        from .swarm import deliberate
        try:
            preds = await deliberate(oracle, brief, preds, personas=used)
        except Exception as e:  # noqa: BLE001 — a stalled council shouldn't sink the what-if
            log.warning("what-if deliberation skipped: %s", e)
    return {"scenario": scen, "narrative": narrative,
            "predictions": [p.model_dump() for p in preds], "personas": used}


@app.get("/webhooks")
async def webhooks_list():
    """Registered outbound webhooks (see engine/webhooks.py for payload shapes)."""
    from . import webhooks
    return {"webhooks": webhooks.HOOKS}


@app.post("/webhooks")
async def webhooks_add(payload: dict = Body(...)):
    """Register a webhook: {"url": "...", "min_probability": 0.7, "min_salience": 0.85}.
    The engine POSTs {kind: "forecasts"|"events", ...} when thresholds are crossed."""
    from . import webhooks
    url = (payload or {}).get("url", "").strip()
    if not url.startswith(("http://", "https://")):
        raise HTTPException(400, "provide an http(s) `url`")
    hook = webhooks.add(url, payload.get("min_probability", 0.7), payload.get("min_salience", 0.85))
    return {"added": hook, "webhooks": webhooks.HOOKS}


@app.delete("/webhooks")
async def webhooks_remove(url: str):
    """Unregister a webhook by exact URL."""
    from . import webhooks
    if not webhooks.remove(url):
        raise HTTPException(404, "no webhook with that url")
    return {"webhooks": webhooks.HOOKS}


# /forecast/issue — 0.3.29+
# Bound to a Multica issue. The Multica Go layer posts here once per round
# during the per-issue SSE loop; we return one synthetic envelope the Go
# stream emits to the renderer. Wire shape mirrors what claude_lab_forecast
# hands the Claude Lab forecast tab so the renderer can share one parser.
@app.post("/forecast/issue")
async def forecast_issue(payload: dict = Body(...)):
    """One forecast round bound to a Multica issue.

    Body:
      - question (str, required): the issue title (forecast target).
      - scenario_context (str): the title + body excerpt from Go.
      - history (list): prior-round digests from earlier runs of the SAME
        issue (continuation) — [{round, narrative, probability}]. Injected
        into the prompt so a later round sharpens an earlier one instead of
        starting from zero (0.5.111 continuation contract).
      - variables (str): user-injected new variables for a continuation
        round ("把汇率冲击调高到 20% 后重新推演…").
      - council (bool, default True): run the FULL 4-persona swarm on the
        round. Response then carries `council` (votes/consensus/spread/
        split) and `base_probability` (the oracle's solo estimate that the
        consensus is measured against).
      - issue_id / issue_number / horizon / persona / round / seed: echo
        fields that round out the envelope so the renderer can render a
        report row without a follow-up GET.

    Returns the Go-layer envelope: {scenario, narrative, probability,
    confidence}. The Multica side fills in `lab_source`, `createdAt`,
    `id` so the wire stays stable across synthetic and oracle paths.
    """
    payload = payload or {}
    question = str(payload.get("question") or "").strip()
    if not question:
        raise HTTPException(400, "provide `question` (the issue title)")
    scenario_context = str(payload.get("scenario_context") or "").strip()
    horizon = str(payload.get("horizon") or "week").strip().lower() or "week"
    persona = str(payload.get("persona") or "strategist").strip() or "strategist"
    variables = str(payload.get("variables") or "").strip()
    council = payload.get("council", True)
    council = True if council is None else bool(council)
    raw_history = payload.get("history")
    if not isinstance(raw_history, list):
        raw_history = []
    history: list[dict] = []
    for h in raw_history:
        if isinstance(h, dict) and h.get("narrative"):
            try:
                history.append({"round": int(h.get("round") or 0),
                                "narrative": str(h["narrative"])[:600],
                                "probability": float(h.get("probability") or 0.0)})
            except (TypeError, ValueError):
                continue
    seed = payload.get("seed") or 0
    try:
        seed = int(seed)
    except (TypeError, ValueError):
        seed = 0
    rnd = payload.get("round") or 1
    try:
        rnd = int(rnd)
    except (TypeError, ValueError):
        rnd = 1
    if rnd < 1:
        rnd = 1

    # Continuation context: prior-round digest + the user's freshly injected
    # variables ride into EVERY prompt (oracle + council) so round N is
    # conditioned on rounds 1..N-1 and on what the user changed — the
    # SocialSim "inherited baseline" idea, cut down to what an issue round
    # can carry.
    context_parts = [scenario_context] if scenario_context else []
    if history:
        lines = "\n".join(
            f"第{h['round']}轮（概率 {round(h['probability'] * 100)}%）：{h['narrative']}"
            for h in history)
        context_parts.append(f"=== 历史推演轮次 ===\n{lines}")
    if variables:
        context_parts.append(f"=== 用户注入的新变量 ===\n{variables[:1200]}")
    continuation_context = "\n\n".join(context_parts)

    # Use the existing whatif() oracle helper — it's a single-shot
    # counterfactual pass that already produces (narrative, predictions).
    # We re-use it so the LLM prompt + JSON parsing stay identical to
    # the rest of the engine (one less code path to maintain).
    #
    # 0.5.104 honesty fix: when the LLM call fails (bridge unreachable,
    # MULTICA_REQUIRED misconfig, unparseable model JSON) we now FLAG the
    # envelope with `synthetic: true` so the Multica Go layer relabels it
    # synthetic_oracle_failover instead of passing fallback data off as a
    # real engine deduction. Pre-fix, a 200 with placeholder narrative was
    # indistinguishable from a genuine model answer.
    from .runtime import oracle
    # 0.5.112 Osiris follow-call: ground the round on FRESH world intel, not
    # whatever the startup refresh left behind. When the snapshot is missing
    # or older than PYTHIA_WORLD_TTL seconds (default 600) re-run the cheap
    # non-LLM sensing pass. Best-effort: a slow or failed intake falls back
    # to the stale snapshot — intel must never block a forecast round.
    try:
        age = STATE.world_age_seconds()
        ttl = float(os.getenv("PYTHIA_WORLD_TTL", "600") or 600)
        if age is None or age > ttl:
            from .pipeline import refresh_world
            await refresh_world()
    except Exception as e:  # noqa: BLE001 — intel refresh is optional
        log.warning("forecast_issue world refresh skipped: %s", e)
    brief = STATE.world
    synthetic = False
    try:
        scenario, narrative, preds = await oracle.what_if(
            scenario=question if not continuation_context else f"{question}\n\n{continuation_context[:3000]}",
            brief=brief,
        )
    except Exception as e:  # noqa: BLE001 — fall back to a synthetic envelope rather than 500
        log.warning("forecast_issue whatif failed: %s", e)
        scenario = question
        narrative = f"（推演第 {rnd} 轮,种子 {seed}）{question} 的演化路径仍在收集中。"
        preds = []
        synthetic = True

    # Pick the most-aligned prediction. The engine returns 4-6 predictions
    # per whatif; for an issue-bound round we surface the top one by
    # probability so the renderer has a stable single-line answer.
    if preds:
        preds_sorted = sorted(preds, key=lambda p: p.probability, reverse=True)
        top = preds_sorted[0]
        base_probability = float(top.probability)
        prob = base_probability
        scenario_out = top.statement or scenario
        reasoning = top.reasoning or ""
        narrative_out = f"{narrative}\n\n关键证据:{reasoning}" if reasoning else narrative
    else:
        # Synthetic fallback: derive a stable probability from seed so
        # the SSE loop produces a coherent (round_index, probability)
        # trajectory even without a live LLM. ±0.06 per round simulates
        # deliberation drift.
        #
        # 0.5.104: NO local `import math` here — a function-level import
        # makes `math` a local name for the WHOLE function, so the
        # success path's confidence expression below raised
        # UnboundLocalError the moment the LLM actually returned
        # predictions. The module-level import at the top of the file is
        # the only one.
        synthetic = True
        base_probability = None
        base = ((seed or 1) % 100) / 100.0
        drift = (rnd - 1) * 0.04
        prob = max(0.05, min(0.95, 0.45 + math.sin(base * 6.28 + rnd) * 0.18 + drift))
        scenario_out = question
        narrative_out = f"第 {rnd} 轮推演:目前证据有限,概率按种子序列生成,作为占位结果。" if not narrative else narrative

    # 0.5.111 full-council contract: the consensus number IS the headline
    # probability (so it can never disagree with the votes it summarizes);
    # the oracle's solo estimate rides along as base_probability for the
    # "oracle → council" delta, mirroring the deck flow. A council that
    # lands zero usable voices leaves the solo number as the headline.
    council_out = None
    if council:
        try:
            from .swarm import deliberate_issue
            council_out = await deliberate_issue(
                oracle, question, continuation_context or scenario_context,
                round_idx=rnd, total_rounds=int(payload.get("total_rounds") or rnd))
            if council_out.get("consensus") is not None:
                prob = float(council_out["consensus"])
        except Exception as e:  # noqa: BLE001 — a stalled council shouldn't sink the round
            log.warning("forecast_issue council skipped: %s", e)
            council_out = None

    return {
        "scenario": scenario_out,
        "narrative": narrative_out,
        "probability": round(prob, 3),
        "base_probability": round(base_probability, 3) if base_probability is not None else None,
        "confidence": round(min(0.95, 0.55 + abs(math.sin(seed or 1)) * 0.3), 3) if preds else round(0.5 + 0.05 * rnd, 3),
        "horizon": horizon,
        "persona": persona,
        "round": rnd,
        "synthetic": synthetic,
        "council": council_out,
    }


@app.post("/forecast/issue/report")
async def forecast_issue_report(payload: dict = Body(...)):
    """Synthesize the conclusion report for a completed per-issue run.

    Body: {question, scenario_context, rounds: [{round, scenario, narrative,
    probability, confidence}], variables?}. One LLM pass turns the whole
    deliberation into the issue-first conclusion report — consensus
    conclusion, per-persona stance comparison, key concerns, risks, and
    recommendations (the SocialSim final-analysis shape, adapted to
    forecasting). Freeform markdown; NOT JSON. `synthetic: true` marks a
    failed synthesis so the Go layer can fall back to its mechanical
    summary instead of passing an apology off as analysis.
    """
    payload = payload or {}
    question = str(payload.get("question") or "").strip()
    if not question:
        raise HTTPException(400, "provide `question`")
    scenario_context = str(payload.get("scenario_context") or "").strip()
    variables = str(payload.get("variables") or "").strip()
    raw_rounds = payload.get("rounds")
    if not isinstance(raw_rounds, list) or not raw_rounds:
        raise HTTPException(400, "provide `rounds` (at least one)")
    round_lines = []
    for i, r in enumerate(raw_rounds[:12]):
        if not isinstance(r, dict):
            continue
        prob = r.get("probability")
        try:
            prob_txt = f"{round(float(prob) * 100)}%" if prob is not None else "?"
        except (TypeError, ValueError):
            prob_txt = "?"
        narrative = str(r.get("narrative") or "").strip()
        if not narrative:
            continue
        round_lines.append(f"第{r.get('round') or i + 1}轮 (概率 {prob_txt}):{narrative[:700]}")
    if not round_lines:
        raise HTTPException(400, "rounds carried no narratives")

    from .runtime import oracle
    sys = (
        "你是 PYTHIA 的预测分析专家。用户刚刚对一个议题完成了多轮多视角推演"
        "(4 个专家角色的 council:Strategist/Economist/Naturalist/Skeptic),"
        "你要把全部轮次综合成一份中文结题报告。结构固定为:\n"
        "## 共识结论\n(直接回答议题问题,给出 council 共识概率区间与一句话判断)\n"
        "## 多视角立场\n(按角色对比分歧,谁更乐观/悲观、各自最关键的论据)\n"
        "## 关键关切\n(推演中反复出现的核心变量或不确定性)\n"
        "## 风险\n(最可能推翻结论的 2-3 条路径)\n"
        "## 建议\n(基于推演的 2-3 条可执行建议;如有用户注入变量,评估其影响)\n"
        "全部内容必须只依据给出的轮次记录,不得编造未出现的证据。直接输出 markdown,不要寒暄。"
    )
    parts = [f"=== 议题 ===\n{question}"]
    if scenario_context:
        parts.append(f"=== 场景背景 ===\n{scenario_context[:1200]}")
    if variables:
        parts.append(f"=== 用户注入的新变量 ===\n{variables[:1200]}")
    parts.append("=== 推演轮次记录 ===\n" + "\n".join(round_lines))
    try:
        report = await oracle._complete(
            [{"role": "system", "content": sys},
             {"role": "user", "content": "\n\n".join(parts)}],
            max_tokens=1600)
        if not report.strip():
            raise RuntimeError("empty report")
        return {"report": report.strip()[:8000], "synthetic": False}
    except Exception as e:  # noqa: BLE001 — Go falls back to the mechanical summary
        log.warning("forecast_issue report synthesis failed: %s", e)
        return {"report": "", "synthetic": True}
