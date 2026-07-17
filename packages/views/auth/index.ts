// Localized build: the only auth surface is a single username field.
// The legacy email / verification-code / Google SSO LoginPage was
// removed in this build. CLI / desktop token handoff utilities
// survive as standalone exports — the web /auth/callback route
// imports them to complete the cookie → bearer-token swap when a
// CLI or Desktop app deep-links back into the browser.
export { validateCliCallback, redirectToCliCallback } from "./cli-callback";
export { useLogout } from "./use-logout";
