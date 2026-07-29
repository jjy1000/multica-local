"use client";

import { useState } from "react";
import { Pencil, Plus, Puzzle, Trash2 } from "lucide-react";
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
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useUpdateExperimentalFlag } from "@multica/core/experimental";
import type { UserPluginResponse } from "@multica/core/types";
import { UserPluginFormDialog } from "../../experimental/components/user-plugin-form-dialog";
import { useT } from "../../i18n";

// 0.3.60 Labs sandbox — user-created plugin management section.
// Rendered below the developer catalog flags in the Labs tab.
// v1: hardcoded Chinese labels; i18n keys deferred.

const userPluginKeys = {
  all: ["user-plugins"] as const,
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

const STATUS_LABELS: Record<string, string> = {
  active: "启用",
  disabled: "停用",
  deleted: "已删除",
};

export function UserPluginsSection() {
  const { t } = useT("experimental");
  const qc = useQueryClient();
  const updateFlag = useUpdateExperimentalFlag();

  const { data: plugins, isLoading } = useQuery({
    queryKey: userPluginKeys.all,
    queryFn: () => api.listUserPlugins(),
    staleTime: 30_000,
  });

  // Create / edit dialog. `formMode` drives the shared form dialog and
  // `editTarget` is the plugin being edited (null in create mode).
  const [formOpen, setFormOpen] = useState(false);
  const [formMode, setFormMode] = useState<"create" | "edit">("create");
  const [editTarget, setEditTarget] = useState<UserPluginResponse | null>(null);

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<UserPluginResponse | null>(null);
  const [deleting, setDeleting] = useState(false);

  const invalidateAll = () => {
    qc.invalidateQueries({ queryKey: userPluginKeys.all });
    qc.invalidateQueries({ queryKey: ["experimental-flags"] });
  };

  function openCreate() {
    setFormMode("create");
    setEditTarget(null);
    setFormOpen(true);
  }

  function openEdit(plugin: UserPluginResponse) {
    setFormMode("edit");
    setEditTarget(plugin);
    setFormOpen(true);
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await api.deleteUserPlugin(deleteTarget.slug);
      toast.success("插件已删除");
      setDeleteTarget(null);
      invalidateAll();
    } catch (err) {
      toast.error(`删除失败: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setDeleting(false);
    }
  }

  function handleToggle(plugin: UserPluginResponse, next: boolean) {
    updateFlag.mutate(
      { key: plugin.flag_key, enabled: next },
      {
        onSuccess: () => invalidateAll(),
        onError: () => toast.error("切换失败"),
      },
    );
  }

  const activePlugins = (plugins ?? []).filter((p) => p.status !== "deleted");

  if (isLoading) {
    return <Skeleton className="h-20 w-full" />;
  }

  return (
    <div className="space-y-4">
      {/* Section header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Puzzle className="h-4 w-4 text-muted-foreground" aria-hidden />
          <h3 className="text-sm font-medium">{t(($) => $.user_plugins.title)}</h3>
        </div>
        <Button type="button" size="sm" variant="outline" onClick={openCreate}>
          <Plus className="h-3 w-3" aria-hidden />
          {t(($) => $.user_plugins.create)}
        </Button>
      </div>

      {activePlugins.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          {t(($) => $.user_plugins.empty_hint)}
        </p>
      ) : (
        activePlugins.map((plugin) => (
          <Card key={plugin.id}>
            <CardContent>
              <div className="flex items-start justify-between gap-4">
                <div className="space-y-1">
                  <div className="flex items-center gap-2">
                    <Label className="text-sm font-medium">
                      {plugin.title.zh || plugin.title.en}
                    </Label>
                    <span className="inline-flex items-center rounded-md border border-blue-400/40 bg-blue-500/10 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-blue-700 dark:text-blue-300">
                      {TRIGGER_MODE_LABELS[plugin.trigger_mode] ?? plugin.trigger_mode}
                    </span>
                    <span className="inline-flex items-center rounded-md border border-muted-foreground/30 bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">
                      {RUNTIME_KIND_LABELS[plugin.runtime_kind] ?? plugin.runtime_kind}
                    </span>
                    <span
                      className={`inline-flex items-center rounded-md px-2 py-0.5 text-[10px] ${
                        plugin.status === "active"
                          ? "border border-emerald-400/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                          : "border border-muted-foreground/30 bg-muted text-muted-foreground"
                      }`}
                    >
                      {STATUS_LABELS[plugin.status] ?? plugin.status}
                    </span>
                  </div>
                  <p className="text-sm text-muted-foreground">
                    {plugin.description.zh || plugin.description.en}
                  </p>
                  <p className="text-xs text-muted-foreground/60 font-mono">{plugin.slug}</p>
                </div>
                <div className="flex items-center gap-2">
                  <Switch
                    checked={plugin.status === "active"}
                    onCheckedChange={(next) => handleToggle(plugin, next)}
                    disabled={updateFlag.isPending}
                  />
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    onClick={() => openEdit(plugin)}
                  >
                    <Pencil className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                  <Button
                    type="button"
                    size="sm"
                    variant="ghost"
                    className="text-destructive hover:text-destructive"
                    onClick={() => setDeleteTarget(plugin)}
                  >
                    <Trash2 className="h-3.5 w-3.5" aria-hidden />
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>
        ))
      )}

      {/* Create / edit dialog (shared form) */}
      <UserPluginFormDialog
        open={formOpen}
        onOpenChange={setFormOpen}
        mode={formMode}
        plugin={editTarget ?? undefined}
        onSuccess={invalidateAll}
      />

      {/* Delete confirmation dialog */}
      <Dialog open={deleteTarget !== null} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.user_plugins.delete_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.user_plugins.delete_description, {
                name: deleteTarget?.title.zh || deleteTarget?.slug || "",
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setDeleteTarget(null)}>
              {t(($) => $.user_plugins.cancel)}
            </Button>
            <Button type="button" variant="destructive" onClick={handleDelete} disabled={deleting}>
              {deleting ? "删除中…" : "删除"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
