import { useParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { IssueDetail } from "@multica/views/issues/components";
import { useWorkspaceId } from "@multica/core/hooks";
import { issueDetailOptions } from "@multica/core/issues/queries";
import { useDocumentTitle } from "@/hooks/use-document-title";

// Workspace-scoped lab routing lives entirely on the
// `/experimental/<suffix>` surface (resolved via the shared
// `labSourceRouteSuffix` helper from
// packages/views/issues/components/issue-labs-section.tsx). The
// issue detail sidebar just links there through `IssueLabsSection`;
// no per-issue inline mounting happens here.
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
    <IssueDetail issueId={id} />
  );
}
