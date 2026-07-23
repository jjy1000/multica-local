import { useParams } from "react-router-dom";
import { PluginShellView } from "@multica/views/experimental";
import { DragStrip } from "@multica/views/platform";

// PluginShellPage (0.3.60) — desktop route wrapper for the generic
// user-plugin shell. The slug comes from the URL param; the view
// fetches plugin info from /api/user-plugins and renders the
// manifest-driven tab layout. Pre-workspace route (no
// :workspaceSlug prefix), mirroring the other /experimental/<slug>
// surfaces — the active workspace is read implicitly by the view.
export function PluginShellPage() {
  const { pluginSlug } = useParams<{ pluginSlug: string }>();
  if (!pluginSlug) return null;
  return (
    <div className="flex h-full flex-col">
      <DragStrip />
      <PluginShellView pluginSlug={pluginSlug} />
    </div>
  );
}
