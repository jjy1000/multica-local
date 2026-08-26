/**
 * Flatten description Markdown into a one-line plain-text preview. Shared by
 * every surface that shows a description snippet next to an issue.
 *
 * Fork note: upstream strips channel-media provenance markers first, but this
 * single-user fork has no channel surfaces, so stripChannelMediaMarkers is
 * not ported — the image/anchor Markdown is removed a line below regardless.
 */
export function descriptionPreview(markdown: string): string {
  return (
    markdown
      // Backslash-escaped punctuation is unescaped first, and the order is
      // load-bearing. The editor serializes a link as `\[label\](url)`, so the
      // link rule below sees `\[label\]` and its `[^\]]+` capture swallows the
      // trailing backslash while the leading one survives — the preview then
      // reads `\label\`. Unescaping after any structural rule is too late,
      // because the escapes are what stop those rules from matching.
      // The character class is CommonMark's escapable set: all ASCII
      // punctuation. A backslash before anything else (a letter, a space) is a
      // literal backslash in prose and is left alone.
      .replace(/\\([!"#$%&'()*+,\-./:;<=>?@[\\\]^_`{|}~])/g, "$1")
      // The destination group tolerates one level of balanced parentheses,
      // e.g. `(https://en.wikipedia.org/wiki/Foo_(bar))` or a nested link
      // produced when a URL inside a link label gets autolinked, which
      // serializes as `[label]([https://x](https://x))`. A naive `[^)]+`
      // stops at the first `)` and leaves the outer one stranded in the
      // preview text — do not simplify this back to `[^)]+`.
      .replace(/!file\[[^\]]*\]\((?:[^()]|\([^()]*\))*\)/g, "")
      .replace(/!\[[^\]]*\]\((?:[^()]|\([^()]*\))*\)/g, "")
      .replace(/\[([^\]]+)\]\((?:[^()]|\([^()]*\))*\)/g, "$1")
      .replace(/[*_`~]+/g, "")
      .replace(/^[\s>#]+/gm, "")
      .replace(/\s+/g, " ")
      .trim()
  );
}
