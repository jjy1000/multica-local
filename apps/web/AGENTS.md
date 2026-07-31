<!-- AUTO-SYNCED MIRROR of ./CLAUDE.md (the source of truth for this directory). Edit CLAUDE.md, then regenerate this file; parity is enforced by scripts/check-agents-docs-sync.mjs. -->

# Web App Rules (apps/web/)

Next.js App Router frontend. Read this before touching `apps/web/`. This app is
thin platform wiring: almost all business pages/components live in
`packages/views/` and are shared with desktop. Cross-cutting product rules,
package boundaries, and the state model live in the root `CLAUDE.md`;
shared-page mechanics live in `packages/views/CLAUDE.md`.

## Layout

- `app/` — App Router routes. Route groups: `(auth)` (login + callback stub),
  `(landing)` (marketing/landing), `[workspaceSlug]/` (workspace-scoped
  dashboard routes). Also `robots.ts`, `sitemap.ts`, `global-error.tsx`.
- `platform/` — `navigation.tsx`: the **only** place for Next.js
  navigation/platform APIs (`next/navigation` etc.). Shared code reaches it
  through `NavigationAdapter` / `useNavigation()` / `<AppLink>`.
- `features/` — web-only feature code (`auth`, `landing`).
- `components/` — web-only providers/bridges (theme, query/web providers,
  notification bridge, pageview tracker).
- `lib/` — locale routing, docs hrefs, use-cases i18n/source helpers.
- `config/runtime-urls.ts` — runtime URL resolution.
- `content/use-cases/` — fumadocs MDX content (compiled by `fumadocs-mdx`).
- `proxy.ts` — request middleware: resolves locale from cookie/header signals
  and rewrites legacy pre-#1131 workspace URLs (`/issues/...` →
  `/{slug}/issues/...`) so old bookmarks and deep links don't 404.

## Hard boundaries (do NOT violate)

- Business pages/components shared with desktop belong in `packages/views/`,
  not here. Keep only platform wiring and web-only UI in this app.
- Next.js APIs stay inside this app (`platform/` for navigation). Never import
  `next/*` from `packages/`.
- Declare directly imported external packages in `apps/web/package.json`;
  shared deps use `catalog:` from `pnpm-workspace.yaml`.
- Login is username-only (`useAuthStore.getState().loginWithUsername(name)`);
  the OAuth callback page is a redirect stub. Do not re-add Google OAuth,
  telemetry, or other upstream cloud features.

## Commands

- `pnpm dev:web` (repo root) — dev server; port comes from `FRONTEND_PORT`
  (default 3000).
- `pnpm --filter @multica/web typecheck | test | lint` — scoped checks.
- `typecheck` and `build` run `fumadocs-mdx` first; a missing MDX codegen pass
  is the usual cause of "cannot find `.source`" type errors.
- Builds use webpack explicitly (`next build --webpack`), not Turbopack.

## Testing

- Only platform-wiring tests live here (`apps/web/**/*.test.ts(x)`): cookies,
  redirects, locale routing, search params, runtime URLs.
- Never test shared component behavior here — that belongs in
  `packages/views/*.test.tsx`.
