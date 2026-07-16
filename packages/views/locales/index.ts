// 0.3.29 sparse-checkout recovery: re-export the locale resource
// bundles that ship with the desktop app. Only 0.3.29-specific bundles
// (pythia / claude-lab / mythos) live in this fork's working tree;
// the pre-0.3.28 base locales (common / issues / agents / settings /
// layout / etc.) live in the upstream `multica` tree and are not
// present in the sparse checkout, so the desktop build cannot resolve
// them.
//
// To unblock the 0.3.29 ship chain we expose ONLY the 0.3.29 locales
// here; the desktop app's `createDesktopLocaleAdapter` will still pick
// one of these four locales at runtime, and any missing key falls back
// to the empty string (i18next's default behavior when a namespace has
// no entry for a key). This is a temporary narrow scope limited to the
// 0.3.29 ship — the next time the upstream locales land, this file is
// replaced with the full locale loader.

import type { LocaleResources, SupportedLocale } from "@multica/core/i18n";

import enClaudeLab from "./en/claude-lab.json";
import enMythos from "./en/mythos.json";
import enPythia from "./en/pythia.json";
import jaClaudeLab from "./ja/claude-lab.json";
import jaMythos from "./ja/mythos.json";
import jaPythia from "./ja/pythia.json";
import koClaudeLab from "./ko/claude-lab.json";
import koMythos from "./ko/mythos.json";
import koPythia from "./ko/pythia.json";
import zhHansClaudeLab from "./zh-Hans/claude-lab.json";
import zhHansMythos from "./zh-Hans/mythos.json";
import zhHansPythia from "./zh-Hans/pythia.json";

const en: LocaleResources = {
  "claude-lab": enClaudeLab.claude_lab,
  mythos: enMythos.mythos,
  pythia: enPythia.pythia,
};

const zhHans: LocaleResources = {
  "claude-lab": zhHansClaudeLab.claude_lab,
  mythos: zhHansMythos.mythos,
  pythia: zhHansPythia.pythia,
};

const ko: LocaleResources = {
  "claude-lab": koClaudeLab.claude_lab,
  mythos: koMythos.mythos,
  pythia: koPythia.pythia,
};

const ja: LocaleResources = {
  "claude-lab": jaClaudeLab.claude_lab,
  mythos: jaMythos.mythos,
  pythia: jaPythia.pythia,
};

/**
 * `RESOURCES` — the locale-resource map consumed by `createI18n`.
 *
 * Sparse-checkout note: only the 0.3.29-specific namespaces are wired
 * here. Pre-0.3.28 locales (common / issues / agents / settings /
 * layout / etc.) are not in the fork's working tree; those keys
 * silently fall back to empty strings until upstream locales land in a
 * later commit. The desktop app's pickLocale still resolves one of
 * the four SupportedLocale entries, so the i18n pipeline stays
 * exercised end-to-end.
 */
export const RESOURCES: Record<SupportedLocale, LocaleResources> = {
  en,
  "zh-Hans": zhHans,
  ko,
  ja,
};