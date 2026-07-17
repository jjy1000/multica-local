import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "@multica/views/locales/en/common.json";
import enAuth from "@multica/views/locales/en/auth.json";
import enSettings from "@multica/views/locales/en/settings.json";
import type { ReactNode } from "react";

const TEST_RESOURCES = {
  en: { common: enCommon, auth: enAuth, settings: enSettings },
};

function createWrapper() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>{children}</QueryClientProvider>
    </I18nProvider>
  );
}

const {
  mockUsernameLogin,
  mockIssueCliToken,
  searchParamsState,
  authStateRef,
} = vi.hoisted(() => ({
  mockUsernameLogin: vi.fn(),
  mockIssueCliToken: vi.fn(),
  searchParamsState: { params: new URLSearchParams() },
  authStateRef: {
    state: {
      loginWithUsername: vi.fn(),
      user: null as null | { id: string; email: string; onboarded_at?: string | null },
      isLoading: false,
    },
  },
}));

vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/login",
  useSearchParams: () => searchParamsState.params,
}));

vi.mock("@multica/core/auth", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/auth")>(
      "@multica/core/auth",
    );
  authStateRef.state.loginWithUsername = mockUsernameLogin;
  const useAuthStore = Object.assign(
    (selector: (s: typeof authStateRef.state) => unknown) =>
      selector(authStateRef.state),
    { getState: () => authStateRef.state },
  );
  return { ...actual, useAuthStore };
});

vi.mock("@/features/auth/auth-cookie", () => ({
  setLoggedInCookie: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listWorkspaces: vi.fn().mockResolvedValue([]),
    usernameLogin: mockUsernameLogin,
    setToken: vi.fn(),
    getMe: vi.fn(),
    issueCliToken: mockIssueCliToken,
  },
}));

import LoginPage from "./page";

describe("LoginPage (localized — username only)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    searchParamsState.params = new URLSearchParams();
    authStateRef.state.user = null;
    authStateRef.state.isLoading = false;
  });

  it("renders username form with single input and sign-in button", () => {
    render(<LoginPage />, { wrapper: createWrapper() });

    expect(screen.getByText("Welcome to Multica")).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Sign in" }),
    ).toBeInTheDocument();
    // No email, no verification, no Google button.
    expect(screen.queryByLabelText("Email")).not.toBeInTheDocument();
    expect(screen.queryByText("Continue")).not.toBeInTheDocument();
    expect(screen.queryByText(/google/i)).not.toBeInTheDocument();
  });

  it("does not submit when username is empty", async () => {
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(mockUsernameLogin).not.toHaveBeenCalled();
  });

  it("calls loginWithUsername with the typed name on submit", async () => {
    mockUsernameLogin.mockResolvedValueOnce({
      id: "u1",
      email: "alice@local",
      onboarded_at: null,
    });
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.type(screen.getByLabelText("Username"), "alice");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() => {
      expect(mockUsernameLogin).toHaveBeenCalledWith("alice");
    });
  });

  it("shows a submitting state while the request is in flight", async () => {
    mockUsernameLogin.mockReturnValueOnce(new Promise(() => {}));
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.type(screen.getByLabelText("Username"), "alice");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Signing in…")).toBeInTheDocument();
  });

  it("surfaces the error message when the server rejects the login", async () => {
    mockUsernameLogin.mockRejectedValueOnce(new Error("name is too long"));
    const user = userEvent.setup();
    render(<LoginPage />, { wrapper: createWrapper() });

    await user.type(screen.getByLabelText("Username"), "alice");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("name is too long")).toBeInTheDocument();
  });

  it("does NOT render any Google / SSO affordance", () => {
    render(<LoginPage />, { wrapper: createWrapper() });
    // The new page intentionally removes the Google button — the previous
    // LoginPage used a Google-styled <svg> with multi-color paths. Make
    // sure no element with a Google label survives.
    const html = document.body.innerHTML;
    expect(html).not.toMatch(/google/i);
    expect(html).not.toMatch(/accounts\.google\.com/);
  });
});
