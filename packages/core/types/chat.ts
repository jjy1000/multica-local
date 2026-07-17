import type { AgentTask } from "./agent";

export interface ChatSession {
  id: string;
  workspace_id: string;
  agent_id: string;
  creator_id: string;
  title: string;
  status: "active" | "archived";
  /**
   * Legacy boolean unread flag from ListChatSessionsByCreator. Still
   * populated by 0.3.x servers; mirrors `unread_count > 0` for backward
   * compatibility.
   */
  has_unread: boolean;
  /**
   * IM-style unread count — number of assistant messages strictly after
   * `last_read_at` (migration 135 + 0.3.4 ListChatSessionsByCreatorWithUnreadCount
   * query). Replaces `has_unread` for new UI: render the number when > 0,
   * fall back to `has_unread` for older servers.
   */
  unread_count?: number;
  /**
   * ISO 8601 timestamp of the user's last mark-as-read cursor. The server
   * stamps this via TouchChatSessionLastRead when MarkChatSessionRead fires.
   * Migration 135 added this column with NOT NULL DEFAULT now(); older
   * rows pre-migration had `unread_since` backfilled to `last_read_at - 1us`.
   */
  last_read_at?: string | null;
  /**
   * True when this session was auto-created at agent-create time to host a
   * proactive self-introduction from the agent. The chat window renders a
   * "introduces itself" badge so the creator can tell apart this thread
   * from a real conversation. Migration 138.
   */
  is_agent_intro?: boolean;
  /**
   * ISO 8601 timestamp when the user pinned this session. Sessions with
   * `pinned_at` non-null render above the activity-sorted list.
   * Migration 139.
   */
  pinned_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface PendingChatTaskItem {
  task_id: string;
  status: string;
  chat_session_id: string;
}

export interface PendingChatTasksResponse {
  tasks: PendingChatTaskItem[];
}

export interface ChatMessage {
  id: string;
  chat_session_id: string;
  role: "user" | "assistant";
  content: string;
  task_id: string | null;
  created_at: string;
  /**
   * Attachments linked to this message via the attachment table's
   * chat_message_id FK. Populated by ListChatMessages. UI renders these
   * as file/image cards inside the bubble; the markdown URL inline in
   * `content` may have an expiring signature, while attachment metadata
   * here is stable and the source of truth for click-time download.
   */
  attachments?: import("./attachment").Attachment[];
  /**
   * When set, this is an assistant message synthesized by the server's
   * FailTask fallback (mirrors the issue path's failure system comment).
   * `content` carries the raw daemon-reported errMsg; the front-end maps
   * `failure_reason` (an enum like "agent_error" / "connection_error" /
   * "timeout") to a user-facing label and renders a destructive bubble.
   * Null on success messages and on user messages.
   */
  failure_reason?: string | null;
  /**
   * Wall-clock duration from `task.created_at` (user hit send) to terminal
   * state (completed/failed). Set by the server on assistant messages
   * synthesized by CompleteTask/FailTask. UI renders it as "Replied in
   * 38s" / "Failed after 12s" beneath the bubble. Null on user messages
   * and on legacy assistant messages predating migration 063.
   */
  elapsed_ms?: number | null;
  /**
   * Migration 143 (MUL-4351) — explicit terminal outcome kind.
   * - "message"     — ordinary assistant message (default for all legacy rows)
   * - "no_response" — agent completed a direct-chat turn without any text
   *                   reply. UI renders a localized "no text reply this
   *                   turn" state instead of a blank bubble. Old clients
   *                   fall back to the non-empty content we still store
   *                   on the row, so the optional field is forward-only.
   */
  message_kind?: "message" | "no_response";
}

export interface ChatMessagesCursor {
  created_at: string;
  id: string;
}

export interface ChatMessagesPage {
  messages: ChatMessage[];
  limit: number;
  has_more: boolean;
  next_cursor?: ChatMessagesCursor | null;
}

export interface SendChatMessageResponse {
  message_id: string;
  task_id: string;
  /**
   * Server-authoritative task creation time. Optimistic StatusPill seed
   * uses this as its anchor so the timer starts from the real `0s` —
   * without it the front-end falls back to its local clock and the
   * timer "snaps backwards" later when WS events update the cache.
   */
  created_at: string;
  /**
   * Attachment ids the server actually bound to this message. The client
   * diffs these against the ids it requested to warn when an attachment
   * silently failed to bind — no extra fetch needed. Optional for forward
   * compat with servers that predate the field.
   */
  attachment_ids?: string[];
}

export interface CancelledChatMessage {
  chat_session_id: string;
  message_id: string;
  content: string;
  restore_to_input: boolean;
  /**
   * Attachments detached from the deleted message so a restored draft can
   * re-bind them on re-send. Absent on servers that predate the field.
   */
  attachments?: import("./attachment").Attachment[];
}

export interface CancelTaskResponse extends AgentTask {
  cancelled_chat_message?: CancelledChatMessage;
}

/**
 * Response from GET /api/chat/sessions/{id}/pending-task.
 * All fields are absent when the session has no in-flight task.
 *
 * `created_at` is the server-authoritative anchor for the chat StatusPill's
 * elapsed-seconds timer — the optimistic seed in chat-window.tsx fills in
 * task_id/status only, then this query catches up with the real created_at
 * so the timer survives refresh / reopen without "resetting to 0s".
 */
export interface ChatPendingTask {
  task_id?: string;
  status?: string;
  created_at?: string;
}
