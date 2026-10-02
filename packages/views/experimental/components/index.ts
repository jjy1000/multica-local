export { ForecastStreamView } from "./forecast-stream-view";
export {
  LabChatPanel,
  labChatPanelPropsFromContext,
} from "./lab-chat-panel";
export { ArtifactRenderer, type Artifact } from "./artifact-renderer";
export { ArtifactGallery } from "./artifact-gallery";
export { PluginShellView } from "./plugin-shell-view";
export { LabOutputPanel } from "./lab-output-panel";
export { IssueBreadcrumb, type IssueBreadcrumbProps } from "./issue-breadcrumb";
export { LabRunLink, labRunHref, type LabRunLinkProps } from "./lab-run-link";
export { useDeepLinkRun, type UseDeepLinkRunResult } from "./use-deep-link-run";
export {
  InteractiveChartEnvelope,
  type ChartEnvelope,
} from "./interactive-chart-envelope";
export {
  LabTaskResultView,
  labTaskHasStructuredDeliverables,
} from "./lab-task-result-view";
export {
  safeSvgMarkup,
  safeImageSrc,
  safeHrefUrl,
} from "./lab-attachment-sanitize";
// 0.5.105 (audit H3): SwarmTopologyGraph / SwarmInterruptBar removed
// with the swarm_topology runtime retirement.
export { CausalMinimap, CAUSAL_NODE_TYPE_COLORS } from "./causal-minimap";
export type { CausalPositionOverride, CausalViewportTransform, CausalPathHighlight } from "./causal-minimap";
export { CausalGraphCanvas } from "./causal-graph-canvas";
export { buildGraphDigest } from "./causal-graph-digest";
export { summarizeCausalPath, causalPathBand } from "./causal-path-summary";
export type { CausalPathSummary, CausalPathBand, CausalPathVerdict } from "./causal-path-summary";
// 0.5.132: Claude Lab brain neural view (issue embed + lab workbench)
export { ClaudeBrainCanvas } from "./claude-lab/claude-brain-canvas";
export {
  CLAUDE_LAB_ROSTER,
  deriveBrainNodes,
  deriveBrainPhase,
  deriveRecentRuns,
} from "./claude-lab/claude-brain-derive";
export type {
  BrainNodeState,
  ClaudeBrainNode,
  ClaudeBrainRosterEntry,
} from "./claude-lab/claude-brain-derive";
