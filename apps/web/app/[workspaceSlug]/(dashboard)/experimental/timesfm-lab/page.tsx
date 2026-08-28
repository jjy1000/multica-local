"use client";

import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { useNavigation } from "@multica/views/navigation";
import { useT } from "@multica/views/i18n";
import { useWorkspacePaths } from "@multica/core/paths";
import { FlaskConical } from "lucide-react";

// 0.5.87: web stub for the TimesFM lab desktop surface (0.5.82 WL2,
// ICP-1 records-only). The desktop view streams forecast runs against
// the vendored torch subprocess — desktop-only. Without this page,
// issue-row deep links (lab_source=timesfm → /experimental/timesfm-lab)
// 404 on web. The bare /experimental/timesfm REST proxy stays server
// -side and is unaffected.
export default function TimesfmLabStubPage() {
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
          {tLayout(($) => $.sidebar.experimental_timesfm)}
        </h1>
      </div>
      <p className="mb-6 text-sm text-muted-foreground">
        {tExp(($) => $.web_stubs.desc_timesfm)}
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
