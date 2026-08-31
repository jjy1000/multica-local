"use client";

import { useMemo } from "react";
import { Loader2, Lock, RefreshCw, PlugZap } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { toast } from "sonner";
import { useWorkspaceId } from "@multica/core/hooks";
import { useMcpSyncSnapshot, useRefreshMcpSync } from "@multica/core/mcp-sync";
import type { McpSyncServer } from "@multica/core/types";
import { useLocale, useT } from "../../i18n";

// The seeded state row's last_synced_at defaults to the epoch; anything at
// or under epoch+1h reads as "never synced" rather than "synced at 1970".
const EPOCH_GRACE_MS = 3_600_000;

function formatWhen(value: string | undefined, locale: string): string {
  if (!value) return "—";
  const t = new Date(value).getTime();
  if (!Number.isFinite(t)) return "—";
  return new Date(t).toLocaleString(locale);
}

function serverType(def: Record<string, unknown>): string {
  const raw = def.type;
  if (typeof raw === "string" && raw !== "") return raw;
  return typeof def.url === "string" ? "sse" : "stdio";
}

function serverTarget(def: Record<string, unknown>): string {
  if (typeof def.url === "string" && def.url !== "") return def.url;
  const command = typeof def.command === "string" ? def.command : "";
  const args = Array.isArray(def.args) ? def.args.filter((a): a is string => typeof a === "string") : [];
  return [command, ...args].join(" ").trim();
}

function envKeyCount(def: Record<string, unknown>): number {
  const env = def.env;
  if (env === null || typeof env !== "object" || Array.isArray(env)) return 0;
  return Object.keys(env as Record<string, unknown>).length;
}

function headerKeyCount(def: Record<string, unknown>): number {
  const headers = def.headers;
  if (headers === null || typeof headers !== "object" || Array.isArray(headers)) return 0;
  return Object.keys(headers as Record<string, unknown>).length;
}

export function McpSyncTab() {
  const { t } = useT("settings");
  const locale = useLocale();
  const wsId = useWorkspaceId();
  const snapshotQuery = useMcpSyncSnapshot(wsId);
  const refresh = useRefreshMcpSync(wsId);

  const snapshot = snapshotQuery.data;
  const servers = useMemo(() => snapshot?.servers ?? [], [snapshot]);
  const liveCount = useMemo(
    () => servers.filter((s) => s.status === "synced").length,
    [servers],
  );
  const lastSyncedMs = snapshot ? new Date(snapshot.last_synced_at).getTime() : NaN;
  const neverSynced =
    !snapshot || !Number.isFinite(lastSyncedMs) || lastSyncedMs <= EPOCH_GRACE_MS;

  const handleRefresh = () => {
    refresh.mutate(undefined, {
      onSuccess: (data) => {
        if (data.last_error) {
          toast.warning(t(($) => $.mcp_sync.refresh_done_with_error));
        } else {
          toast.success(t(($) => $.mcp_sync.refresh_success, { count: liveCount }));
        }
      },
      onError: () => {
        toast.error(t(($) => $.mcp_sync.refresh_failed));
      },
    });
  };

  return (
    <div className="space-y-4">
      <div className="flex items-start justify-between gap-3">
        <div className="space-y-1">
          <h2 className="text-body font-semibold">{t(($) => $.mcp_sync.title)}</h2>
          <p className="text-caption text-muted-foreground">
            {t(($) => $.mcp_sync.description)}
          </p>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={handleRefresh}
          disabled={refresh.isPending}
          className="shrink-0"
        >
          {refresh.isPending ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <RefreshCw className="h-3.5 w-3.5" />
          )}
          {refresh.isPending
            ? t(($) => $.mcp_sync.refreshing)
            : t(($) => $.mcp_sync.refresh)}
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-caption text-muted-foreground">
        <span>
          {neverSynced
            ? t(($) => $.mcp_sync.never_synced)
            : t(($) => $.mcp_sync.last_synced, {
                time: formatWhen(snapshot?.last_synced_at, locale),
              })}
        </span>
        <span>{t(($) => $.mcp_sync.server_count, { count: liveCount })}</span>
      </div>

      {snapshot?.last_error ? (
        <Card className="border-destructive/40">
          <CardContent className="p-3 text-caption text-destructive">
            <p className="font-medium">{t(($) => $.mcp_sync.sync_error_label)}</p>
            <p className="mt-1 break-all font-mono">{snapshot.last_error}</p>
          </CardContent>
        </Card>
      ) : null}

      {snapshotQuery.isLoading ? (
        <div className="flex items-center gap-2 py-8 text-caption text-muted-foreground">
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
          {t(($) => $.mcp_sync.loading)}
        </div>
      ) : servers.length === 0 ? (
        <Card>
          <CardContent className="flex flex-col items-start gap-1 p-4">
            <p className="flex items-center gap-2 text-body font-medium">
              <PlugZap className="h-4 w-4 text-muted-foreground" />
              {t(($) => $.mcp_sync.empty_title)}
            </p>
            <p className="text-caption text-muted-foreground">
              {t(($) => $.mcp_sync.empty_hint)}
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="overflow-hidden rounded-lg border">
          <table className="w-full text-caption">
            <thead className="bg-muted/50 text-left text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">{t(($) => $.mcp_sync.column_name)}</th>
                <th className="px-3 py-2 font-medium">{t(($) => $.mcp_sync.column_type)}</th>
                <th className="px-3 py-2 font-medium">{t(($) => $.mcp_sync.column_target)}</th>
                <th className="px-3 py-2 font-medium">{t(($) => $.mcp_sync.column_secrets)}</th>
                <th className="px-3 py-2 font-medium">{t(($) => $.mcp_sync.column_status)}</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {servers.map((server) => (
                <McpSyncRow key={server.name} server={server} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      <p className="text-caption text-muted-foreground">
        <Lock className="mr-1 inline h-3 w-3" />
        {t(($) => $.mcp_sync.readonly_hint)}
      </p>
    </div>
  );
}

function McpSyncRow({ server }: { server: McpSyncServer }) {
  const { t } = useT("settings");
  const def = server.definition ?? {};
  const secretCount = envKeyCount(def) + headerKeyCount(def);
  const removed = server.status === "removed";

  return (
    <tr className={removed ? "text-muted-foreground" : undefined}>
      <td className="px-3 py-2 font-medium">{server.name}</td>
      <td className="px-3 py-2 font-mono">{serverType(def)}</td>
      <td className="max-w-[280px] truncate px-3 py-2 font-mono" title={serverTarget(def)}>
        {serverTarget(def) || "—"}
      </td>
      <td className="px-3 py-2">
        {secretCount > 0
          ? t(($) => $.mcp_sync.secrets_masked, { count: secretCount })
          : "—"}
      </td>
      <td className="px-3 py-2">
        {removed ? (
          <span className="rounded bg-muted px-1.5 py-0.5 text-muted-foreground">
            {t(($) => $.mcp_sync.status_removed)}
          </span>
        ) : (
          <span className="rounded bg-emerald-500/10 px-1.5 py-0.5 text-emerald-600 dark:text-emerald-400">
            {t(($) => $.mcp_sync.status_synced)}
          </span>
        )}
      </td>
    </tr>
  );
}
