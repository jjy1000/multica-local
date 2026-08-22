// SemanticaModeBanner (0.5.57 P5)
//
// Inline banner rendered inside the semantica-explorer's page chrome
// that surfaces the workspace's isolation mode (individual vs team)
// to the viewer. Single source of truth for the mode is the
// `mode` field on the GET /api/experimental/semantica/decisions
// response (server-side CountWorkspaceMembers at write time +
// per-page echo); the banner is a pure presentational wrapper around
// that signal.
//
// Why a banner and not a setting:
//   - Mode is a derived property of the workspace (member count), not
//     a user preference. Surfacing it as a setting invites the user to
//     "change" something they cannot.
//   - The fork is single-user (CLAUDE.md "Username-only login") so the
//     individual case is overwhelmingly common. The banner is most
//     useful as a heads-up when the user is in the team case.
//
// i18n: arrow-expression selectors only (the 2026-07-14 incident rule).
// No block-body `t(($) => { return $.x.y; })` — the ESLint guard in
// views/eslint.config.mjs rejects it at build time.

import { useT } from "../../i18n";

export type SemanticaMode = "individual" | "team";

export interface SemanticaModeBannerProps {
  mode: SemanticaMode;
  /** Optional override; defaults to the closed list of 2 keys. */
  className?: string;
}

export function SemanticaModeBanner({ mode, className }: SemanticaModeBannerProps) {
  const { t } = useT("experimental");
  const label = t(($) => $.semantica.mode[mode].label);
  const desc = t(($) => $.semantica.mode[mode].desc);
  const isTeam = mode === "team";

  return (
    <div
      className={className}
      data-semantica-mode={mode}
      role={isTeam ? "status" : "note"}
      aria-live={isTeam ? "polite" : "off"}
    >
      <strong>{label}</strong>
      <span className="ml-2 text-muted-foreground">{desc}</span>
    </div>
  );
}