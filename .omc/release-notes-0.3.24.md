# 0.3.24 ship log

**Ship date**: 2026-07-15 (autonomous activation #2)
**Predecessor**: 0.3.23
**Status**: SHIPPED to /Applications/Multica.app 0.3.24
**Cold start**: PASS (5432 + 8090 LISTEN, /health 200)
**DMG**: dist/multica-desktop-0.3.24-mac-arm64.dmg

## Highlights

### Claude Lab forecast SSE endpoint
- New `GET /api/experimental/claude-science-lab/forecast/stream`
  (text/event-stream) emits a `prediction` envelope every 5 s,
  scoped to the `claude_science_lab` flag gate.
- Wire shape: `event: prediction\ndata: {id, scenario, narrative,
  probability, confidence, horizon, persona, createdAt}`. 15 s
  keep-alive comment for proxy survival. 60 s max lifetime per
  subscriber. Deterministic via `?seed=N` query.
- 0.3.24 ships a synthetic generator; 0.3.25 swaps it for a real
  model call through the same MULTICA provider chain used by
  chat.
- Router registration: `RegisterClaudeLabForecastRoutes(r)`
  inside the existing `if experimental.DefaultFor("claude_science_lab")`
  block in `cmd/server/router.go`.
- Unit test (`claude_lab_forecast_test.go`): captures the first
  frame via a custom ResponseWriter shim, asserts wire shape and
  JSON validity. PASS.

### Interactive chart artifact kind
- `experimental-artifact-view.tsx` now accepts `kind: "interactive-chart"`.
  New `<InteractiveChartCard />` + `<ChartRenderer />` components
  render Recharts line / bar / scatter / heatmap from a
  `{ schema, data }` payload embedded in the artifact file.
- Schema: `{ type, x: {field,label}, y: {field,label},
  color?: {field,label}, bins?: number }`. Recharts is already
  in catalog (`packages/ui/components/ui/chart.tsx`); the new
  renderer is renderer-local so the shared chart primitive stays
  the shadcn-style thumbnail.
- Heatmap falls back to a 6×6 cell grid (no first-class Recharts
  primitive) — proper grid is on the 0.3.25 roadmap.
- RuntimeArtifactStub discriminated union widened to include
  `interactive-chart`; same fetch path as the other 8 kinds.

### ClaudeLabView Forecast tab wired to real SSE
- `ForecastTab()` now passes the real forecast URL
  (`${origin}/api/experimental/claude-science-lab/forecast/stream`)
  and `forceSample={false}`, replacing the 0.3.22 sample-data
  fallback. PythiaDashboard reuses the live stream.

## Verification

- `pnpm typecheck` (desktop): 0 errors
- `go vet ./...`: 0 warnings
- `go test -race -count=1 -run TestForecastStream ./internal/handler/...`: PASS
- `go build ./...`: 0 errors
- pre-update-snapshot: 766 MB .app.bak (0.3.22)
- bundle-cli: 3 binaries + 154 migrations + PG manifest + Pythia
- electron-vite build: 11.25 MB renderer bundle
- electron-builder package: DMG 0.3.24 + .app signed
- cold start: 5432 + 8090 LISTEN within 6 s, /health 200

## Known follow-up (deferred to 0.3.25)

- 0.3.23 PR-B (install handler consolidation) — still blocked on
  `experimental_pref` schema (no key/value column). 0.3.25 adds
  migration 155 widening that table.
- 0.3.25: real forecast backend (replace synthetic generator)
- 0.3.25: low-code canvas in ClaudeLabView Code tab
- 0.3.26: 5 literature connectors

## Files changed (5)

Server:
- server/internal/handler/claude_lab_forecast.go (new)
- server/internal/handler/claude_lab_forecast_test.go (new)
- server/cmd/server/router.go (RegisterClaudeLabForecastRoutes)

Desktop:
- apps/desktop/src/renderer/src/components/experimental-artifact-view.tsx
  (interactive-chart kind + ChartRenderer)
- apps/desktop/src/renderer/src/pages/claude-lab-view.tsx
  (ForecastTab real SSE URL + forceSample=false)
- apps/desktop/package.json (version 0.3.22 → 0.3.24)
