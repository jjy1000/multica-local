---
name: release-notes-0.3.69
created: 2026-07-30T18:01:21Z
updated: 2026-07-30T18:01:21Z
status: complete
---

# 0.3.69 — Labs install feedback + runtime GC fix

A backend + frontend patch (ships new Go binaries AND a new renderer — not a
renderer-only patch). Theme: surface lab resource-install failures to the user
and fix the experimental runtime GC archive path.

## User-visible

- **Labs tab now warns when a lab enables but its resource install fails.**
  `UpdateExperimentalFlag` returns `200 + {install_error}` (instead of a
  silent success) when the pref write succeeded but `RunInstall` failed. The
  Labs tab consumes this and shows a warning toast ("实验已开启,但资源装载失败,
  可关闭后重新开启重试") with the underlying error as the description, instead
  of leaving a silently-empty lab. New i18n key `labs.toast_install_warning`
  (4 locales: en / zh-Hans / ja / ko).

## Stability / correctness (backend)

- **`runtime_gc.go`: real `tarGz` implementation** (was an 11-byte
  "placeholder" stub since 0.3.19). The stub returned nil, so the caller's
  post-archive `os.RemoveAll` silently destroyed the session data the 90-day
  tier was meant to preserve until the 120-day final unlink. Now writes a real
  gzip-compressed tarball atomically (tmp + rename), entries rooted under the
  session dir name. (Recorded in root CLAUDE.md "Known Stability Surfaces".)
- **`experimental_resources.go`**: `installableSources` fallback table now
  includes `pythia_oracle` and `code_canvas`, keeping the registry-nil
  fallback in sync with the live registry.
- Labs-related fixes across `skill.go`, `squad.go`, `registry.go`,
  `experimental_mythos_run.go`, `mythos/supervise.go`, `cmd/server/main.go`.

## Frontend (besides the Labs toast)

- `create-issue.tsx`, `issue-labs-section.tsx`, `app-sidebar.tsx`,
  `packages/core/api/client.ts` (parses the `install_error` field).

## Repo infrastructure (does NOT ship in the .app)

Sub-domain `CLAUDE.md`/`AGENTS.md` mirrors, `check-agents-docs-sync.mjs`
(+115), `ci.yml`, eslint boundaries — documentation / build-infra only.

## Ship path

Manual asar-repack fallback, extended to also replace the Go binaries
(`server/` changed, so `bundle-cli` rebuilt `resources/bin/{multica,server,
migrate}`). No resource-path additions/removals/renames, so the fallback
contract holds. electron-builder's electron download remains proxy-blocked.

## Conventions check (per `apps/docs/.../conventions.mdx`)

New i18n key passes: namespace-level `toast_*` naming (consistent with
`toast_failed`), arrow-expression selector, en/zh semantic parity, gentle-clear
error voice. Note: the zh-Hans string uses a half-width comma, matching the
existing `settings.json` house style (the whole file uses half-width commas);
this is a pre-existing doc-vs-practice gap in conventions §3, not a regression
introduced here.
