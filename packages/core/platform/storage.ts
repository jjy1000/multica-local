import type { StorageAdapter } from "../types/storage";

/** SSR-safe localStorage. Works in both Next.js (SSR) and Electron (always client). */
export const defaultStorage: StorageAdapter = {
  getItem: (k) =>
    typeof window !== "undefined" ? localStorage.getItem(k) : null,
  setItem: (k, v) => {
    if (typeof window !== "undefined") localStorage.setItem(k, v);
  },
  removeItem: (k) => {
    if (typeof window !== "undefined") localStorage.removeItem(k);
  },
};

/**
 * SSR-safe sessionStorage adapter (H13 audit 2026-09-06). Per-issue
 * ephemeral state (lab run triggers, pythia trigger keys, etc.) was
 * previously reaching into `window.sessionStorage` directly with no
 * `typeof window` guard and no `window.` prefix — silent under web SSR,
 * silent under Next.js prerender. Routes through this adapter for the
 * same SSR check + key namespacing defaultStorage provides for
 * localStorage. The adapter shape mirrors defaultStorage exactly so
 * call sites stay symmetric.
 */
export const sessionStorageAdapter: StorageAdapter = {
  getItem: (k) =>
    typeof window !== "undefined" ? sessionStorage.getItem(k) : null,
  setItem: (k, v) => {
    if (typeof window !== "undefined") sessionStorage.setItem(k, v);
  },
  removeItem: (k) => {
    if (typeof window !== "undefined") sessionStorage.removeItem(k);
  },
};