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
// the bold-label + parenthesized-URL variant. The type:id dedup in
// ParseMentions collapses bare hits that MentionRe already captured.
//
// Because this pattern has no trailing delimiter anchor, the id must be
// self-terminating or the hex class eats a prefix of a longer token and
// yields a bogus id. Two guards, both required:
//
//  1. `all` comes FIRST in the alternation. With the hex class first,
//     "all/all" loses its "a" to the char class and parses as id "ll".
//     (JYF-490 fixed this case.)
//  2. The uuid branch spells out the full 8-4-4-4-12 shape instead of a bare
//     `[0-9a-fA-F-]+`. Without it, "mention://member/Alice" matches the single
//     leading "A" (a legal hex digit) and dispatches to a phantom member "A"
//     — the regression TestMentioningSkillTeachesTheParserContract caught. A
//     trailing lookahead would be the obvious fix but Go's regexp is RE2,
//     which has no lookaround, so the shape itself must carry the anchor.
//
// Every id this fork mints is a 36-char UUID, so the strict shape costs
// nothing on real mentions; it only rejects prose that merely looks like one.
var bareMentionRe = regexp.MustCompile(`mention://(member|agent|squad|issue|all)/(all|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})`)

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
