// Swarm interrupt bar — sticky bottom action bar for an active swarm.
//
// Mirrors mythos-view.tsx RunForm but is sticky (so the user can
// pause/cancel/inject a message at any moment without scrolling).
// Three actions:
//
//   - Pause    : orchestrator stops enqueueing new agent_task_queue
//                rows but lets in-flight roles complete.
//   - Resume   : orchestrator resumes enqueueing after a pause.
//   - Cancel   : orchestrator cancels all in-flight agent_task_queue
//                rows + flips swarm_run.status='aborted' immediately.
//   - Inject   : opens an inline prompt for a free-form message;
//                posts to /runs/{id}/interrupt with kind='inject_message'.
//
// All three go through the same backend endpoint
// (POST /api/experimental/swarm-topology/runs/{id}/interrupt); only
// `cancel` is synchronous (the handler flips status immediately);
// pause + resume + inject_message are async (the orchestrator's 30s
// tick picks them up).

import { useState } from "react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";

import { useT } from "../../i18n";

export interface SwarmInterruptBarProps {
  runId: string;
  status: string;
  isPaused?: boolean;
  onInterrupt: (
    kind: "pause" | "resume" | "cancel" | "inject_message",
    payload?: string,
  ) => Promise<void>;
  disabled?: boolean;
}

export function SwarmInterruptBar({
  runId,
  status,
  isPaused,
  onInterrupt,
  disabled,
}: SwarmInterruptBarProps) {
  const { t } = useT("swarm");
  const [injectOpen, setInjectOpen] = useState(false);
  const [injectText, setInjectText] = useState("");
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [busy, setBusy] = useState<"pause" | "resume" | "cancel" | "inject_message" | null>(null);

  const isTerminal =
    status === "completed" || status === "aborted" || status === "failed";

  async function fire(
    kind: "pause" | "resume" | "cancel" | "inject_message",
    payload?: string,
  ) {
    if (busy || disabled || isTerminal) return;
    if (kind === "cancel" && !cancelDialogOpen) {
      setCancelDialogOpen(true);
      return;
    }
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
          <span className="font-medium text-foreground">
            {t(($) => $.interrupt.label)}
          </span>
          <span aria-hidden="true">·</span>
          <span>
            {isTerminal
              ? t(($) => $.interrupt.terminal_hint, { status })
              : t(($) => $.interrupt.active_hint)}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={() => fire(isPaused ? "resume" : "pause")}
            disabled={busy !== null || isTerminal || disabled}
            aria-label={isPaused ? t(($) => $.interrupt.resume) : t(($) => $.interrupt.pause)}
            data-testid={isPaused ? "swarm-interrupt-resume" : "swarm-interrupt-pause"}
          >
            {busy === "pause" || busy === "resume"
              ? isPaused ? t(($) => $.interrupt.resuming) : t(($) => $.interrupt.pausing)
              : isPaused ? t(($) => $.interrupt.resume) : t(($) => $.interrupt.pause)}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={() => fire("cancel")}
            disabled={busy !== null || isTerminal || disabled}
            aria-label={t(($) => $.interrupt.cancel)}
            data-testid="swarm-interrupt-cancel"
          >
            {busy === "cancel" ? t(($) => $.interrupt.cancelling) : t(($) => $.interrupt.cancel)}
          </Button>
          <Button
            variant="default"
            size="sm"
            onClick={() => setInjectOpen((v) => !v)}
            disabled={busy !== null || isTerminal || disabled}
            aria-label={t(($) => $.interrupt.inject)}
            data-testid="swarm-interrupt-inject-toggle"
          >
            {injectOpen ? t(($) => $.interrupt.hide) : t(($) => $.interrupt.inject)}
          </Button>
        </div>
      </div>
      {injectOpen && !isTerminal ? (
        <div className="mt-3 space-y-1">
          <div className="flex gap-2">
            <Input
              type="text"
              value={injectText}
              onChange={(e) => setInjectText(e.target.value)}
              placeholder={t(($) => $.interrupt.inject_placeholder)}
              disabled={busy !== null}
              maxLength={2048}
              data-testid="swarm-interrupt-inject-input"
            />
            <Button
              size="sm"
              onClick={() => fire("inject_message", injectText)}
              disabled={busy !== null || !injectText.trim()}
              data-testid="swarm-interrupt-inject-send"
            >
              {busy === "inject_message" ? t(($) => $.interrupt.sending) : t(($) => $.interrupt.inject_send)}
            </Button>
          </div>
          <div className="text-right text-[10px] text-muted-foreground">
            {injectText.length}/2048
          </div>
        </div>
      ) : null}
      <AlertDialog open={cancelDialogOpen} onOpenChange={setCancelDialogOpen}>
        <AlertDialogContent data-testid="swarm-interrupt-cancel-dialog">
          <AlertDialogHeader>
            <AlertDialogTitle>{t(($) => $.interrupt.cancel_confirm_title)}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(($) => $.interrupt.cancel_confirm_description)}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={busy === "cancel"}>
              {t(($) => $.interrupt.cancel_keep_running)}
            </AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              disabled={busy === "cancel"}
              onClick={(e) => {
                e.preventDefault();
                void (async () => {
                  setCancelDialogOpen(false);
                  setBusy("cancel");
                  try {
                    await onInterrupt("cancel");
                  } finally {
                    setBusy(null);
                  }
                })();
              }}
              data-testid="swarm-interrupt-cancel-confirm"
            >
              {busy === "cancel" ? t(($) => $.interrupt.cancelling) : t(($) => $.interrupt.cancel_confirm_action)}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}