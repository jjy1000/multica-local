// PythiaChatBox — 0.3.34.
//
// Live chat with the oracle (POST /chat on the loopback engine). The
// engine resolves the user message against the live world brief +
// the current prediction set, optionally routed through a single
// persona's voice. Each turn streams back as {answer, persona}.
//
// Mounted on the Pythia dashboard alongside the council/forecast
// panels; a future milestone will let the user bind a single issue
// to the chat so engine chat context can pull the issue's metadata.
//
// UX contract (mirrors the reference ChatBox.tsx):
//   - input + persona dropdown + send button
//   - scroll-back message list, user bubbles right / assistant left
//   - persona-colored attribution line on assistant messages
//   - "what-if" mode: messages starting with "/whatif" route through
//     POST /whatif instead of /chat so users can branch a scenario
//     from the same input box without leaving the page

import { useEffect, useRef, useState } from "react";
import { Send, Loader2, ChevronDown } from "lucide-react";
import { useT } from "@multica/views/i18n";

interface Msg {
  role: "user" | "assistant";
  content: string;
  by?: string;
  predictions?: Array<{ horizon: string; probability: number; statement: string; location?: string }>;
  /** Marker used to render the user bubble with a what-if accent when
   *  the user typed `/whatif …`. */
  whatif?: boolean;
}

interface PersonaOption {
  name: string;
  lens: string;
}

const PERSONA_COLOR: Record<string, string> = {
  Strategist: "var(--chart-1, #f43f5e)",
  Economist: "var(--chart-2, #f59e0b)",
  Naturalist: "var(--chart-3, #06b6d4)",
  Skeptic: "var(--chart-4, #8b5cf6)",
};

export function PythiaChatBox() {
  const { t } = useT("pythia");
  const [msgs, setMsgs] = useState<Msg[]>([
    {
      role: "assistant",
      content: t(($) => $.pythia.chat_intro),
    },
  ]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [roster, setRoster] = useState<PersonaOption[]>([]);
  const [speaker, setSpeaker] = useState<string>("");
  const [pickerOpen, setPickerOpen] = useState(false);
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [msgs, busy]);

  // Persona roster comes from the engine; on failure we silently fall
  // back to "the oracle answers itself" (empty speaker = oracle mode).
  useEffect(() => {
    let cancelled = false;
    void window.experimentalAPI.pythia
      .proxy<{ personas?: PersonaOption[] }>("/personas")
      .then((r) => {
        if (!cancelled) setRoster(r.personas ?? []);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  const send = async () => {
    const q = input.trim();
    if (!q || busy) return;
    setInput("");
    const history = msgs.slice(-6).map((m) => ({ role: m.role, content: m.content }));
    const isWhatif = q.toLowerCase().startsWith("/whatif");
    const scenario = isWhatif ? q.slice(7).trim() : null;
    setMsgs((prev) => [...prev, { role: "user", content: q, whatif: isWhatif }]);
    setBusy(true);
    try {
      if (scenario) {
        const r = await window.experimentalAPI.pythia.proxy<{
          scenario: string;
          narrative: string;
          predictions: Array<{ horizon: string; probability: number; statement: string; location?: string }>;
          error?: string;
        }>("/whatif", {
          method: "POST",
          body: JSON.stringify({ scenario }),
        });
        const predLines = (r.predictions ?? [])
          .map(
            (p) =>
              `• [${p.horizon}] ${Math.round(p.probability * 100)}% — ${p.statement}${p.location ? ` (${p.location})` : ""}`,
          )
          .join("\n");
        setMsgs((prev) => [
          ...prev,
          {
            role: "assistant",
            content:
              `${t(($) => $.pythia.chat_whatif_prefix)}: ${r.scenario ?? scenario}\n\n${r.narrative ?? ""}${predLines ? `\n\n${predLines}` : ""}`.trim() ||
              r.error ||
              t(($) => $.pythia.chat_no_response),
            predictions: r.predictions,
          },
        ]);
      } else {
        const r = await window.experimentalAPI.pythia.proxy<{
          answer: string;
          persona: string | null;
          error?: string;
        }>("/chat", {
          method: "POST",
          body: JSON.stringify({
            message: q,
            history,
            ...(speaker ? { persona: speaker } : {}),
          }),
        });
        setMsgs((prev) => [
          ...prev,
          {
            role: "assistant",
            content: r.answer ?? r.error ?? t(($) => $.pythia.chat_no_response),
            by: r.persona ?? undefined,
          },
        ]);
      }
    } catch {
      setMsgs((prev) => [
        ...prev,
        { role: "assistant", content: t(($) => $.pythia.chat_engine_offline) },
      ]);
    } finally {
      setBusy(false);
    }
  };

  const speakerColor = speaker
    ? PERSONA_COLOR[speaker] ?? "var(--muted-foreground)"
    : "var(--primary)";

  return (
    <section className="flex h-full min-h-[420px] flex-col rounded-lg border border-border bg-card/40">
      {/* Header */}
      <header className="flex items-baseline justify-between border-b border-border/60 px-4 py-2.5">
        <div className="flex flex-col gap-0.5">
          <h3 className="text-sm font-semibold text-foreground">
            {t(($) => $.pythia.chat_title)}
          </h3>
          <p className="text-[11px] text-muted-foreground">
            {t(($) => $.pythia.chat_subtitle)}
          </p>
        </div>
        <PersonaPicker
          open={pickerOpen}
          setOpen={setPickerOpen}
          roster={roster}
          speaker={speaker}
          setSpeaker={setSpeaker}
          color={speakerColor}
        />
      </header>

      {/* Message stream */}
      <div className="flex-1 overflow-y-auto px-4 py-3">
        <div className="flex flex-col gap-2.5">
          {msgs.map((m, i) => (
            <Bubble key={i} msg={m} />
          ))}
          {busy && (
            <div className="flex items-center gap-2 px-1 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
              {speaker
                ? t(($) => $.pythia.chat_thinking_persona).replace("{{persona}}", speaker)
                : t(($) => $.pythia.chat_consulting)}
            </div>
          )}
          <div ref={endRef} />
        </div>
      </div>

      {/* Input row */}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
        className="flex items-end gap-2 border-t border-border/60 px-4 py-3"
      >
        <textarea
          rows={1}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              void send();
            }
          }}
          placeholder={t(($) => $.pythia.chat_input_placeholder)}
          className="min-h-[36px] max-h-32 flex-1 resize-none rounded-md border border-input bg-background px-3 py-1.5 text-sm leading-relaxed text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-primary/60"
        />
        <button
          type="submit"
          disabled={busy || !input.trim()}
          className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground transition hover:bg-primary/90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          <Send className="size-3.5" />
          {t(($) => $.pythia.chat_send)}
        </button>
      </form>
      <p className="px-4 pb-2 text-[10px] text-muted-foreground/80">
        {t(($) => $.pythia.chat_hint_whatif)}
      </p>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function Bubble({ msg }: { msg: Msg }) {
  const { t } = useT("pythia");
  const isUser = msg.role === "user";
  const byColor = msg.by ? (PERSONA_COLOR[msg.by] ?? "var(--muted-foreground)") : undefined;

  return (
    <div
      className={`flex max-w-[88%] flex-col ${isUser ? "self-end" : "self-start"}`}
    >
      {!isUser && msg.by && (
        <div className="mb-0.5 flex items-center gap-1 px-1">
          <span className="text-[10px] font-semibold" style={{ color: byColor }}>
            {msg.by}
          </span>
          <span className="text-[10px] text-muted-foreground/70">
            {t(($) => $.pythia.chat_persona_label)}
          </span>
        </div>
      )}
      {isUser && msg.whatif && (
        <div className="mb-0.5 self-end px-1 text-[10px] font-medium text-fuchsia-400">
          {t(($) => $.pythia.chat_whatif_badge)}
        </div>
      )}
      <div
        className={`whitespace-pre-wrap px-3.5 py-2 text-[13px] leading-relaxed ${
          isUser
            ? "rounded-2xl rounded-br-md bg-primary/15 text-foreground ring-1 ring-primary/30"
            : "rounded-2xl rounded-bl-md bg-card/70 text-foreground ring-1 ring-border/60"
        }`}
      >
        {msg.content}
      </div>
      {!isUser && msg.predictions && msg.predictions.length > 0 && (
        <ul className="mt-1 flex flex-col gap-0.5 rounded border border-fuchsia-500/20 bg-fuchsia-500/5 px-3 py-1.5 text-[11px]">
          {msg.predictions.map((p, i) => (
            <li key={i} className="flex items-baseline justify-between gap-2">
              <span className="text-foreground/90">
                <span className="font-mono text-fuchsia-300">[{p.horizon}]</span>{" "}
                {p.statement}
                {p.location ? <span className="text-muted-foreground/80"> · {p.location}</span> : null}
              </span>
              <span className="font-mono text-fuchsia-300">
                {Math.round(p.probability * 100)}%
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function PersonaPicker({
  open,
  setOpen,
  roster,
  speaker,
  setSpeaker,
  color,
}: {
  open: boolean;
  setOpen: (v: boolean) => void;
  roster: PersonaOption[];
  speaker: string;
  setSpeaker: (v: string) => void;
  color: string;
}) {
  const { t } = useT("pythia");
  const speakerLens = roster.find((p) => p.name === speaker)?.lens;
  return (
    <div className="relative">
      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="inline-flex items-center gap-1.5 rounded border border-border bg-background px-2 py-1 text-[11px] text-foreground hover:bg-muted"
      >
        <span className="size-2 rounded-full" style={{ background: color }} />
        <span>{speaker || t(($) => $.pythia.chat_speaker_oracle)}</span>
        {speakerLens && (
          <span className="text-muted-foreground/80">· {speakerLens}</span>
        )}
        <ChevronDown className="size-3" />
      </button>
      {open && (
        <div className="absolute right-0 top-full z-10 mt-1 w-56 rounded-md border border-border bg-popover shadow-md">
          <button
            type="button"
            onClick={() => {
              setSpeaker("");
              setOpen(false);
            }}
            className={`flex w-full flex-col items-start gap-0.5 px-3 py-2 text-left text-xs hover:bg-muted ${
              speaker === "" ? "bg-muted" : ""
            }`}
          >
            <span className="font-medium text-foreground">
              {t(($) => $.pythia.chat_speaker_oracle)}
            </span>
            <span className="text-[10px] text-muted-foreground">
              {t(($) => $.pythia.chat_speaker_oracle_hint)}
            </span>
          </button>
          <div className="border-t border-border" />
          {roster.length === 0 ? (
            <p className="px-3 py-2 text-[10px] text-muted-foreground">
              {t(($) => $.pythia.chat_personas_unavailable)}
            </p>
          ) : (
            roster.map((p) => (
              <button
                key={p.name}
                type="button"
                onClick={() => {
                  setSpeaker(p.name);
                  setOpen(false);
                }}
                className={`flex w-full flex-col items-start gap-0.5 px-3 py-2 text-left text-xs hover:bg-muted ${
                  speaker === p.name ? "bg-muted" : ""
                }`}
              >
                <span className="font-medium" style={{ color: PERSONA_COLOR[p.name] ?? undefined }}>
                  {p.name}
                </span>
                {p.lens && (
                  <span className="text-[10px] text-muted-foreground">{p.lens}</span>
                )}
              </button>
            ))
          )}
        </div>
      )}
    </div>
  );
}