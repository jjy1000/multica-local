// 0.3.29.1 — re-import the full 4-language locale bundle.
//
// Pre-0.3.28 base namespaces (common / issues / agents / settings /
// layout / chat / inbox / autopilot* / projects / squads / skills /
// knowledge / runtime / profile / my-issues / labels / daemons /
// onboard / auth / modals / editor / ui / usage / workspace / billing /
// members / search / server-status / runtimes) are imported here so
// the desktop app renders in the user's chosen locale instead of
// silently falling back to English when a namespace is missing.
//
// 0.3.29-shipped bundles (claude-lab / mythos / pythia) keep their
// wrapper top-level key (claude_lab / mythos / pythia) because they
// were authored after the wrapper convention moved into AppSidebar;
// the pre-0.3.28 namespaces do NOT have wrappers — the filename is
// the namespace key and the file body is the namespace content.

import type { LocaleResources, SupportedLocale } from "@multica/core/i18n";

import enAgents from "./en/agents.json";
import enAuth from "./en/auth.json";
import enAutopilots from "./en/autopilots.json";
import enBilling from "./en/billing.json";
import enChat from "./en/chat.json";
import enClaudeLab from "./en/claude-lab.json";
import enCommon from "./en/common.json";
import enEditor from "./en/editor.json";
import enInbox from "./en/inbox.json";
import enIssues from "./en/issues.json";
import enLabels from "./en/labels.json";
import enLayout from "./en/layout.json";
import enMembers from "./en/members.json";
import enModals from "./en/modals.json";
import enMyIssues from "./en/my-issues.json";
import enMythos from "./en/mythos.json";
import enOnboarding from "./en/onboarding.json";
import enProjects from "./en/projects.json";
import enPythia from "./en/pythia.json";
import enRuntimes from "./en/runtimes.json";
import enSearch from "./en/search.json";
import enServerStatus from "./en/server-status.json";
import enSettings from "./en/settings.json";
import enSkills from "./en/skills.json";
import enSquads from "./en/squads.json";
import enUi from "./en/ui.json";
import enUsage from "./en/usage.json";
import enWorkspace from "./en/workspace.json";

import jaAgents from "./ja/agents.json";
import jaAuth from "./ja/auth.json";
import jaAutopilots from "./ja/autopilots.json";
import jaBilling from "./ja/billing.json";
import jaChat from "./ja/chat.json";
import jaClaudeLab from "./ja/claude-lab.json";
import jaCommon from "./ja/common.json";
import jaEditor from "./ja/editor.json";
import jaInbox from "./ja/inbox.json";
import jaIssues from "./ja/issues.json";
import jaLabels from "./ja/labels.json";
import jaLayout from "./ja/layout.json";
import jaMembers from "./ja/members.json";
import jaModals from "./ja/modals.json";
import jaMyIssues from "./ja/my-issues.json";
import jaMythos from "./ja/mythos.json";
import jaOnboarding from "./ja/onboarding.json";
import jaProjects from "./ja/projects.json";
import jaPythia from "./ja/pythia.json";
import jaRuntimes from "./ja/runtimes.json";
import jaSearch from "./ja/search.json";
import jaServerStatus from "./ja/server-status.json";
import jaSettings from "./ja/settings.json";
import jaSkills from "./ja/skills.json";
import jaSquads from "./ja/squads.json";
import jaUi from "./ja/ui.json";
import jaUsage from "./ja/usage.json";
import jaWorkspace from "./ja/workspace.json";

import koAgents from "./ko/agents.json";
import koAuth from "./ko/auth.json";
import koAutopilots from "./ko/autopilots.json";
import koBilling from "./ko/billing.json";
import koChat from "./ko/chat.json";
import koClaudeLab from "./ko/claude-lab.json";
import koCommon from "./ko/common.json";
import koEditor from "./ko/editor.json";
import koInbox from "./ko/inbox.json";
import koIssues from "./ko/issues.json";
import koLabels from "./ko/labels.json";
import koLayout from "./ko/layout.json";
import koMembers from "./ko/members.json";
import koModals from "./ko/modals.json";
import koMyIssues from "./ko/my-issues.json";
import koMythos from "./ko/mythos.json";
import koOnboarding from "./ko/onboarding.json";
import koProjects from "./ko/projects.json";
import koPythia from "./ko/pythia.json";
import koRuntimes from "./ko/runtimes.json";
import koSearch from "./ko/search.json";
import koServerStatus from "./ko/server-status.json";
import koSettings from "./ko/settings.json";
import koSkills from "./ko/skills.json";
import koSquads from "./ko/squads.json";
import koUi from "./ko/ui.json";
import koUsage from "./ko/usage.json";
import koWorkspace from "./ko/workspace.json";

import zhHansAgents from "./zh-Hans/agents.json";
import zhHansAuth from "./zh-Hans/auth.json";
import zhHansAutopilots from "./zh-Hans/autopilots.json";
import zhHansBilling from "./zh-Hans/billing.json";
import zhHansChat from "./zh-Hans/chat.json";
import zhHansClaudeLab from "./zh-Hans/claude-lab.json";
import zhHansCommon from "./zh-Hans/common.json";
import zhHansEditor from "./zh-Hans/editor.json";
import zhHansInbox from "./zh-Hans/inbox.json";
import zhHansIssues from "./zh-Hans/issues.json";
import zhHansLabels from "./zh-Hans/labels.json";
import zhHansLayout from "./zh-Hans/layout.json";
import zhHansMembers from "./zh-Hans/members.json";
import zhHansModals from "./zh-Hans/modals.json";
import zhHansMyIssues from "./zh-Hans/my-issues.json";
import zhHansMythos from "./zh-Hans/mythos.json";
import zhHansOnboarding from "./zh-Hans/onboarding.json";
import zhHansProjects from "./zh-Hans/projects.json";
import zhHansPythia from "./zh-Hans/pythia.json";
import zhHansRuntimes from "./zh-Hans/runtimes.json";
import zhHansSearch from "./zh-Hans/search.json";
import zhHansServerStatus from "./zh-Hans/server-status.json";
import zhHansSettings from "./zh-Hans/settings.json";
import zhHansSkills from "./zh-Hans/skills.json";
import zhHansSquads from "./zh-Hans/squads.json";
import zhHansUi from "./zh-Hans/ui.json";
import zhHansUsage from "./zh-Hans/usage.json";
import zhHansWorkspace from "./zh-Hans/workspace.json";

const en: LocaleResources = {
  agents: enAgents,
  auth: enAuth,
  autopilots: enAutopilots,
  billing: enBilling,
  chat: enChat,
  "claude-lab": enClaudeLab.claude_lab,
  common: enCommon,
  editor: enEditor,
  inbox: enInbox,
  issues: enIssues,
  labels: enLabels,
  layout: enLayout,
  members: enMembers,
  modals: enModals,
  "my-issues": enMyIssues,
  mythos: enMythos,
  onboarding: enOnboarding,
  projects: enProjects,
  pythia: enPythia.pythia,
  runtimes: enRuntimes,
  search: enSearch,
  "server-status": enServerStatus,
  settings: enSettings,
  skills: enSkills,
  squads: enSquads,
  ui: enUi,
  usage: enUsage,
  workspace: enWorkspace,
};

const zhHans: LocaleResources = {
  agents: zhHansAgents,
  auth: zhHansAuth,
  autopilots: zhHansAutopilots,
  billing: zhHansBilling,
  chat: zhHansChat,
  "claude-lab": zhHansClaudeLab.claude_lab,
  common: zhHansCommon,
  editor: zhHansEditor,
  inbox: zhHansInbox,
  issues: zhHansIssues,
  labels: zhHansLabels,
  layout: zhHansLayout,
  members: zhHansMembers,
  modals: zhHansModals,
  "my-issues": zhHansMyIssues,
  mythos: zhHansMythos,
  onboarding: zhHansOnboarding,
  projects: zhHansProjects,
  pythia: zhHansPythia.pythia,
  runtimes: zhHansRuntimes,
  search: zhHansSearch,
  "server-status": zhHansServerStatus,
  settings: zhHansSettings,
  skills: zhHansSkills,
  squads: zhHansSquads,
  ui: zhHansUi,
  usage: zhHansUsage,
  workspace: zhHansWorkspace,
};

const ko: LocaleResources = {
  agents: koAgents,
  auth: koAuth,
  autopilots: koAutopilots,
  billing: koBilling,
  chat: koChat,
  "claude-lab": koClaudeLab.claude_lab,
  common: koCommon,
  editor: koEditor,
  inbox: koInbox,
  issues: koIssues,
  labels: koLabels,
  layout: koLayout,
  members: koMembers,
  modals: koModals,
  "my-issues": koMyIssues,
  mythos: koMythos,
  onboarding: koOnboarding,
  projects: koProjects,
  pythia: koPythia.pythia,
  runtimes: koRuntimes,
  search: koSearch,
  "server-status": koServerStatus,
  settings: koSettings,
  skills: koSkills,
  squads: koSquads,
  ui: koUi,
  usage: koUsage,
  workspace: koWorkspace,
};

const ja: LocaleResources = {
  agents: jaAgents,
  auth: jaAuth,
  autopilots: jaAutopilots,
  billing: jaBilling,
  chat: jaChat,
  "claude-lab": jaClaudeLab.claude_lab,
  common: jaCommon,
  editor: jaEditor,
  inbox: jaInbox,
  issues: jaIssues,
  labels: jaLabels,
  layout: jaLayout,
  members: jaMembers,
  modals: jaModals,
  "my-issues": jaMyIssues,
  mythos: jaMythos,
  onboarding: jaOnboarding,
  projects: jaProjects,
  pythia: jaPythia.pythia,
  runtimes: jaRuntimes,
  search: jaSearch,
  "server-status": jaServerStatus,
  settings: jaSettings,
  skills: jaSkills,
  squads: jaSquads,
  ui: jaUi,
  usage: jaUsage,
  workspace: jaWorkspace,
};

/**
 * `RESOURCES` — the locale-resource map consumed by `createI18n`.
 *
 * Each supported locale has 28 namespaces wired (the 25 pre-0.3.28 base
 * namespaces plus claude-lab / mythos / pythia). Filenames match the
 * namespace key for pre-0.3.28 files; the 0.3.29 bundles keep their
 * wrapper key (claude_lab / mythos / pythia) which is unwrapped here.
 */
export const RESOURCES: Record<SupportedLocale, LocaleResources> = {
  en,
  "zh-Hans": zhHans,
  ko,
  ja,
};