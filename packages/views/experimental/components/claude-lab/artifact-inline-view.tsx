"use client";

// artifact-inline-view — renders a research artifact's CONTENT in
// place, in the conversation that produced it (0.5.126).
//
// Why this exists: the issue timeline already carries the lab's
// top-level report comment (0.5.124 narrowed
// hides_deliverable_in_issue_timeline down to replies only), so
// re-rendering the deliverable here would duplicate the timeline —
// the exact double-delivery the pythia embed removed in b69c24364.
// What the embed was missing is the OTHER half of the delivery: the
// artifact payloads. A `report.md` showed as a filename plus a
// download button; a `results.csv` showed as "12 B". Reading any
// actual result meant leaving the issue entirely.
//
// AIPOCH open-science keeps generated markdown reports, CSV tables
// and figures reviewable "in place, beside the conversation" while
// collecting them in a project file library. This is the minimal
// multica-shaped half of that: same conversation, no detour, no new
// server surface.
//
// Content law: bytes come from the same authenticated per-artifact
// endpoint the download button already uses, are never written to
// disk, and never outlive the mount. Anything that fails to load
// falls back to the plain download row rather than an error state —
// the artifact is still reachable one click away.

import { useEffect, useState } from "react";
import { AlertTriangle, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import type { LabArtifactStub } from "@multica/core/api/schemas";
import { Markdown } from "../../../common/markdown";
import { useT } from "../../../i18n";

// A 3 GiB CSV is not a preview. Anything past this renders as the
// download row again rather than freezing the issue pane.
const MAX_INLINE_BYTES = 256 * 1024;
const MAX_CSV_ROWS = 20;
const MAX_CSV_COLS = 8;
const MAX_TEXT_CHARS = 20_000;

type ViewKind = "markdown" | "csv" | "json";

function viewKind(kind: string): ViewKind | null {
  const k = kind.toLowerCase();
  if (k === "md" || k === "markdown") return "markdown";
  if (k === "csv" || k === "tsv") return "csv";
  if (k === "json") return "json";
  return null;
}

/**
 * Which artifacts get a disclosure toggle. Anything that would not
 * benefit from it (png / html / binary) keeps the existing
 * click-to-download row untouched.
 */
export function hasInlineView(artifact: LabArtifactStub): boolean {
  return viewKind(artifact.kind) !== null && artifact.bytes <= MAX_INLINE_BYTES;
}

/**
 * Minimal RFC4180-ish CSV row splitter. Handles quoted fields with
 * embedded commas and doubled quotes; a real quoted newline inside a
 * cell is out of scope — research result tables do not use them and
 * mis-splitting one is cheaper than pulling in a parser.
 */
function splitCsvLine(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quoted = false;
  // charAt over `line[i]`: `noUncheckedIndexedAccess` types the latter as
  // `string | undefined`, which then poisons every `cur += ch` below.
  for (let i = 0; i < line.length; i += 1) {
    const ch = line.charAt(i);
    if (quoted) {
      if (ch === '"') {
        if (line.charAt(i + 1) === '"') {
          cur += '"';
          i += 1;
        } else {
          quoted = false;
        }
      } else {
        cur += ch;
      }
    } else if (ch === '"') {
      quoted = true;
    } else if (ch === "," || ch === "\t") {
      out.push(cur.trim());
      cur = "";
    } else {
      cur += ch;
    }
  }
  out.push(cur.trim());
  return out;
}

function CsvTable({ text, delimiter }: { text: string; delimiter: string }) {
  const lines = text.split(/\r?\n/).filter((l) => l.trim() !== "");
  // `noUncheckedIndexedAccess`: a destructured first element types as
  // `string | undefined`, so narrow explicitly instead of relying on
  // the length check above.
  const header = lines[0];
  if (header === undefined) return null;
  const body = lines.slice(1);
  const cols = splitCsvLine(header.replaceAll("\t", delimiter));
  return (
    <div className="max-h-64 overflow-auto rounded border border-sky-500/20">
      <table className="w-full border-collapse text-[11px]" data-testid="claude-embed-csv">
        <thead className="sticky top-0 bg-sky-500/10">
          <tr>
            {cols.slice(0, MAX_CSV_COLS).map((c, i) => (
              <th
                key={`${c}-${i}`}
                className="whitespace-nowrap border-b border-sky-500/20 px-2 py-1 text-left font-medium text-foreground"
              >
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {body.slice(0, MAX_CSV_ROWS).map((line, r) => (
            <tr key={`row-${r}`} className="even:bg-sky-500/5">
              {splitCsvLine(line.replaceAll("\t", delimiter))
                .slice(0, MAX_CSV_COLS)
                .map((c, ci) => (
                  <td
                    key={`cell-${r}-${ci}`}
                    className="max-w-48 truncate border-b border-sky-500/10 px-2 py-1 font-mono text-muted-foreground"
                  >
                    {c}
                  </td>
                ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function ArtifactInlineView({ artifact }: { artifact: LabArtifactStub }) {
  const { t } = useT("experimental");
  const kind = viewKind(artifact.kind);
  const [text, setText] = useState<string | null>(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    if (!kind) return;
    let cancelled = false;
    const ac = new AbortController();
    (async () => {
      try {
        const res = await api.rawRequest(
          `/api/experimental/claude-science-runtime/artifacts/${encodeURIComponent(artifact.id)}`,
          { signal: ac.signal },
        );
        if (!res.ok) throw new Error(`artifact ${res.status}`);
        const body = await res.text();
        if (cancelled) return;
        setText(body.slice(0, MAX_TEXT_CHARS));
      } catch {
        // Inline view is an enhancement — the parent row's download
        // button still works, so a failed preview is not an error
        // state worth shouting about.
        if (!cancelled) setFailed(true);
      }
    })();
    return () => {
      cancelled = true;
      ac.abort();
    };
  }, [artifact.id, kind]);

  if (!kind || artifact.bytes > MAX_INLINE_BYTES) return null;

  if (failed) {
    return (
      <p
        className="flex items-center gap-1.5 text-[10px] text-muted-foreground"
        data-testid="claude-embed-inline-error"
      >
        <AlertTriangle className="size-3 shrink-0" aria-hidden />
        {t(($) => $.claude_lab.embed_inline_error)}
      </p>
    );
  }

  if (text === null) {
    return (
      <p className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
        <Loader2 className="size-3 shrink-0 animate-spin" aria-hidden />
        {t(($) => $.claude_lab.embed_inline_loading)}
      </p>
    );
  }

  if (kind === "markdown") {
    return (
      <div
        className="max-h-96 overflow-auto rounded border border-sky-500/20 bg-background/60 p-3 text-xs"
        data-testid="claude-embed-inline-markdown"
      >
        <Markdown mode="full">{text}</Markdown>
      </div>
    );
  }

  if (kind === "csv") {
    return (
      <div data-testid="claude-embed-inline-csv-wrap">
        <CsvTable text={text} delimiter={artifact.kind.toLowerCase() === "tsv" ? "\t" : ","} />
      </div>
    );
  }

  return (
    <pre
      className="max-h-72 overflow-auto rounded border border-sky-500/20 bg-background/60 p-2 font-mono text-[10px] text-muted-foreground"
      data-testid="claude-embed-inline-json"
    >
      {text}
    </pre>
  );
}
