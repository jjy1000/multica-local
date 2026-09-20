<!-- AUTO-SYNCED MIRROR of ./CLAUDE.md (the source of truth for this directory). Edit CLAUDE.md, then regenerate this file; parity is enforced by scripts/check-agents-docs-sync.mjs. -->

# Shared Package Rules (packages/)

Headless logic and shared UI consumed by `apps/web` and `apps/desktop`. Read this
before touching `packages/`. Cross-cutting product rules, ship chain, and desktop
lifecycle live in the root `CLAUDE.md`; this file is the nearby guide for package
boundaries, commands, and pitfalls so you don't have to read the whole root file
to work here. (Mobile is independent — see `apps/mobile/CLAUDE.md`.)

## Layout & dependency direction

- `core/` — headless business logic, API client, React Query hooks, Zustand stores.
- `ui/` — atomic UI components only (no business logic).
- `views/` — shared business pages/components for web and desktop.
- `tsconfig/`, `eslint-config/` — shared configs.

Packages export raw `.ts` / `.tsx`; consuming apps compile them. Direction:
`views → core + ui`. **`core` and `ui` must stay independent of each other.**

## Hard boundaries (do NOT violate)

- `core/`: no `react-dom`, no `localStorage` (use `StorageAdapter`), no
  `process.env`, no UI libraries.
- `ui/`: no `@multica/core` imports, no business logic.
- `views/`: no `next/*`, no `react-router-dom`, no stores. Navigate via
  `NavigationAdapter`, `useNavigation()`, `<AppLink>`.
- Platform-specific navigation belongs in the apps: `apps/web/platform/` (Next.js)
  and `apps/desktop/src/renderer/src/platform/` (react-router-dom).
- Every workspace declares directly-imported external packages in its own
  `package.json`. Shared deps use `catalog:` from `pnpm-workspace.yaml`.
- Where to put shared code: platform APIs → app/platform layer; headless logic →
  `core/`; shared UI/business views → `views/`; shared primitives → `ui/`.

## State model (server vs client)

- **TanStack Query owns server state**: issues, users, workspaces, inbox, agents,
  members — anything fetched from the API.
- **Zustand owns client state**: selected workspace, filters, drafts, modals, tabs,
  nav history. Shared Zustand stores live in `core/`, never in `views/` or apps.
- React Context is for platform plumbing only (`WorkspaceIdProvider`, `NavigationProvider`).
- Only auth/workspace stores may call `api.*` directly; other server interaction
  belongs in queries/mutations.
- Workspace-scoped query keys must include `wsId`.
- Mutations are optimistic by default: patch locally, send, roll back on failure,
  invalidate on settle. WS events invalidate/patch the Query cache — they never
  write to Zustand directly.
- Persist durable prefs/drafts/layout only; never persist server data or ephemeral
  UI state. Zustand selectors must return stable references.
- Hooks needing workspace context accept `wsId`; don't call `useWorkspaceId()`
  internally unless guaranteed to be under the provider.

## Realtime transcript streaming (0.5.109)

`core/realtime/use-realtime-sync.ts` writes task-message WS frames into the
MUL-6396 100ms batch window, EXCEPT the leading edge of a burst, which goes
out immediately so streamed agent output (Codex deltas since 0.5.109) appears
without window latency. Both write paths merge through
`mergeTaskMessagesBySeq` (core/chat/queries.ts) — seq-sorted and deduped —
so cross-POST arrival order can never reach the render order. When touching
either path, keep the seq merge; arrival order between the leading-edge POST
and the windowed POST is genuinely racy by design (daemon flushes from two
goroutines).

## Commands

```bash
pnpm test path/to/file.test.ts   # single Vitest test (from repo root)
pnpm test                        # all TS/Vitest tests via Turborepo
pnpm typecheck
pnpm lint
pnpm ui:add badge                # add a shadcn/Base UI component into packages/ui
```

## API compatibility (core/api/)

Frontend must survive backend response drift (especially installed desktop builds):

- Parse API JSON with `parseWithFallback` in `core/api/schema.ts` + a zod schema.
  Do NOT cast network JSON to `T`. Responses consumed by UI logic pass through a
  schema before returning.
- Optional-chain and default fields defensively; prefer explicit `=== true` over
  truthy checks on server fields; server-driven enum switches need a `default`.
- When adding/changing an endpoint, update the schema and add a
  malformed-response test.

## i18n selector rule (views/)

Use **arrow-expression selectors only**: `t(($) => $.foo.bar)` ✓.
`t(($) => { return $.foo.bar; })` ✗ — a block body returns a plain string, so
i18next's `[PATH_KEY]` becomes `undefined` and the next line throws `TypeError`,
which unmounted the whole desktop window in the 2026-07-14 incident. ESLint blocks
both `t(($) => {...})` and `useT(($) => {...})` via `no-restricted-syntax` in
`views/eslint.config.mjs`. See `views/i18n/use-t.ts` for the incident reference.

## Conventions & UI tokens

- Naming, i18n glossary, and Chinese product voice source of truth:
  `apps/docs/content/docs/developers/conventions.mdx` (+ `.zh.mdx`). Read it before
  editing `views/locales/`. `views/locales/glossary.md` is only a redirect stub.
- Reserved slugs: edit `server/internal/handler/reserved_slugs.json`, run
  `pnpm generate:reserved-slugs`, commit the generated `core/paths/reserved-slugs.ts`.
- CSS is shared from `ui/styles/`. Use semantic tokens (`bg-background`,
  `text-muted-foreground`); avoid hardcoded Tailwind colors. Prefer shadcn/Base UI
  components over custom ones.

## Testing (tests follow the code)

| What is tested | Location |
| --- | --- |
| Shared business logic, stores, queries, hooks | `core/*.test.ts` |
| Shared UI components, pages, forms, modals | `views/*.test.tsx` |

- Never test shared-component behavior in an app test file.
- `views/` tests must not mock `next/*` or `react-router-dom`.
- Mock `@multica/core` stores with the Zustand callable-store shape (`selectorFn`
  plus `getState`); mock `@multica/core/api` for API calls.
- Prefer writing the failing test in the correct package before implementing a
  behavioral change.

## Reviewing UI

When auditing UI code (accessibility, UX, visual design), invoke the
`web-design-guidelines` skill (`.agents/skills/web-design-guidelines/SKILL.md`).
