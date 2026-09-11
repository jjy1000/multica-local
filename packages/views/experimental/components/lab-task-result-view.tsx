"use client";

// LabTaskResultView — shared renderer for one lab run's structured
// deliverables (LabTaskBrief.result_*). Single source of truth for
// "what a run's result looks like" across surfaces:
//
//   - desktop claude-lab-view PlanTimeline rows + latest-result panel
//   - issue-side LabDeliverableSummary expandable result rendering
//   - (LabOutputPanel keeps its own linked variant — its per-item
//     "在实验室查看 →" footers are surface-specific)
//
// Rendering order mirrors the 0.3.40 v2 workbench contract: charts and
// artifacts first (highest information density), then predictions
// (probability bars), then code blocks, and the textual summary as the
// footer. `interactive-chart` envelopes render as real recharts via the
// shared InteractiveChartEnvelope — not the JSON code dump the generic
// ArtifactRenderer path used to produce.
//
// Agent payloads are untrusted: SVG goes through safeSvgMarkup, image
// srcs through safeImageSrc, and the legacy `html` kind degrades to a
// download link (same 0.3.42 hardening policy as the desktop view).

import { useState } from "react";
import { Download } from "lucide-react";
import type { LabAttachment, LabCodeBlock, LabPrediction, LabTaskBrief } from "@multica/core/types/api";
import { useT } from "../../i18n";
import { InteractiveChartEnvelope } from "./interactive-chart-envelope";
import { safeHrefUrl, safeImageSrc, safeSvgMarkup } from "./lab-attachment-sanitize";

export function labTaskHasStructuredDeliverables(task: LabTaskBrief): boolean {
  return (
    (task.result_attachments?.length ?? 0) > 0 ||
    (task.result_predictions?.length ?? 0) > 0 ||
    (task.result_code_blocks?.length ?? 0) > 0
  );
}

function clamp01(v: number): number {
  return Math.max(0, Math.min(1, v));
}

function LabAttachmentCard({
  attachment,
  chartHeightClass,
}: {
  attachment: LabAttachment;
  chartHeightClass: string;
}) {
  const { t } = useT("experimental");
  const kind = attachment.kind;
  if (kind === "interactive-chart" && attachment.data) {
    return (
      <figure className="overflow-hidden rounded-md border border-border bg-background/40">
        <InteractiveChartEnvelope data={attachment.data} heightClass={chartHeightClass} />
        {attachment.name ? (
          <figcaption className="border-t border-border px-2 py-1 text-[10px] text-muted-foreground">
            {attachment.name}
          </figcaption>
        ) : null}
      </figure>
    );
  }
  if (kind === "svg" && typeof attachment.data === "string") {
    return (
      <figure className="overflow-hidden rounded-md border border-border bg-background/40">
        <div
          className="max-h-72 overflow-auto"
          dangerouslySetInnerHTML={{ __html: safeSvgMarkup(attachment.data) }}
        />
        {attachment.name ? (
          <figcaption className="border-t border-border px-2 py-1 text-[10px] text-muted-foreground">
            {attachment.name}
          </figcaption>
        ) : null}
      </figure>
    );
  }
  if (kind === "png" || kind === "jpg" || kind === "jpeg" || kind === "webp" || kind === "gif") {
    const safeSrc = safeImageSrc(attachment);
    return (
      <figure className="overflow-hidden rounded-md border border-border bg-background/40">
        {safeSrc ? (
          <img
            src={safeSrc}
            alt={attachment.name ?? ""}
            className="block max-h-72 w-full object-contain"
          />
        ) : (
          <div className="flex h-32 items-center justify-center text-xs text-muted-foreground">
            {t(($) => $.lab_output_panel.image_no_source)}
          </div>
        )}
        {attachment.name ? (
          <figcaption className="border-t border-border px-2 py-1 text-[10px] text-muted-foreground">
            {attachment.name}
          </figcaption>
        ) : null}
      </figure>
    );
  }
  // Text-y kinds — render inline so the result stays scannable.
  if (
    (kind === "md" || kind === "csv" || kind === "json" || kind === "txt" || kind === "log") &&
    typeof attachment.data === "string"
  ) {
    return (
      <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-muted/30 p-2 font-mono text-[11px]">
        {attachment.data.slice(0, 2000)}
      </pre>
    );
  }
  // `html` kind was dropped from the server-side allowlist in 0.3.42 —
  // legacy rows degrade to a download link rather than rendering
  // interactive content.
  const safeDownloadHref = attachment.url ? safeHrefUrl(attachment.url) : null;
  return (
    <div className="rounded-md border border-border bg-background/40 p-2 text-[11px] text-muted-foreground">
      <span className="font-mono">{attachment.kind ?? "attachment"}</span>
      {attachment.name ? <span className="ml-2">{attachment.name}</span> : null}
      {safeDownloadHref ? (
          <a
            href={safeDownloadHref}
            className="ml-2 inline-flex items-center gap-0.5 underline"
            target="_blank"
            rel="noopener noreferrer"
          >
            <Download className="size-3" aria-hidden />
            {t(($) => $.lab_output_panel.download)}
          </a>
      ) : null}
    </div>
  );
}

function LabPredictionsView({ predictions }: { predictions: LabPrediction[] }) {
  return (
    <div className="space-y-1.5">
      {predictions.map((p, i) => {
        const pct = Math.round(clamp01(p.probability) * 100);
        return (
          <div key={i} className="space-y-0.5">
            <div className="flex items-baseline justify-between gap-2 text-[11px]">
              <span className="truncate text-foreground/90">
                {p.round > 0 ? `R${p.round} · ` : ""}
                {p.scenario}
              </span>
              <span className="shrink-0 font-medium text-foreground">{pct}%</span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-purple-500 transition-[width] duration-500 ease-out"
                style={{ width: `${pct}%` }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}

function LabCodeBlocksView({ blocks }: { blocks: LabCodeBlock[] }) {
  return (
    <div className="space-y-2">
      {blocks.map((b, i) => (
        <div key={i} className="overflow-hidden rounded-md border border-border bg-muted/20">
          {(b.filename || b.language) && (
            <div className="flex items-center gap-2 border-b border-border bg-background/40 px-2 py-1 text-[10px] text-muted-foreground">
              {b.language && <span className="rounded bg-secondary px-1.5 py-0.5 font-mono">{b.language}</span>}
              {b.filename && <span className="truncate">{b.filename}</span>}
            </div>
          )}
          <pre
            className={
              "overflow-auto whitespace-pre-wrap break-all p-2 font-mono text-[11px] " +
              (b.code.split("\n").length > 50 ? "max-h-72" : "")
            }
          >
            {b.code}
          </pre>
        </div>
      ))}
    </div>
  );
}

export function LabTaskResultView({
  task,
  includeSummary = true,
  chartHeightClass = "h-44",
}: {
  task: LabTaskBrief;
  // The issue-side summary card already shows a clamped summary line —
  // pass false when the caller renders its own summary header.
  includeSummary?: boolean;
  // interactive-chart canvas height (taller on the lab workbench panel).
  chartHeightClass?: string;
}) {
  const { t } = useT("experimental");
  const [codeExpanded, setCodeExpanded] = useState(false);
  const attachments = task.result_attachments ?? [];
  const predictions = task.result_predictions ?? [];
  const codeBlocks = task.result_code_blocks ?? [];
  const hasStructured =
    attachments.length > 0 || predictions.length > 0 || codeBlocks.length > 0;

  // Older runs pre-dating the structured-emitter convention: fall back to
  // summary / error / explicit empty note so a run row never renders blank.
  if (!hasStructured) {
    if (includeSummary && task.result_summary) {
      return (
        <p className="whitespace-pre-wrap text-xs leading-relaxed text-foreground">
          {task.result_summary}
        </p>
      );
    }
    if (task.error) {
      return <p className="text-xs text-destructive">{task.error.slice(0, 200)}</p>;
    }
    return (
      <p className="text-xs italic text-muted-foreground">
        {t(($) => $.lab_output_panel.no_output)}
      </p>
    );
  }

  // Code blocks can be long — collapse to the first two behind an
  // expander so charts stay the visual lead (Claude Science-style:
  // result first, code on demand).
  const visibleCode = codeExpanded ? codeBlocks : codeBlocks.slice(0, 2);
  const hiddenCodeCount = codeBlocks.length - visibleCode.length;

  return (
    <div className="flex flex-col gap-2.5">
      {attachments.length > 0 ? (
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
          {attachments.map((a, i) => (
            <LabAttachmentCard
              key={`${a.kind}-${a.name ?? i}`}
              attachment={a}
              chartHeightClass={chartHeightClass}
            />
          ))}
        </div>
      ) : null}
      {predictions.length > 0 ? (
        <div className="space-y-1">
          <p className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.lab_output_panel.predictions_label)}
          </p>
          <LabPredictionsView predictions={predictions} />
        </div>
      ) : null}
      {visibleCode.length > 0 ? (
        <div className="space-y-1">
          <p className="text-[11px] font-medium text-muted-foreground">
            {t(($) => $.lab_output_panel.code_blocks_label)}
          </p>
          <LabCodeBlocksView blocks={visibleCode} />
          {hiddenCodeCount > 0 && (
            <button
              type="button"
              onClick={() => setCodeExpanded(true)}
              className="text-[11px] text-muted-foreground underline hover:text-foreground"
            >
              {t(($) => $.lab_output_panel.show_more_code, { count: hiddenCodeCount })}
            </button>
          )}
        </div>
      ) : null}
      {includeSummary && task.result_summary ? (
        <p className="whitespace-pre-wrap text-xs leading-relaxed text-muted-foreground">
          {task.result_summary}
        </p>
      ) : null}
    </div>
  );
}
