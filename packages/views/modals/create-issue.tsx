"use client";

import { issueStatusCategory } from "@multica/core/issues";
import { useState, useRef, useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { useNavigation } from "../navigation";
import {
  AlertTriangle,
  ArrowDown,
  ArrowLeftRight,
  ArrowUp,
  CalendarClock,
  Check,
  ChevronRight,
  FlaskConical,
  Maximize2,
  Minimize2,
  MoreHorizontal,
  Plus,
  X as XIcon,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import type { Issue, IssueStatus, IssuePriority, IssueAssigneeType, Attachment } from "@multica/core/types";
import { contentReferencesAttachment } from "@multica/core/types";
import {
  DialogContent,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider } from "@multica/ui/components/ui/tooltip";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { ContentEditor, type ContentEditorRef, TitleEditor, useFileDropZone, FileDropOverlay } from "../editor";
import { StatusIcon, StatusPicker, PriorityPicker, StagePicker, AssigneePicker, StartDatePicker, DueDatePicker, LabPicker } from "../issues/components";
import { maxSiblingStage } from "../issues/components/pickers/stage-picker";
import { labSourceRouteSuffix } from "../issues/components/issue-labs-section";
import { ProjectPicker } from "../projects/components/project-picker";
import { useIssueTriggerPreview } from "../issues/hooks/use-issue-trigger-preview";
import { useActorName } from "@multica/core/workspace/hooks";
import { useCurrentWorkspace, useWorkspacePaths } from "@multica/core/paths";
import { useWorkspaceId } from "@multica/core/hooks";
import { useIssueDraftStore } from "@multica/core/issues/stores/draft-store";
import { useCreateModeStore } from "@multica/core/issues/stores/create-mode-store";
import { useExperimentalFlags } from "@multica/core/experimental";
import { useQuickCreateStore } from "@multica/core/issues/stores/quick-create-store";
import { issueDetailOptions, childIssuesOptions } from "@multica/core/issues/queries";
import { useCreateIssue, useUpdateIssue } from "@multica/core/issues/mutations";
import { useFileUpload } from "@multica/core/hooks/use-file-upload";
import {
  api,
  ApiError,
  DuplicateIssueErrorBodySchema,
  type DuplicateIssueErrorBody,
  parseWithFallback,
} from "@multica/core/api";
import { FileUploadButton } from "@multica/ui/components/common/file-upload-button";
import { PillButton } from "../common/pill-button";
import { ActorAvatar } from "../common/actor-avatar";
import { IssuePickerModal } from "./issue-picker-modal";
import { useT } from "../i18n";

function toDraftAttachment(attachment: Attachment): Attachment {
  return {
    ...attachment,
    // `download_url` is minted for the current API response and may be a
    // short-lived signed URL. Drafts survive across dialog closes and app
    // restarts, so persist only durable fields and let render/download paths
    // re-resolve through id/markdown_url when needed.
    download_url: "",
  };
}

// ---------------------------------------------------------------------------
// ManualCreatePanel — manual-mode body of the create-issue dialog. Renders
// DialogContent + everything inside; the surrounding `<Dialog>` is owned by
// CreateIssueDialog so mode switching swaps only the inner panel without
// remounting the Dialog Root (no overlay flash). `onSwitchMode` flips the
// shell's local mode state.
// ---------------------------------------------------------------------------

// CreateRunHint is the create modal's passive pre-trigger label (MUL-3375 §4):
// whether saving will start a run, driven by the unified backend predicate
// (preview, isCreate) — never a frontend guess. No dialog, no blocking.
//
// Visually it borrows the comment header's avatar+text line, minus the
// interactivity — purely a caption, never a link/hover-card. It renders its own
// reveal band (a grid 0fr→1fr collapse) so it sits on a dedicated row above the
// property toolbar without reflowing anything: collapsed it is 0px (the flex-1
// editor absorbs the delta), and it expands only once the predicate resolves,
// animating straight to the correct copy.
function CreateRunHint({
  assigneeType,
  assigneeId,
  status,
}: {
  assigneeType?: IssueAssigneeType;
  assigneeId?: string;
  status: IssueStatus;
}) {
  const { t } = useT("modals");
  const { getActorName } = useActorName();
  const isAgentLike = assigneeType === "agent" || assigneeType === "squad";
  const preview = useIssueTriggerPreview({
    isCreate: true,
    assigneeType: assigneeType ?? null,
    assigneeId: assigneeId ?? null,
    status,
    enabled: isAgentLike && !!assigneeId,
  });

  // Reveal only after the predicate resolves so the band animates to the final
  // copy instead of flashing "parked" before the run preview lands.
  const ready = isAgentLike && !!assigneeId && !preview.isLoading;
  const willStart = preview.totalCount > 0;
  const isSquad = assigneeType === "squad";
  const triggerAgentId = preview.triggers[0]?.agent_id ?? assigneeId;

  // Avatar + copy mirror the flow. A squad doesn't "work" — its leader
  // evaluates and delegates — so the squad path keeps the squad as the subject
  // (avatar + name) and uses the leader-delegates copy. A single agent picks
  // the issue up directly; a parked issue shows whoever it was assigned to.
  let avatarType: string;
  let avatarId: string | undefined;
  let text: string;
  if (!willStart) {
    avatarType = assigneeType ?? "agent";
    avatarId = assigneeId;
    text = t(($) => $.run_confirm.create_parked);
  } else if (isSquad) {
    avatarType = "squad";
    avatarId = assigneeId;
    text = t(($) => $.run_confirm.create_will_start_squad, {
      name: getActorName("squad", assigneeId ?? ""),
    });
  } else {
    avatarType = "agent";
    avatarId = triggerAgentId;
    text = t(($) => $.run_confirm.create_will_start, {
      name: getActorName("agent", triggerAgentId ?? assigneeId ?? ""),
    });
  }

  return (
    <div
      className={cn(
        "grid shrink-0 transition-[grid-template-rows] duration-200 ease-out motion-reduce:transition-none",
        ready ? "grid-rows-[1fr]" : "grid-rows-[0fr]",
      )}
      aria-hidden={!ready}
    >
      <div className="overflow-hidden">
        <div
          aria-live="polite"
          className="flex items-center gap-1.5 px-4 pb-1 pt-0.5 text-micro text-muted-foreground"
        >
          {avatarId && (
            <ActorAvatar
              actorType={avatarType}
              actorId={avatarId}
              size={16}
              profileLink={false}
            />
          )}
          <span className="truncate">{text}</span>
        </div>
      </div>
    </div>
  );
}

/**
 * LabPickerRow — dedicated row in the create-issue dialog that lets the
 * user pick a lab (mythos / pythia / claude-lab / …). Lives OUTSIDE the
 * inline property toolbar so it can never be pushed off-screen by the
 * toolbar's `flex-wrap` overflow.
 *
 * Visibility rule (0.3.33 user request):
 *
 *   - When the experimental flag API hasn't loaded yet → render
 *     a placeholder pulse so the chrome stays stable.
 *   - When the API returns zero enabled flags → render nothing
 *     (dialog falls back to its original slim layout, with zero
 *     extra rows).
 *   - When at least one flag is enabled → render the LabPicker
 *     inline. Picking mythos_swarm in sole mode clears the current
 *     assignee because that lab owns the agent roster; other labs
 *     (and mythos enhancer mode) keep the manual assignee.
 */
function LabPickerRow({
  labSource,
  setLabSource,
  setLabMode,
  clearAssignee,
  isLabCreation,
  onToggleLabCreation,
  hasAssignee,
}: {
  labSource: string | undefined;
  setLabSource: (next: string | undefined) => void;
  setLabMode: (next: string | undefined) => void;
  /** Called when the user picks a non-empty lab so the issue never
   *  carries a stale assignee into a lab-owned run. Mirrors the
   *  same callback issue-detail.tsx wires into the issue-detail
   *  LabPicker; without this the dialog submit lands on the
   *  server-side mutex gate ("lab_source and assignee are
   *  mutually exclusive") and the user sees a 400 toast. */
  clearAssignee: () => void;
  /** 0.3.60: lab-plugin creation request mode. When true the user
   *  is picking an agent who will AUTHOR a new lab plugin via the
   *  multica-lab-builder skill — this is a creation request, NOT a
   *  lab_source binding, so the issue keeps its manual assignee and
   *  lab_source stays unset. */
  isLabCreation: boolean;
  onToggleLabCreation: () => void;
  /** 0.3.62: whether the issue already carries a manual assignee
   *  (agent or squad). Used to flip the lab-creation hint from a
   *  prompt ("pick an agent/squad") to a confirmation once the user
   *  HAS picked one — otherwise the static "选择一个智能体或团队" copy
   *  keeps nagging even after a squad is selected, and the user reads
   *  it as "my selection didn't register". */
  hasAssignee: boolean;
}) {
  const { t: tModals } = useT("modals");
  const { data: flags } = useExperimentalFlags();
  // Wait for the flag query to settle. Skipping the placeholder while
  // the request is in flight would cause a flicker from "missing row"
  // to "row present" once the query resolves — the placeholder keeps
  // the dialog height stable.
  if (flags === undefined) {
    return (
      <div className="shrink-0 px-4 py-1 text-caption text-muted-foreground/60">
        <span className="inline-block h-3 w-24 rounded bg-muted/40 animate-pulse" aria-hidden />
      </div>
    );
  }
  const enabledCount = flags.filter((f) => f.enabled).length;
  // 0.5.4: always-show action labs (agent_creation_studio) must keep the
  // row visible even when NO flag is enabled — the entry opens a read-only
  // info panel inside the LabPicker popover, so the user can see recent
  // activity + a one-line hint without flipping the flag on.
  const hasAlwaysShow = flags.some((f) => f.always_show_in_lab_picker);
  if (enabledCount === 0 && !hasAlwaysShow) {
    // All flags off → no lab UI. Dialog returns to its original
    // layout with no extra chrome.
    return null;
  }
  return (
    <div className="shrink-0 px-4 py-1 flex items-center gap-1.5 flex-wrap">
      <span className="text-[10px] uppercase tracking-wide text-muted-foreground/70 select-none">
        {tModals(($) => $.create_issue.lab_row_title, { count: enabledCount })}
      </span>
      <LabPicker
        labSource={labSource}
        onUpdate={(u) => {
          setLabSource(u.lab_source ?? undefined);
          setLabMode(u.lab_mode ?? undefined);
          // 0.3.60: binding an existing lab ends the "create a new
          // lab plugin" request — the two are mutually exclusive.
          if (u.lab_source && isLabCreation) {
            onToggleLabCreation();
          }
          // 0.3.33: only mythos_swarm reserves the agent roster
          // (and even then only in sole mode). Other labs
          // (claude_science_lab, pythia_oracle, llm_wiki_bridge,
          // code_canvas, agent_self_optimization,
          // chat_pin_ui) ship their own
          // runtime agents / skills — the user is free to keep a
          // manual assignee on top. Clearing the old assignee
          // unconditionally would erase work the user did
          // intentionally.
          if (u.lab_source && u.lab_source === "mythos_swarm" && u.lab_mode !== "enhancer") {
            clearAssignee();
          }
        }}
        onClearAssignee={clearAssignee}
        triggerRender={
          // Inline label inside the trigger button so the user
          // sees the chosen lab name (or a "pick a lab" hint) even
          // before opening the popover. Default empty PillButton
          // is invisible against the dialog background.
          <PillButton>
            <FlaskConical className="size-3" />
            {labSource
              ? (labDisplayLabel(labSource) ?? labSource)
              : "实验插件..."}
          </PillButton>
        }
        align="start"
      />
      {/* 0.3.60: "创建实验室" — ask a picked agent to author a NEW
          lab plugin via the multica-lab-builder skill. Orthogonal
          to lab_source: this never binds the issue to an existing
          lab; it sets a creation hint + keeps the manual assignee. */}
      <PillButton
        onClick={onToggleLabCreation}
        className={cn(
          isLabCreation && "bg-accent text-accent-foreground",
        )}
      >
        <Plus className="size-3" />
        {tModals(($) => $.create_issue.lab_row_create_lab)}
      </PillButton>
      {isLabCreation && (
        <span className="text-[10px] text-muted-foreground select-none">
          {hasAssignee
            ? tModals(($) => $.create_issue.lab_row_create_with_assignee)
            : tModals(($) => $.create_issue.lab_row_pick_assignee_first)}
        </span>
      )}
    </div>
  );
}

const LAB_DISPLAY_LABELS: Record<string, string> = {
  mythos_swarm: "Mythos 蜂群",
  pythia_oracle: "Pythia 多视角预测",
  claude_science_lab: "Claude 科研实验室",
  llm_wiki_bridge: "LLM Wiki",
  code_canvas: "代码画布",
  agent_self_optimization: "智能体自优化",
  // (0.3.57: constitution_agent removed alongside the lab
  // retirement in migration 165.)
  chat_pin_ui: "聊天置顶",
};

function labDisplayLabel(key: string): string | undefined {
  // 0.3.60: user plugins (user_* prefix) have no static label — prettify
  // the slug into a Title Case display name (user_foo_bar → "Foo Bar").
  if (key.startsWith("user_")) {
    const slug = key.slice(5).replace(/[_-]+/g, " ").trim();
    if (!slug) return undefined;
    return slug.replace(/\b\w/g, (c) => c.toUpperCase());
  }
  return LAB_DISPLAY_LABELS[key];
}

// experimentalLabRouteFor returns the desktop `/experimental/<flag>`
// route that should receive a freshly-created lab issue, or null
// for labs without a dedicated view (chat_pin_ui has no surface).
// The caller appends `?issue=<id>` to pre-select the issue inside
// the panel — see ClaudeLabView / PythiaView / MythosView's
// useSearchParams hooks.
//
// 0.3.68: delegates to labSourceRouteSuffix (issue-labs-section.tsx)
// so the flag→route mapping — including the user_* plugin-shell
// handling — has a single source of truth instead of a drifting
// duplicate switch here.
function experimentalLabRouteFor(labSource: string | undefined): string | null {
  const suffix = labSourceRouteSuffix(labSource);
  return suffix ? `/experimental/${suffix}` : null;
}

export function ManualCreatePanel({
  onClose,
  onSwitchMode,
  data,
  isExpanded,
  setIsExpanded,
}: {
  onClose: () => void;
  /** Called with the carry payload to seed the agent panel after switch. */
  onSwitchMode?: (carry?: Record<string, unknown> | null) => void;
  data?: Record<string, unknown> | null;
  /** Lifted to the shell so DialogContent's mode-aware className can react
   *  without the body itself having to live inside DialogContent (which would
   *  re-mount the Portal on mode swap and replay the open animation). */
  isExpanded: boolean;
  setIsExpanded: (v: boolean) => void;
}) {
  const { t: tModals } = useT("modals");
  const { t: tIssues } = useT("issues");
  const router = useNavigation();
  const p = useWorkspacePaths();
  const workspaceName = useCurrentWorkspace()?.name;

  const draft = useIssueDraftStore((s) => s.draft);
  const setDraft = useIssueDraftStore((s) => s.setDraft);
  const clearDraft = useIssueDraftStore((s) => s.clearDraft);
  const setLastAssignee = useIssueDraftStore((s) => s.setLastAssignee);
  const setLastMode = useCreateModeStore((s) => s.setLastMode);
  const keepOpen = useQuickCreateStore((s) => s.keepOpen);
  const setKeepOpen = useQuickCreateStore((s) => s.setKeepOpen);

  const [title, setTitle] = useState(draft.title);
  const [formResetKey, setFormResetKey] = useState(0);
  const descEditorRef = useRef<ContentEditorRef>(null);
  const { isDragOver: descDragOver, dropZoneProps: descDropZoneProps } = useFileDropZone({
    onDrop: (files) => files.forEach((f) => descEditorRef.current?.uploadFile(f)),
  });
  const [status, setStatus] = useState<IssueStatus>((data?.status as IssueStatus) || draft.status);
  const [priority, setPriority] = useState<IssuePriority>(draft.priority);
  const [submitting, setSubmitting] = useState(false);
  const [assigneeType, setAssigneeType] = useState<IssueAssigneeType | undefined>(() => {
    if (data && "assignee_type" in data) {
      return (data.assignee_type as IssueAssigneeType | null) ?? undefined;
    }
    return draft.assigneeType;
  });
  const [assigneeId, setAssigneeId] = useState<string | undefined>(() => {
    if (data && "assignee_id" in data) {
      return (data.assignee_id as string | null) ?? undefined;
    }
    return draft.assigneeId;
  });
  const [startDate, setStartDate] = useState<string | null>(draft.startDate);
  const [dueDate, setDueDate] = useState<string | null>(draft.dueDate);
  const [projectId, setProjectId] = useState<string | undefined>(
    (data?.project_id as string) || undefined,
  );
  const [parentIssueId, setParentIssueId] = useState<string | undefined>(
    (data?.parent_issue_id as string) || undefined,
  );
  // Stage only applies to a sub-issue; kept local (not in the persisted draft)
  // since it's a per-creation choice tied to the chosen parent.
  const [stage, setStage] = useState<number | null>(
    typeof data?.stage === "number" ? (data.stage as number) : null,
  );
  const [parentPickerOpen, setParentPickerOpen] = useState(false);
  // Start date is a low-frequency field — by default it lives in the
  // overflow ⋯ menu. Clicking the menu item flips this open, which both
  // mounts the inline pill (the popover's anchor) AND opens the calendar.
  // When the popover closes without a value set, the pill unmounts again.
  const [startDatePickerOpen, setStartDatePickerOpen] = useState(false);
  // Lab source — associates the issue with an experimental lab.
  const [labSource, setLabSource] = useState<string | undefined>(undefined);
  const [labMode, setLabMode] = useState<string | undefined>(undefined);
  // 0.3.60: lab-plugin creation request. When true the picked agent
  // is asked to AUTHOR a new lab plugin (multica-lab-builder skill);
  // the issue keeps its manual assignee and lab_source stays unset.
  const [isLabCreation, setIsLabCreation] = useState(false);
  const toggleLabCreation = () => {
    const next = !isLabCreation;
    setIsLabCreation(next);
    if (next) {
      // A creation request is not a lab binding — drop any picked lab
      // so submit never carries a stale lab_source into the mutex gate.
      setLabSource(undefined);
      setLabMode(undefined);
    }
  };
  // Children live as full Issue objects — the picker always returns the whole
  // object, and we never need to hydrate from an ID the way we do for parent.
  const [childIssues, setChildIssues] = useState<Issue[]>([]);
  const [childPickerOpen, setChildPickerOpen] = useState(false);
  // Fetch parent issue details for the chip (status/identifier/title).
  // List cache usually has it already, so this resolves synchronously.
  const wsId = useWorkspaceId();
  const { data: parentIssue } = useQuery({
    ...issueDetailOptions(wsId, parentIssueId ?? ""),
    enabled: !!parentIssueId,
  });
  // Sibling stages under the chosen parent, so the Stage picker can offer the
  // already-used max stage (and one beyond) instead of flooring at Stage 1–3.
  const { data: parentChildren = [] } = useQuery({
    ...childIssuesOptions(wsId, parentIssueId ?? ""),
    enabled: !!parentIssueId,
  });

  const draftAttachments = draft.attachments ?? [];

  // Prune draft attachments whose markdown reference was deleted in an
  // earlier editing session. Runs once on mount: at that point the persisted
  // description IS the draft body (no editor edits have happened yet), so
  // dropping unreferenced records is safe. Don't prune on description updates
  // — an onUpdate flush can race a just-finished upload whose markdown link
  // hasn't been inserted yet, and pruning there would drop a live attachment.
  useEffect(() => {
    const { draft: current } = useIssueDraftStore.getState();
    const attachments = current.attachments ?? [];
    const kept = attachments.filter((a) =>
      contentReferencesAttachment(current.description, a),
    );
    if (kept.length !== attachments.length) setDraft({ attachments: kept });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const { uploadWithToast } = useFileUpload(api);
  const handleUpload = async (file: File) => {
    const result = await uploadWithToast(file);
    if (result) {
      const currentAttachments =
        useIssueDraftStore.getState().draft.attachments ?? [];
      const attachments = currentAttachments.some((a) => a.id === result.id)
        ? currentAttachments
        : [...currentAttachments, toDraftAttachment(result)];
      setDraft({ attachments });
    }
    return result;
  };

  // Sync field changes to draft store
  const updateTitle = (v: string) => { setTitle(v); setDraft({ title: v }); };
  const updateStatus = (v: IssueStatus) => { setStatus(v); setDraft({ status: v }); };
  const updatePriority = (v: IssuePriority) => { setPriority(v); setDraft({ priority: v }); };
  const updateAssignee = (type?: IssueAssigneeType, id?: string) => {
    setAssigneeType(type); setAssigneeId(id);
    setDraft({ assigneeType: type, assigneeId: id });
  };
  const updateStartDate = (v: string | null) => { setStartDate(v); setDraft({ startDate: v }); };
  const updateDueDate = (v: string | null) => { setDueDate(v); setDraft({ dueDate: v }); };

  const createIssueMutation = useCreateIssue();
  const updateIssueMutation = useUpdateIssue();
  const resetForNextIssue = () => {
    setTitle("");
    setStatus("todo");
    setPriority("none");
    setStartDate(null);
    setDueDate(null);
    setProjectId(undefined);
    setParentIssueId(undefined);
    setStage(null);
    setLabSource(undefined);
    setLabMode(undefined);
    setIsLabCreation(false);
    setChildIssues([]);
    setDraft({
      title: "",
      description: "",
      status: "todo",
      priority: "none",
      assigneeType,
      assigneeId,
      startDate: null,
      dueDate: null,
      attachments: [],
    });
    descEditorRef.current?.clearContent();
    setFormResetKey((key) => key + 1);
  };

  const handleSubmit = async () => {
    if (!title.trim() || submitting) return;
    setSubmitting(true);
    try {
      const description = descEditorRef.current?.getMarkdown()?.trim() || undefined;
      const activeAttachmentIds = draftAttachments
        .filter((a) => contentReferencesAttachment(description ?? "", a))
        .map((a) => a.id);
      // 0.3.60: a lab-creation request tags the title + description so the
      // assigned agent (or squad) knows to author a lab plugin via the
      // multica-lab-builder skill rather than treat this as an ordinary
      // issue. lab_source is intentionally left unset — this is a creation
      // request, not a binding.
      //
      // 0.3.62: the hint now opens with an environment/capability briefing so
      // the assignee understands (a) it is operating INSIDE the Labs sandbox,
      // (b) it holds the full plugin lifecycle — survey / create / edit /
      // delete / enable-disable (pause) — over the local API, and (c) the
      // built-in-vs-user-plugin boundary. Then it is told to FIRST survey the
      // current lab landscape before deciding what to build.
      const LAB_CREATION_HINT =
        "[实验室创建] 你现在处于 Multica 实验室环境（Labs sandbox），已被指派专门处理实验室插件工作，" +
        "拥有对用户插件的完整生命周期能力：了解(survey)、创建、编辑、删除、启用/停用(暂停)。请用 multica-lab-builder 技能操作。" +
        "第一步先了解现状：读取 GET /api/experimental-flags（含内置实验室与已有用户插件及其启用状态）与 " +
        "GET /api/user-plugins（已有插件清单与完整 manifest），弄清目前有哪些实验室、哪些已启用、还缺什么。" +
        "重要：survey 只是起点，不是终点——除非任务明确只要求调研，否则你应主动通过 POST/PUT/DELETE /api/user-plugins " +
        "实际创建、编辑或删除用户插件来完成需求，不要在调研后就停下或声称自己没有权限。创建时 slug 用纯小写字母/数字/连字符（如 my-plugin，不加 user_ 前缀，前缀由服务端自动添加），完成后在本 issue 上回报改动。" +
        "所有操作走本机 http://localhost:8090，鉴权用环境变量 MULTICA_API_TOKEN（已注入，勿硬编码、勿访问外网）。" +
        "边界：仅 8 个内置实验室由开发者维护、你只能启用/停用而不能增删改其定义；用户插件是你可自由完整增删改的对象，请放手操作。";
      const finalTitle =
        isLabCreation && !title.trim().startsWith("[实验室创建]")
          ? `[实验室创建] ${title.trim()}`
          : title.trim();
      const finalDescription = isLabCreation
        ? (description ? `${LAB_CREATION_HINT}\n\n${description}` : LAB_CREATION_HINT)
        : description;
      const issue = await createIssueMutation.mutateAsync({
        title: finalTitle,
        description: finalDescription,
        status,
        priority,
        assignee_type: assigneeType,
        assignee_id: assigneeId,
        start_date: startDate || undefined,
        due_date: dueDate || undefined,
        attachment_ids: activeAttachmentIds.length > 0 ? activeAttachmentIds : undefined,
        parent_issue_id: parentIssueId,
        // Stage is only meaningful for a sub-issue (relative to its siblings).
        stage: parentIssueId && stage != null ? stage : undefined,
        project_id: projectId,
        lab_source: isLabCreation ? undefined : labSource,
        lab_mode: isLabCreation ? undefined : (labMode as "sole" | "enhancer" | undefined),
      });

      // 0.3.30.3: when the issue is tagged with lab_source =
      // pythia_oracle, auto-launch a 10-round Pythia deliberation
      // against the new issue in the background. The user sees the
      // issue toast; the report accumulates on the issue detail's
      // Pythia panel. Best-effort: a Pythia failure must not block
      // the create flow (the issue already exists in the DB).
      //
      // 0.5.59: stamp the sessionStorage trigger key BEFORE firing so
      // the PythiaPanel that mounts when the user navigates to the
      // issue detail immediately flips into "推演中..." instead of
      // showing the silent-empty state for the first ~5s of polling.
      // Key shape matches <PythiaPanel> in lab-output-panel.tsx.
      if (labSource === "pythia_oracle") {
        try {
          window.sessionStorage.setItem(
            `pythia-triggered-${wsId}-${issue.id}`,
            String(Date.now()),
          );
        } catch {
          // sessionStorage unavailable (private mode, SSR, etc.) — ignore.
        }
        try {
          await api.rawRequest(
            "/api/experimental/pythia-oracle/forecast/issue",
            {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ issue_id: issue.id, rounds: 10 }),
            },
          );
        } catch (err) {
          console.warn(
            "[create-issue] pythia_oracle auto-launch failed",
            err,
          );
        }
      }

      // Link queued children to the new parent. Deferred to after create
      // because the new issue's ID doesn't exist yet. Partial failures don't
      // roll back the new issue — it's already committed.
      if (childIssues.length > 0) {
        const results = await Promise.allSettled(
          childIssues.map((child) =>
            updateIssueMutation.mutateAsync({
              id: child.id,
              parent_issue_id: issue.id,
            }),
          ),
        );
        // Aggregate fan-out: N independent requests can fail for N different
        // reasons. The user-facing toast stays count-based (any single
        // err.message would mislead), but log each rejection so developers
        // still have signal in dev-tools / Sentry.
        for (const result of results) {
          if (result.status === "rejected") {
            console.error("[create-issue] sub-issue link failed", result.reason);
          }
        }
        const failed = results.filter((r) => r.status === "rejected").length;
        if (failed > 0) {
          toast.error(
            failed === childIssues.length
              ? tModals(($) => $.create_issue.toast_link_subissues_all_failed)
              : tModals(($) => $.create_issue.toast_link_subissues_partial, {
                  failed,
                  total: childIssues.length,
                }),
          );
        }
      }

      setLastAssignee(assigneeType, assigneeId);
      setLastMode("manual");
      clearDraft();
      // The old post-create "agent paused in Backlog" blocking panel is gone —
      // a passive inline hint now warns before submit (MUL-3375). Just close or
      // reset and confirm the create.
      if (keepOpen) {
        resetForNextIssue();
      } else {
        onClose();
      }

      {
        toast.custom((toastId) => (
          <div className="bg-popover text-popover-foreground border rounded-lg shadow-lg p-4 w-[360px]">
            <div className="flex items-center gap-2 mb-2">
              <div className="flex items-center justify-center size-5 rounded-full bg-emerald-500/15 text-emerald-500">
                <Check className="size-3" />
              </div>
              <span className="text-body font-medium">{tModals(($) => $.create_issue.toast_created)}</span>
            </div>
            <div className="flex items-center gap-2 text-body text-muted-foreground ml-7">
              <StatusIcon
                status={issue.status}
                category={issueStatusCategory(issue) ?? undefined}
                className="size-3.5 shrink-0"
              />
              <span className="truncate">{issue.identifier} – {issue.title}</span>
            </div>
            <button
              type="button"
              className="ml-7 mt-2 text-body text-primary hover:underline cursor-pointer"
              onClick={() => {
                // 0.3.35: lab-tagged issues should land in the lab
                // panel, not the issue detail page. The "view issue"
                // button now doubles as "open in lab" so the user
                // sees the lab surface (Claude Lab Plan/Forecast/
                // Code/Knowledge, Pythia report, Mythos swarm form)
                // already pre-scoped to the just-created issue via
                // the `?issue=<id>` URL param (parsed in each view's
                // useSearchParams).
                const labRoute = experimentalLabRouteFor(labSource);
                if (labRoute) {
                  router.push(`${labRoute}?issue=${encodeURIComponent(issue.id)}`);
                } else {
                  router.push(p.issueDetail(issue.id));
                }
                toast.dismiss(toastId);
              }}
            >
              {tModals(($) => $.create_issue.view_issue)}
            </button>
          </div>
        ), { duration: 5000 });
      }
    } catch (err) {
      // Duplicate-issue is the only structured 409 the create endpoint
      // returns. We schema-guard the body (ApiError.body is `unknown`) so a
      // future server-side rename / drop of `code` / `issue` degrades to the
      // normal error toast instead of throwing inside the toast renderer.
      if (err instanceof ApiError && err.status === 409) {
        const dup = parseWithFallback<DuplicateIssueErrorBody | null>(
          err.body,
          DuplicateIssueErrorBodySchema,
          null,
          { endpoint: "POST /api/workspaces/:wsId/issues (active_duplicate_issue)" },
        );
        if (dup) {
          toast.custom(
            (toastId) => (
              <div className="bg-popover text-popover-foreground border rounded-lg shadow-lg p-4 w-[360px]">
                <div className="flex items-center gap-2 mb-2">
                  <div className="flex items-center justify-center size-5 rounded-full bg-amber-500/15 text-amber-500">
                    <AlertTriangle className="size-3" />
                  </div>
                  <span className="text-body font-medium">
                    {tModals(($) => $.create_issue.toast_duplicate_title)}
                  </span>
                </div>
                <div className="flex items-center gap-2 text-body text-muted-foreground ml-7">
                  <span className="truncate">{dup.issue.identifier} – {dup.issue.title}</span>
                </div>
                <button
                  type="button"
                  className="ml-7 mt-2 text-body text-primary hover:underline cursor-pointer"
                  onClick={() => {
                    // Mirror the create-success path: a duplicate
                    // lab-tagged issue should also land in the lab
                    // panel. Falls back to issue detail for non-lab
                    // issues (unchanged from 0.3.30).
                    const labRoute = experimentalLabRouteFor(labSource);
                    if (labRoute) {
                      router.push(`${labRoute}?issue=${encodeURIComponent(dup.issue.id)}`);
                    } else {
                      router.push(p.issueDetail(dup.issue.id));
                    }
                    toast.dismiss(toastId);
                  }}
                >
                  {tModals(($) => $.create_issue.toast_duplicate_view)}
                </button>
              </div>
            ),
            { duration: 5000 },
          );
          return;
        }
      }
      toast.error(
        err instanceof Error && err.message
          ? err.message
          : tModals(($) => $.create_issue.toast_failed),
      );
    } finally {
      setSubmitting(false);
    }
  };

  // Switch to agent mode. Hand the typed text up to the shell as the carry
  // payload; the shell stores it as the next panel's `data` so the agent
  // panel reads `data.prompt` on mount. Concatenate title + description so
  // nothing the user typed is lost — the agent derives a fresh title from
  // the combined text. Persist the mode flip so the next `c` lands in agent.
  // Also forward the picked project so the agent panel pins the new issue
  // to it; without this the agent panel would fall back to its persisted
  // `lastProjectId`, silently routing the issue to the wrong project.
  // Forward squad picks alongside agent picks so the agent panel honors
  // the actor the user already chose — otherwise a squad selection silently
  // falls back to the persisted actor / first visible agent on flip.
  // parent_issue_id rides through the same carry channel: the modal opener
  // (openCreateSubIssue) seeded it on the manual panel, and the agent panel
  // needs it so the new issue is still created as a sub-issue when the user
  // flips from "Add sub issue" → "Create with agent".
  const switchToAgent = () => {
    const desc = descEditorRef.current?.getMarkdown()?.trim() ?? "";
    const prompt = [title.trim(), desc].filter(Boolean).join("\n\n");
    // Title + description have been packed into the agent prompt — clear them
    // from the shared draft so a later agent→manual switch doesn't surface
    // stale manual state on top of the prompt-as-description, which would
    // duplicate content on every round-trip.
    setDraft({ title: "", description: "" });
    setLastMode("agent");
    // Prefer the hydrated identifier from `parentIssue`, but fall back to the
    // identifier the modal opener seeded on `data`. Without the fallback, a
    // flip that happens before the issue detail query resolves drops the
    // identifier and the agent chip renders as "Sub-issue of " with an empty
    // tail. The UUID alone still wires the sub-issue relationship correctly;
    // this only affects the display affordance.
    const carryParentIdentifier =
      parentIssue?.identifier ?? (data?.parent_issue_identifier as string | undefined);
    onSwitchMode?.({
      prompt,
      ...(assigneeId && assigneeType === "agent"
        ? { agent_id: assigneeId }
        : assigneeId && assigneeType === "squad"
          ? { squad_id: assigneeId }
          : {}),
      ...(projectId ? { project_id: projectId } : {}),
      ...(parentIssueId ? { parent_issue_id: parentIssueId } : {}),
      ...(carryParentIdentifier ? { parent_issue_identifier: carryParentIdentifier } : {}),
    });
  };

  return (
    <>
            <DialogTitle className="sr-only">{tModals(($) => $.create_issue.sr_manual)}</DialogTitle>

            {/* Header */}
            <div className="flex items-center justify-between px-5 pt-3 pb-2 shrink-0">
              <div className="flex items-center gap-1.5 text-caption">
                <span className="text-muted-foreground">{workspaceName}</span>
                <ChevronRight className="size-3 text-muted-foreground/50" />
                <span className="font-medium">{tModals(($) => $.create_issue.manual_breadcrumb)}</span>
              </div>
              <div className="flex items-center gap-1">
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <button
                        type="button"
                        onClick={() => setIsExpanded(!isExpanded)}
                        className="rounded-sm p-1.5 opacity-70 hover:opacity-100 hover:bg-accent/60 transition-all cursor-pointer"
                      >
                        {isExpanded ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
                      </button>
                    }
                  />
                  <TooltipContent side="bottom">
                    {isExpanded
                      ? tModals(($) => $.common.collapse_tooltip)
                      : tModals(($) => $.common.expand_tooltip)}
                  </TooltipContent>
                </Tooltip>
                <Tooltip>
                  <TooltipTrigger
                    render={
                      <button
                        type="button"
                        onClick={onClose}
                        className="rounded-sm p-1.5 opacity-70 hover:opacity-100 hover:bg-accent/60 transition-all cursor-pointer"
                      >
                        <XIcon className="size-4" />
                      </button>
                    }
                  />
                  <TooltipContent side="bottom">{tModals(($) => $.common.close)}</TooltipContent>
                </Tooltip>
              </div>
            </div>

            {/* Title */}
            <div className="px-5 pb-2 shrink-0">
              <TitleEditor
                key={formResetKey}
                autoFocus
                defaultValue={draft.title}
                placeholder={tModals(($) => $.create_issue.title_placeholder)}
                className="text-title font-semibold"
                onChange={(v) => updateTitle(v)}
                onSubmit={handleSubmit}
              />
            </div>

            {/* Description — flex-1 when expanded so the editor fills
                the dialog's full height (otherwise the maximized
                dialog would show a blank band below the editor).
                Collapsed state keeps max-h-48 so the inline toolbar
                stays visible regardless of dialog height — without
                the cap ContentEditor's flex-1 would push the toolbar
                past the overflow-hidden gutter. */}
            <div
              {...descDropZoneProps}
              className={cn(
                "relative flex min-h-24 overflow-y-auto px-5",
                isExpanded ? "flex-1" : "max-h-48",
              )}
            >
              <ContentEditor
                ref={descEditorRef}
                defaultValue={draft.description}
                placeholder={tModals(($) => $.create_issue.description_placeholder)}
                onUpdate={(md) => setDraft({ description: md })}
                onUploadFile={handleUpload}
                debounceMs={500}
                attachments={draftAttachments}
              />
              {descDragOver && <FileDropOverlay />}
            </div>

            {/* Pre-trigger preview — a passive caption above the toolbar; reveals
                when an agent assignee will pick the issue up. */}
            <CreateRunHint assigneeType={assigneeType} assigneeId={assigneeId} status={status} />

            {/* Experimental lab picker — its own dedicated row above the
                property toolbar. Only renders when at least one lab
                flag is enabled; otherwise the dialog falls back to its
                original slim layout (no extra chrome). The picker
                sits in its own row so it can never fall behind the
                dialog's overflow-hidden gutter, regardless of how
                many pills the inline toolbar holds. */}
            <LabPickerRow
              labSource={labSource}
              setLabSource={setLabSource}
              setLabMode={setLabMode}
              clearAssignee={() => updateAssignee(undefined, undefined)}
              isLabCreation={isLabCreation}
              onToggleLabCreation={toggleLabCreation}
              hasAssignee={!!assigneeType}
            />

            {/* Property toolbar */}
            <div className="flex items-center gap-1.5 px-4 py-2 shrink-0 flex-wrap">
              {/* Status */}
              <StatusPicker
                status={status}
                onUpdate={(u) => { if (u.status) updateStatus(u.status); }}
                triggerRender={<PillButton />}
                align="start"
              />

              {/* Priority */}
              <PriorityPicker
                priority={priority}
                onUpdate={(u) => { if (u.priority) updatePriority(u.priority); }}
                triggerRender={<PillButton />}
                align="start"
              />

              {/* Assignee — disabled (with tooltip) only when mythos_swarm
                  is picked in sole mode, which reserves the agent roster.
                  Every other lab (claude_science_lab / pythia_oracle /
                  code_canvas) lets the user keep a manual assignee, and
                  mythos enhancer mode REQUIRES one — it's the target the
                  swarm supervises — so locking the picker there would
                  trap the user out of a required field and guarantee the
                  server-side "lab_mode=enhancer requires an assignee" 400.
                  Mirrors issue-detail.tsx's lab_mode !== "enhancer" gate. */}
              <AssigneePicker
                assigneeType={assigneeType ?? null}
                assigneeId={assigneeId ?? null}
                onUpdate={(u) => updateAssignee(
                  u.assignee_type ?? undefined,
                  u.assignee_id ?? undefined,
                )}
                lockedReason={
                  labSource === "mythos_swarm" && labMode !== "enhancer"
                    ? tIssues(($) => $.lab_section.clear_lab_first_tooltip)
                    : undefined
                }
                triggerRender={<PillButton />}
                align="start"
              />

              {/* Due date */}
              <DueDatePicker
                dueDate={dueDate}
                onUpdate={(u) => updateDueDate(u.due_date ?? null)}
                triggerRender={<PillButton />}
                align="start"
              />

              {/* Project */}
              <ProjectPicker
                projectId={projectId ?? null}
                onUpdate={(u) => setProjectId(u.project_id ?? undefined)}
                triggerRender={<PillButton />}
                align="start"
              />

              {/* Lab source — moved OUT of the inline toolbar (which
                  already wraps to a second row at the dialog's
                  default width with Project + Inbox + start-date
                  pills). The lab picker is now mounted above the
                  toolbar as its own row, only when at least one
                  experimental flag is enabled. This guarantees the
                  binding is reachable regardless of dialog width,
                  and stays out of the way when no flags are
                  enabled — restoring the original modal layout. */}

              {/* Stage — only relevant when creating a sub-issue under a parent */}
              {parentIssueId && (
                <StagePicker
                  stage={stage}
                  onUpdate={(u) => setStage(u.stage ?? null)}
                  maxStage={maxSiblingStage(parentChildren)}
                  triggerRender={<PillButton />}
                  align="start"
                />
              )}

              {/* Start date — collapsed into the ⋯ menu by default since it's
                  a low-frequency field. Renders inline only when the field
                  has a value OR the user just opened it from the overflow
                  menu (the picker's calendar popover needs the inline pill
                  as its anchor). */}
              {(startDate || startDatePickerOpen) && (
                <StartDatePicker
                  startDate={startDate}
                  onUpdate={(u) => updateStartDate(u.start_date ?? null)}
                  triggerRender={<PillButton />}
                  align="start"
                  open={startDatePickerOpen}
                  onOpenChange={setStartDatePickerOpen}
                />
              )}

              {/* Parent chip — appears when parent is set.
                  Placed before the ⋯ so it wraps to a new line with ⋯ if
                  space is tight, but ⋯ always stays last in DOM order. */}
              {parentIssueId && parentIssue && (
                <div className="inline-flex items-center rounded-full border text-caption transition-colors hover:bg-accent/60">
                  <button
                    type="button"
                    onClick={() => setParentPickerOpen(true)}
                    className="flex items-center gap-1.5 py-1 pl-2.5 cursor-pointer"
                  >
                    <ArrowUp className="size-3 text-muted-foreground" />
                    <span>
                      {tModals(($) => $.create_issue.subissue_of, { identifier: parentIssue.identifier })}
                    </span>
                  </button>
                  <button
                    type="button"
                    onClick={() => setParentIssueId(undefined)}
                    className="p-1 pr-2 text-muted-foreground hover:text-foreground cursor-pointer"
                    aria-label={tModals(($) => $.create_issue.remove_parent_aria)}
                  >
                    <XIcon className="size-3" />
                  </button>
                </div>
              )}

              {/* Child chips — one per queued sub-issue. Links are deferred
                  until create resolves (see handleSubmit). */}
              {childIssues.map((c) => (
                <div
                  key={c.id}
                  className="inline-flex items-center rounded-full border text-caption transition-colors hover:bg-accent/60"
                >
                  <div className="flex items-center gap-1.5 py-1 pl-2.5">
                    <ArrowDown className="size-3 text-muted-foreground" />
                    <span>{tModals(($) => $.create_issue.subissue_chip, { identifier: c.identifier })}</span>
                  </div>
                  <button
                    type="button"
                    onClick={() =>
                      setChildIssues((prev) => prev.filter((x) => x.id !== c.id))
                    }
                    className="p-1 pr-2 text-muted-foreground hover:text-foreground cursor-pointer"
                    aria-label={tModals(($) => $.create_issue.remove_subissue_aria, { identifier: c.identifier })}
                  >
                    <XIcon className="size-3" />
                  </button>
                </div>
              ))}

              {/* Overflow — always the last child so DOM order keeps it at the
                  end of the wrap flow, no matter how many chips are present. */}
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <PillButton aria-label={tModals(($) => $.create_issue.more_options_aria)}>
                      <MoreHorizontal className="size-3.5" />
                    </PillButton>
                  }
                />
                <DropdownMenuContent align="start" className="w-auto">
                  {!startDate && (
                    <DropdownMenuItem onClick={() => setStartDatePickerOpen(true)}>
                      <CalendarClock className="h-3.5 w-3.5" />
                      {tModals(($) => $.create_issue.set_start_date)}
                    </DropdownMenuItem>
                  )}
                  {parentIssueId && parentIssue ? (
                    <DropdownMenuItem onClick={() => setParentPickerOpen(true)}>
                      <ArrowUp className="h-3.5 w-3.5" />
                      {tModals(($) => $.create_issue.parent_with_id, { identifier: parentIssue.identifier })}
                    </DropdownMenuItem>
                  ) : (
                    <DropdownMenuItem onClick={() => setParentPickerOpen(true)}>
                      <ArrowUp className="h-3.5 w-3.5" />
                      {tModals(($) => $.create_issue.set_parent)}
                    </DropdownMenuItem>
                  )}
                  <DropdownMenuItem onClick={() => setChildPickerOpen(true)}>
                    <ArrowDown className="h-3.5 w-3.5" />
                    {tModals(($) => $.create_issue.add_subissue)}
                  </DropdownMenuItem>
                  {parentIssueId && parentIssue && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem
                        variant="destructive"
                        onClick={() => setParentIssueId(undefined)}
                      >
                        <XIcon className="h-3.5 w-3.5" />
                        {tModals(($) => $.create_issue.remove_parent)}
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </div>

            {/* Parent / child pickers — rendered inline so they stack over this
                modal instead of replacing it via useModalStore. */}
            <IssuePickerModal
              open={parentPickerOpen}
              onOpenChange={setParentPickerOpen}
              title={tModals(($) => $.create_issue.set_parent_picker.title)}
              description={tModals(($) => $.create_issue.set_parent_picker.description)}
              excludeIds={[
                ...childIssues.map((c) => c.id),
                ...(parentIssueId ? [parentIssueId] : []),
              ]}
              onSelect={(selected) => {
                setParentIssueId(selected.id);
              }}
            />
            <IssuePickerModal
              open={childPickerOpen}
              onOpenChange={setChildPickerOpen}
              title={tModals(($) => $.create_issue.add_subissue_picker.title)}
              description={tModals(($) => $.create_issue.add_subissue_picker.description)}
              excludeIds={[
                ...childIssues.map((c) => c.id),
                ...(parentIssueId ? [parentIssueId] : []),
              ]}
              onSelect={(selected) => {
                setChildIssues((prev) =>
                  prev.some((x) => x.id === selected.id) ? prev : [...prev, selected],
                );
              }}
            />

            {/* Footer */}
            <div className="flex flex-col gap-2 border-t px-4 py-3 shrink-0 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex min-h-7 items-center gap-2">
                <FileUploadButton
                  onSelect={(file) => descEditorRef.current?.uploadFile(file)}
                />
              </div>
              <div className="flex flex-wrap items-center justify-end gap-2">
                <button
                  type="button"
                  onClick={switchToAgent}
                  title={tModals(($) => $.create_issue.switch_to_agent_tooltip)}
                  className="border-beam group flex shrink-0 items-center gap-1.5 text-caption px-2 py-1 rounded-sm text-muted-foreground bg-brand/5 hover:bg-brand/10 hover:text-foreground transition-colors cursor-pointer"
                >
                  <ArrowLeftRight className="size-3.5 text-brand/80 transition-transform duration-300 group-hover:rotate-180" />
                  {tModals(($) => $.create_issue.switch_to_agent)}
                </button>
                <label className="flex shrink-0 items-center gap-1.5 text-caption text-muted-foreground cursor-pointer select-none">
                  <Switch
                    size="sm"
                    checked={keepOpen}
                    onCheckedChange={setKeepOpen}
                  />
                  {tModals(($) => $.create_issue.create_another)}
                </label>
                {!title.trim() ? (
                  <TooltipProvider delay={200}>
                    <Tooltip>
                      <TooltipTrigger render={<span><Button size="sm" onClick={handleSubmit} disabled>{tModals(($) => $.create_issue.submit)}</Button></span>} />
                      <TooltipContent side="top">{tModals(($) => $.create_issue.title_required)}</TooltipContent>
                    </Tooltip>
                  </TooltipProvider>
                ) : (
                  <Button size="sm" onClick={handleSubmit} disabled={submitting}>
                    {submitting ? tModals(($) => $.create_issue.submitting) : tModals(($) => $.create_issue.submit)}
                  </Button>
                )}
              </div>
            </div>
    </>
  );
}

/** className for DialogContent in manual mode — depends on isExpanded.
 *  Exported so the shell (which now owns the DialogContent) can apply the same
 *  visual treatment without duplicating it. */
export function manualDialogContentClass(isExpanded: boolean) {
  return cn(
    "p-0 gap-0 flex flex-col overflow-hidden",
    "!top-1/2 !left-1/2 !-translate-x-1/2",
    "!transition-all !duration-300 !ease-out",
    isExpanded
      ? "!max-w-4xl !w-full !h-5/6 !-translate-y-1/2"
      // 0.3.33: the dialog used to be fixed at h-96 (384px), which
      // forced the description into flex-1 territory and pushed
      // the inline toolbar past the overflow-hidden gutter. We
      // now let the dialog height track its content (Title +
      // Description + LabPickerRow + Toolbar + Footer ≈ natural
      // height) and cap the description at max-h-48 instead.
      : "!max-w-2xl !w-full !-translate-y-1/2",
  );
}

// Thin Dialog-wrapping export — registry mounts the panel directly under the
// shell's shared Dialog, but a few legacy callers (and the test suite) still
// import this module's modal version. Equivalent runtime behavior to the
// pre-refactor component when used standalone.
import { Dialog as DialogRoot } from "@multica/ui/components/ui/dialog";
export function CreateIssueModal(props: {
  onClose: () => void;
  data?: Record<string, unknown> | null;
}) {
  const [isExpanded, setIsExpanded] = useState(false);
  return (
    <DialogRoot open onOpenChange={(v) => { if (!v) props.onClose(); }}>
      <DialogContent
        finalFocus={false}
        showCloseButton={false}
        className={manualDialogContentClass(isExpanded)}
      >
        <ManualCreatePanel
          {...props}
          isExpanded={isExpanded}
          setIsExpanded={setIsExpanded}
        />
      </DialogContent>
    </DialogRoot>
  );
}
