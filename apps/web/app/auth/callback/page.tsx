"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { paths } from "@multica/core/paths";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
} from "@multica/ui/components/ui/card";

// Google OAuth callback is disabled in this localized build.
// Redirect to username-only login.
export default function CallbackPage() {
  const router = useRouter();

  useEffect(() => {
    router.replace(paths.login());
  }, [router]);

  return (
    <div className="flex min-h-screen items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">Redirecting...</CardTitle>
          <CardDescription>
            Google login is disabled in this build.
          </CardDescription>
        </CardHeader>
      </Card>
    </div>
  );
}
