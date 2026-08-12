"use client";

import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { useNavigation } from "@multica/views/navigation";
import { useT } from "@multica/views/i18n";
import { useWorkspacePaths } from "@multica/core/paths";
import { FlaskConical } from "lucide-react";

// 0.5.17 (B2c): web stub for the LLM Wiki Bridge desktop surface.
// The full view talks to a local stdio subprocess (MCP-style verbs:
// /files /search /status /chat) — desktop-only.
export default function LlmWikiBridgeStubPage() {
  const { t: tLayout } = useT("layout");
  const { t: tExp } = useT("experimental");
  const { push } = useNavigation();
  const p = useWorkspacePaths();
  const backHref = `${p.root().replace(/\/issues$/, "")}/experimental`;

  return (
    <div className="mx-auto w-full max-w-3xl px-8 py-10">
      <div className="mb-6 flex items-center gap-3">
        <FlaskConical className="size-6 text-muted-foreground" />
        <h1 className="text-2xl font-semibold tracking-tight">
          {tLayout(($) => $.sidebar.experimental_llm_wiki_bridge)}
        </h1>
      </div>
      <p className="mb-6 text-sm text-muted-foreground">
        {tExp(($) => $.web_stubs.desc_llm_wiki_bridge)}
      </p>
      <Card>
        <CardHeader>
          <CardTitle className="text-base">
            {tExp(($) => $.web_stubs.web_only_title)}
          </CardTitle>
        </CardHeader>
        <CardContent>
          <p className="mb-4 text-sm text-muted-foreground">
            {tExp(($) => $.web_stubs.web_only_desc)}
          </p>
          <div className="flex gap-2">
            <button
              type="button"
              className={buttonVariants()}
              onClick={() => push("/download")}
            >
              {tExp(($) => $.web_stubs.download_cta)}
            </button>
            <button
              type="button"
              className={buttonVariants({ variant: "outline" })}
              onClick={() => push(backHref)}
            >
              {tExp(($) => $.web_stubs.back_to_list)}
            </button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}