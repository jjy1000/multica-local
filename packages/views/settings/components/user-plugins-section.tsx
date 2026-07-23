"use client";

import { useEffect, useState } from "react";
import { Plus, Puzzle, Trash2 } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from "@multica/ui/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@multica/ui/components/ui/select";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import { useUpdateExperimentalFlag } from "@multica/core/experimental";
import type { UserPluginResponse } from "@multica/core/types";

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

// ---- Create dialog form state ----

interface CreateFormState {
  slug: string;
  titleEn: string;
  titleZh: string;
  descEn: string;
  descZh: string;
  triggerMode: "auto" | "issue_select";
  runtimeKind: "none" | "inline" | "subprocess";
}

const EMPTY_FORM: CreateFormState = {
  slug: "",
  titleEn: "",
  titleZh: "",
  descEn: "",
  descZh: "",
  triggerMode: "issue_select",
  runtimeKind: "none",
};

export function UserPluginsSection() {
  const qc = useQueryClient();
  const updateFlag = useUpdateExperimentalFlag();

  const { data: plugins, isLoading } = useQuery({
    queryKey: userPluginKeys.all,
    queryFn: () => api.listUserPlugins(),
    staleTime: 30_000,
  });

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false);
  const [form, setForm] = useState<CreateFormState>(EMPTY_FORM);
  const [creating, setCreating] = useState(false);

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<UserPluginResponse | null>(null);
  const [deleting, setDeleting] = useState(false);

  // Reset form when dialog opens
  useEffect(() => {
    if (createOpen) setForm(EMPTY_FORM);
  }, [createOpen]);

  const invalidateAll = () => {
    qc.invalidateQueries({ queryKey: userPluginKeys.all });
    qc.invalidateQueries({ queryKey: ["experimental-flags"] });
  };

  async function handleCreate() {
    if (!form.slug.trim() || !form.titleZh.trim()) {
      toast.error("slug 和中文标题为必填项");
      return;
    }
    setCreating(true);
    try {
      // Auto-prefix with user_ if not already present
      const slug = form.slug.startsWith("user_") ? form.slug : `user_${form.slug}`;
      await api.createUserPlugin({
        slug,
        title: { en: form.titleEn || form.titleZh, zh: form.titleZh },
        description: { en: form.descEn || form.descZh, zh: form.descZh || form.descEn },
        trigger_mode: form.triggerMode,
        runtime_kind: form.runtimeKind,
      });
      toast.success("插件已创建");
      setCreateOpen(false);
      invalidateAll();
    } catch (err) {
      toast.error(`创建失败: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setCreating(false);
    }
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
          <h3 className="text-sm font-medium">用户插件</h3>
        </div>
        <Button type="button" size="sm" variant="outline" onClick={() => setCreateOpen(true)}>
          <Plus className="h-3 w-3" aria-hidden />
          创建插件
        </Button>
      </div>

      {activePlugins.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          暂无用户插件。点击「创建插件」添加自定义实验性功能。
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

      {/* Create dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>创建插件</DialogTitle>
            <DialogDescription>
              创建一个自定义实验性插件。创建后可在实验室列表中开关。
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3">
            <div className="space-y-1">
              <Label htmlFor="up-slug" className="text-xs">
                Slug（自动加 user_ 前缀）
              </Label>
              <Input
                id="up-slug"
                placeholder="my-plugin"
                value={form.slug}
                onChange={(e) => setForm((f) => ({ ...f, slug: e.target.value }))}
              />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <Label htmlFor="up-title-zh" className="text-xs">
                  标题（中文）*
                </Label>
                <Input
                  id="up-title-zh"
                  placeholder="我的插件"
                  value={form.titleZh}
                  onChange={(e) => setForm((f) => ({ ...f, titleZh: e.target.value }))}
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="up-title-en" className="text-xs">
                  标题（English）
                </Label>
                <Input
                  id="up-title-en"
                  placeholder="My Plugin"
                  value={form.titleEn}
                  onChange={(e) => setForm((f) => ({ ...f, titleEn: e.target.value }))}
                />
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <Label htmlFor="up-desc-zh" className="text-xs">
                  描述（中文）
                </Label>
                <Input
                  id="up-desc-zh"
                  placeholder="插件功能描述"
                  value={form.descZh}
                  onChange={(e) => setForm((f) => ({ ...f, descZh: e.target.value }))}
                />
              </div>
              <div className="space-y-1">
                <Label htmlFor="up-desc-en" className="text-xs">
                  描述（English）
                </Label>
                <Input
                  id="up-desc-en"
                  placeholder="Plugin description"
                  value={form.descEn}
                  onChange={(e) => setForm((f) => ({ ...f, descEn: e.target.value }))}
                />
              </div>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1">
                <Label className="text-xs">触发模式</Label>
                <Select
                  value={form.triggerMode}
                  onValueChange={(v) =>
                    setForm((f) => ({ ...f, triggerMode: v as CreateFormState["triggerMode"] }))
                  }
                >
                  <SelectTrigger size="sm">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="auto">自驱（auto）</SelectItem>
                    <SelectItem value="issue_select">任务绑定（issue_select）</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1">
                <Label className="text-xs">运行时类型</Label>
                <Select
                  value={form.runtimeKind}
                  onValueChange={(v) =>
                    setForm((f) => ({ ...f, runtimeKind: v as CreateFormState["runtimeKind"] }))
                  }
                >
                  <SelectTrigger size="sm">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="none">无运行时（none）</SelectItem>
                    <SelectItem value="inline">内联（inline）</SelectItem>
                    <SelectItem value="subprocess">子进程（subprocess）</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>
              取消
            </Button>
            <Button type="button" onClick={handleCreate} disabled={creating}>
              {creating ? "创建中…" : "创建"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={deleteTarget !== null} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>删除插件</DialogTitle>
            <DialogDescription>
              确定要删除插件「{deleteTarget?.title.zh || deleteTarget?.slug}」吗？此操作不可撤销。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setDeleteTarget(null)}>
              取消
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
