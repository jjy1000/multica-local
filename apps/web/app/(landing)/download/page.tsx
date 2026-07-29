import type { Metadata } from "next";
import { fetchLatestRelease } from "@/features/landing/utils/github-release";

// Vercel ISR: the server fetch inside fetchLatestRelease carries
// `next: { revalidate: 300 }`, which makes GitHub API cost at most
// one request per region per 5 minutes. Page-level revalidate mirrors
// that window so the first paint also refreshes every 5 minutes.
export const revalidate = 300;

export const metadata: Metadata = {
  title: "Download Multica",
  description:
    "Download Multica for macOS, Windows, or Linux — or install the CLI for servers and remote dev boxes.",
  openGraph: {
    title: "Download Multica",
    description:
      "Get the Multica desktop app with a bundled daemon, or install the CLI for servers and remote dev boxes.",
    url: "/download",
  },
  alternates: {
    canonical: "/download",
  },
};

// 0.3.66 (M3 PR2): the prior download-client.tsx mounted a CloudSection
// (cloud-waitlist form) on this page. The cloud-waitlist funnel was
// retired to align with CLAUDE.md "No cloud features". The download
// assets and install instructions are still served here, but the page
// is now a thin server component. The previous client-component
// layout is being re-implemented piecemeal as the fork drops upstream's
// billing/CRM surfaces — see apps/web/features/landing for rebuilt
// components.

function asset(name: string | undefined, label: string) {
  if (!name) return null;
  return (
    <p className="mt-2">
      <a href={name} className="text-blue-600 underline">
        {label}
      </a>
    </p>
  );
}

export default async function DownloadPage() {
  const release = await fetchLatestRelease();
  const a = release.assets;
  return (
    <main className="mx-auto max-w-3xl px-6 py-16">
      <h1 className="text-3xl font-bold">Download Multica</h1>
      <p className="mt-4 text-muted-foreground">
        Latest release: {release.version ?? "unavailable"}.
        Pick your platform and grab the desktop bundle, or install the
        CLI for server / remote-dev environments.
      </p>
      {asset(a.macArm64Dmg, "macOS (Apple Silicon) — DMG")}
      {asset(a.macArm64Zip, "macOS (Apple Silicon) — ZIP")}
      {asset(a.winX64Exe, "Windows (x64) — EXE")}
      {asset(a.winArm64Exe, "Windows (ARM64) — EXE")}
      {asset(a.linuxAmd64AppImage, "Linux (amd64) — AppImage")}
      {asset(a.linuxAmd64Deb, "Linux (amd64) — DEB")}
      {asset(a.linuxAmd64Rpm, "Linux (amd64) — RPM")}
      {asset(a.linuxArm64AppImage, "Linux (arm64) — AppImage")}
      {asset(a.linuxArm64Deb, "Linux (arm64) — DEB")}
      {asset(a.linuxArm64Rpm, "Linux (arm64) — RPM")}
    </main>
  );
}