"use client";

import { Suspense, useEffect, useState } from "react";
import { useSearchParams, useRouter } from "next/navigation";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { sanitizeNextUrl, useAuthStore } from "@multica/core/auth";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { paths, resolvePostAuthDestination } from "@multica/core/paths";
import { api } from "@multica/core/api";
import { setLoggedInCookie } from "@/features/auth/auth-cookie";
import type { Workspace } from "@multica/core/types";
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
  CardFooter,
} from "@multica/ui/components/ui/card";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Loader2 } from "lucide-react";
import { useT } from "@multica/views/i18n";

/**
 * Localized build: the only auth surface is a single username field.
 * No email, no verification code, no Google SSO. The server creates the
 * user on first sight of a name and returns a JWT for the same row on
 * every subsequent login. The cookie / token handoff for the CLI / desktop
 * flows still runs through the same `api.issueCliToken()` path the
 * shared LoginPage used — that part is preserved because it doesn't
 * touch any vendor auth provider.
 */
async function resolveLoggedInDestination(
  qc: QueryClient,
  hasOnboarded: boolean,
  workspaces: Workspace[],
): Promise<string> {
  return resolvePostAuthDestination(workspaces, hasOnboarded);
}

function LoginPageContent() {
  const router = useRouter();
  const qc = useQueryClient();
  const { t } = useT("auth");
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const searchParams = useSearchParams();

  const nextUrl = sanitizeNextUrl(searchParams.get("next"));

  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // Already authenticated → skip the form.
  useEffect(() => {
    if (isLoading || !user) return;
    if (nextUrl) {
      router.replace(nextUrl);
      return;
    }
    const list = qc.getQueryData<Workspace[]>(workspaceKeys.list()) ?? [];
    void resolveLoggedInDestination(qc, user.onboarded_at != null, list).then(
      (dest) => router.replace(dest),
    );
  }, [isLoading, user, router, nextUrl, qc]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) {
      setError("Please enter a username");
      return;
    }
    setSubmitting(true);
    setError("");
    try {
      await useAuthStore.getState().loginWithUsername(trimmed);
      setLoggedInCookie();
      const wsList = await api.listWorkspaces();
      qc.setQueryData(workspaceKeys.list(), wsList);
      const currentUser = useAuthStore.getState().user;
      const onboarded = currentUser?.onboarded_at != null;
      if (nextUrl) {
        router.push(nextUrl);
        return;
      }
      router.push(
        await resolveLoggedInDestination(qc, onboarded, wsList),
      );
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-svh items-center justify-center">
      <Card className="w-full max-w-sm">
        <CardHeader className="text-center">
          <CardTitle className="text-2xl">Welcome to Multica</CardTitle>
          <CardDescription>
            Enter a username to sign in. First time creates the account;
            subsequent logins return to your workspace.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form
            id="login-form"
            onSubmit={handleSubmit}
            className="space-y-4"
          >
            <div className="space-y-2">
              <Label htmlFor="login-name">Username</Label>
              <Input
                id="login-name"
                type="text"
                placeholder="alice"
                value={name}
                onChange={(e) => setName(e.target.value)}
                autoFocus
                autoComplete="username"
                disabled={submitting}
                required
                maxLength={64}
              />
            </div>
            {error && (
              <p className="text-sm text-destructive">{error}</p>
            )}
          </form>
        </CardContent>
        <CardFooter>
          <Button
            type="submit"
            form="login-form"
            className="w-full"
            size="lg"
            disabled={!name.trim() || submitting}
          >
            {submitting ? (
              <>
                <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                Signing in…
              </>
            ) : (
              "Sign in"
            )}
          </Button>
        </CardFooter>
      </Card>
    </div>
  );
}

export default function Page() {
  return (
    <Suspense fallback={null}>
      <LoginPageContent />
    </Suspense>
  );
}
