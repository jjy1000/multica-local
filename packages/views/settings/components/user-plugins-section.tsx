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
// All visible strings come from the experimental i18n namespace.

export const userPluginKeys = {
  all: ["user-plugins"] as const,
};

// ── F-008: global skill-injection ack ───────────────────────────────────────
// Enabling a plugin whose manifest declares capabilities.skills injects those
// skills into EVERY agent in the workspace (0.3.63 tool-lab contract). The
// user must explicitly acknowledge this once per plugin; the marker is a
// durable client-side preference (zero-migration, no new table).

// pluginInjectedSkillNames parses a plugin manifest's capabilities.skills
// list. Non-empty means enabling the plugin globally injects those skills.
export function pluginInjectedSkillNames(
  manifest: Record<string, unknown> | undefined,
): string[] {
  if (!manifest || typeof manifest !== "object") return [];
  const caps = manifest.capabilities as { skills?: unknown } | undefined;
  if (!caps || !Array.isArray(caps.skills)) return [];
  return caps.skills.filter((s): s is string => typeof s === "string" && s.length > 0);
}

const SKILLS_ACK_PREFIX = "multica.plugin_skills_ack.";

function isSkillsAcked(slug: string): boolean {
  try {
    return window.localStorage.getItem(SKILLS_ACK_PREFIX + slug) === "1";
  } catch {
    return false;
  }
}

function markSkillsAcked(slug: string): void {
  try {
    window.localStorage.setItem(SKILLS_ACK_PREFIX + slug, "1");
  } catch {
    // Best-effort: a blocked storage must not block enabling the plugin.
  }
}

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

  // F-008: plugin awaiting the global skill-injection ack (null = none).
  const [skillsAckTarget, setSkillsAckTarget] = useState<UserPluginResponse | null>(null);

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
      toast.success(t(($) => $.user_plugins.toast.delete_success));
      setDeleteTarget(null);
      invalidateAll();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      toast.error(t(($) => $.user_plugins.toast.delete_failed, { msg }));
    } finally {
      setDeleting(false);
    }
  }

  function handleToggle(plugin: UserPluginResponse, next: boolean) {
    // F-008: enabling a plugin that globally injects skills into every agent
    // requires a one-time explicit ack (persisted client-side).
    if (next && !isSkillsAcked(plugin.slug)) {
      const skills = pluginInjectedSkillNames(plugin.manifest);
      if (skills.length > 0) {
        setSkillsAckTarget(plugin);
        return;
      }
    }
    doToggle(plugin, next);
  }

  function doToggle(plugin: UserPluginResponse, next: boolean) {
    updateFlag.mutate(
      { key: plugin.flag_key, enabled: next },
      {
        onSuccess: () => invalidateAll(),
        onError: () =>
          toast.error(t(($) => $.user_plugins.toast.toggle_failed)),
      },
    );
  }

  function confirmSkillsAck() {
    if (!skillsAckTarget) return;
    markSkillsAcked(skillsAckTarget.slug);
    doToggle(skillsAckTarget, true);
    setSkillsAckTarget(null);
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
                      {plugin.trigger_mode === "auto"
                        ? t(($) => $.user_plugins.trigger_mode.auto)
                        : plugin.trigger_mode === "issue_select"
                          ? t(($) => $.user_plugins.trigger_mode.issue_select)
                          : plugin.trigger_mode}
                    </span>
                    <span className="inline-flex items-center rounded-md border border-muted-foreground/30 bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">
                      {plugin.runtime_kind === "none"
                        ? t(($) => $.user_plugins.runtime_kind.none)
                        : plugin.runtime_kind === "inline"
                          ? t(($) => $.user_plugins.runtime_kind.inline)
                          : plugin.runtime_kind === "subprocess"
                            ? t(($) => $.user_plugins.runtime_kind.subprocess)
                            : plugin.runtime_kind}
                    </span>
                    <span
                      className={`inline-flex items-center rounded-md px-2 py-0.5 text-[10px] ${
                        plugin.status === "active"
                          ? "border border-emerald-400/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                          : "border border-muted-foreground/30 bg-muted text-muted-foreground"
                      }`}
                    >
                      {plugin.status === "active"
                        ? t(($) => $.user_plugins.status.active)
                        : plugin.status === "disabled"
                          ? t(($) => $.user_plugins.status.disabled)
                          : plugin.status === "deleted"
                            ? t(($) => $.user_plugins.status.deleted)
                            : plugin.status}
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

      {/* F-008: global skill-injection ack dialog */}
      <Dialog
        open={skillsAckTarget !== null}
        onOpenChange={(open) => !open && setSkillsAckTarget(null)}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t(($) => $.user_plugins.skills_ack_title)}</DialogTitle>
            <DialogDescription>
              {t(($) => $.user_plugins.skills_ack_description, {
                skills: skillsAckTarget
                  ? pluginInjectedSkillNames(skillsAckTarget.manifest).join(", ")
                  : "",
              })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setSkillsAckTarget(null)}>
              {t(($) => $.user_plugins.skills_ack_cancel)}
            </Button>
            <Button type="button" onClick={confirmSkillsAck}>
              {t(($) => $.user_plugins.skills_ack_confirm)}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
