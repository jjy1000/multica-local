// Shared XSS hardening helpers for agent-emitted lab attachment payloads
// (moved out of the desktop claude-lab-view in the 0.5.97 cycle so the
// issue-side result renderers enforce the exact same policy).
//
// The server-side `allowedAttachmentKinds` allowlist already drops unknown
// kinds; these helpers add a second layer in case any new sink ever
// forgets the check.

// DANGEROUS_TAGS — stripped from agent SVG before dangerouslySetInnerHTML.
// `<script>` is the obvious one; `<foreignObject>` can embed HTML that
// contains scripts; the rest can carry event-handler attributes.
const DANGEROUS_SVG_TAGS = [
  "script",
  "foreignobject",
  "iframe",
  "object",
  "embed",
  "form",
  "input",
  "button",
  "textarea",
  "select",
  "link",
  "meta",
  "base",
  "style",
];

// DANGEROUS_ATTR_PREFIXES — drop any attribute starting with `on` (event
// handlers) and the rare ones that can run JS (`xlink:href` with
// javascript: scheme is filtered by safeHrefUrl; we strip it here too).
const DANGEROUS_ATTR_PATTERN = /\son[a-z]+\s*=/i;

// safeSvgMarkup strips script-like tags and event-handler attributes
// from an agent-supplied SVG string before inlining it via
// dangerouslySetInnerHTML. The input is treated as opaque markup; we
// don't try to be a real XML parser — for hostile input that's
// unsafe, but the matching is on the substring level which is
// sufficient to block the obvious vectors the agent emits.
//
// The server-side allowlist already drops unknown `kind` values, so
// a malicious agent can't reach this sink with kind="html" — but
// any agent that emits `<svg><script>...</script></svg>` while
// following the SKILL.md contract still gets the script stripped.
export function safeSvgMarkup(raw: string): string {
  if (!raw) return "";
  let out = raw;
  for (const tag of DANGEROUS_SVG_TAGS) {
    // Match open or self-closing forms. Case-insensitive.
    const reOpen = new RegExp(`<${tag}\\b[^>]*>`, "gi");
    const reClose = new RegExp(`</${tag}\\s*>`, "gi");
    const reSelf = new RegExp(`<${tag}\\b[^>]*/>`, "gi");
    out = out.replace(reOpen, "").replace(reClose, "").replace(reSelf, "");
  }
  // Strip event handler attributes from any remaining tag.
  out = out.replace(DANGEROUS_ATTR_PATTERN, " data-blocked=");
  // Drop javascript:/vbscript:/data:text/html href values. The href
  // matcher is intentionally narrow — agent SVG that uses real
  // relative hrefs is preserved.
  out = out.replace(
    /\s(href|xlink:href)\s*=\s*("|')\s*(javascript|vbscript|data\s*:\s*text\/html|data\s*:\s*application\/javascript|data\s*:\s*image\/svg)[^"']*\2/gi,
    ' $1="#"',
  );
  // DOMParser pass — element / attribute walk. The renderer ships
  // with DOMParser in every Electron build. jsdom provides it for
  // tests. Fallback (older runtimes): keep the regex-cleaned string.
  if (typeof DOMParser !== "undefined") {
    try {
      const doc = new DOMParser().parseFromString(out, "image/svg+xml");
      if (doc.getElementsByTagName("parsererror").length === 0) {
        walkAndSanitizeSvg(doc.documentElement);
        out = new XMLSerializer().serializeToString(doc.documentElement);
      }
    } catch {
      // Parser failure — fall back to the regex-cleaned string.
    }
  }
  return out;
}

// ALLOWED_HREF_SCHEMES — explicit list of schemes that are safe to
// leave on href / xlink:href / src after sanitization. data:image/*
// is permitted ONLY on `<image>` elements (per the SVG 2 spec the
// browser rasterizes the image and runs no script context); every
// other data: variant is rejected. javascript: / vbscript: /
// protocol-relative (`//evil.com`) / data:text/html /
// data:image/svg+xml all reject.
const ALLOWED_HREF_SCHEMES = new Set(["http:", "https:", "mailto:"]);

// resolveHrefScheme returns the parsed scheme (lowercased, with colon)
// for any href / src value, or null when the value is unsafe.
// Protocol-relative URLs (`//evil.com/x`) explicitly reject — the
// browser resolves them against the page origin (localhost on dev,
// file:// in packaged builds), so a successful GET leaks the user's
// IP / User-Agent / Referer.
function resolveHrefScheme(value: string): string | null {
  const trimmed = value.trim();
  if (!trimmed) return null;
  const lower = trimmed.toLowerCase();
  if (lower.startsWith("javascript:") || lower.startsWith("vbscript:")) {
    return null;
  }
  if (lower.startsWith("//")) {
    return null;
  }
  if (lower.startsWith("data:")) {
    const mime = /^data:(image\/(?:png|jpeg|jpg|webp|gif));base64,/i.exec(trimmed)?.[1];
    return mime ? `data:${mime.toLowerCase()}` : null;
  }
  // Parse with URL constructor. We anchor against a placeholder
  // origin so relative paths parse cleanly; only the protocol
  // matters here.
  try {
    const parsed = new URL(trimmed, "http://__workbench_placeholder__/");
    return parsed.protocol;
  } catch {
    return "relative:";
  }
}

// walkAndSanitizeSvg recursively strips dangerous elements /
// attributes from a parsed SVG document tree. Element removal uses
// parentNode.removeChild so the element AND its subtree are gone
// (not just hidden — hidden elements still execute onload in some
// engines).
function walkAndSanitizeSvg(node: Element): void {
  // Snapshot children — removing during iteration corrupts the live
  // HTMLCollection / NodeList.
  const children = Array.from(node.children);
  for (const child of children) {
    if (DANGEROUS_SVG_TAGS.includes(child.tagName.toLowerCase())) {
      child.parentNode?.removeChild(child);
      continue;
    }
    for (const attr of Array.from(child.attributes)) {
      const name = attr.name.toLowerCase();
      if (name !== "href" && name !== "xlink:href" && name !== "src") {
        continue;
      }
      const scheme = resolveHrefScheme(attr.value);
      if (scheme === null) {
        child.removeAttribute(attr.name);
        continue;
      }
      // data: is only allowed on <image> elements (rasterized).
      if (scheme.startsWith("data:") && child.tagName.toLowerCase() !== "image") {
        child.removeAttribute(attr.name);
        continue;
      }
      // http / https / mailto must hit the explicit allowlist.
      if (
        scheme !== "relative:" &&
        !scheme.startsWith("data:") &&
        !ALLOWED_HREF_SCHEMES.has(scheme)
      ) {
        child.removeAttribute(attr.name);
      }
    }
    walkAndSanitizeSvg(child);
  }
}

// ALLOWED_IMAGE_MIMES — mime allowlist for data: URL fallback. We
// never build a data: URL with image/svg+xml (browsers execute scripts
// in SVG-in-img contexts inconsistently); use the inline <svg> sink
// for SVGs.
const ALLOWED_IMAGE_MIMES = new Set([
  "image/png",
  "image/jpeg",
  "image/webp",
  "image/gif",
]);

// safeImageSrc builds the <img src> for an attachment, returning null
// when the source can't be safely rendered. The scheme-allowlist is
// the strict gate; the data: URL fallback is only used when no
// `attachment.url` is set AND the agent-supplied mime is in the
// allowlist above.
export function safeImageSrc(attachment: {
  url?: string;
  mime?: string;
  data?: unknown;
}): string | null {
  if (attachment.url) {
    return safeHrefUrl(attachment.url);
  }
  if (typeof attachment.data === "string") {
    const mime = (attachment.mime ?? "").toLowerCase();
    if (!ALLOWED_IMAGE_MIMES.has(mime)) {
      return null;
    }
    return `data:${mime};base64,${attachment.data}`;
  }
  return null;
}

// safeHrefUrl — scheme allowlist for arbitrary href / src values. The
// renderer falls back to this when the agent provided a URL but no
// kind-specific validation exists (download links). Returns null when
// the URL is unsafe or unparseable.
//
// 0.3.43: the previous implementation accepted any string starting
// with "/" — that included "//evil.com/x" (protocol-relative URLs).
// The browser resolves protocol-relative URLs against the page origin
// (localhost on dev, file:// in packaged builds), so a successful GET
// to attacker.example leaks the user's IP / User-Agent / Referer.
// The fix routes through resolveHrefScheme which explicitly rejects
// "//" prefixes.
export function safeHrefUrl(raw: string): string | null {
  if (!raw) return null;
  const trimmed = raw.trim();
  if (!trimmed) return null;
  const scheme = resolveHrefScheme(trimmed);
  if (scheme === null) return null;
  if (scheme.startsWith("data:")) {
    // resolveHrefScheme only returns data:image/* (other data: variants
    // were rejected upstream). For href values we still double-check.
    const m = /^data:(image\/(?:png|jpeg|jpg|webp|gif));base64,/i.exec(trimmed);
    return m ? trimmed : null;
  }
  if (scheme === "relative:") return trimmed;
  if (ALLOWED_HREF_SCHEMES.has(scheme)) return trimmed;
  return null;
}
