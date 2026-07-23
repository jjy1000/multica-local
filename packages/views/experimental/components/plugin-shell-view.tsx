"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Puzzle, Trash2, Play, Loader2 } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@multica/ui/components/ui/tabs";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useUpdateExperimentalFlag } from "@multica/core/experimental";
import { getCurrentWsId } from "@multica/core/platform";
import type { UserPluginResponse } from "@multica/core/types";
import { ChatWindow } from "../../chat/components/chat-window";
import { ArtifactGallery } from "./artifact-gallery";

// 0.3.60 Labs sandbox — generic plugin shell view.
//
// Replaces per-plugin hand-written views: a single tabbed surface whose
// tabs are declared in the plugin manifest (`manifest.ui.tabs`). When a
// manifest has no ui block, a default artifacts / chat / settings set is
// rendered so every plugin is usable out of the box.
//
// All network calls go through api.rawRequest or the typed api client —
// never a bare fetch (CLAUDE.md 0.3.30 contract).
//
// v1: hardcoded Chinese labels; i18n keys deferred.

export interface PluginShellViewProps {
  pluginSlug: string;
}

/** One tab declared in `manifest.ui.tabs`. */
interface PluginTabDef {
  key: string;
  kind: "artifacts" | "chat" | "table" | "iframe" | "code" | "settings";
  label?: string;
  /** iframe src (relative to the API host) — kind === "iframe". */
  src?: string;
  /** data source URL — kind === "table" (placeholder in v1). */
  data_source?: string;
  /** inline code — kind === "code". */
  code?: string;
}

const DEFAULT_TABS: PluginTabDef[] = [
  { key: "artifacts", kind: "artifacts", label: "产物" },
  { key: "chat", kind: "chat", label: "对话" },
  { key: "settings", kind: "settings", label: "设置" },
];

const DEFAULT_TAB_LABELS: Record<string, string> = {
  artifacts: "产物",
  chat: "对话",
  settings: "设置",
};

const TRIGGER_MODE_LABELS: Record<string, string> = {
  auto: "自驱",
  issue_select: "任务绑定",
};

const RUNTIME_KIND_LABELS: Record<string, string> = {
  none: "无运行时",
  inline: "内联",
  subprocess: "子进程",
};

/**
 * Poll `getCurrentWsId()` every 500 ms so the pre-workspace chat tab can
 * follow the user's currently active workspace without unmounting — same
 * pattern the Claude Lab Chat tab uses (CLAUDE.md, Pre-workspace surfaces).
 */
function useCurrentWsIdPoll(): string | null {
  const [wsId, setWsId] = useState<string | null>(() => getCurrentWsId());
  useEffect(() => {
    const id = setInterval(() => {
      setWsId(getCurrentWsId());
    }, 500);
    return () => clearInterval(id);
  }, []);
  return wsId;
}

function parseTabs(manifest: Record<string, unknown> | undefined): PluginTabDef[] {
  const ui = manifest?.ui as { tabs?: unknown } | undefined;
  if (Array.isArray(ui?.tabs) && ui.tabs.length > 0) {
    return ui.tabs.filter(
      (t): t is PluginTabDef =>
        typeof t === "object" && t !== null && typeof (t as PluginTabDef).key === "string",
    );
  }
  return DEFAULT_TABS;
}

function tabLabel(tab: PluginTabDef): string {
  return tab.label ?? DEFAULT_TAB_LABELS[tab.key] ?? tab.key;
}

export function PluginShellView({ pluginSlug }: PluginShellViewProps) {
  const qc = useQueryClient();
  const updateFlag = useUpdateExperimentalFlag();
  const wsId = useCurrentWsIdPoll();

  const [deleteOpen, setDeleteOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [running, setRunning] = useState(false);

  const { data: plugins, isLoading } = useQuery({
    queryKey: ["user-plugins"],
    queryFn: () => api.listUserPlugins(),
    staleTime: 30_000,
  });

  const plugin: UserPluginResponse | undefined = (plugins ?? []).find(
    (p) => p.slug === pluginSlug,
  );

  const tabs = parseTabs(plugin?.manifest);

  function handleToggle(next: boolean) {
    if (!plugin) return;
    updateFlag.mutate(
      { key: plugin.flag_key, enabled: next },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["user-plugins"] });
          qc.invalidateQueries({ queryKey: ["experimental-flags"] });
        },
        onError: () => toast.error("切换失败"),
      },
    );
  }

  async function handleRun() {
    if (!plugin) return;
    setRunning(true);
    try {
      const res = await api.runUserPlugin(plugin.slug);
      if (res.status === "completed") {
        toast.success(
          `运行完成 · 退出码 ${res.exit_code} · 产物 ${res.artifacts.length}`,
        );
      } else {
        toast.error(
          `运行${res.status === "timeout" ? "超时" : "失败"} · 退出码 ${res.exit_code}`,
        );
      }
      // Re-render the ArtifactGallery with any files the run produced.
      qc.invalidateQueries({ queryKey: ["user-plugin-artifacts", plugin.slug] });
    } catch (err) {
      toast.error(`运行失败: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setRunning(false);
    }
  }

  async function handleDelete() {
    if (!plugin) return;
    setDeleting(true);
    try {
      await api.deleteUserPlugin(plugin.slug);
      toast.success("插件已删除");
      setDeleteOpen(false);
      qc.invalidateQueries({ queryKey: ["user-plugins"] });
      qc.invalidateQueries({ queryKey: ["experimental-flags"] });
    } catch (err) {
      toast.error(`删除失败: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setDeleting(false);
    }
  }

  if (isLoading) {
    return <Skeleton className="h-40 w-full" />;
  }

  if (!plugin) {
    return (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Puzzle className="h-4 w-4" aria-hidden />
        未找到插件「{pluginSlug}」
      </div>
    );
  }

  const isActive = plugin.status === "active";

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="space-y-1">
        <div className="flex items-center justify-between gap-2">
          <div className="flex items-center gap-2">
            <h2 className="text-lg font-semibold text-foreground">
              {plugin.title.zh || plugin.title.en}
            </h2>
            <span
              className={`inline-flex items-center rounded-md px-2 py-0.5 text-[10px] ${
                isActive
                  ? "border border-emerald-400/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                  : "border border-muted-foreground/30 bg-muted text-muted-foreground"
              }`}
            >
              {isActive ? "启用" : "停用"}
            </span>
          </div>
          {plugin.runtime_kind !== "none" ? (
            <Button
              type="button"
              size="sm"
              onClick={handleRun}
              disabled={!isActive || running}
              title={isActive ? "运行插件运行时" : "启用插件后可运行"}
            >
              {running ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
              ) : (
                <Play className="h-3.5 w-3.5" aria-hidden />
              )}
              运行
            </Button>
          ) : null}
        </div>
        <p className="text-sm text-muted-foreground">
          {plugin.description.zh || plugin.description.en}
        </p>
      </div>

      {/* Tabs */}
      <Tabs defaultValue={tabs[0]?.key ?? "artifacts"}>
        <TabsList>
          {tabs.map((tab) => (
            <TabsTrigger key={tab.key} value={tab.key}>
              {tabLabel(tab)}
            </TabsTrigger>
          ))}
        </TabsList>

        {tabs.map((tab) => (
          <TabsContent key={tab.key} value={tab.key} className="mt-4">
            {renderTabContent(tab, plugin, pluginSlug, wsId)}
          </TabsContent>
        ))}
      </Tabs>

      {/* Settings controls live in the settings tab via renderTabContent;
          delete confirmation is hoisted here so it survives tab switches. */}
      <SettingsDialogs
        plugin={plugin}
        deleteOpen={deleteOpen}
        setDeleteOpen={setDeleteOpen}
        onDelete={handleDelete}
        deleting={deleting}
      />
    </div>
  );

  // ---- tab content dispatcher ----

  function renderTabContent(
    tab: PluginTabDef,
    p: UserPluginResponse,
    slug: string,
    chatWsId: string | null,
  ) {
    switch (tab.kind) {
      case "artifacts":
        return <ArtifactGallery pluginSlug={slug} />;

      case "chat":
        if (!chatWsId) {
          return (
            <p className="text-sm text-muted-foreground">请先选择或创建一个工作区。</p>
          );
        }
        return <ChatWindow wsId={chatWsId} />;

      case "table":
        return (
          <div className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
            表格视图（v1 占位）
            {tab.data_source ? (
              <p className="mt-1 font-mono text-xs">data_source: {tab.data_source}</p>
            ) : null}
          </div>
        );

      case "iframe": {
        const src = tab.src ? `${api.getBaseUrl()}${tab.src}` : undefined;
        if (!src) {
          return <p className="text-sm text-muted-foreground">iframe 缺少 src。</p>;
        }
        return (
          <iframe
            title={tab.key}
            src={src}
            className="h-[480px] w-full rounded-lg border border-border bg-background"
          />
        );
      }

      case "code":
        return (
          <pre className="overflow-x-auto rounded-lg border border-border bg-muted/40 p-3 text-xs">
            <code className="font-mono text-foreground">{tab.code ?? ""}</code>
          </pre>
        );

      case "settings":
        return (
          <SettingsTab
            plugin={p}
            onToggle={handleToggle}
            togglePending={updateFlag.isPending}
            onDeleteClick={() => setDeleteOpen(true)}
          />
        );

      default:
        return <p className="text-sm text-muted-foreground">未知的标签类型。</p>;
    }
  }
}

// ---- settings tab ----

interface SettingsTabProps {
  plugin: UserPluginResponse;
  onToggle: (next: boolean) => void;
  togglePending: boolean;
  onDeleteClick: () => void;
}

function SettingsTab({ plugin, onToggle, togglePending, onDeleteClick }: SettingsTabProps) {
  const rows: Array<[string, string]> = [
    ["Slug", plugin.slug],
    ["触发模式", TRIGGER_MODE_LABELS[plugin.trigger_mode] ?? plugin.trigger_mode],
    ["运行时类型", RUNTIME_KIND_LABELS[plugin.runtime_kind] ?? plugin.runtime_kind],
    ["创建时间", new Date(plugin.created_at).toLocaleString("zh-CN")],
  ];

  return (
    <Card>
      <CardContent>
        <div className="space-y-4">
          <dl className="space-y-2">
            {rows.map(([k, v]) => (
              <div key={k} className="flex items-center justify-between gap-4">
                <dt className="text-sm text-muted-foreground">{k}</dt>
                <dd className="text-sm font-medium text-foreground">{v}</dd>
              </div>
            ))}
          </dl>

          <div className="flex items-center justify-between border-t border-border pt-4">
            <div className="space-y-0.5">
              <Label className="text-sm font-medium">启用插件</Label>
              <p className="text-xs text-muted-foreground">停用后插件的侧边栏入口与视图将隐藏。</p>
            </div>
            <Switch
              checked={plugin.status === "active"}
              onCheckedChange={onToggle}
              disabled={togglePending}
            />
          </div>

          <div className="flex justify-end border-t border-border pt-4">
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={onDeleteClick}
            >
              <Trash2 className="h-3.5 w-3.5" aria-hidden />
              删除插件
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

// ---- delete confirmation ----

interface SettingsDialogsProps {
  plugin: UserPluginResponse;
  deleteOpen: boolean;
  setDeleteOpen: (open: boolean) => void;
  onDelete: () => void;
  deleting: boolean;
}

function SettingsDialogs({
  plugin,
  deleteOpen,
  setDeleteOpen,
  onDelete,
  deleting,
}: SettingsDialogsProps) {
  return (
    <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>删除插件</DialogTitle>
          <DialogDescription>
            确定要删除插件「{plugin.title.zh || plugin.slug}」吗？此操作不可撤销。
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => setDeleteOpen(false)}>
            取消
          </Button>
          <Button type="button" variant="destructive" onClick={onDelete} disabled={deleting}>
            {deleting ? "删除中…" : "删除"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
