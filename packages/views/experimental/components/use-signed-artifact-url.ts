"use client";

import { useEffect, useState } from "react";
import { api, parseWithFallback } from "@multica/core/api";
import {
  EMPTY_PLUGIN_ARTIFACT_SIGN_RESPONSE,
  PluginArtifactSignResponseSchema,
} from "@multica/core/api/schemas";
import type { PluginArtifactSignResponse } from "@multica/core/api/schemas";

// 0.5.18 M5 — short-lived signed artifact URLs.
//
// A file-backed artifact raw URL requires a Bearer token, but an <img> /
// <iframe> / <a download> element cannot attach the Authorization header, so
// it 401s in token-mode desktop. This hook POSTs to the /sign endpoint (which
// IS Bearer-authenticated) to mint a short-lived, self-contained signed URL
// that the element can then load without credentials.

const SIGN_REUSE_WINDOW_MS = 30_000;

interface SignedCacheEntry {
  url: string;
  exp: number;
}

// Module-level cache so the same artifact rendered in multiple surfaces
// (e.g. image + iframe) reuses one signature until it nears expiry.
const signedUrlCache = new Map<string, SignedCacheEntry>();

function isAbsoluteUrl(url: string): boolean {
  return /^https?:\/\//i.test(url) || url.startsWith("data:");
}

/** Resolve a relative URL against the configured API host. */
function resolveUrl(url: string): string {
  if (isAbsoluteUrl(url)) return url;
  return `${api.getBaseUrl()}${url}`;
}

/**
 * Returns the URL to use for a file-backed artifact. When `url` is relative
 * and both `slug` and `artifactId` are supplied, it mints a signed URL via
 * the Bearer-authenticated /sign endpoint (best-effort: on failure it falls
 * back to the unsigned URL). Absolute URLs and the empty-slug case (shared
 * ArtifactRenderer reuse from the LabOutputPanel) are returned unsigned.
 */
export function useSignedArtifactUrl(
  url: string | undefined,
  slug: string,
  artifactId: string,
): string | undefined {
  const [resolved, setResolved] = useState<string | undefined>(() => {
    if (!url) return undefined;
    if (isAbsoluteUrl(url)) return url;
    if (slug === "" || artifactId === "") return resolveUrl(url);
    // Signable: resolve once the effect fetches a signature.
    return undefined;
  });

  useEffect(() => {
    if (!url) {
      setResolved(undefined);
      return;
    }
    if (isAbsoluteUrl(url)) {
      setResolved(url);
      return;
    }
    if (slug === "" || artifactId === "") {
      setResolved(resolveUrl(url));
      return;
    }

    const key = `${slug}|${artifactId}`;
    const cached = signedUrlCache.get(key);
    if (cached && cached.exp * 1000 - Date.now() > SIGN_REUSE_WINDOW_MS) {
      setResolved(`${api.getBaseUrl()}${cached.url}`);
      return;
    }

    let cancelled = false;
    (async () => {
      try {
        const r = await api.rawRequest(
          `/api/user-plugins/${encodeURIComponent(slug)}/artifacts/${encodeURIComponent(artifactId)}/sign`,
          { method: "POST" },
        );
        if (!r.ok) {
          if (!cancelled) setResolved(resolveUrl(url));
          return;
        }
        const raw: unknown = await r.json();
        const parsed = parseWithFallback<PluginArtifactSignResponse>(
          raw,
          PluginArtifactSignResponseSchema,
          EMPTY_PLUGIN_ARTIFACT_SIGN_RESPONSE,
          { endpoint: "POST /api/user-plugins/:slug/artifacts/:id/sign" },
        );
        if (cancelled) return;
        if (!parsed.url) {
          setResolved(resolveUrl(url));
          return;
        }
        signedUrlCache.set(key, { url: parsed.url, exp: parsed.exp });
        setResolved(`${api.getBaseUrl()}${parsed.url}`);
      } catch {
        if (!cancelled) setResolved(resolveUrl(url));
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [url, slug, artifactId]);

  return resolved;
}
