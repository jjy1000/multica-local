// Mention-markup parser for comment content. Ported from upstream MUL-7447
// (the self-contained half of `comment-trigger-outcomes.ts`; the fork has no
// blocked-trigger layer, so only the parser came over).

// Source for a rendered mention in comment markdown, capturing the label the
// user picked, the target type, and the target id: `[@Go](mention://agent/UUID)`.
// Kept as a string so every parse builds its OWN global RegExp — sharing one
// global instance across `matchAll` calls leaks `lastIndex` and drops matches.
const MENTION_MARKUP_SOURCE =
  "\\[@?(.+?)\\]\\(mention:\\/\\/(member|agent|squad|issue|all)\\/([0-9a-fA-F-]+|all)\\)";

export interface ParsedMention {
  label: string;
  type: string;
  id: string;
}

// Every mention in the body, in order. Callers that only care about a subset
// (e.g. skipping `issue` links, or deduping) filter the result.
export function parseMentions(content: string): ParsedMention[] {
  const re = new RegExp(MENTION_MARKUP_SOURCE, "g");
  const mentions: ParsedMention[] = [];
  for (const match of content.matchAll(re)) {
    const label = match[1];
    const type = match[2];
    const id = match[3];
    if (!label || !type || !id) continue;
    mentions.push({ label, type, id });
  }
  return mentions;
}
