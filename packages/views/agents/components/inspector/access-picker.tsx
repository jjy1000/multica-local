"use client";

import { useMemo, useState } from "react";
import { Lock, Users, X } from "lucide-react";
import type {
  Agent,
  InvocationTarget,
  MemberWithUser,
  PermissionMode,
} from "@multica/core/types";
import {
  Button,
} from "@multica/ui/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import { ActorAvatar } from "../../../common/actor-avatar";
import { CHIP_CLASS } from "./chip";
import { useT } from "../../../i18n";

/**
 * Inline access picker for the agent inspector (MUL-3963 port).
 *
 * Surfaces the new permission_mode axis (private | public_to) plus the
 * allow-list (invocation_targets). Owner-only editable; non-owners see
 * a static read-only label + member count. Mirrors the runtime / model
 * picker pattern: trigger chip + PropertyPicker-style popover, except
 * the editing surface is rich enough to warrant a modal dialog (similar
 * to the inspector's DescriptionEditor) — three radios, a workspace
 * toggle, and a member list are too cramped inside the 14rem popover
 * the other pickers use.
 */
export function AccessPicker({
  agent,
  members,
  workspaceId,
  canEdit = true,
  onChange,
}: {
  agent: Agent;
  members: MemberWithUser[];
  /** Used to construct / match the workspace target row. */
  workspaceId: string;
  /** When false, render a static read-only label and skip the modal. */
  canEdit?: boolean;
  onChange: (next: {
    permission_mode: PermissionMode;
    invocation_targets: InvocationTarget[];
  }) => Promise<void>;
}) {
  const { t } = useT("agents");
  const [open, setOpen] = useState(false);

  const mode = agent.permission_mode ?? "private";

  const modeLabel =
    mode === "private"
      ? t(($) => $.access_picker.permission_mode.private)
      : t(($) => $.access_picker.permission_mode.public_to);
  const triggerTitle = t(($) => $.access_picker.trigger_tooltip, {
    value: modeLabel,
  });

  if (!canEdit) {
    return (
      <span
        className="inline-flex min-w-0 items-center gap-1.5 px-1.5 py-0.5 text-caption text-muted-foreground"
        title={triggerTitle}
      >
        {mode === "private" ? (
          <Lock className="h-3 w-3 shrink-0" />
        ) : (
          <Users className="h-3 w-3 shrink-0" />
        )}
        <span className="min-w-0 truncate font-mono">{modeLabel}</span>
      </span>
    );
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className={CHIP_CLASS}
        aria-label={triggerTitle}
      >
        {mode === "private" ? (
          <Lock className="h-3 w-3 shrink-0 text-muted-foreground" />
        ) : (
          <Users className="h-3 w-3 shrink-0 text-muted-foreground" />
        )}
        <span className="min-w-0 truncate font-mono">{modeLabel}</span>
      </button>
      {open && (
        <AccessDialog
          agent={agent}
          members={members}
          workspaceId={workspaceId}
          onClose={() => setOpen(false)}
          onSave={async (next) => {
            try {
              await onChange(next);
              setOpen(false);
            } catch {
              // parent surfaces the toast — keep the dialog open so the
              // user can retry or cancel
            }
          }}
        />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Editor body — mounted only while the dialog is open so the draft state
// is initialised from `agent` at mount time and never reset by an
// external update mid-edit. Same React-recommended "key / mount" pattern
// the inspector's DescriptionEditor uses.
// ---------------------------------------------------------------------------

interface AccessDialogProps {
  agent: Agent;
  members: MemberWithUser[];
  workspaceId: string;
  onClose: () => void;
  onSave: (next: {
    permission_mode: PermissionMode;
    invocation_targets: InvocationTarget[];
  }) => Promise<void>;
}

function AccessDialog({
  agent,
  members,
  workspaceId,
  onClose,
  onSave,
}: AccessDialogProps) {
  const { t } = useT("agents");
  // Mount-time snapshot. The dialog is keyed on `agent.id` upstream so
  // navigating to a different agent remounts this body and reseeds.
  const initialMode: PermissionMode = agent.permission_mode ?? "private";
  const initialTargets: InvocationTarget[] = agent.invocation_targets ?? [];

  const [mode, setMode] = useState<PermissionMode>(initialMode);
  const [targets, setTargets] = useState<InvocationTarget[]>(initialTargets);
  const [saving, setSaving] = useState(false);

  // Build a derived list of "all workspace members except the owner".
  // The owner is always allowed by definition so we don't surface them
  // in the add-list (would be visual noise). Already-invited members
  // stay visible via the active chips; the add-list below only contains
  // members NOT yet on the allow-list.
  const ownerId = agent.owner_id;
  const memberIdsOnList = useMemo(() => {
    const ids = new Set<string>();
    for (const tgt of targets) {
      if (tgt.target_type === "member" && tgt.target_id) ids.add(tgt.target_id);
    }
    return ids;
  }, [targets]);

  const workspaceTarget = targets.find((t) => t.target_type === "workspace");
  const memberTargetIds = Array.from(memberIdsOnList);

  const addWorkspace = () => {
    if (workspaceTarget) return;
    setTargets((prev) => [
      ...prev.filter((t) => t.target_type !== "workspace"),
      { target_type: "workspace", target_id: workspaceId },
    ]);
  };
  const removeWorkspace = () => {
    setTargets((prev) => prev.filter((t) => t.target_type !== "workspace"));
  };
  const addMember = (userId: string) => {
    if (memberIdsOnList.has(userId)) return;
    setTargets((prev) => [
      ...prev,
      { target_type: "member", target_id: userId },
    ]);
  };
  const removeMember = (userId: string) => {
    setTargets((prev) =>
      prev.filter((t) => !(t.target_type === "member" && t.target_id === userId)),
    );
  };

  const dirty =
    mode !== initialMode ||
    targets.length !== initialTargets.length ||
    targets.some((t, i) => {
      const a = initialTargets[i];
      if (!a) return true;
      return a.target_type !== t.target_type || a.target_id !== t.target_id;
    });

  const commit = async () => {
    if (!dirty || saving) return;
    setSaving(true);
    try {
      await onSave({ permission_mode: mode, invocation_targets: targets });
    } finally {
      setSaving(false);
    }
  };

  const memberById = (id: string) => members.find((m) => m.user_id === id);

  const eligibleMembers = members.filter(
    (m) => m.user_id !== ownerId && !memberIdsOnList.has(m.user_id),
  );
  const activeMembers = memberTargetIds
    .map((id) => memberById(id))
    .filter((m): m is MemberWithUser => !!m);

  return (
    <Dialog open onOpenChange={(v) => { if (!v) onClose(); }}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t(($) => $.access_picker.section_title)}</DialogTitle>
        </DialogHeader>
        <p className="text-caption text-muted-foreground">
          {t(($) => $.access_picker.section_intro)}
        </p>

        {/* Permission-mode radios — owned by base-ui RadioGroup */}
        <div className="flex flex-col gap-1.5 pt-2">
          <RadioRow
            checked={mode === "private"}
            onSelect={() => setMode("private")}
            title={t(($) => $.access_picker.permission_mode.private)}
            hint={t(($) => $.access_picker.permission_mode.private_hint)}
            icon={<Lock className="h-3.5 w-3.5" />}
          />
          <RadioRow
            checked={mode === "public_to"}
            onSelect={() => setMode("public_to")}
            title={t(($) => $.access_picker.permission_mode.public_to)}
            hint={t(($) => $.access_picker.permission_mode.public_to_hint)}
            icon={<Users className="h-3.5 w-3.5" />}
          />
        </div>

        {/* Allow-list editor — only relevant for public_to mode. Rendered
             even when private so the chip flips cleanly when the user
             toggles public_to without dismissing the modal first. */}
        <div className="flex flex-col gap-2 pt-4">
          <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
            {t(($) => $.access_picker.allowlist_title)}
          </div>

          {/* Workspace target — single row, toggle on/off */}
          <div className="rounded-md border bg-muted/30 p-2">
            {workspaceTarget ? (
              <div className="flex items-center gap-2">
                <Users className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="text-body font-medium">
                    {t(($) => $.access_picker.workspace_target_label)}
                  </div>
                  <div className="text-caption text-muted-foreground">
                    {t(($) => $.access_picker.workspace_target_hint)}
                  </div>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-sm"
                  onClick={removeWorkspace}
                  aria-label={t(($) => $.access_picker.workspace_target_remove_aria)}
                >
                  <X className="h-3.5 w-3.5" />
                </Button>
              </div>
            ) : (
              <button
                type="button"
                onClick={addWorkspace}
                disabled={mode !== "public_to"}
                className="flex w-full items-center gap-2 rounded text-left transition-colors hover:bg-accent/50 disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Users className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
                <div className="min-w-0 flex-1">
                  <div className="text-body font-medium">
                    {t(($) => $.access_picker.workspace_target_add_label)}
                  </div>
                  <div className="text-caption text-muted-foreground">
                    {t(($) => $.access_picker.workspace_target_add_hint)}
                  </div>
                </div>
              </button>
            )}
          </div>

          {/* Members */}
          <div className="text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
            {t(($) => $.access_picker.members_section)}
          </div>

          {activeMembers.length === 0 ? (
            <p className="rounded-md border bg-muted/30 px-2 py-2 text-caption text-muted-foreground">
              {mode === "public_to"
                ? t(($) => $.access_picker.allowlist_empty_public)
                : t(($) => $.access_picker.allowlist_empty)}
            </p>
          ) : (
            <div className="flex flex-col gap-1">
              {activeMembers.map((m) => (
                <div
                  key={m.user_id}
                  className="flex items-center gap-2 rounded-md border bg-muted/30 px-2 py-1.5"
                >
                  <ActorAvatar
                    actorType="member"
                    actorId={m.user_id}
                    size={20}
                  />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-body font-medium">
                      {m.name}
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    onClick={() => removeMember(m.user_id)}
                    aria-label={t(($) => $.access_picker.member_remove_aria, {
                      name: m.name,
                    })}
                  >
                    <X className="h-3.5 w-3.5" />
                  </Button>
                </div>
              ))}
            </div>
          )}

          {/* Add member affordance — only when public_to mode. Filtered
               to members not yet on the list and not the owner. */}
          {mode === "public_to" && eligibleMembers.length > 0 && (
            <details className="group rounded-md border bg-muted/30">
              <summary className="cursor-pointer list-none px-2 py-1.5 text-caption font-medium text-muted-foreground transition-colors hover:text-foreground">
                {t(($) => $.access_picker.add_member_aria)}
              </summary>
              <div className="flex flex-col gap-1 border-t p-2">
                {eligibleMembers.map((m) => (
                  <button
                    key={m.user_id}
                    type="button"
                    onClick={() => addMember(m.user_id)}
                    className="flex items-center gap-2 rounded px-1.5 py-1 text-left transition-colors hover:bg-accent"
                  >
                    <ActorAvatar
                      actorType="member"
                      actorId={m.user_id}
                      size={18}
                    />
                    <span className="min-w-0 truncate text-body">{m.name}</span>
                  </button>
                ))}
                {eligibleMembers.length === 0 && (
                  <p className="px-1.5 py-2 text-caption text-muted-foreground">
                    {t(($) => $.access_picker.members_empty)}
                  </p>
                )}
              </div>
            </details>
          )}
        </div>

        <DialogFooter>
          <Button
            variant="ghost"
            size="sm"
            onClick={onClose}
            disabled={saving}
          >
            {t(($) => $.inspector.cancel)}
          </Button>
          <Button
            size="sm"
            onClick={() => void commit()}
            disabled={saving || !dirty}
          >
            {t(($) => $.inspector.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// RadioRow — single radio-like row without depending on base-ui RadioGroup,
// which the project has not enabled across the renderer yet. Keeps the
// keyboard contract minimal (Enter to select, role=radio + aria-checked).
// ---------------------------------------------------------------------------

function RadioRow({
  checked,
  onSelect,
  title,
  hint,
  icon,
}: {
  checked: boolean;
  onSelect: () => void;
  title: string;
  hint: string;
  icon: React.ReactNode;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={checked}
      onClick={onSelect}
      className={`flex items-start gap-2 rounded-md border p-2 text-left transition-colors ${
        checked
          ? "border-primary bg-primary/5"
          : "hover:bg-accent/50"
      }`}
    >
      <span
        className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border ${
          checked ? "border-primary bg-primary" : "border-input"
        }`}
      >
        {checked && (
          <span className="h-1.5 w-1.5 rounded-full bg-primary-foreground" />
        )}
      </span>
      <span className="mt-0.5 shrink-0 text-muted-foreground">{icon}</span>
      <span className="min-w-0 flex-1">
        <span className="block text-body font-medium">{title}</span>
        <span className="mt-0.5 block text-caption text-muted-foreground">
          {hint}
        </span>
      </span>
    </button>
  );
}