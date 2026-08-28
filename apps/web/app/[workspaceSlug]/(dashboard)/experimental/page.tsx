"use client";

import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { AppLink, useNavigation } from "@multica/views/navigation";
import { useT } from "@multica/views/i18n";
import { useWorkspacePaths } from "@multica/core/paths";
import { FlaskConical } from "lucide-react";

// 0.5.17 (B2c): web-side stub for the experimental nav. Lists every
// known lab as a card; each card links to a per-lab stub page that
// surfaces a "desktop-only" notice + download CTA. We do NOT mount
// real lab UI here — desktop-only runtimes (Electron + native PG +
// lab sandbox) are out of scope for the web platform. The user is
// redirected to Multica Desktop via the landing /download page.
const LABS = [
  { slug: "claude-lab", labelKey: "experimental_claude_science_lab", descKey: "desc_claude_lab" },
  { slug: "pythia", labelKey: "experimental_pythia", descKey: "desc_pythia" },
  { slug: "mythos", labelKey: "experimental_mythos", descKey: "desc_mythos" },
  { slug: "llm-wiki", labelKey: "experimental_llm_wiki_bridge", descKey: "desc_llm_wiki_bridge" },
  { slug: "code-canvas", labelKey: "experimental_code_canvas", descKey: "desc_code_canvas" },
  { slug: "swarm-topology", labelKey: "experimental_swarm_topology", descKey: "desc_swarm_topology" },
  // 0.5.87: semantica + timesfm cards close the same gap as their new
  // stub pages — issue-row deep links for these labs existed on web
  // while the index listed neither.
  { slug: "semantica-explorer", labelKey: "experimental_semantica", descKey: "desc_semantica" },
  { slug: "timesfm-lab", labelKey: "experimental_timesfm", descKey: "desc_timesfm" },
] as const;

export default function ExperimentalIndexPage() {
  const { t: tLayout } = useT("layout");
  const { t: tExp } = useT("experimental");
  const { push } = useNavigation();
  const p = useWorkspacePaths();

  return (
    <div className="mx-auto w-full max-w-4xl px-8 py-10">
      <div className="mb-8 flex items-center gap-3">
        <FlaskConical className="size-6 text-muted-foreground" />
        <h1 className="text-2xl font-semibold tracking-tight">
          {tExp(($) => $.web_stubs.intro_title)}
        </h1>
      </div>
      <p className="mb-8 text-sm text-muted-foreground">
        {tExp(($) => $.web_stubs.intro_desc)}
      </p>

      <div className="grid gap-4">
        {LABS.map((lab) => {
          const href = `${p.root().replace(/\/issues$/, "")}/experimental/${lab.slug}`;
          return (
            <Card key={lab.slug}>
              <CardHeader>
                <CardTitle className="flex items-center gap-2 text-base">
                  <FlaskConical className="size-4 text-muted-foreground" />
                  <AppLink href={href} className="hover:underline">
                    {tLayout(($) => $.sidebar[lab.labelKey])}
                  </AppLink>
                </CardTitle>
              </CardHeader>
              <CardContent>
                <p className="mb-3 text-sm text-muted-foreground">
                  {tExp(($) => $.web_stubs[lab.descKey])}
                </p>
                <button
                  type="button"
                  className={buttonVariants({ variant: "outline", size: "sm" })}
                  onClick={() => push(href)}
                >
                  {tExp(($) => $.web_stubs.web_only_title)} →
                </button>
              </CardContent>
            </Card>
          );
        })}
      </div>

      <div className="mt-10">
        <button
          type="button"
          className={buttonVariants()}
          onClick={() => push("/download")}
        >
          {tExp(($) => $.web_stubs.download_cta)}
        </button>
      </div>
    </div>
  );
}