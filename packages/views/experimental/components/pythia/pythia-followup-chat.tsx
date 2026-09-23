"use client";

// pythia-followup-chat — the 追问 tab (0.5.111, SocialSim Step5
// "deep interaction" counterpart). Talk to the oracle (or one council
// persona) about THIS issue's deliberation: the server grounds the
// answer in the issue context + the latest run's conclusion report, the
// engine answers through its /chat endpoint. Conversations live in
// component state — they are working Q&A, not persisted deliverables
// (the report comment owns that role).

import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Loader2, Send } from "lucide-react";
import { api, parseWithFallback } from "@multica/core/api";
import { PythiaChatAnswerSchema } from "@multica/core/api/schemas";
import { useT } from "../../../i18n";

// Mirrors the engine's PERSONAS lens roster (swarm.py) — name-only here;
// the engine resolves the lens.
const CHAT_PERSONAS = ["", "Strategist", "Economist", "Naturalist", "Skeptic"] as const;

interface ChatMessage {
  role: "user" | "assistant";
  content: string;
  persona?: string | null;
}

export function PythiaFollowUpChat({ issueId }: { issueId: string }) {
  const { t } = useT("experimental");
  const [persona, setPersona] = useState<(typeof CHAT_PERSONAS)[number]>("");
  const [draft, setDraft] = useState("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [engineDown, setEngineDown] = useState(false);

  const send = useMutation({
    mutationFn: async (message: string) => {
      const history = messages.slice(-6).map((m) => ({ role: m.role, content: m.content }));
      const r = await api.rawRequest("/api/experimental/pythia-oracle/chat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ issue_id: issueId, message, persona: persona || undefined, history }),
      });
      if (r.status === 503) {
        setEngineDown(true);
        throw new Error("engine down");
      }
      setEngineDown(false);
      if (!r.ok) throw new Error(`chat ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<{ answer: string; persona: string | null }>(
        raw,
        PythiaChatAnswerSchema,
        { answer: "", persona: null },
        { endpoint: "POST /api/experimental/pythia-oracle/chat" },
      );
    },
    onSuccess: (res) => {
      setMessages((m) => [...m, { role: "assistant", content: res.answer, persona: res.persona }]);
    },
    onError: () => {
      setMessages((m) => [...m, { role: "assistant", content: t(($) => $.pythia_lab.chat_error) }]);
    },
  });

  const submit = () => {
    const message = draft.trim();
    if (!message || send.isPending) return;
    setMessages((m) => [...m, { role: "user", content: message }]);
    setDraft("");
    send.mutate(message);
  };

  return (
    <div className="space-y-2" data-testid="pythia-followup-chat">
      <div className="flex items-center gap-1.5">
        <select
          value={persona}
          onChange={(e) => setPersona(e.target.value as (typeof CHAT_PERSONAS)[number])}
          aria-label={t(($) => $.pythia_lab.chat_persona_label)}
          className="h-7 rounded-md border border-border bg-background px-1.5 text-[11px] text-foreground"
        >
          {CHAT_PERSONAS.map((p) => (
            <option key={p || "oracle"} value={p}>
              {p || t(($) => $.pythia_lab.chat_persona_oracle)}
            </option>
          ))}
        </select>
        {engineDown && (
          <span className="text-[10px] text-amber-600 dark:text-amber-400">
            {t(($) => $.pythia_lab.chat_engine_down)}
          </span>
        )}
      </div>

      <div className="max-h-56 space-y-1.5 overflow-y-auto rounded-md border border-border p-2">
        {messages.length === 0 && (
          <p className="text-[11px] text-muted-foreground">{t(($) => $.pythia_lab.chat_empty)}</p>
        )}
        {messages.map((m, i) => (
          <div
            key={i}
            className={
              m.role === "user"
                ? "ml-6 rounded-md bg-primary/10 px-2 py-1 text-[11px] text-foreground"
                : "mr-2 rounded-md border border-border bg-background px-2 py-1 text-[11px] leading-relaxed text-foreground/90"
            }
          >
            {m.role === "assistant" && m.persona && (
              <span className="mb-0.5 block font-mono text-[9px] text-purple-500">{m.persona}</span>
            )}
            <span className="whitespace-pre-wrap">{m.content}</span>
          </div>
        ))}
        {send.isPending && (
          <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <Loader2 className="size-3 animate-spin" aria-hidden />
            {t(($) => $.pythia_lab.chat_thinking)}
          </div>
        )}
      </div>

      <div className="flex items-end gap-1.5">
        <textarea
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              submit();
            }
          }}
          placeholder={t(($) => $.pythia_lab.chat_placeholder)}
          rows={2}
          className="min-h-0 flex-1 resize-y rounded-md border border-border bg-background p-2 text-[11px] text-foreground outline-none focus:border-primary"
        />
        <button
          type="button"
          onClick={submit}
          disabled={send.isPending || !draft.trim()}
          aria-label={t(($) => $.pythia_lab.chat_send)}
          className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground disabled:opacity-50"
        >
          <Send className="size-3.5" aria-hidden />
        </button>
      </div>
      <p className="text-right text-[9px] text-muted-foreground">
        {t(($) => $.pythia_lab.chat_send_hint)}
      </p>
    </div>
  );
}
