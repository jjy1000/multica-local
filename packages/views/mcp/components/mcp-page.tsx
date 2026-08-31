"use client";

import { PlugZap } from "lucide-react";
import { PageHeader } from "../../layout/page-header";
import { useT } from "../../i18n";
import { McpSyncTab } from "./mcp-sync-tab";

/**
 * MCP 管理 — first-class configure page (0.5.93). Originally a settings tab
 * (?tab=mcp-sync); promoted to the sidebar's 配置 group below Skills on user
 * feedback the same day. Strings stay in the settings.json locale namespace
 * (mcp_sync.*) — the component predates the move and the keys are already
 * shipped in all four locales.
 */
export function McpPage() {
  const { t } = useT("settings");

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageHeader className="h-auto min-h-12 flex-wrap justify-between gap-y-1.5 px-5 py-1.5 sm:py-0">
        <div className="flex min-w-0 items-center gap-2">
          <PlugZap className="h-4 w-4 shrink-0 text-muted-foreground" />
          <h1 className="truncate text-body font-medium">
            {t(($) => $.mcp_sync.title)}
          </h1>
        </div>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto w-full max-w-5xl p-4 sm:p-6">
          <McpSyncTab />
        </div>
      </div>
    </div>
  );
}
