"use client";

import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2, Play, Loader2, Pencil, ArrowLeft } from "lucide-react";
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
import { useNavigation } from "../../navigation";
import type { UserPluginResponse } from "@multica/core/types";
import { useT } from "../../i18n";
import { ChatWindow } from "../../chat/components/chat-window";
import { ArtifactGallery } from "./artifact-gallery";
import { UserPluginFormDialog } from "./user-plugin-form-dialog";
import { IssueBreadcrumb } from "./issue-breadcrumb";
import { useSignedArtifactUrl } from "./use-signed-artifact-url";

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
  /** 0.5.81: issue id from a `?issue=` deep link (issue side → shell). */
  issueId?: string;
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
  { key: "artifacts", kind: "artifacts" },
  { key: "chat", kind: "chat" },
  { key: "settings", kind: "settings" },
];

// 0.5.105 (audit M1): the default tab / trigger-mode / runtime-kind
// labels used to be hardcoded Chinese in this shared (packages/views)
// file — every locale rendered 中文. They now resolve through the
// experimental locale at render time; manifest-authored tab labels
// (user content) still win verbatim. The label resolution lives inside
// PluginShellView where the `t` instance is in scope.

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


export function PluginShellView({ pluginSlug, issueId }: PluginShellViewProps) {
  const { t } = useT("experimental");
  const qc = useQueryClient();
  const updateFlag = useUpdateExperimentalFlag();
  const wsId = useCurrentWsIdPoll();
  const navigation = useNavigation();

  const [deleteOpen, setDeleteOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [running, setRunning] = useState(false);

  const { data: plugins, isLoading } = useQuery({
    queryKey: ["user-plugins"],
    queryFn: () => api.listUserPlugins(),
    staleTime: 30_000,
    // 0.5.60: while the slug is missing, auto-poll every 5s — the user
    // typically arrives here a few seconds before the lab-builder agent
    // finishes POSTing the plugin (audit: "no progress feedback during
    // lab creation"). Backs off to no auto-polling once the plugin
    // lands or after 12 retries (~60s) so a typo'd / deleted slug
    // doesn't burn a forever-loop.
    refetchInterval: (query) => {
      const list = query.state.data as UserPluginResponse[] | undefined;
      const found = (list ?? []).some((p) => p.slug === pluginSlug);
      if (found) return false;
      if ((query.state.errorUpdateCount ?? 0) > 12) return false;
      return 5_000;
    },
  });

  const plugin: UserPluginResponse | undefined = (plugins ?? []).find(
    (p) => p.slug === pluginSlug,
  );

  const tabs = parseTabs(plugin?.manifest);

  const tabLabelText = (tab: PluginTabDef): string => {
    if (tab.label) return tab.label;
    if (tab.kind === "artifacts") return t(($) => $.user_plugins.tab_artifacts);
    if (tab.kind === "chat") return t(($) => $.user_plugins.tab_chat);
    if (tab.kind === "settings") return t(($) => $.user_plugins.tab_settings);
    return tab.key;
  };

  function handleToggle(next: boolean) {
    if (!plugin) return;
    updateFlag.mutate(
      { key: plugin.flag_key, enabled: next },
      {
        onSuccess: () => {
          qc.invalidateQueries({ queryKey: ["user-plugins"] });
          qc.invalidateQueries({ queryKey: ["experimental-flags"] });
        },
        onError: () => toast.error(t(($) => $.user_plugins.toast.toggle_failed)),
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
          t(($) => $.user_plugins.run_ok, {
            code: res.exit_code,
            count: res.artifacts.length,
          }),
        );
      } else {
        toast.error(
          res.status === "timeout"
            ? t(($) => $.user_plugins.run_timeout, { code: res.exit_code })
            : t(($) => $.user_plugins.run_failed, { code: res.exit_code }),
        );
      }
      // Re-render the ArtifactGallery with any files the run produced.
      qc.invalidateQueries({ queryKey: ["user-plugin-artifacts", plugin.slug] });
    } catch (err) {
      toast.error(
        t(($) => $.user_plugins.run_error, {
          msg: err instanceof Error ? err.message : String(err),
        }),
      );
    } finally {
      setRunning(false);
    }
  }

  async function handleDelete() {
    if (!plugin) return;
    setDeleting(true);
    try {
      await api.deleteUserPlugin(plugin.slug);
      toast.success(
        t(($) => $.user_plugins.toast.delete_success, { count: res.reclaim.length }),
      );
      setDeleteOpen(false);
      qc.invalidateQueries({ queryKey: ["user-plugins"] });
      qc.invalidateQueries({ queryKey: ["experimental-flags"] });
    } catch (err) {
      toast.error(
        t(($) => $.user_plugins.toast.delete_failed, {
          msg: err instanceof Error ? err.message : String(err),
        }),
      );
    } finally {
      setDeleting(false);
    }
  }

  if (isLoading) {
    return <Skeleton className="h-40 w-full" />;
  }

  if (!plugin) {
    // 0.5.60: while the lab-builder agent is creating this plugin, the
    // user lands here with a missing slug — surface that as a live
    // "creating" affordance with a spinner + auto-retry (refetchInterval
    // above) + an explicit Back so the user is never trapped staring at
    // a one-line "not found". After 12 polls (~60s) the refetch backs
    // off and we fall back to the static message — so a typo'd or
    // deleted slug doesn't burn a forever-loop.
    return (
      <div className="flex max-w-md flex-col items-start gap-3 rounded-lg border border-border bg-card p-5 text-sm text-card-foreground">
        <div className="flex items-center gap-2 text-foreground">
          <Loader2 className="size-4 animate-spin text-muted-foreground" aria-hidden />
          <span className="font-medium">{t(($) => $.user_plugins.creating_title)}</span>
        </div>
        <p className="text-xs text-muted-foreground">
          {t(($) => $.user_plugins.creating_desc, { slug: pluginSlug })}
        </p>
        <div className="flex items-center gap-2">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => {
              qc.invalidateQueries({ queryKey: ["user-plugins"] });
            }}
          >
            {t(($) => $.lab_output_panel.retry)}
          </Button>
          {/* 0.5.81: keep the existing navigation.back() button for the
              bare `/experimental/plugin/<slug>` visit; the bound-from-
              task affordance lives at the top of the active view below
              (rendered unconditionally in the bound branch). */}
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => navigation.back()}
          >
            <ArrowLeft className="size-3.5" aria-hidden />
            {t(($) => $.user_plugins.back_to_labs)}
          </Button>
        </div>
      </div>
    );
  }

  const isActive = plugin.status === "active";

  return (
    <div className="space-y-4">
      {/* 0.5.81: shared back-link strip (IssueBreadcrumb) replaces the
          prior truncated-id chip — grounds the shell in the task the user
          arrived from (IssueLabsSection / create-issue redirect pass
          ?issue=<id>) AND provides a one-click jump back to that task
          via useNavigation().push, matching the other 7 lab surfaces. */}
      {issueId ? <IssueBreadcrumb issueId={issueId} /> : null}
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
              {isActive
                ? t(($) => $.user_plugins.status.active)
                : t(($) => $.user_plugins.status.disabled)}
            </span>
          </div>
          {plugin.runtime_kind !== "none" ? (
            <Button
              type="button"
              size="sm"
              onClick={handleRun}
              disabled={!isActive || running}
              title={
                isActive
                  ? t(($) => $.user_plugins.run_title_active)
                  : t(($) => $.user_plugins.run_title_disabled)
              }
            >
              {running ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden />
              ) : (
                <Play className="h-3.5 w-3.5" aria-hidden />
              )}
              {t(($) => $.user_plugins.run)}
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
              {tabLabelText(tab)}
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
          the edit + delete dialogs are hoisted here so they survive tab
          switches. */}
      <UserPluginFormDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        mode="edit"
        plugin={plugin}
        onSuccess={() => {
          qc.invalidateQueries({ queryKey: ["user-plugins"] });
          qc.invalidateQueries({ queryKey: ["experimental-flags"] });
        }}
      />
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
            <p className="text-sm text-muted-foreground">
              {t(($) => $.user_plugins.chat_no_workspace)}
            </p>
          );
        }
        return <ChatWindow wsId={chatWsId} />;

      case "table":
        return (
          <div className="rounded-lg border border-dashed border-border p-6 text-center text-sm text-muted-foreground">
            {t(($) => $.user_plugins.table_placeholder)}
            {tab.data_source ? (
              <p className="mt-1 font-mono text-xs">
                {t(($) => $.user_plugins.table_data_source, { source: tab.data_source })}
              </p>
            ) : null}
          </div>
        );

      case "iframe":
        return <PluginIframeTab tab={tab} />;

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
            onEditClick={() => setEditOpen(true)}
            onDeleteClick={() => setDeleteOpen(true)}
          />
        );

      default:
        return (
          <p className="text-sm text-muted-foreground">
            {t(($) => $.user_plugins.unknown_tab_kind)}
          </p>
        );
    }
  }
}

// ---- iframe tab ----

// Matches a file-backed artifact raw path so the iframe tab can mint a signed
// URL (an <iframe> cannot send the Bearer header). Slug and artifact IDs are
// path-safe (lowercase alphanumeric + hyphen / hex), so a bare segment regex
// is sufficient.
const ARTIFACT_RAW_PATH_RE = /^\/api\/user-plugins\/([^/]+)\/artifacts\/([^/]+)\/raw$/;

function PluginIframeTab({ tab }: { tab: PluginTabDef }) {
  const { t } = useT("experimental");
  const match = tab.src ? ARTIFACT_RAW_PATH_RE.exec(tab.src) : null;
  const signedUrl = useSignedArtifactUrl(
    tab.src,
    match?.[1] ?? "",
    match?.[2] ?? "",
  );

  if (!signedUrl) {
    return (
      <p className="text-sm text-muted-foreground">
        {t(($) => $.user_plugins.iframe_no_src)}
      </p>
    );
  }

  return (
    <iframe
      title={tab.key}
      src={signedUrl}
      // Plugin-authored content is untrusted: sandbox without
      // allow-same-origin so scripts run in an opaque origin and cannot
      // reach the app's localStorage/cookies or call the API with the
      // user's credentials (mirrors the html artifact sandbox rules in
      // CLAUDE.md).
      sandbox="allow-scripts"
      referrerPolicy="no-referrer"
      className="h-[480px] w-full rounded-lg border border-border bg-background"
    />
  );
}

// ---- settings tab ----

interface SettingsTabProps {
  plugin: UserPluginResponse;
  onToggle: (next: boolean) => void;
  togglePending: boolean;
  onEditClick: () => void;
  onDeleteClick: () => void;
}

function SettingsTab({ plugin, onToggle, togglePending, onEditClick, onDeleteClick }: SettingsTabProps) {
  const { t } = useT("experimental");
  // 0.5.105 (audit M1): row labels + enum values resolve through the
  // locale (the form_* / trigger_* keys are shared with the create form);
  // created_at renders in the runtime locale instead of hardcoded zh-CN.
  const rows: Array<[string, string]> = [
    ["Slug", plugin.slug],
    [
      t(($) => $.user_plugins.form_trigger_mode),
      plugin.trigger_mode === "issue_select"
        ? t(($) => $.user_plugins.trigger_issue)
        : t(($) => $.user_plugins.trigger_auto),
    ],
    [
      t(($) => $.user_plugins.form_runtime_kind),
      plugin.runtime_kind === "none"
        ? t(($) => $.user_plugins.form_runtime_none)
        : plugin.runtime_kind === "inline"
          ? t(($) => $.user_plugins.form_runtime_inline)
          : t(($) => $.user_plugins.form_runtime_subprocess),
    ],
    [t(($) => $.user_plugins.settings_created_at), new Date(plugin.created_at).toLocaleString()],
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
              <Label className="text-sm font-medium">{t(($) => $.user_plugins.enable_plugin)}</Label>
              <p className="text-xs text-muted-foreground">
                {t(($) => $.user_plugins.enable_plugin_hint)}
              </p>
            </div>
            <Switch
              checked={plugin.status === "active"}
              onCheckedChange={onToggle}
              disabled={togglePending}
            />
          </div>

          <div className="flex justify-end gap-2 border-t border-border pt-4">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onEditClick}
            >
              <Pencil className="h-3.5 w-3.5" aria-hidden />
              {t(($) => $.user_plugins.edit)}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={onDeleteClick}
            >
              <Trash2 className="h-3.5 w-3.5" aria-hidden />
              {t(($) => $.user_plugins.delete)}
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
  const { t } = useT("experimental");
  return (
    <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(($) => $.user_plugins.delete_title)}</DialogTitle>
          <DialogDescription>
            {t(($) => $.user_plugins.delete_description, {
              name: plugin.title.zh || plugin.slug,
            })}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => setDeleteOpen(false)}>
            {t(($) => $.user_plugins.cancel)}
          </Button>
          <Button type="button" variant="destructive" onClick={onDelete} disabled={deleting}>
            {deleting
              ? t(($) => $.user_plugins.delete_button_deleting)
              : t(($) => $.user_plugins.delete_button)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
