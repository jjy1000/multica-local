import { useState } from "react";
import { FlaskConical, Play } from "lucide-react";
import { api } from "@multica/core/api";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useT } from "@multica/views/i18n";
import { IssueBreadcrumb } from "@multica/views/experimental/components";

// CodeCanvasView (0.5.18: real render + preview; replaces the static stub)
//
// code_canvas is a local stdlib-only service (apps/desktop/resources/
// code-canvas/run.sh) exposed through the same-origin proxy at
// /experimental/code-canvas/*. The view lets the user paste a snippet, pick
// a language, and render it into a self-contained HTML canvas view that is
// displayed in a sandboxed iframe.
//
// Network discipline (0.3.30 contract): every call goes through
// api.rawRequest — a bare fetch would resolve against the renderer origin
// and silently fail in the desktop app. The service itself is stdlib-only
// and deterministic; it renders code, it never executes it.
const LANGUAGES = [
  "python",
  "javascript",
  "typescript",
  "go",
  "rust",
  "java",
  "cpp",
  "c",
  "ruby",
  "bash",
  "sql",
  "html",
  "css",
  "json",
  "text",
];

const DEFAULT_SNIPPET = `# fibonacci\ndef fib(n: int) -> int:\n    """Return the n-th Fibonacci number."""\n    if n < 2:\n        return n\n    return fib(n - 1) + fib(n - 2)\n\nprint(fib(10))  # 55\n`;

export function CodeCanvasView() {
  const enabled = useExperimentalFlag("code_canvas", false);
  const { t } = useT("experimental");

  const [code, setCode] = useState(DEFAULT_SNIPPET);
  const [language, setLanguage] = useState("python");
  const [rendering, setRendering] = useState(false);
  const [html, setHtml] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const handleRender = async () => {
    if (rendering) return;
    setRendering(true);
    setError(null);
    try {
      const resp = await api.rawRequest("/experimental/code-canvas/render", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ code, language }),
      });
      if (!resp.ok) {
        const message = await resp.text().catch(() => `HTTP ${resp.status}`);
        throw new Error(message || `HTTP ${resp.status}`);
      }
      setHtml(await resp.text());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setRendering(false);
    }
  };

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background">
      <Header crumbLabs={t(($) => $.code_canvas.crumb_labs)} title={t(($) => $.code_canvas.title)} />
      <main className="mx-auto flex w-full max-w-4xl flex-col gap-6 px-6 py-8">
        {/* 0.5.81: code_canvas is workspace-level by design (single
            shared render service), so the breadcrumb renders the
            unbound-hint variant when there is no ?issue=. */}
        <IssueBreadcrumb infoHintWhenUnbound />
        <section className="flex flex-col gap-3">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            {t(($) => $.code_canvas.title)}
          </h1>
          <p className="text-sm leading-relaxed text-muted-foreground">
            {t(($) => $.code_canvas.intro)}
          </p>
        </section>

        {!enabled ? (
          <section className="rounded-xl border border-border bg-card p-6">
            <h2 className="text-base font-semibold text-foreground">
              {t(($) => $.code_canvas.not_enabled_title)}
            </h2>
            <p className="mt-1 text-sm text-muted-foreground">
              {t(($) => $.code_canvas.not_enabled_desc)}
            </p>
          </section>
        ) : (
          <>
            <section className="rounded-xl border border-border bg-card p-6">
              <div className="flex items-center justify-between gap-4">
                <label htmlFor="code-canvas-code" className="text-sm font-medium text-foreground">
                  {t(($) => $.code_canvas.input_label)}
                </label>
                <div className="flex items-center gap-2">
                  <label htmlFor="code-canvas-lang" className="text-xs text-muted-foreground">
                    {t(($) => $.code_canvas.language_label)}
                  </label>
                  <select
                    id="code-canvas-lang"
                    value={language}
                    onChange={(e) => setLanguage(e.target.value)}
                    className="h-8 rounded-md border border-border bg-background px-2 text-xs text-foreground"
                  >
                    {LANGUAGES.map((l) => (
                      <option key={l} value={l}>
                        {l}
                      </option>
                    ))}
                  </select>
                </div>
              </div>
              <textarea
                id="code-canvas-code"
                value={code}
                onChange={(e) => setCode(e.target.value)}
                placeholder={t(($) => $.code_canvas.input_placeholder)}
                spellCheck={false}
                className="mt-3 h-64 w-full resize-y rounded-lg border border-border bg-background p-3 font-mono text-xs leading-relaxed text-foreground outline-none focus:border-primary"
              />
              <div className="mt-4 flex items-center gap-3">
                <button
                  type="button"
                  onClick={() => void handleRender()}
                  disabled={rendering || code.trim().length === 0}
                  className="inline-flex h-9 items-center gap-2 rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
                >
                  <Play className="size-3.5" aria-hidden />
                  {rendering ? t(($) => $.code_canvas.rendering) : t(($) => $.code_canvas.render_button)}
                </button>
                {error && (
                  <span className="text-xs text-destructive">
                    {t(($) => $.code_canvas.error_render, { error })}
                  </span>
                )}
              </div>
            </section>

            <section className="flex flex-col gap-2">
              <h2 className="text-sm font-semibold text-foreground">
                {t(($) => $.code_canvas.result_title)}
              </h2>
              {html ? (
                <iframe
                  srcDoc={html}
                  sandbox=""
                  title={t(($) => $.code_canvas.result_title)}
                  className="h-[65vh] w-full rounded-xl border border-border bg-background"
                />
              ) : (
                <div className="rounded-xl border border-dashed border-border bg-card/50 p-10 text-center text-xs text-muted-foreground">
                  {t(($) => $.code_canvas.empty_hint)}
                </div>
              )}
            </section>
          </>
        )}
      </main>
    </div>
  );
}

function Header({ crumbLabs, title }: { crumbLabs: string; title: string }) {
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">{crumbLabs}</span>
        <span className="text-muted-foreground/60">/</span>
        <span>{title}</span>
      </div>
    </header>
  );
}
