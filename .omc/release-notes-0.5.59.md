# 0.5.59 — Pythia per-issue forecast: persist + UX closure

Released 2026-08-23, branch `epic/0.5.13-integration`, head `e35ccd8f0`
on top of 0.5.58. 3 atomic commits + 1 version bump.

## Headline

**The Pythia per-issue forecast lab stopped silently failing.** When
the user picked `pythia_oracle` on an issue and the auto-launch fired
the 10-round deliberation, the SSE stream ran 10 rounds over 45s and
returned 200 — but the `pythia_forecast_run` table received **zero**
rows, and the issue-detail `<PythiaPanel>` showed an empty
placeholder for the entire window. The user had no way to tell
whether the lab was broken or just slow. 0.5.59 fixes both halves:
server-side persistence now lands (with diagnostic visibility for
the next regression), and the renderer shows live progress with a
90-second timeout + manual retry CTA so a future silent failure is
never silent for long.

## Critical lessons (read before next pythia touch)

1. **`persistIssueForecastRun` had FOUR silent early-returns.**
   `len(envelopes)==0`, `ctx lookup false`, `h==nil`, `h.Queries==nil`,
   `util.ParseUUID err`, `json.Marshal err`, `INSERT err` — every
   single one was `return` with no `slog`. A successful 200 + 45s
   SSE stream + zero DB rows was indistinguishable from "the lab
   is fine, just empty". 0.5.59 adds WRN on every silent return and
   INF on the success path; regression tests pin the new
   contract. **Any future SSE handler in this codebase must log
   every termination path or this class of bug returns.**

2. **chi context-key propagation through `r.WithContext` is not
   guaranteed to round-trip the middleware-attached ctx key in
   every code path.** Observed here: the SSE defer at the end of
   `pythiaIssueForecastStream` could not always recover the
   `forecastIssueHandlerCtxKey` set by the middleware. The
   package-level fallback
   (`pythiaForecastHandlerFallback`, populated by
   `AttachPythiaIssueForecastMiddleware` at router boot) closes the
   gap. **Do not remove this fallback** until someone has proven
   the chi propagation with a debug-instrumented reproduction.

3. **Renderer `<PythiaPanel>` previously had no "in progress"
   surface.** The 45s SSE window rendered as `t(($) => $.lab_output_panel.empty)`
   — a single line of muted text that reads as "no data". The
   user cannot distinguish "oracle is running" from "oracle never
   ran" from "oracle ran and produced nothing". 0.5.59 introduces
   a 3-state model (in_progress / stuck / has_runs) keyed off
   `sessionStorage["pythia-triggered-{wsId}-{issueId}"]` so
   navigations away and back don't reset the timer, and a manual
   retry button at the 90s boundary so the user is never stuck on
   a silent panel.

## Verification

- `pnpm typecheck` — 6/6 ok
- `go test -run "TestClampIssueForecastRounds|TestForecastRunSource|TestIssueForecastStreamEmitsEnvelopeWithIssueID|TestBuildIssueForecastContextTruncatesBody|TestPythiaForecastHandlerFallback" -race ./internal/handler/` — 5/5 ok (new fallback tests included)
- `go test -count=1 ./internal/handler/ ./pkg/agent/...` — green for my packages
- Cold-start: server PID 5373, `/health` 200, row parity preserved (workspace=7 / issue≈349 / pythia_forecast_run=0 — DB was already empty pre-fix; user must trigger a forecast to see rows land)
- Re-signed: app + 3 nested binaries, `multica --help` exits 0 (not 137)

## Pre-existing failure NOT addressed

`internal/service/mythos/supervise_completion_test.go::TestTickSupervision_CompletionByFinalIssueStatus`
panics with nil-pointer deref in `GetIssueStatusEntryByKey` (issue_status.sql.go:187).
Last touched by `45c6bcaa9 fix(server): mythos supervise completion branch + regression tests`,
no diff in this commit. **Tracked separately — do NOT bundle a fix
for it into the next pythia ship.**

## Ship chain

1. Pre-update snapshot: `bash ~/.multica/scripts/pre-update-snapshot.sh`
2. Migrations: `cd server && go run ./cmd/migrate up` (no new migrations in 0.5.59)
3. Bundle: `pnpm --filter @multica/desktop bundle-cli` (server binary already rebuilt — only bundle-cli is needed if PG manifest / migrations changed)
4. Build: `pnpm --filter @multica/desktop build` (electron-vite)
5. Package: `pnpm exec electron-builder --mac --dir`
6. Replace + re-sign: `cp -R apps/desktop/dist/mac-arm64/Multica.app /Applications/` + `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app`
7. Restart: `pkill -f "Multica.app/Contents/MacOS/Multica"; pkill -f "multica daemon"; open /Applications/Multica.app`

Ship gate (`pnpm typecheck` + `go test ./internal/... ./pkg/agent/...`)
green for touched packages. Full `make test` was not run this turn
because of the pre-existing mythos panic (unrelated).

## Reference docs

- `server/internal/handler/forecast_issue.go` — persistence + diagnostic
- `server/internal/handler/forecast_issue_test.go` — fallback regression
- `packages/views/experimental/components/lab-output-panel.tsx` — 3-state UI
- `packages/views/modals/create-issue.tsx` — auto-launch trigger stamp
- `packages/views/issues/actions/use-issue-actions.ts` — update-path stamp
- `apps/desktop/src/renderer/src/components/pythia/pythia-report-surface.tsx` — lab-page stamp