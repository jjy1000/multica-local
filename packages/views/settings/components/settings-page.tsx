"use client";

import React from "react";
import {
  User,
  SlidersHorizontal,
  Key,
  Settings,
  FolderGit2,
  FlaskConical,
  Bell,
  CircleDot,
} from "lucide-react";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@multica/ui/components/ui/tabs";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useNavigation } from "../../navigation";
import { AccountTab } from "./account-tab";
import { PreferencesTab } from "./preferences-tab";
import { TokensTab } from "./tokens-tab";
import { WorkspaceTab } from "./workspace-tab";
import { RepositoriesTab } from "./repositories-tab";
import { LabsTab } from "./labs-tab";
import { NotificationsTab } from "./notifications-tab";
import { IssueStatusesTab } from "./issue-statuses-tab";
import { useT } from "../../i18n";

const ACCOUNT_TAB_KEYS = ["profile", "preferences", "notifications", "tokens"] as const;
const ACCOUNT_TAB_ICONS = {
  profile: User,
  preferences: SlidersHorizontal,
  notifications: Bell,
  tokens: Key,
} as const;

// 0.3.29 — Integrations, GitHub, and Updates were removed from the
// settings UI per the local-fork surface decision documented in
// .omc/release-notes-0.3.28.md (Integrations and the GitHub App had no
// remaining production usage after the upstream integrations sunset,
// Git is not exposed in the localized build, and Updates is dead since
// the desktop auto-update channel is itself disabled). Repositories
// stays because agents still consume the URL list. The catalog/handlers
// and the underlying auto-update/oauth source code are intentionally
// NOT deleted — CLAUDE.md forbids that drift.
const WORKSPACE_TAB_KEYS = [
  "general",
  "repositories",
  "labs",
  "issue_statuses",
] as const;
const WORKSPACE_TAB_VALUES = {
  general: "workspace",
  repositories: "repositories",
  labs: "labs",
  issue_statuses: "issue-statuses",
} as const;
const WORKSPACE_TAB_ICONS = {
  general: Settings,
  repositories: FolderGit2,
  labs: FlaskConical,
  issue_statuses: CircleDot,
} as const;

const DEFAULT_TAB = "profile";
const TAB_QUERY_KEY = "tab";

// Legacy `?tab=…` values that previously landed on the now-removed
// Integrations / GitHub / Updates surfaces silently fall through to the
// default Profile tab — we delete the old keys rather than redirect to
// any current tab so old bookmarks degrade gracefully without us
// preserving a dead TabsContent entry.
const LEGACY_WORKSPACE_TAB_REDIRECTS: Record<string, string> = {
  lark: DEFAULT_TAB,
  integrations: DEFAULT_TAB,
  github: DEFAULT_TAB,
  updates: DEFAULT_TAB,
};

export interface ExtraSettingsTab {
  value: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  content: React.ReactNode;
}

interface SettingsPageProps {
  /** Additional tabs injected by platform (e.g. desktop daemon settings) */
  extraAccountTabs?: ExtraSettingsTab[];
}

export function SettingsPage({ extraAccountTabs }: SettingsPageProps = {}) {
  const { t } = useT("settings");
  const workspaceName = useCurrentWorkspace()?.name;
  const navigation = useNavigation();

  // Whitelist of valid tab values; unknown ?tab=… values silently fall back to
  // the default. Whitelisting also blocks junk like ?tab=<script> from
  // surfacing in the DOM via Radix Tabs internals.
  const validTabs = React.useMemo(
    () =>
      new Set<string>([
        ...ACCOUNT_TAB_KEYS,
        ...Object.values(WORKSPACE_TAB_VALUES),
        ...(extraAccountTabs?.map((tab) => tab.value) ?? []),
      ]),
    [extraAccountTabs],
  );

  const tabFromUrl = navigation.searchParams.get(TAB_QUERY_KEY);
  const candidateTab = tabFromUrl
    ? LEGACY_WORKSPACE_TAB_REDIRECTS[tabFromUrl] ?? tabFromUrl
    : null;
  const activeTab =
    candidateTab && validTabs.has(candidateTab) ? candidateTab : DEFAULT_TAB;

  // replace (not push) so settings tab switches don't pollute browser history.
  // Preserve any other query params the page may carry.
  const handleTabChange = (next: string) => {
    const params = new URLSearchParams(navigation.searchParams);
    params.set(TAB_QUERY_KEY, next);
    navigation.replace(`${navigation.pathname}?${params.toString()}`);
  };

  return (
    <Tabs
      value={activeTab}
      onValueChange={handleTabChange}
      orientation="vertical"
      className="flex-1 min-h-0 gap-0 flex flex-col md:flex-row md:overflow-hidden overflow-y-auto"
    >
      {/* Left nav (stacks on top on mobile, sidebar on md+) */}
      <div className="shrink-0 md:w-52 border-b md:border-b-0 md:border-r md:overflow-y-auto p-3 md:p-4">
        <h1 className="text-body font-semibold mb-4 px-2">{t(($) => $.page.title)}</h1>
        <TabsList variant="line" className="flex-col items-stretch w-full">
          {/* My Account group */}
          <span className="px-2 pb-1 pt-2 text-caption font-medium text-muted-foreground">
            {t(($) => $.page.my_account)}
          </span>
          {ACCOUNT_TAB_KEYS.map((key) => {
            const Icon = ACCOUNT_TAB_ICONS[key];
            return (
              <TabsTrigger key={key} value={key}>
                <Icon className="h-4 w-4" />
                {t(($) => $.page.tabs[key])}
              </TabsTrigger>
            );
          })}
          {extraAccountTabs?.map((tab) => (
            <TabsTrigger key={tab.value} value={tab.value}>
              <tab.icon className="h-4 w-4" />
              {tab.label}
            </TabsTrigger>
          ))}

          {/* Workspace group */}
          <span className="px-2 pb-1 pt-4 text-caption font-medium text-muted-foreground truncate">
            {workspaceName ?? t(($) => $.page.workspace_fallback)}
          </span>
          {WORKSPACE_TAB_KEYS.map((key) => {
            const Icon = WORKSPACE_TAB_ICONS[key];
            return (
              <TabsTrigger key={key} value={WORKSPACE_TAB_VALUES[key]}>
                <Icon className="h-4 w-4" />
                {t(($) => $.page.tabs[key])}
              </TabsTrigger>
            );
          })}
        </TabsList>
      </div>

      {/* Right content */}
      <div className="min-w-0 flex-1 md:overflow-y-auto">
        <div className={`mx-auto w-full p-4 sm:p-6 md:p-8 ${activeTab === "issue-statuses"
              ? "max-w-5xl"
              : "max-w-3xl"}`}>
          <TabsContent value="profile"><AccountTab /></TabsContent>
          <TabsContent value="preferences"><PreferencesTab /></TabsContent>
          <TabsContent value="notifications"><NotificationsTab /></TabsContent>
          <TabsContent value="tokens"><TokensTab /></TabsContent>
          <TabsContent value="workspace"><WorkspaceTab /></TabsContent>
          <TabsContent value="repositories"><RepositoriesTab /></TabsContent>
          <TabsContent value="labs"><LabsTab /></TabsContent>
          <TabsContent value="issue-statuses"><IssueStatusesTab /></TabsContent>
          {extraAccountTabs?.map((tab) => (
            <TabsContent key={tab.value} value={tab.value}>{tab.content}</TabsContent>
          ))}
        </div>
      </div>
    </Tabs>
  );
}
