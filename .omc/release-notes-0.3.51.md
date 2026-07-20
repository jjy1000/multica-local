# 0.3.51 — Labs follow-up closure

## 修复 (3 P2/P3 follow-up + 1 deprecated cleanup)

### P2-1: `code_canvas` subprocess spawner

`apps/desktop/vendor/code-canvas/run.sh` + `resources/code-canvas/run.sh`:

- **Removed `exec python3 - <<PY`** — under `exec` the heredoc is consumed by
  the surrounding shell and `python3 -` reads empty stdin, so the server
  silently exits without binding the port. Without this fix, the
  `BaseExperimentalManager` health probe times out at 30 s and the IPC
  dispatcher surfaces a bogus `code_canvas_unavailable` error.
- **Added `SO_REUSEADDR` (`allow_reuse_address = True`)** so a prior
  binding in TIME_WAIT doesn't make the stub silently exit with
  EADDRINUSE on restart.

The generic subprocess manager (`subprocess-manager.ts::resolveGenericSubprocessManager`)
already wires `code_canvas` end-to-end via the manifest — no manager-template
changes were needed.

Verified end-to-end via Node `child_process.spawn` (the actual production
spawner): the stub serves `/health` → 200 with the canonical `{"status":"ok","stub":"code_canvas"}`
body, returns 404 on unknown paths, and shuts down cleanly on SIGTERM
(verified in the 0.3.51 sandbox, no orphans).

### P2-2: artifact binary blob loader

`apps/desktop/src/renderer/src/components/experimental-artifact-view.tsx`:

The pre-0.3.51 `ArtifactTile` rendered `<img src>`, `<iframe src>`, and
`<a href download>` against a site-relative URL (`/api/experimental/...`),
which 401/404'd on a token-auth desktop build because:

- **Origin**: the renderer origin (`file://` packaged, `localhost:5173`
  dev) is not the bundled backend on `localhost:8090`. There is no vite
  proxy and no `<base>` element, so the request never reaches the server.
- **Auth**: desktop runs in token mode (CoreProvider has no
  `cookieAuth`), so a cross-origin cookie is never attached. The
  `Authorization: Bearer` from `authHeaders()` is the only working
  credential.

The new `useArtifactBlobUrl` hook fetches via `api.rawRequest` (which
prepends `baseUrl` and injects Bearer), calls `res.blob()`, creates
`URL.createObjectURL`, and revokes the blob URL on unmount or URL
change. Three new components (`ArtifactImage`, `ArtifactIframe`,
`ArtifactDownloadLink`) replace the native `<img>` / `<iframe>` / `<a>`
paths. The `sandbox=""` iframe policy is preserved.

Text and chart payloads continue to use the existing `TextFetch` /
`InteractiveChartCard` paths because those read the body as text/JSON.

### P3-3: `agent.system_key` column + constitution wiring

End-to-end wiring of the previously-placeholder "与智能体宪法兼容" toggle
in the agent creation studio:

- **Migration 161** (`server/migrations/161_agent_system_key.up.sql`):
  adds `agent.system_key TEXT DEFAULT NULL`. Forward-only, NULL default
  preserves pre-0.3.51 semantics.
- **sqlc regen**: `CreateAgentParams` / `UpdateAgentParams` gained
  `SystemKey pgtype.Text`. `CreateAgent` INSERT and `UpdateAgent`
  COALESCE both wired to set/preserve the column.
- **Handler (`server/internal/handler/agent.go`)**: `CreateAgentRequest.SystemKey`
  and `UpdateAgentRequest.SystemKey` (tri-state pointer: omitted / `""`
  clears / value sets). `AgentResponse.SystemKey` exposed on read.
- **Task dispatch (`server/internal/handler/daemon.go`)**: when an
  agent's `TaskAgentData.SystemKey` matches a known binding, the body
  is prepended to `Instructions` before the daemon dispatches the
  task. Today only `constitution_agent_v1` → `multica-constitution-agent`
  Skill is wired.
- **New builtin Skill**: `server/internal/service/builtin_skills/multica-constitution-agent/SKILL.md`
  ships the constitution Skill body (~70 lines, includes CTR / CSIL /
  TAOL loop descriptions, hard constraints, and an explicit
  "system_key relationship" section so a future re-writer doesn't
  silently break the binding contract).
- **New public helper**: `service.LoadBuiltinSkillByName(name)` so the
  daemon binding layer can resolve a Skill body without re-walking the
  embed.FS. Three sub-tests: known / unknown / empty-name.
- **Frontend** (`packages/core/types/agent.ts`,
  `apps/desktop/src/renderer/src/pages/agent-creation-studio-view.tsx`):
  `CreateAgentRequest.system_key` typed; the `compat` checkbox state
  is now threaded through `ResourceForm.onSubmit` to `api.createAgent`.
  Skill / Squad forms still show the checkbox for visual parity but
  ignore it server-side — `compatConstitution: boolean` is exposed in
  the form context for future Skill / Squad wiring without re-plumbing
  the toggle.
- **i18n**: `compat_hint` in zh-Hans updated from the 0.3.45 placeholder
  text to describe the live wiring.

### P3-4: remove `@deprecated VIEW_LAB_SOURCES`

`packages/views/issues/components/issue-labs-section.tsx`:

After 0.3.50 shipped and verified the `Flag.HidesDeliverableInIssueTimeline`
migration, the renderer-side hardcoded set was no longer referenced by
any consumer — only by historical comments and a single inline comment
in `issue-labs-section.tsx` itself. Removed the export entirely (44
lines including the long JSDoc block). Comment in `issue-labs-section`
updated to drop the "rather than the deprecated VIEW_LAB_SOURCES const"
wording now that the const is gone.

## Files changed

```
server/migrations/161_agent_system_key.{up,down}.sql          (new)
server/pkg/db/queries/agent.sql                                (+SystemKey)
server/pkg/db/generated/agent.sql.go                          (sqlc regen)
server/internal/handler/agent.go                              (+SystemKey in 4 places)
server/internal/handler/daemon.go                             (+loadSystemPromptBinding + injection point)
server/internal/service/builtin_skills.go                     (+LoadBuiltinSkillByName)
server/internal/service/builtin_skills/multica-constitution-agent/SKILL.md  (new)
server/internal/service/builtin_skills_system_key_test.go     (new, 3 sub-tests)

apps/desktop/vendor/code-canvas/run.sh                        (exec→foreground + SO_REUSEADDR)
apps/desktop/resources/code-canvas/run.sh                     (synced from vendor)

apps/desktop/src/renderer/src/components/experimental-artifact-view.tsx
                                                              (+useArtifactBlobUrl, +ArtifactImage, +ArtifactIframe, +ArtifactDownloadLink)

apps/desktop/src/renderer/src/pages/agent-creation-studio-view.tsx
                                                              (system_key wired; file header + compat_hint updated)
packages/core/types/agent.ts                                  (+system_key on CreateAgentRequest)
packages/views/issues/components/issue-labs-section.tsx      (-VIEW_LAB_SOURCES export)
packages/views/locales/zh-Hans/experimental.json              (compat_hint updated)

apps/desktop/package.json                                     (0.3.50 → 0.3.51)
```

## Verification

- `pnpm typecheck` — 6/6 tasks pass
- `cd server && go test -count=1 ./internal/handler/ -run "TestAgent"` — pass
- `cd server && go test -count=1 ./internal/service/ -run "TestBuiltinSkills|TestLoadBuiltinSkill"` — pass
- Pre-update snapshot: `0.3.50` saved at `~/.multica/backups/pre-update-20260720-011604`
- bundle-cli: 3 binaries `version=0.3.51`
- electron-vite build: 4.49s
- electron-builder --dir: success, ad-hoc signed
- `/Applications/Multica.app` Info.plist `CFBundleShortVersionString` = `0.3.51` (strict 3-segment semver; the 0.3.49.1 attempt was rejected by macOS earlier and forced a rebrand)
- Cold start: `5432 + 8090` listeners in ~12s; `curl /health` → `{"status":"ok"}`
- Row parity: `workspace=1 / issue=206 / comment=1157 / agent=90` (vs 0.3.50 baseline `1/205/1156/90` — +1 issue, +1 comment from agent-creation-studio smoke testing during this ship; agent unchanged)
- verify-desktop-cold-start: PASS

## Known follow-ups (not fixed in 0.3.51)

- `TestExperimentalResourcesRoundTrip_InstalledThenHidden` fixture
  still requires `MULTICA_RESOURCES_DIR` env in the test harness.
  Pre-existing failure unrelated to this ship.
- `agent.system_key` only honours `constitution_agent_v1`. Adding more
  bindings is a daemon-side change: add the Skill + a case in
  `daemon.go::loadSystemPromptBinding`. No schema change needed.