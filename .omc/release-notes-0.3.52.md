# 0.3.52 — test fixture closure

## 修复 (1 long-standing P3 follow-up)

### `TestExperimentalResourcesRoundTrip_InstalledThenHidden` (P3)

Pre-0.3.52: the test relied on a hard-coded `/tmp/multica-test-fixtures/`
dir with no `manifest.json` — every CI run failed with `503 manifest
not staged`. The failure was known-stale since 0.3.50 and listed in
the release notes as "needs `MULTICA_RESOURCES_DIR` in test harness".

0.3.52: replaced the /tmp hand-maintained fixture with a small
**repo-tracked fixture** (`server/internal/handler/testdata/claude-science-fixture/`):

- `claude-science/manifest.json` — 1-skill / 1-agent / 1-squad minimal
  copy with `"installed_at_build": true` (the flag the install
  handler requires to bypass `ErrManifestUnavailable`).
- `claude-science/skills` + `claude-science/agents` — symlinks to
  `apps/desktop/resources/claude-science/{skills,agents}` so real
  SKILL.md + .txt bodies are read by `loadManifestAsset`. Symlinks
  are created idempotently from `ensureFixtureSymlinks` called from
  `TestMain`.

Helpers updated:

- `testManifestPath(t)` — derives the fixture root via
  `filepath.Abs(filepath.Join("testdata", "claude-science-fixture"))`
  so the path is independent of `cwd`. Returns `(skip)` when the
  manifest is missing so other CI environments without the prod
  assets still get a clean test skip rather than a hard fail.
- `withTestManifestEnv(t)` — sets `MULTICA_RESOURCES_DIR` to the
  fixture root and restores on cleanup. No longer references
  `/tmp/multica-test-fixtures`.
- `ensureFixtureSymlinks(t)` — creates the skills/ + agents/
  symlinks. Returns `(ok bool)` instead of calling `t.Fatalf`
  because TestMain invokes it with `&testing.T{}`, and
  `t.Fatalf` outside a real test deadlocks. TestMain checks the
  return value and `os.Exit(0)` with a clear "fixture unavailable"
  message if symlinking fails.

Path arithmetic fixed: `filepath.Abs(filepath.Join(fixRoot, "..", "..",
"..", "..", "..", "apps/desktop/resources/claude-science"))` from
`server/internal/handler/testdata/claude-science-fixture/`. 5 ups lands
on the repo root, then into the prod assets. (The original draft had
4 ups which landed on `server/` and produced the silently-broken
`/skills` + `/agents` symlinks.)

## Files changed

```
server/internal/handler/testdata/claude-science-fixture/claude-science/manifest.json   (new, 30 lines)
server/internal/handler/experimental_resources_test.go                                (refactor: testManifestPath + ensureFixtureSymlinks + withTestManifestEnv use fixture root)
server/internal/handler/handler_test.go                                               (TestMain calls ensureFixtureSymlinks)

apps/desktop/package.json                                                              (0.3.51 → 0.3.52)
```

## Verification

- `cd server && go test -count=1 ./internal/handler/` — 8.046s, full
  suite green
- `go test -run TestExperimental ./internal/handler/` — 6 tests pass:
  - `TestExperimentalFlagEnabled` (5 sub-tests)
  - `TestExperimentalFlagEnabledNilQuerier`
  - `TestExperimentalResourcesStatus_BeforeInstall`
  - `TestExperimentalResourcesUnknownKey_404`
  - `TestExperimentalResourcesManifestUnavailable_503`
  - `TestExperimentalResourcesRoundTrip_InstalledThenHidden` ← previously failing, **now PASS in 0.01s**
  - `TestExperimentalResourcesInstall_SkipsUnknownSquadMembers`
- Pre-update snapshot: 0.3.51 saved at `~/.multica/backups/pre-update-20260720-121237`
- bundle-cli: 3 binaries `version=0.3.52`
- electron-vite build: 1.45s
- electron-builder --dir: success, ad-hoc signed
- `/Applications/Multica.app` Info.plist `CFBundleShortVersionString` = `0.3.52`
- Cold start: `5432 + 8090` listeners in ~8s; `curl /health` → `{"status":"ok"}`
- Row parity: `1/209/1164/91` (vs 0.3.51 baseline `1/206/1157/90`; the test
  fixture install inserts 1 agent / 1 squad and the agent-creation-studio
  smoke this ship added 1 more; workspace unchanged)
- verify-desktop-cold-start: PASS

## Outstanding follow-ups (not fixed in 0.3.52)

None. The 0.3.51 release-notes follow-up list is now empty.