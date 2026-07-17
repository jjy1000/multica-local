// Localized build: telemetry is permanently disabled. The export surface
// is preserved (initAnalytics, identify, captureEvent, captureException,
// capturePageview, setPersonProperties, resetAnalytics, captureSignupSource,
// captureDownloadIntent / captureDownloadPageViewed / captureDownloadInitiated,
// captureFeedbackOpened) so callers across web / desktop / docs / mobile
// references keep compiling without any change. Every function is a no-op
// — nothing is initialized, no event is shipped, no buffer is kept.
//
// If telemetry is ever re-enabled, restore the previous implementation that
// was backed by `posthog-js`. The last known-good event catalog is in
// docs/analytics.md and the prop / lifecycle shape is documented inline in
// the corresponding handler call sites (signup, workspace_created, ...).

export {
  captureDownloadIntent,
  captureDownloadPageViewed,
  captureDownloadInitiated,
  type DownloadIntentSource,
  type DownloadDetectPayload,
  type DownloadInitiatedPayload,
} from "./download";
export { captureFeedbackOpened, type FeedbackOpenedSource } from "./feedback";

export const EVENT_SCHEMA_VERSION = 2;

export type ClientType = "desktop" | "web";

export interface AnalyticsConfig {
  key: string;
  host: string;
  appVersion?: string;
  environment?: string;
}

export function detectClientType(): ClientType {
  if (typeof window === "undefined") return "web";
  const w = window as unknown as { electron?: unknown; desktopAPI?: unknown };
  if (w.electron || w.desktopAPI) return "desktop";
  if (typeof navigator !== "undefined" && /Electron/i.test(navigator.userAgent)) {
    return "desktop";
  }
  return "web";
}

export function initAnalytics(_config: AnalyticsConfig | null | undefined): boolean {
  return false;
}

export function identify(_userId: string, _userProperties?: Record<string, unknown>): void {
  // no-op
}

export function resetAnalytics(): void {
  // no-op
}

export function captureEvent(
  _name: string,
  _props?: Record<string, unknown>,
): void {
  // no-op
}

export function captureException(
  _error: unknown,
  _props?: Record<string, unknown>,
): void {
  // no-op
}

export function setPersonProperties(_props: Record<string, unknown>): void {
  // no-op
}

export function capturePageview(_path?: string): void {
  // no-op
}

export function captureSignupSource(): void {
  // no-op
}
