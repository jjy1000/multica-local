import { infiniteQueryOptions, queryOptions, type QueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { TaskMessagePayload } from "../types/events";

// NOTE on workspace scoping:
// `wsId` is used only as part of queryKey for cache isolation per workspace.
// The actual workspace context comes from ApiClient's X-Workspace-Slug header,
// which is set by the URL-driven [workspaceSlug] layout. Callers must ensure
// the header is in sync with the wsId they pass here — otherwise cache writes
// will be misattributed during a workspace switch race window.

export const chatKeys = {
  all: (wsId: string) => ["chat", wsId] as const,
  /** Full sessions list (active + archived); the dropdown splits locally. */
  sessions: (wsId: string) => [...chatKeys.all(wsId), "sessions"] as const,
  session: (wsId: string, id: string) => [...chatKeys.all(wsId), "session", id] as const,
  messages: (sessionId: string) => ["chat", "messages", sessionId] as const,
  messagesPage: (sessionId: string) => ["chat", "messages-page", sessionId] as const,
  pendingTask: (sessionId: string) => ["chat", "pending-task", sessionId] as const,
  /** Aggregate of in-flight chat tasks for the current user — FAB reads this. */
  pendingTasks: (wsId: string) => [...chatKeys.all(wsId), "pending-tasks"] as const,
  /** Per-task execution messages — shared with issue agent cards. */
  taskMessages: (taskId: string) => ["task-messages", taskId] as const,
};

const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function isTaskMessageTaskId(taskId: string | null | undefined): taskId is string {
  return typeof taskId === "string" && UUID_PATTERN.test(taskId);
}

export function chatSessionsOptions(wsId: string) {
  return queryOptions({
    queryKey: chatKeys.sessions(wsId),
    queryFn: () => api.listChatSessions({ status: "all" }),
    staleTime: Infinity,
  });
}

export function chatSessionOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: chatKeys.session(wsId, id),
    queryFn: () => api.getChatSession(id),
    enabled: !!id,
    staleTime: Infinity,
  });
}

export function chatMessagesOptions(sessionId: string) {
  return queryOptions({
    queryKey: chatKeys.messages(sessionId),
    queryFn: () => api.listChatMessages(sessionId),
    enabled: !!sessionId,
    staleTime: Infinity,
  });
}

export function chatMessagesPageOptions(sessionId: string, limit = 50) {
  return infiniteQueryOptions({
    queryKey: chatKeys.messagesPage(sessionId),
    queryFn: ({ pageParam }) =>
      api.listChatMessagesPage(sessionId, { before: pageParam, limit }),
    initialPageParam: null as { created_at: string; id: string } | null,
    getNextPageParam: (lastPage) =>
      lastPage.has_more ? lastPage.next_cursor ?? undefined : undefined,
    enabled: !!sessionId,
    staleTime: Infinity,
  });
}

/**
 * Pending task for a chat session — the "is something still running?" signal.
 * Refetched via WS invalidation in useRealtimeSync when chat:message / chat:done
 * / task:completed / task:failed arrive.
 */
export function pendingChatTaskOptions(sessionId: string) {
  return queryOptions({
    queryKey: chatKeys.pendingTask(sessionId),
    queryFn: () => api.getPendingChatTask(sessionId),
    enabled: !!sessionId,
    staleTime: Infinity,
  });
}

/**
 * Timeline for a single task — rendered by both the live chat view (while a
 * task is running) and AssistantMessage (for completed tasks). WS
 * `task:message` events seed this cache in real time via useRealtimeSync.
 */
export function taskMessagesOptions(taskId: string) {
  return queryOptions({
    queryKey: chatKeys.taskMessages(taskId),
    queryFn: () => api.listTaskMessages(taskId),
    enabled: isTaskMessageTaskId(taskId),
    staleTime: Infinity,
    // Every write to this cache — this fetch, a backfill, or a realtime batch —
    // folds into what is already there instead of replacing it. Without this a
    // response that resolves after a live frame was written would drop that
    // seq, and staleTime:Infinity means nothing would ever fetch it again.
    structuralSharing: (prev, next) =>
      unionTaskMessagesBySeq(
        prev as TaskMessagePayload[] | undefined,
        next as TaskMessagePayload[],
      ),
  });
}

/**
 * Merge task-message batches into one seq-ordered, seq-deduplicated list for
 * the shared `["task-messages", taskId]` cache. Existing entries win on
 * conflict, and the original array reference is preserved when nothing new
 * arrives so React Query observers don't re-render on duplicate events.
 *
 * Both the realtime `task:message` handler (a single payload) and the
 * transcript backfill (a full refetch) write this cache. Routing both through
 * one helper keeps a forced backfill from blind-replacing a seq the WebSocket
 * already delivered — and keeps a late WS event from being lost to an
 * in-flight backfill.
 */
export function mergeTaskMessagesBySeq(
  existing: readonly TaskMessagePayload[],
  incoming: readonly TaskMessagePayload[],
): TaskMessagePayload[] {
  if (incoming.length === 0) return existing as TaskMessagePayload[];
  const knownSeqs = new Set(existing.map((m) => m.seq));
  const fresh = incoming.filter((m) => !knownSeqs.has(m.seq));
  if (fresh.length === 0) return existing as TaskMessagePayload[];
  return [...existing, ...fresh].sort((a, b) => a.seq - b.seq);
}

/**
 * Union two task-message lists by seq, with `authoritative` winning on conflict.
 *
 * This is the rule every write to the `["task-messages", taskId]` cache goes
 * through, wired in as `structuralSharing` on the query itself so a fetch
 * result cannot replace the array wholesale.
 *
 * That matters because a normal query response and the realtime stream race:
 * the timeline is fetched on first open, and any frame that arrives while that
 * request is in flight is written to the cache before the response lands. A
 * plain replace would drop those seqs, and `staleTime: Infinity` means nothing
 * would ever refetch them — the gap would survive until a reload.
 *
 * Server data wins on conflict because the persisted row is the authority: a
 * broadcast copy may have been clipped for the fanout, and the fetched row
 * never is. Rows the response did not mention are kept rather than deleted —
 * a response snapshotted before a seq was persisted must not erase it.
 *
 * The `base` reference is returned unchanged when nothing differs, so an event
 * that adds nothing does not re-render every subscriber.
 */
export function unionTaskMessagesBySeq(
  base: readonly TaskMessagePayload[] | undefined,
  authoritative: readonly TaskMessagePayload[],
): TaskMessagePayload[] {
  if (!base || base.length === 0) {
    return [...authoritative].sort((a, b) => a.seq - b.seq);
  }

  const bySeq = new Map(base.map((m) => [m.seq, m]));
  let changed = false;
  for (const msg of authoritative) {
    if (bySeq.get(msg.seq) !== msg) {
      bySeq.set(msg.seq, msg);
      changed = true;
    }
  }
  if (!changed) return base as TaskMessagePayload[];
  return [...bySeq.values()].sort((a, b) => a.seq - b.seq);
}

/**
 * True when this client holds a timeline cache entry for `taskId` — i.e. the
 * task was opened at some point and has not been garbage-collected since.
 *
 * Deliberately NOT "a view is mounted right now". Observer count would be a
 * tighter bound but an unsafe one: with `staleTime: Infinity` a task that
 * briefly drops to zero observers (an unmount/remount while navigating) would
 * discard frames, and the remount would read the surviving cache as fresh and
 * never fetch them back. Entry presence has no such gap — either the entry is
 * there and keeps accumulating, or it is gone and the next open fetches the
 * whole timeline. The cost is a bounded tail: writes do not postpone the GC
 * timer, so an entry outlives its last viewer by at most one `gcTime`.
 *
 * The realtime layer uses this to decide whether a `task:message` frame is
 * worth caching. Every client in the workspace receives every run's frames,
 * but only a handful of runs are ever opened; without this gate every client
 * accumulates the transcript of runs its user will never look at (MUL-6396).
 *
 * Presence, not data: mounting a `useQuery` registers the entry before the
 * fetch resolves, so a frame that lands mid-backfill is still kept. Dropping
 * a frame for an unregistered task is safe — the row is persisted before it
 * is broadcast, so whoever opens the task next fetches it from the server.
 */
export function isTaskMessageTimelineHeld(
  qc: QueryClient,
  taskId: string,
): boolean {
  return qc.getQueryCache().find({ queryKey: chatKeys.taskMessages(taskId) }) !== undefined;
}

/**
 * Refetch a task's full timeline and fold it into the cache.
 *
 * Used when a broadcast frame arrived `truncated`: the live copy carries
 * clipped tool input/output and the full text exists only in the DB. Writing
 * the fetched rows straight in is enough — `unionTaskMessagesBySeq`, installed
 * as the query's `structuralSharing`, is what makes the fetched row replace the
 * clipped one while keeping any seq the response had not yet seen.
 */
export async function backfillTaskMessages(
  qc: QueryClient,
  taskId: string,
): Promise<void> {
  if (!isTaskMessageTaskId(taskId)) return;
  const msgs = await api.listTaskMessages(taskId);
  // The entry can be collected while this request is open. Writing anyway
  // would rebuild a timeline nothing is watching, re-arming another gcTime of
  // accumulation for a task the user has already closed.
  if (!isTaskMessageTimelineHeld(qc, taskId)) return;
  qc.setQueryData<TaskMessagePayload[]>(chatKeys.taskMessages(taskId), msgs);
}

/**
 * Aggregate of in-flight chat tasks for the current user in this workspace.
 * Drives the FAB "running" indicator while the chat window is minimised —
 * no per-session query is active then, so we need this roll-up.
 */
export function pendingChatTasksOptions(wsId: string) {
  return queryOptions({
    queryKey: chatKeys.pendingTasks(wsId),
    queryFn: () => api.listPendingChatTasks(),
    staleTime: Infinity,
  });
}
