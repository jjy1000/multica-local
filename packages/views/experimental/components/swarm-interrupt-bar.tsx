// Swarm interrupt bar — sticky bottom action bar for an active swarm.
//
// Mirrors mythos-view.tsx RunForm but is sticky (so the user can
// pause/cancel/inject a message at any moment without scrolling).
// Three actions:
//
//   - Pause    : orchestrator stops enqueueing new agent_task_queue
//                rows but lets in-flight roles complete.
//   - Cancel   : orchestrator cancels all in-flight agent_task_queue
//                rows + flips swarm_run.status='aborted' immediately.
//   - Inject   : opens an inline prompt for a free-form message;
//                posts to /runs/{id}/interrupt with kind='inject_message'.
//
// All three go through the same backend endpoint
// (POST /api/experimental/swarm-topology/runs/{id}/interrupt); only
// `cancel` is synchronous (the handler flips status immediately);
// pause + inject_message are async (the orchestrator's 30s tick
// picks them up).

import { useState } from "react";

import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";

export interface SwarmInterruptBarProps {
  runId: string;
  status: string;
  onInterrupt: (kind: "pause" | "cancel" | "inject_message", payload?: string) => Promise<void>;
  disabled?: boolean;
}

export function SwarmInterruptBar({
  runId,
  status,
  onInterrupt,
  disabled,
}: SwarmInterruptBarProps) {
  const [injectOpen, setInjectOpen] = useState(false);
  const [injectText, setInjectText] = useState("");
  const [busy, setBusy] = useState<"pause" | "cancel" | "inject_message" | null>(null);

  const isTerminal =
    status === "completed" || status === "aborted" || status === "failed";

  async function fire(kind: "pause" | "cancel" | "inject_message", payload?: string) {
    if (busy || disabled || isTerminal) return;
    setBusy(kind);
    try {
      await onInterrupt(kind, payload);
      if (kind === "inject_message") {
        setInjectText("");
        setInjectOpen(false);
      }
    } finally {
      setBusy(null);
    }
  }

  return (
    <div
      className="sticky bottom-0 z-10 -mx-4 border-t bg-background/95 px-4 py-3 backdrop-blur supports-[backdrop-filter]:bg-background/80"
      data-testid="swarm-interrupt-bar"
      data-run-id={runId}
      data-status={status}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <span className="font-medium text-foreground">Swarm controls</span>
          <span aria-hidden="true">·</span>
          <span>
            {isTerminal
              ? `Run ${status}; controls disabled.`
              : `Phase-based dispatch; pause lets in-flight roles complete.`}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => fire("pause")}
            disabled={busy !== null || isTerminal || disabled}
            data-testid="swarm-interrupt-pause"
          >
            {busy === "pause" ? "Pausing…" : "Pause"}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => fire("cancel")}
            disabled={busy !== null || isTerminal || disabled}
            data-testid="swarm-interrupt-cancel"
          >
            {busy === "cancel" ? "Cancelling…" : "Cancel"}
          </Button>
          <Button
            variant="default"
            size="sm"
            onClick={() => setInjectOpen((v) => !v)}
            disabled={busy !== null || isTerminal || disabled}
            data-testid="swarm-interrupt-inject-toggle"
          >
            {injectOpen ? "Hide" : "Inject message"}
          </Button>
        </div>
      </div>
      {injectOpen && !isTerminal ? (
        <div className="mt-3 flex gap-2">
          <Input
            type="text"
            value={injectText}
            onChange={(e) => setInjectText(e.target.value)}
            placeholder="Tell the swarm what to do next…"
            disabled={busy !== null}
            data-testid="swarm-interrupt-inject-input"
          />
          <Button
            size="sm"
            onClick={() => fire("inject_message", injectText)}
            disabled={busy !== null || !injectText.trim()}
            data-testid="swarm-interrupt-inject-send"
          >
            {busy === "inject_message" ? "Sending…" : "Send"}
          </Button>
        </div>
      ) : null}
    </div>
  );
}