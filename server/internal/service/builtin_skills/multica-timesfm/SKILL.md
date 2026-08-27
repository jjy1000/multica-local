---
name: multica-timesfm
description: "Zero-shot time-series forecasting for issue work. Use when an issue needs a numeric forecast with uncertainty bands — demand, traffic, revenue, sensor readings, weekly counts. POST a numeric series to the bundled TimesFM 2.5 engine via /api/experimental/timesfm/forecast/issue and the answer (median + 80%/90% quantile bands) persists into the issue's forecast records automatically. Requires the `timesfm` Labs flag enabled and the desktop-spawned engine. Do not use it for qualitative scenario/judgment questions (that is multica-pythia) or for platform operations."
user-invocable: false
allowed-tools: Bash(multica *)
---

# TimesFM Forecasting (0.5.82+)

TimesFM is Google Research's decoder-only time-series foundation model.
This fork vendors TimesFM 2.5 (200M parameters, torch stack) as a local
Python subprocess (`apps/desktop/vendor/timesfm-src/`, started by the
desktop from `resources/timesfm/run.sh` when the `timesfm` Labs flag is
enabled). You never touch the model directly — you POST a numeric series
to Multica's forecast endpoint, the server forwards it to the engine, and
the persisted answer is what the read-only records view
(`/experimental/timesfm-lab`) lists.

This skill teaches the agent-facing contract for that endpoint.

## When to use

- The issue context carries a **univariate numeric series** (CSV
  artifact, metric table in a comment, issue metadata) and the user asks
  "what happens next / forecast the next N steps".
- The user wants **uncertainty**, not a single number — the engine
  returns calibrated quantile bands (80% + 90%).
- Zero-shot is the point: no training, no ARIMA parameter tuning.

When **not** to use:

- Qualitative scenario reasoning ("will we win the deal?") →
  `multica-pythia` (oracle judgments), not TimesFM.
- The series is not numeric or has no temporal ordering → say so and
  stop; do not reshape non-temporal data into a forecast.

## Hard rule — flag check before any call

```sh
multica experimental flags list 2>/dev/null | grep -E '^timesfm\s+(true|enabled)'
```

If the flag is off (or missing), **refuse**:

> TimesFM 集成未启用。请在 Settings → Labs 打开「timesfm」开关。

Do NOT fall back to forecasting "from your own knowledge" — an unfounded
number is worse than no number.

## How to forecast an issue

### Step 1 — extract the series from the issue

Read the issue (`multica issue get <issue-id> --output json`, CSV
artifacts, metric comments) and pull out the numeric history. Quality
floor: **at least ~32 points** of context (the OSS upstream checklist;
fewer points degrade sharply), values finite, evenly spaced if possible.

### Step 2 — POST the forecast

```sh
curl -sS -X POST \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "issue_id": "<issue-id-or-identifier>",
    "series": [120, 135, 128, 142, 150, 148, 161, 158],
    "horizon": 24
  }' \
  "$MULTICA_API_URL/api/experimental/timesfm/forecast/issue"
```

Wire contract:

| Field | Shape | Notes |
| --- | --- | --- |
| `issue_id` | string | identifier or UUID; resolves through the standard issue loader (foreign-workspace ids 404) |
| `series` | `number[]` or `number[][]` | single series or batch; empty arrays and infinities are 400 |
| `horizon` | int, optional | steps ahead; default 24, clamped to [1, 256] |
| `dates` | `string[]` / `string[][]`, optional | future point labels, echoed for charting, never interpreted |

Response (HTTP 200):

```json
{
  "run_id": "<uuid>",
  "issue_id": "<uuid>",
  "provenance": "model",
  "model_present": true,
  "horizon": 24,
  "series": [
    {
      "point": [163.2, 165.8],
      "quantiles": {
        "lower_90": [151.0, 152.2],
        "lower_80": [156.1, 158.0],
        "median":   [163.2, 165.8],
        "upper_80": [170.4, 173.9],
        "upper_90": [176.8, 181.5]
      },
      "provenance": "model",
      "dates": ["2026-08-28"]
    }
  ]
}
```

`point` is the median forecast; the quantile map carries the 80% and 90%
prediction-interval edges. Anomaly framing: an actual value outside
`lower_90`…`upper_90` is statistically unusual (< 10% under the model).

### Step 3 — read the provenance and be honest about it

- `provenance: "model"` + `model_present: true` — real TimesFM output.
- `provenance: "seasonal_naive"` or `model_present: false` — the engine
  answered with its seasonal-naive statistical fallback (weights not
  seeded at `~/.multica/models/timesfm/model.safetensors`, RAM floor, or
  torch-stack failure). **You must say so** when reporting: the numbers
  are a naive repeat of the last season, not neural forecasts.
- `provenance: "mixed"` — a batch where some series used each path;
  per-series `provenance` tells you which.

### Step 4 — report into the issue thread

Echo the forecast back as an issue comment (audit trail — the chat reply
alone is the silent-drop bug class):

```sh
multica issue comment <issue-id> --body "预测(horizon=24, provenance=model): 中位路径 …;80% 区间 …"
```

Include the records-view pointer so the user can open the run:
`/experimental/timesfm-lab?issue=<issue-id>&run=<run_id>` (the run list
deep-links straight to a persisted row).

## Reading history (works engine-down)

```sh
curl -sS \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  "$MULTICA_API_URL/api/experimental/timesfm/forecast/issue/runs?issue_id=<issue-id>&limit=10"
```

Returns an array of `{id, horizons, provenance, created_at, result}`
newest-first. This endpoint reads persisted rows only — it answers even
when the engine subprocess is down.

## Error contract

| Status | Meaning | What to do |
| --- | --- | --- |
| 400 | bad series/horizon shape | fix the payload; the message names the field |
| 404 | flag off (uniform guard) or issue not found | check the flag; verify the issue id |
| 503 | engine not running | say the engine is down; do NOT fabricate numbers; GET /runs still works for history |
| 502 | engine call failed | retry once; then surface the diagnostic |

## Hard rules

- **The engine is the single source of truth for forecasts.** Do not
  import or re-implement TimesFM, do not load the checkpoint into your
  own runtime, do not add packages to the engine environment (it is an
  offline wheelhouse managed by the desktop).
- **Never strip the provenance.** Reporting a `seasonal_naive` number as
  a model forecast is a lie the user cannot detect.
- **Forecasts are issue-first (ICP-1).** You run because the issue was
  assigned to the `timesfm_oracle` agent; the lab view is a read-only
  records surface — never tell the user to "trigger a forecast from the
  lab view"; the trigger is assigning the issue (or asking in a comment).
- **Batch sensibly.** A `number[][]` series forecasts all series in one
  call; keep context windows ≤ ~1024 points per series (2.5 supports up
  to 16,384, but issue-scale series rarely need it).

## Quality checklist

Run before reporting success:

- [ ] Series values finite, ≥ ~32 context points where possible
- [ ] `horizon` inside [1, 256]; the response echoes the clamped value
- [ ] `run_id` non-empty (persisted → deep-linkable); empty means the
      persist failed (engine answer still returned — say records may be
      missing)
- [ ] `provenance` surfaced verbatim in your report
- [ ] Issue comment posted with the quantile-band summary + records link

## References

- `references/timesfm-source-map.md` — fork source map (handler, engine
  wrapper, vendor, install) + upstream OSS derivation.
