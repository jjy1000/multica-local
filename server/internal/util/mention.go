package util

import "regexp"

// Mention represents a parsed @mention from markdown content.
type Mention struct {
	Type string // "member", "agent", "issue", or "all"
	ID   string // user_id, agent_id, issue_id, or "all"
}

// MentionRe matches [@Label](mention://type/id) or [Label](mention://issue/id) in markdown.
// The @ prefix is optional to support issue mentions which use [MUL-123](mention://issue/...).
// Uses .+? (non-greedy) instead of [^\]]* so labels containing square brackets
// (e.g. "David[TF]") are matched correctly — the ](mention:// anchor is specific
// enough to prevent over-matching.
var MentionRe = regexp.MustCompile(`\[@?(.+?)\]\(mention://(member|agent|squad|issue|all)/([0-9a-fA-F-]+|all)\)`)

// bareMentionRe matches a mention URL that is NOT wrapped in the canonical
// markdown link form — e.g. "**@Name** (mention://agent/<uuid>)" where the
// label and the link target were split apart. Agents deviate from the
// [@Label](mention://type/id) shape taught in multica-mentioning/SKILL.md,
// and a missed mention silently drops the dispatch: the 0.5.123 incident had
// a squad leader's 派工 comment route zero tasks because every mention used
// the bold-label + parenthesized-URL variant. The `all` alternative must
// come FIRST — unlike MentionRe this pattern has no trailing `\)` anchor, so
// the hex-char class would otherwise greedily eat the "a" of "all/all" and
// parse a bogus id. The type:id dedup in ParseMentions collapses bare hits
// that MentionRe already captured.
var bareMentionRe = regexp.MustCompile(`mention://(member|agent|squad|issue|all)/(all|[0-9a-fA-F-]+)`)

// IsMentionAll returns true if the mention is an @all mention.
func (m Mention) IsMentionAll() bool {
	return m.Type == "all"
}

// ParseMentions extracts deduplicated mentions from markdown content.
// Canonical markdown-link mentions are parsed first; bare mention URLs the
// link regex cannot see are picked up by the tolerant fallback scan (same
// type:id dedup applies, so a mention matched by both shapes lands once).
func ParseMentions(content string) []Mention {
	matches := MentionRe.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var result []Mention
	for _, m := range matches {
		key := m[2] + ":" + m[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, Mention{Type: m[2], ID: m[3]})
	}
	for _, m := range bareMentionRe.FindAllStringSubmatch(content, -1) {
		key := m[1] + ":" + m[2]
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, Mention{Type: m[1], ID: m[2]})
	}
	return result
}

// HasMentionAll returns true if any mention in the slice is an @all mention.
func HasMentionAll(mentions []Mention) bool {
	for _, m := range mentions {
		if m.IsMentionAll() {
			return true
		}
	}
	return false
}
