---
name: release-notes-0.5.79
created: 2026-08-27T12:15:00+08:00
updated: 2026-08-27T12:15:00+08:00
---

# 0.5.79 — Skill Local Import (MUL-6703 split port)

**Branch:** `epic/0.5.72-followups`
**Upstream origin:** `9a19c90f8` (MUL-6703, 2026-08-27) — see re-grade #2 in `.omc/upstream-integration-triage-2026-08-26.md`

## TL;DR

The New-skill dialog gains a fourth route: **Import from local** — pick a folder containing `SKILL.md` or a `.skill` / `.zip` archive on this machine and pack it straight into the workspace. Preview shows name / description / file count before import; missing SKILL.md, oversize input, and name conflicts fail with the server's reason instead of creating a partial skill. Pure-local feature: no network source, no cloud surface.

## What changed (3 commits)

### `701deb32b` — server: archive branch of POST /api/skills/import

- New `skill_import_archive.go`: multipart gate on Content-Type, 16 MiB compressed upload cap (`MaxBytesReader` + `ParseMultipartForm`), zip-slip-safe root resolution ("root on the shallowest SKILL.md", accepting both root-level and single-wrapper layouts), per-file 1 MiB / bundle 8 MiB / 128-file caps shared with the URL path, binary-asset drops, `__MACOSX`/dotfile/license filtering.
- `skill.go`: `ImportSkill` branches to `importSkillFromArchive` when multipart; the create/conflict tail is extracted into **`finishSkillImport`** so URL imports and archive imports converge on one code path (matches upstream's structure for future ports).

### `a78df9892` — core: pack-archive + client method

- `skills/pack-archive.ts` (+234-line test) ported verbatim: directory walking, frontmatter preview, size/count guards mirroring the handler caps, browser-side zip layout; `path+size decided before any file is read`.
- `ApiClient.importSkillArchive(file, onConflict)` posts the multipart body through `fetchRaw`. **Fork adaptation:** implemented WITHOUT upstream's zod envelope layer (`SkillImportResultSchema` / `EMPTY_SKILL_IMPORT_RESULT`) because this fork has no `SkillSchema`/parse layer for skills at all — the method reads the raw JSON envelope and surfaces `reason` from `ApiError.body` on structured failures.

### `8194b1198` — views: dialog route + i18n ×4 + docs

- Dialog (33-line drift pre-port) took upstream's patch nearly clean: new chooser card, `LocalForm`, back-button `resetLocal()` hand-applied. Upstream's dialog test file passes verbatim: 8/8.
- Locale keys (`create.local.*`) applied via upstream's own patches in all four languages.
- Fork-authored skills docs gained a 从本地文件夹 / 归档 bullet in all four languages (upstream's docs text was a wholesale divergence; fork keeps its own voice).

## Skipped by design

- Upstream's `client.test.ts` additions pin the zod-envelope internals that don't exist here; the adapted path is covered by the dialog suite instead.
- `tutorial*.mdx` pages + screenshot webp: absent from this fork.

## Inbox decision gate (recorded, not actioned)

MUL-6632 + inbox-archive family upgraded from "defer" to an explicit decision gate: upstream's inbox is a 19-file / ~4400 LOC architecture born inside that commit; fork's shared-name files are whole-file rewrites (page: 911↔523 lines). Adopting = multi-session architecture replacement across desktop shell nav + realtime + locales; keeping fork inbox means status/priority filters would be future fork-local work. Full note: triage doc, "Post-triage re-grade #2".

## Gates

- `go build`/`go vet` clean; handler suite green incl. `TestParseSkillArchive_*` units + DB-backed `TestImportSkill_ArchiveUploadCreatesSkill`
- core: tsc clean, pack-archive 11/11
- views: tsc clean, dialog 8/8
- turbo `pnpm typecheck`: exit 0
