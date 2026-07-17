import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { IssueDetail } from "@multica/views/issues/components";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueDetailOptions } from "@multica/core/issues/queries";
import { useDocumentTitle } from "@/hooks/use-document-title";

// Resolve the workspace-scoped lab route suffix for the bound issue
// (e.g. "claude-lab" for `lab_source="claude_science_lab"`). The
// issue detail's right column uses this to surface a single
// "打开实验室面板 →" jump link; the detailed view (Plan / Forecast /
// Council / Artifact / Knowledge tabs) lives at /experimental/<suffix>
// and is *not* mounted inline on the issue page. Inline visualization
// turned out to crowd the issue detail chrome (~600px height) and
// hid the activity / description flows. Keeping it workspace-scoped
// preserves the lab contract: tag the issue, jump to the lab view, do
// the work, return to the issue and read the result. The lab view
// module itself is unchanged — it just isn't mounted here.
//
// Visibility-gating mirrors the 0.3.31 inline dispatch map so the
// jump link shows for any lab the catalog exposes; turning the
// underlying flag off drops both the inline chrome and the link.
function pickLabRouteSuffix(
  labSource: string | null | undefined,
): string | undefined {
  if (!labSource) return undefined;
  switch (labSource) {
    case "claude_science_lab":
      return "claude-lab";
    case "pythia_oracle":
      return "pythia";
    case "mythos_swarm":
      return "mythos";
    case "llm_wiki_bridge":
      return "llm-wiki";
    case "code_canvas":
      return "code-canvas";
    case "agent_self_optimization":
      return "agent-self-optimization";
    case "constitution_agent":
      return "constitution-agent";
    // chat_pin_ui has no lab view (UI-only flag); nothing to jump to.
    default:
      return undefined;
  }
}

export function IssueDetailPage() {
  const { id } = useParams<{ id: string }>();
  const wsId = useWorkspaceId();
  const { data: issue } = useQuery(issueDetailOptions(wsId, id!));

  useDocumentTitle(issue ? `${issue.identifier}: ${issue.title}` : "Issue");

  if (!id) return null;
  // Render errors bubble to the root route errorElement (DesktopRouteErrorPage),
  // which contains the crash inside the tab content pane. No page-level boundary
  // here — a whole-page wrapper duplicates the route-level error UI.
  return (
    <IssueDetail
      issueId={id}
      labRouteSuffix={pickLabRouteSuffix(issue?.lab_source)}
    />
  );
}
