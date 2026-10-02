"use client";

// pythia-followup-chat — the 追问 tab (0.5.111, SocialSim Step5
// "deep interaction" counterpart). Talk to the oracle (or one council
// persona) about THIS issue's deliberation: the server grounds the
// answer in the issue context + the latest run's conclusion report, the
// engine answers through its /chat endpoint. Conversations live in
// component state — they are working Q&A, not persisted deliverables
// (the report comment owns that role).
//
// 0.5.131 adds the second MiroFish Step5 mode: 全员问卷 (batch survey)
// — one question fanned out to every picked persona in parallel, each
// answer a card (pending spinner → answer / per-card failure). The
// fan-out is client-side over the SAME /chat endpoint; no server
// change. Survey blocks are newest-first component state.

import { useRef, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { motion, useReducedMotion } from "motion/react";
import { Loader2, Send, Users } from "lucide-react";
import { UI_EASE_OUT, UI_MOTION_DURATION } from "@multica/ui/lib/motion";
import { api, parseWithFallback } from "@multica/core/api";
import { PythiaChatAnswerSchema } from "@multica/core/api/schemas";
import { useT } from "../../../i18n";

// Mirrors the engine's PERSONAS lens roster (swarm.py) — name-only here;
// the engine resolves the lens. "" is the Oracle itself.
const CHAT_PERSONAS = ["", "Strategist", "Economist", "Naturalist", "Skeptic"] as const;
type ChatPersona = (typeof CHAT_PERSONAS)[number];

interface ChatMessage {
  role: "user" | "assistant";
  content: string;
  persona?: string | null;
}

interface SurveyAnswer {
  persona: string;
  label: string;
  state: "pending" | "done" | "error";
  answer?: string;
}
interface SurveyBlock {
  id: number;
  q: string;
  answers: SurveyAnswer[];
}

export function PythiaFollowUpChat({ issueId }: { issueId: string }) {
  const { t } = useT("experimental");
  const reduceMotion = useReducedMotion() ?? false;
  const [mode, setMode] = useState<"single" | "survey">("single");
  const [persona, setPersona] = useState<ChatPersona>("");
  const [draft, setDraft] = useState("");
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [engineDown, setEngineDown] = useState(false);

  // Survey state (MiroFish batch-interview port).
  const [surveyDraft, setSurveyDraft] = useState("");
  const [surveyPicked, setSurveyPicked] = useState<string[]>([...CHAT_PERSONAS]);
  const [surveys, setSurveys] = useState<SurveyBlock[]>([]);
  const [surveyBusy, setSurveyBusy] = useState(false);
  const surveySeq = useRef(0);

  const personaLabel = (p: string) => p || t(($) => $.pythia_lab.chat_persona_oracle);

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

  const askSurvey = async () => {
    const q = surveyDraft.trim();
    if (!q || surveyBusy || surveyPicked.length === 0) return;
    setSurveyBusy(true);
    const id = ++surveySeq.current;
    const answers: SurveyAnswer[] = surveyPicked.map((p) => ({
      persona: p,
      label: personaLabel(p),
      state: "pending" as const,
    }));
    setSurveys((s) => [{ id, q, answers }, ...s]);
    setSurveyDraft("");
    const patch = (persona: string, next: Partial<SurveyAnswer>) => {
      setSurveys((s) =>
        s.map((b) =>
          b.id === id
            ? {
                ...b,
                answers: b.answers.map((a) => (a.persona === persona ? { ...a, ...next } : a)),
              }
            : b,
        ),
      );
    };
    await Promise.all(
      answers.map(async (a) => {
        try {
          const r = await api.rawRequest("/api/experimental/pythia-oracle/chat", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ issue_id: issueId, message: q, persona: a.persona || undefined, history: [] }),
          });
          if (!r.ok) throw new Error(`chat ${r.status}`);
          const raw: unknown = await r.json();
          const parsed = parseWithFallback<{ answer: string; persona: string | null }>(
            raw,
            PythiaChatAnswerSchema,
            { answer: "", persona: null },
            { endpoint: "POST /api/experimental/pythia-oracle/chat" },
          );
          patch(a.persona, { state: "done", answer: parsed.answer });
        } catch {
          patch(a.persona, { state: "error" });
        }
      }),
    );
    setSurveyBusy(false);
  };

  return (
    <div className="space-y-2" data-testid="pythia-followup-chat">
      <div className="flex items-center gap-1.5">
        <div className="flex items-center gap-0.5 rounded-md border border-border bg-card/50 p-0.5">
          <button
            type="button"
            onClick={() => setMode("single")}
            aria-pressed={mode === "single"}
            className={
              "rounded px-1.5 py-0.5 text-[10px] font-medium transition-colors " +
              (mode === "single"
                ? "bg-purple-500/15 text-purple-700 dark:text-purple-300"
                : "text-muted-foreground hover:text-foreground")
            }
          >
            {t(($) => $.pythia_lab.chat_mode_single)}
          </button>
          <button
            type="button"
            onClick={() => setMode("survey")}
            aria-pressed={mode === "survey"}
            data-testid="pythia-survey-toggle"
            className={
              "rounded px-1.5 py-0.5 text-[10px] font-medium transition-colors " +
              (mode === "survey"
                ? "bg-purple-500/15 text-purple-700 dark:text-purple-300"
                : "text-muted-foreground hover:text-foreground")
            }
          >
            {t(($) => $.pythia_lab.chat_mode_survey)}
          </button>
        </div>
        {mode === "single" && (
          <select
            value={persona}
            onChange={(e) => setPersona(e.target.value as ChatPersona)}
            aria-label={t(($) => $.pythia_lab.chat_persona_label)}
            className="h-7 rounded-md border border-border bg-background px-1.5 text-[11px] text-foreground"
          >
            {CHAT_PERSONAS.map((p) => (
              <option key={p || "oracle"} value={p}>
                {personaLabel(p)}
              </option>
            ))}
          </select>
        )}
        {engineDown && (
          <span className="text-[10px] text-amber-600 dark:text-amber-400">
            {t(($) => $.pythia_lab.chat_engine_down)}
          </span>
        )}
      </div>

      {mode === "single" ? (
        <>
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
        </>
      ) : (
        <div className="space-y-2" data-testid="pythia-survey-panel">
          <div className="flex items-end gap-1.5">
            <textarea
              value={surveyDraft}
              onChange={(e) => setSurveyDraft(e.target.value)}
              placeholder={t(($) => $.pythia_lab.survey_placeholder)}
              rows={2}
              className="min-h-0 flex-1 resize-y rounded-md border border-border bg-background p-2 text-[11px] text-foreground outline-none focus:border-primary"
            />
            <button
              type="button"
              onClick={() => void askSurvey()}
              disabled={surveyBusy || !surveyDraft.trim() || surveyPicked.length === 0}
              data-testid="pythia-survey-ask"
              className="inline-flex h-8 shrink-0 items-center gap-1 rounded-md bg-purple-600 px-2 text-[11px] font-medium text-white hover:bg-purple-500 disabled:opacity-50"
            >
              {surveyBusy ? (
                <span className="size-3 animate-spin rounded-full border-2 border-white border-t-transparent" />
              ) : (
                <Users className="size-3.5" aria-hidden />
              )}
              {t(($) => $.pythia_lab.survey_ask)}
            </button>
          </div>
          <div className="flex flex-wrap items-center gap-1" aria-label={t(($) => $.pythia_lab.survey_pick_hint)}>
            {CHAT_PERSONAS.map((p) => {
              const picked = surveyPicked.includes(p);
              return (
                <button
                  key={p || "oracle"}
                  type="button"
                  aria-pressed={picked}
                  onClick={() =>
                    setSurveyPicked((cur) =>
                      cur.includes(p) ? cur.filter((x) => x !== p) : [...cur, p],
                    )
                  }
                  className={
                    "rounded-full border px-2 py-0.5 text-[10px] transition-colors " +
                    (picked
                      ? "border-purple-500/50 bg-purple-500/10 text-purple-700 dark:text-purple-300"
                      : "border-border text-muted-foreground hover:text-foreground")
                  }
                >
                  {personaLabel(p)}
                </button>
              );
            })}
          </div>
          {surveys.map((block) => (
            <div
              key={block.id}
              className="space-y-1.5 rounded-md border border-border bg-card/40 p-2"
              data-testid="pythia-survey-block"
            >
              <p className="text-[11px] font-medium text-foreground/90">{block.q}</p>
              <div className="grid grid-cols-1 gap-1.5 md:grid-cols-2">
                {block.answers.map((a, i) => (
                  <motion.div
                    key={a.persona || "oracle"}
                    initial={reduceMotion ? false : { opacity: 0, y: 4 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{
                      duration: UI_MOTION_DURATION.fast,
                      delay: reduceMotion ? 0 : Math.min(i * 0.04, 0.2),
                      ease: UI_EASE_OUT,
                    }}
                    className="rounded border border-border/70 bg-background px-2 py-1.5"
                    data-testid="pythia-survey-answer"
                    data-persona={a.persona || "oracle"}
                    data-state={a.state}
                  >
                    <span className="mb-0.5 block font-mono text-[9px] text-purple-500">{a.label}</span>
                    {a.state === "pending" ? (
                      <span className="flex items-center gap-1 text-[10px] text-muted-foreground">
                        <Loader2 className="size-3 animate-spin" aria-hidden />
                        {t(($) => $.pythia_lab.survey_pending)}
                      </span>
                    ) : a.state === "error" ? (
                      <span className="text-[10px] text-amber-600 dark:text-amber-400">
                        {t(($) => $.pythia_lab.survey_error)}
                      </span>
                    ) : (
                      <span className="whitespace-pre-wrap text-[10px] leading-relaxed text-foreground/90">
                        {a.answer}
                      </span>
                    )}
                  </motion.div>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
