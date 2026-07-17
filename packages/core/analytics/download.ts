// Localized build: download-funnel instrumentation is a no-op. The export
// surface (captureDownloadIntent / captureDownloadPageViewed /
// captureDownloadInitiated + their types) is preserved so the
// landing / login / welcome / step-3 call sites keep compiling. If
// telemetry is ever re-enabled, restore the previous posthog-js backed
// implementation — the prop shapes are documented in
// docs/analytics.md.

export type DownloadIntentSource =
  | "landing_hero"
  | "landing_footer"
  | "login"
  | "welcome"
  | "step3";

export interface DownloadDetectPayload {
  detected_os: "mac" | "windows" | "linux" | "unknown";
  detected_arch: "arm64" | "x64" | "unknown";
  detect_confident: boolean;
  version_available: boolean;
}

export interface DownloadInitiatedPayload {
  platform: "mac" | "windows" | "linux";
  arch: "arm64" | "x64";
  format: "dmg" | "zip" | "exe" | "appimage" | "deb" | "rpm";
  version: string;
  primary_cta: boolean;
  matched_detect: boolean;
}

export function captureDownloadIntent(_source: DownloadIntentSource): void {
  // no-op
}

export function captureDownloadPageViewed(_payload: DownloadDetectPayload): void {
  // no-op
}

export function captureDownloadInitiated(_payload: DownloadInitiatedPayload): void {
  // no-op
}
