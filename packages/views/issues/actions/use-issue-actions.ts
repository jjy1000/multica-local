"use client";

import { useCallback } from "react";
import { useQuery } from "@tanstack/react-query";
import { toast } from "sonner";
import type { Issue, UpdateIssueRequest } from "@multica/core/types";
import { api } from "@multica/core/api";
import { useAuthStore } from "@multica/core/auth";
import { useWorkspaceId } from "@multica/core/hooks";
import { useWorkspacePaths } from "@multica/core/paths";
import { useModalStore } from "@multica/core/modals";
import { useUpdateIssue } from "@multica/core/issues/mutations";
import { useExperimentalFlags } from "@multica/core/experimental";
import { pinListOptions, useCreatePin, useDeletePin } from "@multica/core/pins";
import { copyText } from "@multica/ui/lib/clipboard";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";
import {
  labLockLabel,
  matchAssigneeLabLockError,
} from "../components/pickers/assignee-lab-lock";

export interface UseIssueActionsResult {
  isPinned: boolean;
  updateField: (updates: Partial<UpdateIssueRequest>) => void;
  openInNewTab: () => void;
  togglePin: () => void;
  copyLink: () => Promise<void>;
  openCreateSubIssue: () => void;
  openSetParent: () => void;
  openAddChild: () => void;
  openDeleteConfirm: (opts?: { onDeletedNavigateTo?: string }) => void;
}

/**
 * Accepts a nullable issue so callers can invoke the hook before they've
 * early-returned on a missing issue. Returned handlers are safe no-ops when
 * `issue` is null.
 */
export function useIssueActions(issue: Issue | null): UseIssueActionsResult {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const paths = useWorkspacePaths();
  const navigation = useNavigation();
  const user = useAuthStore((s) => s.user);
  const userId = user?.id;
  const { data: flags } = useExperimentalFlags();

  const { data: pinnedItems = [] } = useQuery({
    ...pinListOptions(wsId, userId ?? ""),
    enabled: !!userId,
  });

  const isPinned =
    !!issue &&
    pinnedItems.some(
      (p) => p.item_type === "issue" && p.item_id === issue.id,
    );

  const updateIssue = useUpdateIssue();
  const createPin = useCreatePin();
  const deletePin = useDeletePin();
  const openModal = useModalStore((s) => s.open);

  const issueId = issue?.id ?? null;
  const issueIdentifier = issue?.identifier ?? null;
  const issueProjectId = issue?.project_id ?? null;
  const issueAssigneeType = issue?.assignee_type ?? null;
  const issueAssigneeId = issue?.assignee_id ?? null;
  const issueStatus = issue?.status ?? null;

  const updateField = useCallback(
    (updates: Partial<UpdateIssueRequest>) => {
      if (!issueId) return;
      // Assigning to an agent/squad may start a run. Route through the
      // pre-trigger confirm modal (preview + optional handoff note + "暂不开始"),
      // which applies the change itself — the four entry points share this one
      // backend-driven flow instead of guessing (MUL-3375). Every other field
      // change (status, priority, member assign, unassign) applies directly.
      //
      // Backlog is the parking lot: assigning a backlog issue never starts a run
      // (server/internal/service/issue_trigger.go), so the modal would only show
      // an empty "won't start" box with a single Apply button. Apply directly,
      // matching the batch backlog short-circuit in BatchActionToolbar.
      if (
        (updates.assignee_type === "agent" || updates.assignee_type === "squad") &&
        updates.assignee_id &&
        issueStatus !== "backlog"
      ) {
        openModal("issue-run-confirm", {
          issueIds: [issueId],
          mode: "assign",
          assigneeType: updates.assignee_type,
          assigneeId: updates.assignee_id,
        });
        return;
      }
      updateIssue.mutate(
        { id: issueId, ...updates },
        {
          onSuccess: () => {
            // Lab parity with the create-issue path: when the user tags an
            // existing issue with lab_source = pythia_oracle (the update
            // path, e.g. via the LabPicker in the detail panel), auto-launch
            // a 10-round Pythia deliberation so "selecting the lab starts the
            // work" — mirroring create-issue.tsx. The report accumulates in
            // the Pythia panel (the lab's experiment interface), never the
            // issue timeline. Best-effort: a launch failure must not surface
            // as an update error, because the field change already succeeded.
            if (updates.lab_source === "pythia_oracle") {
              // 0.5.59: stamp the trigger key so <PythiaPanel> on the
              // issue detail flips into "推演中..." immediately after
              // the update mutation resolves. Key shape matches
              // lab-output-panel.tsx.
              try {
                window.sessionStorage.setItem(
                  `pythia-triggered-${wsId}-${issueId}`,
                  String(Date.now()),
                );
              } catch {
                // sessionStorage unavailable — ignore.
              }
              void api
                .rawRequest(
                  "/api/experimental/pythia-oracle/forecast/issue",
                  {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ issue_id: issueId, rounds: 10 }),
                  },
                )
                .catch((err) => {
                  console.warn(
                    "[issue-actions] pythia_oracle auto-launch failed",
                    err,
                  );
                });
            }
          },
          onError: (err) => {
            // 0.5.86 assignee-lab-lock rejection: the server's 400 names
            // the lab and its leader in a fixed English sentence — a
            // dedicated guided toast replaces the raw message here.
            const lock = matchAssigneeLabLockError(err);
            if (lock) {
              toast.error(t(($) => $.detail.assignee_lab_lock_title), {
                description: t(($) => $.detail.assignee_lab_lock_description, {
                  lab: labLockLabel(flags, lock.labSource),
                }),
              });
              return;
            }
            toast.error(
              err instanceof Error && err.message
                ? err.message
                : t(($) => $.detail.update_failed),
            );
          },
        },
      );
    },
    [issueId, issueStatus, updateIssue, openModal, t, flags],
  );

  // Explicit "open it somewhere else" CTA, so the new tab takes focus
  // (`activate: true`) — the user is asking to move into the new context, not
  // to stash it for later the way modifier-click does. Same contract as the
  // table row open and the attachment preview's "Open in new tab".
  //
  // Only desktop implements `openInNewTab`; on web it is undefined and we fall
  // back to a real browser tab via the shareable URL.
  const openInNewTab = useCallback(() => {
    if (!issueId) return;
    const path = paths.issueDetail(issueId);
    if (navigation.openInNewTab) {
      navigation.openInNewTab(path, issueIdentifier ?? undefined, {
        activate: true,
      });
      return;
    }
    window.open(
      navigation.getShareableUrl(path),
      "_blank",
      "noopener,noreferrer",
    );
  }, [issueId, issueIdentifier, navigation, paths]);

  const togglePin = useCallback(() => {
    if (!issueId) return;
    if (isPinned) {
      deletePin.mutate({ itemType: "issue", itemId: issueId });
    } else {
      createPin.mutate({ item_type: "issue", item_id: issueId });
    }
  }, [isPinned, issueId, createPin, deletePin]);

  const copyLink = useCallback(async () => {
    if (!issueId) return;
    const url = navigation.getShareableUrl(paths.issueDetail(issueId));
    if (await copyText(url)) {
      toast.success(t(($) => $.detail.link_copied));
    } else {
      toast.error(t(($) => $.detail.link_copy_failed));
    }
  }, [paths, issueId, navigation, t]);

  const openCreateSubIssue = useCallback(() => {
    if (!issueId) return;
    openModal("create-issue", {
      parent_issue_id: issueId,
      parent_issue_identifier: issueIdentifier,
      ...(issueProjectId ? { project_id: issueProjectId } : {}),
      // Inherit the parent's assignee (member/agent/squad) so a sub-issue
      // created from the "Add sub-issue" entry starts with the same owner
      // (discussion #1728). The modal keys off whether these fields are
      // present, not their value, so a seed overrides the sticky last-used
      // assignee it would otherwise fall back to, while omitting both for
      // an unassigned parent leaves that fallback intact. Seed the two
      // together — assignee_type is meaningless without assignee_id.
      ...(issueAssigneeType && issueAssigneeId
        ? { assignee_type: issueAssigneeType, assignee_id: issueAssigneeId }
        : {}),
    });
  }, [
    openModal,
    issueId,
    issueIdentifier,
    issueProjectId,
    issueAssigneeType,
    issueAssigneeId,
  ]);

  const openSetParent = useCallback(() => {
    if (!issueId) return;
    openModal("issue-set-parent", { issueId });
  }, [openModal, issueId]);

  const openAddChild = useCallback(() => {
    if (!issueId) return;
    openModal("issue-add-child", { issueId });
  }, [openModal, issueId]);

  const openDeleteConfirm = useCallback(
    (opts?: { onDeletedNavigateTo?: string }) => {
      if (!issueId) return;
      openModal("issue-delete-confirm", {
        issueId,
        identifier: issueIdentifier,
        onDeletedNavigateTo: opts?.onDeletedNavigateTo,
      });
    },
    [openModal, issueId, issueIdentifier],
  );

  return {
    isPinned,
    updateField,
    openInNewTab,
    togglePin,
    copyLink,
    openCreateSubIssue,
    openSetParent,
    openAddChild,
    openDeleteConfirm,
  };
}
