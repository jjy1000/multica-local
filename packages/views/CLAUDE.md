# Shared Views Rules (packages/views/)

Shared business pages/components consumed by both `apps/web` and
`apps/desktop`. This file consolidates the views-scoped constraints from the
root `CLAUDE.md` and `packages/CLAUDE.md` so you don't have to read either in
full to work here. Broader package-layer rules (state model, API compat,
`core`/`ui` boundaries) live in `packages/CLAUDE.md`.

## Hard boundaries (do NOT violate)

- No `next/*`, no `react-router-dom`, no stores in `views/`. Navigate via the
  adapters: `NavigationAdapter`, `useNavigation()`, `<AppLink>`.
- Platform-specific navigation belongs in the apps: `apps/web/platform/`
  (Next.js) and `apps/desktop/src/renderer/src/platform/` (react-router-dom).
- Shared Zustand stores live in `packages/core/`, never in `views/`.
- Dependency direction: `views → core + ui`. Declare directly-imported external
  packages in `views/package.json`; shared deps use `catalog:` from
  `pnpm-workspace.yaml`.
- Where code goes: platform APIs → app/platform layer; headless logic →
  `core/`; shared UI/business views → here; shared primitives → `ui/`.

## Adding a shared web/desktop page

1. Put the page/component in `views/<domain>/`.
2. Add platform wiring in both `apps/web/app/` and the desktop router (unless
   the desktop flow is a `WindowOverlay` transition).
3. Use `useNavigation().push()` or `<AppLink>` in shared code; cross-workspace
   navigation must go through the navigation adapter (`switchWorkspace`).
4. Use shared guards/providers such as `DashboardGuard` from `views/layout/`.
5. Keep platform-only UI in the app or inject it through props/slots.
6. Hooks that need workspace context accept `wsId`; don't call
   `useWorkspaceId()` internally unless guaranteed to run under the provider.
   Pre-workspace surfaces (e.g. Labs chat) pass `wsId` explicitly — see
   `ChatWindow`'s optional `wsId` prop and `experimental-chat-pane.tsx`.

## Network calls in experimental/Labs components

Every HTTP/SSE call from a Labs surface must use `api.rawRequest(path, init)`
(`@multica/core/api`), never a bare `fetch()` — bare fetch resolves against the
renderer origin and silently fails in the desktop app (no proxy, token-mode
auth). See root `CLAUDE.md` → "Experimental tab network calls (0.3.30)".

## Lab run result rendering (0.5.97 — one shared renderer, two distinct affordances)

A lab run's structured deliverables
(`LabTaskBrief.result_attachments` / `result_predictions` / `result_code_blocks`)
have exactly ONE renderer: `experimental/components/lab-task-result-view.tsx`
(`LabTaskResultView` + `labTaskHasStructuredDeliverables`). Every surface that
shows a run's result — the desktop claude-lab PlanTimeline rows, the desktop
LatestResultPanel 实验结果 panel, and the issue-side `LabDeliverableSummary`
card — renders through it. Do NOT re-embed per-surface attachment/prediction/
code strips (0.5.97 deleted the desktop-local copies for exactly this reason).

- `interactive-chart` envelopes render as real recharts via
  `experimental/components/interactive-chart-envelope.tsx`. Never route them
  into a generic code/JSON dump path again (that was a long-standing TODO the
  0.5.97 cycle closed).
- Agent payloads are untrusted: any sink rendering attachment markup/URLs goes
  through `experimental/components/lab-attachment-sanitize.ts`
  (`safeSvgMarkup` / `safeImageSrc` / `safeHrefUrl`). The legacy `html` kind
  degrades to a download link (server allowlist dropped it in 0.3.42).
- Affordance contract on the issue-side summary card
  (`lab-deliverable-summary.tsx`): 查看结果渲染 expands the card IN PLACE
  (toggle only for terminal runs WITH structured deliverables — expanding an
  in-flight run renders a void, a summary-only run would duplicate the clamped
  summary) vs 在实验室查看完整记录 = the run-scoped `LabRunLink` deep link
  (`?issue=&run=`). Do NOT re-add unscoped "open in lab" links — the two
  duplicate same-page jumps were the defect this contract replaced.

Lint traps hit while writing these components: views enforces
`i18next/no-literal-string` (desktop does not — literals brought over from a
desktop page must be keyed) and `noUncheckedIndexedAccess` (regex match groups
need `m?.[1]`, not `m[1]` guarded only by the match test).

## i18n selector rule (crashes the app if violated)

Selectors MUST be arrow expressions: `t(($) => $.foo.bar)` ✓.
`t(($) => { return $.foo.bar; })` ✗ — a block body returns a plain string, so
i18next's `[PATH_KEY]` becomes `undefined` and the next line throws
`TypeError`, which unmounted the whole desktop window in the 2026-07-14
incident. ESLint blocks both `t(($) => {...})` and `useT(($) => {...})` via
`no-restricted-syntax` in `views/eslint.config.mjs`; incident reference in
`views/i18n/use-t.ts`.

## Locales & conventions

- Naming, i18n glossary, and Chinese product voice source of truth:
  `apps/docs/content/docs/developers/conventions.mdx` (+ `.zh.mdx`). Read it
  before editing `views/locales/`. `views/locales/glossary.md` is only a
  redirect stub.
- New locale namespaces must be registered in `views/locales/index.ts` (all 4
  locales: en / zh-Hans / ja / ko) and typed in `views/i18n/resources-types.ts`.

## UI tokens

CSS is shared from `packages/ui/styles/`. Use semantic tokens
(`bg-background`, `text-muted-foreground`); avoid hardcoded Tailwind colors.
Prefer shadcn/Base UI components (`pnpm ui:add <component>` from repo root)
over custom ones. When auditing UI (accessibility, UX, visual design), invoke
the `web-design-guidelines` skill.

## Detail-page content width

- Issue detail (`issues/components/issue-detail.tsx`) renders its content
  column full-width (`w-full px-8`, no `max-w-*` cap, skeleton included) so it
  fills whatever space the panel has — collapsing the app sidebar, the inbox
  list, or the issue's right property sidebar widens the content accordingly.
  Do NOT re-add `mx-auto max-w-4xl` there.
- Chat surfaces (`chat/components/*`) intentionally keep the centered
  `mx-auto max-w-4xl` column — leave them as-is.
- Project detail and autopilot detail still use centered `max-w-4xl` columns;
  if that changes, follow the issue-detail pattern above.

## Testing

- Shared UI components, pages, forms, modals are tested here: `views/*.test.tsx`
  (Vitest; `pnpm test path/to/file.test.tsx` from repo root).
- Never test shared-component behavior in an app test file.
- `views/` tests must not mock `next/*` or `react-router-dom`.
- Mock `@multica/core` stores with the Zustand callable-store shape
  (`selectorFn` plus `getState`); mock `@multica/core/api` for API calls.
- Prefer writing the failing test here before implementing a behavioral change.
