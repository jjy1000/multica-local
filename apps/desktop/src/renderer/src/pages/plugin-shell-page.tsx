import { useParams, useSearchParams } from "react-router-dom";
import { PluginShellView } from "@multica/views/experimental";
import { DragStrip } from "@multica/views/platform";

// PluginShellPage (0.3.60) — desktop route wrapper for the generic
// user-plugin shell. The slug comes from the URL param; the view
// fetches plugin info from /api/user-plugins and renders the
// manifest-driven tab layout. Pre-workspace route (no
// :workspaceSlug prefix), mirroring the other /experimental/<slug>
// surfaces — the active workspace is read implicitly by the view.
//
// 0.5.81: `?issue=<id>` deep links (IssueLabsSection "open panel",
// create-issue post-create redirect) now flow into the shell so it can
// show which task the user arrived from — matching the binding chip
// ClaudeLabView / PythiaView / MythosView render.
export function PluginShellPage() {
  const { pluginSlug } = useParams<{ pluginSlug: string }>();
  const [searchParams] = useSearchParams();
  if (!pluginSlug) return null;
  return (
    <div className="flex h-full flex-col">
      <DragStrip />
      <PluginShellView pluginSlug={pluginSlug} issueId={searchParams.get("issue") ?? undefined} />
    </div>
  );
}
