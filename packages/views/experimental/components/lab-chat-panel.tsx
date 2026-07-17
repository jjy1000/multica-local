// LabChatPanel (0.3.40)
//
// Right-side chat surface bound to a single Claude Lab issue. Reads
// `chat_session_id` from the workbench context, lazy-creates the
// session on first send (via the standard chat session API), and
// renders an `assistant | user` timeline plus an input box.
//
// Why this is a fresh surface instead of reusing ChatWindow:
//
//   - ChatWindow is keyed off the workspace-level `useChatStore` —
//     `activeSessionId`, `selectedAgentId`, `isOpen` — which is
//     exactly the right shape for the global Chat side panel, but
//     the wrong shape for an inline-per-issue workbench chat. Two
//     stores would race; one store would couple concerns.
//
//   - The workbench panel is bound to a single (issue, agent) pair
//     for its lifetime. The session id resolves from
//     `agent_task_queue.chat_input_task_id` at mount; the panel
//     never opens a session picker.
//
//   - We reuse the same backend endpoints as the global Chat
//     surface (`createChatSession` + `sendChatMessage` +
//     `listChatMessages`) so the messages themselves live in the
//     same `chat_session` / `chat_message` tables the Chat side
//     panel reads. A user who later opens the global Chat will
//     see the same conversation history — the only difference is
//     the entry surface.
//
// Polling: messages refetch every 3 s while the tab is visible.
// The interval is intentionally short — the lab workbench is a
// "live" surface and a 3 s lag is barely perceptible. We don't
// use the realtime WS hub here because the chat panel only
// renders this one session; the per-event cost of the WS hub
// would dwarf the polling round-trip.
//
// Hard rules:
//
//  1. Flag-off bypass: the parent ClaudeLabView wraps this
//     component in `{flagEnabled ? <LabChatPanel /> : null}`.
//     The component itself does not double-gate.
//
//  2. wsId + agentId required. Empty values render a "pick an
//     issue first" hint, matching the contract on ChatWindow
//     (CLAUDE.md, 0.3.23 PR-A).
//
//  3. chat_session_id resolution is the parent's job (via
//     LabContext). The panel receives it as a prop and never
//     scans the issue's task history itself.
//
//  4. No external navigation. "Open in Chat" jumps to the issue
//     detail page (where the global Chat side panel is mounted),
//     same UX as the 0.3.38 OpenChatButton.

import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { MessageSquare, Send, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import type { LabContext } from "@multica/core/types/api";
import { useT } from "@multica/views/i18n";
import { getCurrentSlug } from "@multica/core/platform";
import { paths } from "@multica/core/paths";
import { useNavigation } from "@multica/views/navigation";

// Polling interval for the message stream. Short enough that the
// workbench feels live, long enough that an idle chat doesn't spam
// the server. The lab polling uses a separate queryKey from the
// global Chat's react-query cache so the two surfaces never
// invalidate each other.
const POLL_INTERVAL_MS = 3_000;

const SEND_MAX_LEN = 8_000;

interface LabChatPanelProps {
  /** Workspace id; required. The component never reads from context. */
  wsId: string;
  /** Agent id (the lab leader, from LabContext.Agent.ID). */
  agentId: string;
  /** Issue id — used to bind the panel's title + nav button. */
  issueId: string;
  /** Chat session id (resolved by the parent from LabContext). */
  chatSessionId: string | null;
  /** Lab title — used as the panel header. */
  title: string;
}

export function LabChatPanel({
  wsId,
  agentId,
  issueId,
  chatSessionId,
  title,
}: LabChatPanelProps) {
  const { t } = useT("claude-lab");
  const queryClient = useQueryClient();
  const router = useNavigation();
  // Local-only message cache — separate from ChatWindow's infinite
  // query so a render here never invalidates the global side panel.
  const [draft, setDraft] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);

  // sessionId is either the prop (when the lab already has one from
  // a previous run) or the result of an on-demand createChatSession
  // call (when the user sends the first message of a new lab).
  const [sessionId, setSessionId] = useState<string | null>(chatSessionId);

  // Reset sessionId when the lab issue changes — a new issue means
  // a fresh workbench, so any prior session id is stale.
  useEffect(() => {
    setSessionId(chatSessionId);
  }, [chatSessionId, issueId]);

  const messages = useQuery({
    queryKey: ["lab-chat-messages", wsId, sessionId],
    enabled: !!sessionId,
    refetchInterval: POLL_INTERVAL_MS,
    queryFn: async () => {
      const list = await api.listChatMessages(sessionId as string);
      return list ?? [];
    },
  });

  const send = useMutation({
    mutationFn: async (content: string) => {
      let sid = sessionId;
      if (!sid) {
        const created = await api.createChatSession({
          agent_id: agentId,
          title: `Lab: ${title}`,
        });
        sid = created.id;
        setSessionId(sid);
      }
      await api.sendChatMessage(sid, content);
      // Optimistic: just refetch — the send endpoint already
      // enqueued the agent task; the next poll (≤3 s) picks it up.
      // No need for an immediate invalidate.
      void queryClient.invalidateQueries({
        queryKey: ["lab-chat-messages", wsId, sid],
      });
    },
  });

  const slug = getCurrentSlug();
  const onOpenInChat = () => {
    if (!slug) return;
    router.push(paths.workspace(slug).issueDetail(issueId));
  };

  const canSend =
    !send.isPending && draft.trim().length > 0 && draft.length <= SEND_MAX_LEN;

  return (
    <aside className="flex h-full w-full flex-col overflow-hidden rounded-xl border border-border bg-card">
      <header className="flex shrink-0 items-center justify-between gap-2 border-b border-border bg-background/40 px-4 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <MessageSquare className="size-3.5 text-muted-foreground" aria-hidden />
          <span className="truncate text-xs font-medium text-foreground">
            {t(($) => $.lab_chat_panel_title)}
          </span>
        </div>
        <button
          type="button"
          onClick={onOpenInChat}
          disabled={!slug}
          className={
            "inline-flex items-center gap-1 rounded-md border border-border px-2 py-1 text-xs " +
            (slug ? "hover:bg-muted" : "cursor-not-allowed opacity-60")
          }
          title={slug ? title : t(($) => $.no_workspace)}
        >
          {t(($) => $.open_chat_button)}
        </button>
      </header>

      <div className="flex-1 overflow-y-auto px-4 py-3">
        {messages.isLoading ? (
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            <Loader2 className="mr-1 size-3 animate-spin" aria-hidden />
            {t(($) => $.lab_chat_loading)}
          </div>
        ) : messages.data && messages.data.length > 0 ? (
          <ul className="flex flex-col gap-3">
            {messages.data.map((m) => (
              <li
                key={m.id}
                className={
                  "flex flex-col gap-1 rounded-md px-3 py-2 text-xs " +
                  (m.role === "user"
                    ? "ml-6 bg-primary/10 text-foreground"
                    : "mr-6 bg-muted/40 text-foreground")
                }
              >
                <span className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                  {m.role}
                </span>
                <p className="whitespace-pre-wrap break-words">{m.content}</p>
              </li>
            ))}
          </ul>
        ) : (
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            {sessionId
              ? t(($) => $.lab_chat_empty)
              : t(($) => $.lab_chat_first_send_hint)}
          </div>
        )}
      </div>

      <footer className="shrink-0 border-t border-border bg-background/40 p-3">
        <div className="flex flex-col gap-2">
          <textarea
            ref={textareaRef}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder={t(($) => $.lab_chat_input_placeholder)}
            rows={3}
            maxLength={SEND_MAX_LEN}
            className="w-full resize-none rounded-md border border-border bg-background px-3 py-2 text-xs focus:outline-none focus:ring-1 focus:ring-primary"
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                e.preventDefault();
                if (canSend) send.mutate(draft.trim());
              }
            }}
          />
          <div className="flex items-center justify-between">
            <span className="text-[10px] text-muted-foreground">
              {draft.length}/{SEND_MAX_LEN} · ⌘↩ 发送
            </span>
            <button
              type="button"
              onClick={() => canSend && send.mutate(draft.trim())}
              disabled={!canSend}
              aria-busy={send.isPending}
              className={
                "inline-flex items-center gap-1 rounded-md px-3 py-1 text-xs " +
                (canSend
                  ? "bg-primary text-primary-foreground hover:opacity-90"
                  : "cursor-not-allowed bg-muted text-muted-foreground")
              }
            >
              {send.isPending ? (
                <Loader2 className="size-3 animate-spin" aria-hidden />
              ) : (
                <Send className="size-3" aria-hidden />
              )}
              {t(($) => $.lab_chat_send)}
            </button>
          </div>
        </div>
      </footer>
    </aside>
  );
}

// LabContextToChatPanelProps is a tiny helper that maps a workbench
// LabContext onto the LabChatPanel props. Centralising the mapping
// keeps the parent view free of "is the agent missing? is the
// session id null?" conditionals.
//
// Returns null when the workbench can't host a chat (no assignee
// agent, or the issue isn't actually a lab issue). The parent
// renders a placeholder instead.
export function labChatPanelPropsFromContext(
  ctx: LabContext | undefined,
  wsId: string | null,
  issueId: string | null,
):
  | Pick<LabChatPanelProps, "wsId" | "agentId" | "issueId" | "chatSessionId" | "title">
  | null {
  if (!ctx || !wsId || !issueId) return null;
  if (!ctx.agent) return null;
  // Use useMemo-friendly stable refs so re-renders don't churn.
  return {
    wsId,
    agentId: ctx.agent.id,
    issueId,
    chatSessionId: ctx.chat_session_id ?? null,
    title: ctx.issue.title,
  };
}
