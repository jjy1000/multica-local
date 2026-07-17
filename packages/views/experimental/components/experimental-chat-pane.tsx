"use client";

// ExperimentalChatPane (0.3.23)
//
// Thin wrapper around <ChatWindow /> for pre-workspace Labs surfaces
// (e.g. Claude Lab `Chat` tab). The pane supplies a workspace id so
// ChatWindow does not call useWorkspaceId() — without this the lab
// would crash on the very first render because the ClaudeLabView
// route is pre-workspace (no <WorkspaceSlugProvider /> in scope).
//
// Where the wsId comes from:
//
//   - 0.3.23 ships the prop-driven path: callers pass an explicit
//     `wsId` (the user's currently active workspace, taken from
//     <ExperimentalChatPane wsId={activeWsId} />).
//   - 0.3.23+ PR-B (install-handler consolidation) will hand a
//     `labId`-derived fake id when no active workspace exists; for
//     now the pane renders an empty-state hint when wsId is empty
//     instead of fabricating data.
//
// Hard rules:
//
//   1. wsId must be a valid UUID (or undefined to render the empty
//      state). The pane does NOT validate the format — that's the
//      caller's job. Passing garbage just makes ChatWindow render
//      no rows.
//   2. No push() / navigate(). The pane is a pure render of
//      ChatWindow in a particular surface.
//   3. i18n selectors MUST be arrow expressions, never block bodies,
//      per the 2026-07-14 AppSidebar incident (see CLAUDE.md).

import { ChatWindow } from "@multica/views/chat";

interface ExperimentalChatPaneProps {
  /** Workspace id to bind ChatWindow to. Required. */
  wsId: string;
  /** Optional className for the outer container. */
  className?: string;
}

export function ExperimentalChatPane({ wsId, className }: ExperimentalChatPaneProps) {
  if (!wsId) {
    return (
      <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
        请先选择或创建一个工作区
      </div>
    );
  }
  return (
    <div className={"flex h-full w-full overflow-hidden rounded-xl border border-border bg-card " + (className ?? "")}>
      <ChatWindow wsId={wsId} />
    </div>
  );
}