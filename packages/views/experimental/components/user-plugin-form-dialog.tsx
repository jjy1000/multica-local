"use client";

import { useEffect, useState } from "react";
import { Label } from "@multica/ui/components/ui/label";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
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
import { api } from "@multica/core/api";
import type { UserPluginManifest, UserPluginResponse } from "@multica/core/types";
import { useT } from "../../i18n";

// 0.3.63 Labs sandbox — shared create/edit dialog for user plugins.
//
// Reused by the Settings → Labs section and the generic plugin shell view
// so both surfaces expose the same full CRUD form. In "create" mode the
// slug is editable and the request goes to api.createUserPlugin; in "edit"
// mode the slug is read-only (it is the resource key) and the request goes
// to api.updateUserPlugin. All network calls go through the typed api
// client — never a bare fetch (CLAUDE.md 0.3.30 contract).
//
// v1: hardcoded Chinese labels; i18n keys deferred (matches the sibling
// user-plugins-section.tsx / plugin-shell-view.tsx style).

// Slug contract mirrors the server-side userPluginSlugPattern:
// 2-64 chars, lowercase alphanumeric segments joined by single hyphens,
// no leading/trailing hyphen and NO underscore. The "user_" flag_key
// prefix is added server-side — callers must send the bare slug.
const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
const SLUG_MIN_LEN = 2;
const SLUG_MAX_LEN = 64;

type TriggerMode = "auto" | "issue_select";
type RuntimeKind = "none" | "inline" | "subprocess";
// 0.5.88 P4: mirrors the server-side interaction-model contract
// (ParseUserPluginContract). "auxiliary" is the default — unclassified
// user plugins never locked and auxiliary never locks either.
type InteractionModel = "assignee" | "auxiliary";

interface FormState {
  slug: string;
  titleEn: string;
  titleZh: string;
  descEn: string;
  descZh: string;
  triggerMode: TriggerMode;
  runtimeKind: RuntimeKind;
  interactionModel: InteractionModel;
  leaderAgent: string;
}

const EMPTY_FORM: FormState = {
  slug: "",
  titleEn: "",
  titleZh: "",
  descEn: "",
  descZh: "",
  triggerMode: "issue_select",
  runtimeKind: "none",
  interactionModel: "auxiliary",
  leaderAgent: "",
};

function formFromPlugin(plugin: UserPluginResponse): FormState {
  return {
    slug: plugin.slug,
    titleEn: plugin.title.en ?? "",
    titleZh: plugin.title.zh ?? "",
    descEn: plugin.description.en ?? "",
    descZh: plugin.description.zh ?? "",
    triggerMode: plugin.trigger_mode,
    runtimeKind: plugin.runtime_kind,
    interactionModel:
      plugin.manifest?.interaction_model === "assignee" ? "assignee" : "auxiliary",
    leaderAgent:
      typeof plugin.manifest?.leader_agent === "string" ? plugin.manifest.leader_agent : "",
  };
}

// buildManifest merges the interaction-model contract into the plugin's
// existing manifest, preserving every other key (capabilities, ui, …).
// auxiliary drops leader_agent (meaningless without the lock); the
// legacy capabilities.leader block is never touched.
function buildManifest(form: FormState, base?: UserPluginManifest): UserPluginManifest {
  const manifest: UserPluginManifest = { ...(base ?? {}) };
  manifest.interaction_model = form.interactionModel;
  if (form.interactionModel === "assignee") {
    manifest.leader_agent = form.leaderAgent.trim();
  } else {
    delete manifest.leader_agent;
  }
  return manifest;
}

export interface UserPluginFormDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** "create" shows an editable slug; "edit" locks the slug. */
  mode: "create" | "edit";
  /** The plugin being edited — required when mode === "edit". */
  plugin?: UserPluginResponse;
  /** Called after a successful create/update so callers can invalidate. */
  onSuccess: () => void;
}

export function UserPluginFormDialog({
  open,
  onOpenChange,
  mode,
  plugin,
  onSuccess,
}: UserPluginFormDialogProps) {
  const { t } = useT("experimental");
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [submitting, setSubmitting] = useState(false);

  const isEdit = mode === "edit";

  // Seed the form each time the dialog opens: blank for create, the
  // existing plugin's fields for edit.
  useEffect(() => {
    if (!open) return;
    setForm(isEdit && plugin ? formFromPlugin(plugin) : EMPTY_FORM);
  }, [open, isEdit, plugin]);

  async function handleSubmit() {
    const slug = form.slug.trim();
    const titleZh = form.titleZh.trim();

    if (!isEdit) {
      if (!slug || !titleZh) {
        toast.error("slug 和中文标题为必填项");
        return;
      }
      if (
        slug.length < SLUG_MIN_LEN ||
        slug.length > SLUG_MAX_LEN ||
        !SLUG_PATTERN.test(slug)
      ) {
        toast.error("slug 需为 2-64 位小写字母、数字或连字符（如 my-plugin），不含下划线");
        return;
      }
    } else if (!titleZh) {
      toast.error("中文标题为必填项");
      return;
    }

    // 0.5.88 P4: 独立工作型 requires a leader agent — the server
    // rejects the contract without it, so fail fast client-side.
    if (form.interactionModel === "assignee" && !form.leaderAgent.trim()) {
      toast.error(t(($) => $.user_plugins.form_leader_agent_required));
      return;
    }

    const manifest = buildManifest(form, isEdit ? plugin?.manifest : undefined);

    setSubmitting(true);
    try {
      if (isEdit && plugin) {
        await api.updateUserPlugin(plugin.slug, {
          title: { en: form.titleEn || titleZh, zh: titleZh },
          description: { en: form.descEn || form.descZh, zh: form.descZh || form.descEn },
          trigger_mode: form.triggerMode,
          runtime_kind: form.runtimeKind,
          manifest,
        });
        toast.success("插件已更新");
      } else {
        // Send the bare slug — the server adds the "user_" flag_key prefix.
        await api.createUserPlugin({
          slug,
          title: { en: form.titleEn || titleZh, zh: titleZh },
          description: { en: form.descEn || form.descZh, zh: form.descZh || form.descEn },
          trigger_mode: form.triggerMode,
          runtime_kind: form.runtimeKind,
          manifest,
        });
        toast.success("插件已创建");
      }
      onOpenChange(false);
      onSuccess();
    } catch (err) {
      toast.error(
        `${isEdit ? "更新" : "创建"}失败: ${err instanceof Error ? err.message : String(err)}`,
      );
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isEdit ? "编辑插件" : "创建插件"}</DialogTitle>
          <DialogDescription>
            {isEdit
              ? "修改插件的标题、描述、触发模式与运行时类型。slug 不可更改。"
              : "创建一个自定义实验性插件。创建后可在实验室列表中开关。"}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1">
            <Label htmlFor="up-slug" className="text-xs">
              {t(($) => $.user_plugins.form_slug_label)}
            </Label>
            <Input
              id="up-slug"
              placeholder="my-plugin"
              value={form.slug}
              disabled={isEdit}
              onChange={(e) => setForm((f) => ({ ...f, slug: e.target.value }))}
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1">
              <Label htmlFor="up-title-zh" className="text-xs">
                {t(($) => $.user_plugins.form_title_zh_label)}
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
                {t(($) => $.user_plugins.form_title_en_label)}
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
                {t(($) => $.user_plugins.form_desc_zh_label)}
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
                {t(($) => $.user_plugins.form_desc_en_label)}
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
              <Label className="text-xs">{t(($) => $.user_plugins.form_trigger_mode)}</Label>
              <Select
                value={form.triggerMode}
                onValueChange={(v) =>
                  setForm((f) => ({ ...f, triggerMode: v as TriggerMode }))
                }
              >
                <SelectTrigger size="sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="auto">{t(($) => $.user_plugins.form_trigger_auto)}</SelectItem>
                  <SelectItem value="issue_select">
                    {t(($) => $.user_plugins.form_trigger_issue)}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="space-y-1">
              <Label className="text-xs">{t(($) => $.user_plugins.form_runtime_kind)}</Label>
              <Select
                value={form.runtimeKind}
                onValueChange={(v) =>
                  setForm((f) => ({ ...f, runtimeKind: v as RuntimeKind }))
                }
              >
                <SelectTrigger size="sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">{t(($) => $.user_plugins.form_runtime_none)}</SelectItem>
                  <SelectItem value="inline">{t(($) => $.user_plugins.form_runtime_inline)}</SelectItem>
                  <SelectItem value="subprocess">
                    {t(($) => $.user_plugins.form_runtime_subprocess)}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
          <div className="space-y-1">
            <Label className="text-xs">{t(($) => $.user_plugins.form_interaction_model)}</Label>
            <Select
              value={form.interactionModel}
              onValueChange={(v) =>
                setForm((f) => ({ ...f, interactionModel: v as InteractionModel }))
              }
            >
              <SelectTrigger size="sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="auxiliary">
                  {t(($) => $.user_plugins.form_model_auxiliary)}
                </SelectItem>
                <SelectItem value="assignee">
                  {t(($) => $.user_plugins.form_model_assignee)}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          {form.interactionModel === "assignee" && (
            <div className="space-y-1">
              <Label htmlFor="up-leader-agent" className="text-xs">
                {t(($) => $.user_plugins.form_leader_agent_label)}
              </Label>
              <Input
                id="up-leader-agent"
                placeholder="my-lab-leader"
                value={form.leaderAgent}
                onChange={(e) => setForm((f) => ({ ...f, leaderAgent: e.target.value }))}
              />
              <p className="text-xs text-muted-foreground">
                {t(($) => $.user_plugins.form_leader_agent_hint)}
              </p>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            {t(($) => $.user_plugins.cancel)}
          </Button>
          <Button type="button" onClick={handleSubmit} disabled={submitting}>
            {submitting
              ? isEdit
                ? "更新中…"
                : "创建中…"
              : isEdit
                ? "保存"
                : "创建"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
