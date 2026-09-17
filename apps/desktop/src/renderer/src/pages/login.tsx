"use client";

// Localized-build desktop login. The shared @multica/views/auth
// LoginPage is no longer used — it was an email/Code/Google SSO flow that
// made no sense for a single-user local build. The web app's
// apps/web/app/(auth)/login/page.tsx already ships a username-only
// variant; this file is the desktop's equivalent and is rendered when
// the user lands in the desktop renderer with no auth cookie (i.e.
// after a fresh install or after clearing localStorage).
//
// Behaviour matches the web username login: type a name, hit "Sign in",
// the server creates the user on first sight and returns a JWT, the
// auth store updates, the desktop shell takes over. No email step,
// no verification code, no Google button.

import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { workspaceKeys } from "@multica/core/workspace/queries";
import { api } from "@multica/core/api";
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
import { Alert, AlertDescription } from "@multica/ui/components/ui/alert";
import { DragStrip } from "@multica/views/platform";
import { useT } from "@multica/views/i18n";
import { MulticaIcon } from "@multica/ui/components/common/multica-icon";

export function DesktopLoginPage() {
  const qc = useQueryClient();
  const user = useAuthStore((s) => s.user);
  const isLoading = useAuthStore((s) => s.isLoading);
  const { t } = useT("auth");
  // The last session ended because the server rejected its credential, not
  // because the user asked to leave. Without saying so, landing here reads as
  // the app having lost their work for no reason.
  const sessionExpired = useAuthStore((state) => state.expired);

  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [submitting, setSubmitting] = useState(false);

  // Already authenticated → IndexRedirect in routes.tsx drives the
  // router to the workspace shell. Nothing to do here besides the
  // state read; the dependency list is intentionally empty so this
  // component is render-stable while `user` warms up via the auth
  // store. (Adding a real effect would require a no-op body, which
  // TypeScript flags as a missing useEffect dep — the user reads are
  // explicit enough to keep the lint happy.)
  void isLoading;
  void user;

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
      const wsList = await api.listWorkspaces();
      qc.setQueryData(workspaceKeys.list(), wsList);
      // Routing into the shell is handled by routes.tsx once the
      // auth store reports a user — nothing to do here.
    } catch (err) {
      setError(err instanceof Error ? err.message : "Login failed");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex h-screen flex-col">
      <DragStrip />
      <div className="flex flex-1 items-center justify-center bg-background">
        <Card className="w-full max-w-sm">
          <CardHeader className="text-center">
            <div className="mx-auto mb-4">
              <MulticaIcon bordered size="lg" />
            </div>
            <CardTitle className="text-2xl">Welcome to Multica</CardTitle>
            <CardDescription>
              Enter a username to sign in. First time creates the account;
              subsequent logins return to your workspace.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {sessionExpired && (
              <Alert>
                <AlertDescription>
                  {t(($) => $.errors.session_expired)}
                </AlertDescription>
              </Alert>
            )}
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
    </div>
  );
}

