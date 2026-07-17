# Multica 0.3.14 — 2026-07-13

## Summary

Retire the SolidJS OpenScience browser surface that backed `claude_science`
through 0.3.13. From 0.3.14 the experimental research work lives inside
Multica itself: the desktop manager becomes a no-op (no subprocess, no
byok binary), the view becomes a Multica-native page, and the
`multica-claude-science` Skill drives the work through the standard
Multica agent runtime using whatever model the user has configured in
Settings → 模型.

## What changed

### Catalog (`server/internal/experimental/catalog.go`)
- `claude_science` description rewritten: previously claimed "reads its
  own BYOK keys". Now states that research flows through the configured
  Multica model with no extra API keys to set up.
- Comment block above the `claude_science` literal updated to call out
  the no-op manager and the agent-runtime backend.

### Desktop manager (`apps/desktop/src/main/claude-science-manager.ts`)
- `ClaudeScienceManager` no longer implements the spawn / port-pick /
  health-probe lifecycle. It is a thin IPC stub.
- All five IPC channels (`ensure-up`, `get-status`, `get-url`, `stop`,
  `get-webview-preload-path`) now return immediately. `get-url` and
  `get-webview-preload-path` return `null`.
- The reason for keeping the IPC stub (instead of deleting the file):
  removing the channel names would force renderer-side and Skill-side
  rewrites everywhere the names are referenced; a no-op manager
  preserves the wire contract with zero behavior.

### Renderer view (`apps/desktop/src/renderer/src/pages/claude-science-view.tsx`)
- `<webview>` / iframe / loopback URL / partition / preload are all
  gone. The view is a pure Multica-native React page with no embedded
  surface.
- Header reads `试验性功能 / 科研工作台` with a static `ready` indicator.
- Three sections: `工作流程` (3-step plan), `推荐方向` (literature /
  bio / physics / ML), and `通过技能调用` (Skill body excerpt).
- All copy is Chinese, written for first-time users. No hardcoded
  English UI strings remain in this file.

### Preload (`apps/desktop/src/preload/index.{ts,d.ts}`)
- Dropped `claudeScience.getWebviewPreloadPath`. The remaining four
  channels (`ensure-up`, `stop`, `get-status`, `get-url`) stay; the
  view does not actually call them but the contract is preserved.

### CLI dispatcher (`server/cmd/multica/cmd_claude_science.go`)
- `claude-science status` reports `{"status":"no-binary","url":null}`
  with a description string pointing at the agent runtime.
- `claude-science research` and `claude-science get-result` are kept
  as deprecated verbs: they accept the old flags so legacy shell history
  / Skill bodies do not break, but emit a `no-binary` envelope and
  return an error explaining the migration.
- Reuses `writeJSON` from `cmd_pythia.go` instead of declaring a
  duplicate (which would collide at package-link time).

### Skill (`server/internal/service/builtin_skills/multica-claude-science/SKILL.md`)
- New 4-step recipe: create / pick up an issue → write a comment with
  the research plan → let the agent runtime carry out the work in-band
  → read the issue / inbox to monitor; write a summary comment to close.
- All hard rules updated: no BYOK keys, no separate binary, no
  re-implementing the agent. Model comes from Settings → 模型.
- `allowed-tools` widened from `Bash(multica claude-science *)` to
  `Bash(multica *)` because the work no longer routes through the
  retired subcommands.

### Bundle (`apps/desktop/scripts/bundle-cli.mjs`)
- No code change. The OpenScience vendor directory has been removed
  from `apps/desktop/vendor/openscience-bin/`, so `bundle-cli` now
  warns "OpenScience binary not vendored → service not bundled" and
  skips the copy step. Existing 0.3.13 bundles stay unaffected.

## Why we did not delete the manager

Deleting `claude-science-manager.ts` would force:

- delete the IPC handler registration block in `apps/desktop/src/main/index.ts:597-599`;
- delete the IPC stubs from the preload `experimentalAPI.claudeScience`;
- delete the `<ClaudeScienceView>` import + route in `routes.tsx`;
- delete the `claude_science` flag from the catalog;
- delete the sidebar entry in `app-sidebar.tsx`.

At least the first four are reversible but high-churn for a 5-minute
behavioural change. The no-op manager is the minimal patch that:

- keeps the Labs sidebar entry visible (the user still has the option
  to toggle the flag on, even if the only thing it does is point at the
  Multica-native index page),
- keeps the Skill surface resolveable,
- keeps a single sealed wire contract that the next deprecation PR can
  safely delete end-to-end.

The cleanup PR (delete the manager, drop the flag, drop the sidebar
entry) is now on the 0.3.15 backlog — the right move is to keep the
ship isolated rather than bundle it.

## Verification (live cold-start, 0.3.14)

```
5432 LISTEN  (postgres)             < 6 s
8090 LISTEN  (server, multica)      < 8 s
/health  →  {"status":"ok"}
row parity  workspace=1 / issue=162 / comment=855 / agent=80 / squad=11 / schema_migrations=184
GUI launched, dashboard window visible
defaults read /Applications/Multica.app/Contents/Info.plist CFBundleShortVersionString → 0.3.14
```

DMG packaging itself did not complete (`create-dmg -s` flag mismatch
with electron-builder's invocation pattern, same as 0.3.7 / 0.3.12 /
0.3.13), so the unpacked `dist/mac-arm64/Multica.app` was installed
directly via `cp -R` and re-signed ad-hoc. Functionally identical to
the previous local-fork installs.

## Risks / Known limits

- **The deprecated CLI verbs still resolve.** `multica claude-science
  research --topic …` returns a structured error instead of starting a
  subprocess, which keeps history / Skill bodies stable during the
  transition. The wires are not removed.
- **Renderer view does not consume the Labs flag visually.** Enabling
  `claude_science` in Labs today is a no-op at the view level — the
  page renders identically with the flag off. Future work: hide the
  sidebar entry when the flag is off (currently the same entry pattern
  Pythia uses).
- **The Skill's new flow depends on the user already having a
  research-capable agent in the workspace.** If no agent in the
  workspace is configured for research, `multica issue comment` will
  not trigger any autonomous follow-up. The Skill body surfaces this
  in the "Do not assume sub-domain mapping" rule.

## Rollback

```bash
rm -rf /Applications/Multica.app
cp -R /Applications/Multica.app.0.3.13.pre-update-20260713-221125.bak /Applications/Multica.app
# (or the prior .bak in case of alphabetical rotation)
open /Applications/Multica.app
```

Post-rollback invariants:
- `defaults read /Applications/Multica.app/Contents/Info.plist
  CFBundleShortVersionString` → `0.3.13`
- The Claude Science entry in Labs still says "reads its own BYOK
  keys" (the rollback reverts the catalog change).
- 5432 + 8090 listen still < 8 s.

## Files changed

| File | Lines |
|---|---|
| `apps/desktop/package.json` | version `0.3.13` → `0.3.14` |
| `server/internal/experimental/catalog.go` | two string updates in the `claude_science` entry (title description + comment block) |
| `apps/desktop/src/main/claude-science-manager.ts` | full rewrite (no-op IPC stub) |
| `apps/desktop/src/preload/index.ts` | drop `getWebviewPreloadPath` from `claudeScience` |
| `apps/desktop/src/preload/index.d.ts` | drop `getWebviewPreloadPath` interface entry |
| `apps/desktop/src/renderer/src/pages/claude-science-view.tsx` | full rewrite (Multica-native page, all Chinese) |
| `server/cmd/multica/cmd_claude_science.go` | full rewrite (no-op verbs, reuses cmd_pythia.go `writeJSON`) |
| `server/internal/service/builtin_skills/multica-claude-science/SKILL.md` | full rewrite (4-step in-band flow, agent runtime as backend) |
| `apps/desktop/resources/main/experimental/webview-preload-claude-science.js` | DELETED (no longer needed) |
| `apps/desktop/vendor/openscience-bin/` | DELETED (no longer needed) |
| `apps/desktop/resources/openscience/` | DELETED (no longer needed) |
