"use client";

import { Card, CardContent, CardHeader, CardTitle } from "@multica/ui/components/ui/card";
import { buttonVariants } from "@multica/ui/components/ui/button";
import { Waypoints } from "lucide-react";
import { useT } from "@multica/views/i18n";

// 0.5.83 (WL3): web stub for the issue causal graph desktop surface.
// The desktop view renders the workspace-wide / issue-focused graph
// over /api/causal-graph/*; web parity lands with the shared views
// extraction (the desktop is the primary surface per the fork's
// product law).
export default function CausalGraphStubPage() {
  const { t: tLayout } = useT("layout");
  const { t } = useT("causal-graph");

  return (
    <div className="mx-auto w-full max-w-3xl px-8 py-10">
      <div className="mb-6 flex items-center gap-3">
        <Waypoints className="size-6 text-muted-foreground" />
        <h1 className="text-2xl font-semibold tracking-tight">
          {tLayout(($) => $.sidebar.experimental_causal_graph)}
        </h1>
      </div>
      <Card>
        <CardHeader>
          <CardTitle>{t(($) => $.title)}</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 text-sm text-muted-foreground">
          <p>{t(($) => $.unbound_hint)}</p>
          <a href="/experimental" className={buttonVariants({ variant: "outline" })}>
            ←
          </a>
        </CardContent>
      </Card>
    </div>
  );
}
