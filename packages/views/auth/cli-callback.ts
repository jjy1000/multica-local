// CLI / desktop token-handoff helpers. Extracted from the legacy
// email/Code/Google LoginPage so the OAuth CLI redirect can keep
// working without dragging the multi-step verification UI back in.
//
// The web /auth/callback route (apps/web/app/auth/callback/page.tsx)
// imports `validateCliCallback` + `redirectToCliCallback` from here.

/**
 * Redirect the browser to a localhost CLI callback URL with the
 * freshly-minted token + opaque state attached as query parameters.
 * URL-encoded so the token can carry characters that would otherwise
 * need escaping.
 */
export function redirectToCliCallback(url: string, token: string, state: string) {
  const separator = url.includes("?") ? "&" : "?";
  window.location.href = `${url}${separator}token=${encodeURIComponent(token)}&state=${encodeURIComponent(state)}`;
}

/**
 * Validate that a CLI callback URL points to a safe host over HTTP.
 * Allows localhost and private/LAN IPs (RFC 1918) to support self-hosted
 * setups on local VMs while blocking arbitrary public hosts.
 */
export function validateCliCallback(cliCallback: string): boolean {
  try {
    const cbUrl = new URL(cliCallback);
    if (cbUrl.protocol !== "http:") return false;
    const h = cbUrl.hostname;
    if (h === "localhost" || h === "127.0.0.1") return true;
    // Allow RFC 1918 private IPs: 10.x.x.x, 172.16-31.x.x, 192.168.x.x
    if (/^10\./.test(h)) return true;
    if (/^172\.(1[6-9]|2\d|3[01])\./.test(h)) return true;
    if (/^192\.168\./.test(h)) return true;
    return false;
  } catch {
    return false;
  }
}
