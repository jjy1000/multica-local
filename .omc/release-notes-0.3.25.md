# 0.3.25 — Labs hardening + reserved-workspace removal

Ship date: 2026-07-15 (UTC)
Type: stability + security
Previous: 0.3.24

## Headline

The Labs platform is now genuinely catalog-driven end to end, and the
documented "no reserved workspace for new labs" hard constraint is
finally enforced in code rather than aspirational. Ship includes one
Critical security fix (SSRF on the experimental reverse-proxy), several
panic / data-loss bug fixes, and a generic subprocess manager that
unblocks every future subprocess lab without per-flag TypeScript.

## Security

- **SSRF on `/__experimental/upstream` (Critical).** The upstream
  registration endpoint now refuses any URL whose host is not on the
  loopback interface (127.0.0.0/8 or ::1, plus `localhost`). Before
  this fix, an unauthenticated caller on the same network as a
  self-hosted Multica server could register an arbitrary external URL
  and turn the same-origin reverse proxy into an open proxy that
  forwarded requests (and, via the missing director-side strip, the
  caller's `Cookie` / `Authorization`) to an attacker-controlled host.
  The reverse-proxy director now also strips outbound
  `Cookie` / `Authorization` so even a misregistered upstream cannot
  harvest the user's session. New `TestIsLoopbackUpstreamURL` pins
  the loopback-only allowlist (14 cases including http(s), non-http
  schemes, metadata endpoint, public IPs, garbage strings).

- **X-Experimental-Flag forgery on safety net.** The 5xx-burst
  middleware is mounted globally, but the flag-attribution header was
  trusted unconditionally. A user could forge
  `X-Experimental-Flag: <flag>` on any 5xx-prone endpoint and trip the
  blacklist for a flag the request never touched. The middleware now
  only attributes a 5xx to a flag when both (a) the path is a real
  experimental route (`/experimental/*` or `/api/experimental*`) and
  (b) the flag is a known catalog key. New test
  `TestBurstIgnoresForgedHeader` pins the guard.

- **Concurrent data race in burst middleware.** The flag state map
  had no lock on map insert/lookup; the per-flag `flagState` mutex
  only protected the failure slice. The `-race` detector flagged it
  under concurrent requests for the same key. Fixed with a single
  `statesMu` guarding the map.

## Stability

- **forecast SSE: no more panic → blacklist auto-disable.**
  `claude_lab_forecast.go::forecastStream` is wrapped in a local
  `defer recover()` that logs the panic and exits cleanly. Before
  this, any panic inside the SSE loop tore the connection mid-frame
  (chi's outer Recoverer cannot rewrite a response that already had
  WriteHeader(200) flushed); the experimental safety net read the
  half-written stream as a 5xx burst and auto-blacklisted
  `claude_science_lab`, forcing a manual Restore + relaunch.

- **`parseSeed` strict.** Switched from `fmt.Sscanf("%d", …)` to
  `strconv.ParseInt`. Sscanf accepted trailing garbage (`"123abc"`)
  and overflow silently; ParseInt rejects both and the handler falls
  back to the time-based default.

- **`loadManifestAsset` slice panic on >256 KB assets.** Off-by-one
  in the truncation math produced a negative slice bound and
  crashed the install handler. Fixed by computing the in-bounds byte
  count explicitly. Pinned by the existing claude_science round-trip
  test on the real DB.

- **filter visibility helper no longer mutates caller's backing
  array.** `filterLabsHiddenByDefault` was reusing `items[:0]` to
  save an alloc; replaced with `make([]T, 0, len(items))`. The
  allocation is negligible compared to the SQL round-trip and a
  caller holding the original slice for diagnostics no longer sees
  silently truncated data.

- **`MountExperimentalProxies` nil-Handler guard.** Tests / older
  boot paths that called it with `nil` used to dereference
  `h.experimentalClaudeScienceProxy` (method value) and panic. The
  function now returns immediately when `h == nil` so callers see a
  clean 404 instead of a crash.

## Architecture: hard constraint enforced

- **A1: removed `upsertClaudeScienceWorkspace` and
  `upsertMythosWorkspace`.** The install path for both flags now
  writes resources into the **caller's currently active workspace**
  (resolved from the request's `X-Workspace-ID` via the existing
  `resolveWorkspaceID`, with a fallback to the user's first workspace
  for unauthenticated fixture callers). No new workspace is created
  on install, and the workspace row itself is **never `Claim`ed** —
  locking a user's own workspace as lab-owned would let a rollback
  hide it. Isolation now comes purely from `experimental_resource_lock`
  (`experimental_source` column) + the visibility table, which the
  fork has used for resource isolation since 0.3.17.

  - The `InstallHandler` registry signature gained a `workspaceID`
    parameter; the dispatcher reads it via
    `h.resolveWorkspaceID(r)`.
  - New helper `resolveLabWorkspace(ctx, h, workspaceID, userID)`
    centralises the resolve-or-fallback path.
  - `claudeScienceActivity` now reports the caller's active
    workspace instead of looking up the deleted reserved slug.
  - The `claude-science` / `mythos-swarm` / `code-canvas` reserved
    slugs stay in `reserved_slugs.json` as **legacy protection only**
    (a new user cannot claim a name a legacy install may still
    occupy); they are not created by any current flag.
  - `CLAUDE.md` hard-constraint #5 rewritten from aspirational to
    enforced: "As of 0.3.25 this is **enforced in code, not
    aspirational**."

  Verified post-ship: no row exists in `workspace` with slug
  `claude-science`, `mythos-swarm`, or `code-canvas`.

- **Mythos runtime status: `online` → `offline`.** The synthetic
  runtime row claimed `Status="online"` but no daemon ever registers
  against it. Switched to `"offline"` to match the
  `upsertClaudeScienceRuntime` honesty contract; the agent runtime
  surface is unchanged.

- **Removed the dangling `mythos.Service.Run` reference.** The
  comment promised code that was never written; the OpenMythos RDT
  loop algorithm is not reimplemented server-side. Comment block now
  accurately describes the lab as a "themed multi-agent team" rather
  than a literal recurrent-transformer runner.

## Functionality

- **`code_canvas` actually starts.** It used to fall through to an
  `idle` stub whose `ensureUp()` threw and whose reverse proxy
  returned 502. Added a generic manifest-driven subprocess manager
  (`apps/desktop/src/main/experimental/subprocess-manager.ts`) that
  reads `resources/experiments/<flagKey>/manifest.json` and
  constructs a `BaseExperimentalManager` from
  `spec.runtime.{binary, args, health_path, ready_timeout_ms,
  stop_grace_ms, on_ready, on_stop}`. `code_canvas`' `run.sh` stub
  (30-line Python `/health` server) now spawns, health-checks, and
  registers its loopback URL through the existing reverse proxy
  with no per-flag TypeScript. The `pythia_oracle` switch in
  `manager-factory.ts` is kept only because Pythia needs the
  Multica JWT-bridge env that no other lab needs. Five new tests
  pin the manifest parsing contract.

- **`staticFlagDescriptors` covers all 8 catalog flags.** Was 3/8,
  causing the IPC layer to throw "no handler registered" on a cold
  boot for any of the other five before `loadFlagDescriptors` ran.
  Now the static list is complete; `loadFlagDescriptors` only
  merges server-only additions.

- **`code_canvas` manifest no longer lies about installability.**
  Was `installable: true` with `install_handler: "code_canvas_install"`
  but no install handler was registered (POST returned 404). Set to
  `installable: false`; the lab provisions no resources, so install
  has nothing to do.

- **Sidebar icons: 4/8 → 8/8.** Added `Code2`, `Sparkles`,
  `ScrollText` to the icon map for `code_canvas`,
  `agent_self_optimization`, `constitution_agent`. i18n label keys
  for all eight flags were already present in every locale.

- **`readInstallStatus` now reports the real `Hidden` state.**
  Hardcoded `Hidden: false` regardless of the visibility table;
  the `/api/experimental-resources/{key}/status` endpoint already
  computed it correctly via `isManifestHidden(counts)`. They now
  agree, so the Labs side panel correctly shows the "已隐藏" badge
  after a rollback.

- **Chat tab `getCurrentWsId()` polling short-circuit.** The
  500 ms interval now uses a functional `setWsId` that returns
  `prev` when unchanged, so React bails out of the re-render. The
  poll costs one string read, not a ChatWindow subtree reconcile.

## Files changed

### Server (Go)
- `server/internal/handler/claude_lab_forecast.go` — panic recover,
  strict parseSeed
- `server/internal/handler/labs_visibility_filter.go` — independent
  slice allocation
- `server/internal/handler/experimental_proxy.go` — loopback URL
  guard, director-side credential strip, nil-Handler guard
- `server/internal/handler/experimental_proxy_test.go` — new
  `TestIsLoopbackUpstreamURL`
- `server/internal/handler/experimental_flags.go` — `Hidden` from
  `isManifestHidden(counts)`
- `server/internal/handler/experimental_resources.go` —
  `claudeScienceActivity` resolves via active workspace
- `server/internal/handler/install_claude_science.go` — A1
  enforcement, header comment refresh, slice-math fix in
  `loadManifestAsset`
- `server/internal/handler/install_mythos.go` — A1 enforcement,
  status honesty, comment refresh
- `server/internal/experimental/registry.go` — `InstallHandler`
  signature gains `workspaceID`
- `server/internal/experimental/registry_test.go` — test update
- `server/internal/handler/handler_test.go` — test update
- `server/internal/middleware/experimental_burst.go` — forgery
  guard, map mutex
- `server/internal/middleware/experimental_burst_test.go` — test
  rewrite to use real path + known key, new
  `TestBurstIgnoresForgedHeader`
- `server/cmd/server/router.go` — install-closure signatures,
  version=0.3.25
- `server/internal/handler/reserved_slugs.json` — corrected
  description (legacy protection, not lab creation)
- `apps/desktop/resources/experiments/code_canvas/manifest.json` —
  `installable: false`, empty `lockable_resources`
- `apps/desktop/package.json` — `0.3.24` → `0.3.25`

### Desktop (TS)
- `apps/desktop/src/main/experimental/manager-factory.ts` —
  static descriptors 3 → 8, subprocess branch routes non-pythia
  to the generic manager, header comment refresh
- `apps/desktop/src/main/experimental/subprocess-manager.ts` —
  new generic manifest-driven subprocess manager
- `apps/desktop/src/main/experimental/subprocess-manager.test.ts` —
  new 5-test suite
- `apps/desktop/src/main/experimental/upstream-registry.ts` —
  `service` widened to `string` (server validates via allowlist)
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` —
  ChatTab polling short-circuit
- `packages/views/layout/app-sidebar.tsx` — icons 4 → 8

### Docs
- `CLAUDE.md` — hard-constraint #5 rewritten to enforced; code_canvas
  note expanded to cover the installable/install_handler pair and
  the manifest-driven spawner status.

## Verification

```
pre-update snapshot   PASS  (766M .app + 13 PG tables + 740 KB KB)
migrate up            PASS  (no pending; latest = 154)
go build ./...        PASS
go test -race ./internal/handler/   PASS  (13.6s, all tests)
go test -race ./internal/middleware/ PASS (3.2s, all tests + race-clean)
go test -race ./internal/experimental/ PASS
pnpm typecheck @multica/desktop      PASS
pnpm typecheck @multica/views       PASS
pnpm vitest src/main/experimental/  PASS  (8 tests)
electron-vite build                  PASS  (single 11.2MB entry)
electron-builder --dir               PASS  (778M .app, ad-hoc signed)
cold start: 5432 LISTEN / 8090 LISTEN
cold start: GET /health → {"status":"ok"}
row parity: workspace=1 issue=165 comment=880 agent=85
GUI process live; no reserved-slug workspace rows created
```

## Migration / data

No new migration shipped. The reserved-workspace removal in A1 is
purely code-side: the `claude-science` / `mythos-swarm` / `code-canvas`
workspace rows from a prior install, if any, remain in the database
but are not referenced by any current flag. They are invisible to the
new code path (the lock-overlay + visibility-table isolation is the
single source of truth).

If a user previously installed a lab and rolled it back, the hidden
rows will remain hidden and the install will be re-attempted against
the user's active workspace on next toggle.

## Known deferred items

- **pythia-view Phase 2 (real SSE data source).** The Phase 1
  `SAMPLE_PREDICTIONS` placeholder remains. Wiring
  `/experimental/pythia/state/stream` through a streaming IPC channel
  is a UI enhancement, not a stability fix; deferred to a follow-up
  to keep this release minimal and reversible.
- **`claude_lab_forecast.go` synthetic generator.** The 0.3.24
  forecast stream still emits mock data; the 0.3.25 plan is to swap
  the stub for a real model call through the same MULTICA provider
  chain. Wire shape is stable.
- **OpenScience native binary not vendored.** `claude_science_lab`
  remains inline-only; the lab surface works, but the OpenScience
  `run.sh` + native binary pipeline is not part of this ship.

## What this release does NOT touch

- Upstream sync — fork-local decisions; no upstream PR.
- Mobile (`apps/mobile/`) — independent release cadence.
- Telemetry, auto-update, Google OAuth, cloud features — still
  deleted (fork hard contracts unchanged).
