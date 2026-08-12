"use client";

import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { useNavigation } from "@multica/views/navigation";
import { useT } from "@multica/views/i18n";
import { useWorkspacePaths } from "@multica/core/paths";
import { FlaskConical } from "lucide-react";

// 0.5.17 (B2c): web stub for a user plugin shell. The dynamic
// slug comes from the [pluginSlug] route param; the desktop view
// resolves the manifest, runs the per-plugin inline runtime, and
// renders manifest-driven tabs. None of that is reachable from
// the web app — install Multica Desktop to use this plugin.
export default function PluginStubPage({
  params,
}: {
  params: Promise<{ pluginSlug: string }>;
}) {
  const { t: tExp } = useT("experimental");
  const { push } = useNavigation();
  const p = useWorkspacePaths();
  const backHref = `${p.root().replace(/\/issues$/, "")}/experimental`;

  // The slug is decorative here — we deliberately do not hit
  // /api/user-plugins because plugins are desktop-flag-gated and
  // the web fetch would 404 anyway. Resolve the param so Next.js
  // is happy and the page is keyed per-slug.
  void params;

  return (
    <div className="mx-auto w-full max-w-3xl px-8 py-10">
      <div className="mb-6 flex items-center gap-3">
        <FlaskConical className="size-6 text-muted-foreground" />
        <h1 className="text-2xl font-semibold tracking-tight">
          {tExp(($) => $.web_stubs.desc_plugin)}
        </h1>
      </div>
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