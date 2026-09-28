package util

import (
	"testing"
)

func TestParseMentions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []Mention
	}{
		{
			name:    "simple agent mention",
			content: "[@Agent](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) please fix",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "agent name with square brackets",
			content: "[@David[TF]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) please fix",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "agent name with nested brackets",
			content: "[@Bot[v2][beta]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) help",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "multiple mentions with brackets",
			content: "[@A[1]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) and [@B[2]](mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb)",
			want: []Mention{
				{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
				{Type: "agent", ID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},
			},
		},
		{
			name:    "issue mention without @",
			content: "[MUL-123](mention://issue/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) is related",
			want:    []Mention{{Type: "issue", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "member mention",
			content: "[@Bob](mention://member/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) look",
			want:    []Mention{{Type: "member", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "all mention",
			content: "[@All](mention://all/all) heads up",
			want:    []Mention{{Type: "all", ID: "all"}},
		},
		{
			name:    "deduplicate same mention",
			content: "[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) and again [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "no mentions",
			content: "just a plain comment",
			want:    nil,
		},
		{
			name:    "escaped brackets in label",
			content: `[@David\[TF\]](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) hi`,
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMentions(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseMentions() returned %d mentions, want %d\ngot:  %+v\nwant: %+v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i].Type != tt.want[i].Type || got[i].ID != tt.want[i].ID {
					t.Errorf("mention[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHasMentionAll(t *testing.T) {
	tests := []struct {
		name     string
		mentions []Mention
		want     bool
	}{
		{"empty", nil, false},
		{"no all", []Mention{{Type: "agent", ID: "x"}}, false},
		{"has all", []Mention{{Type: "all", ID: "all"}}, true},
		{"mixed", []Mention{{Type: "agent", ID: "x"}, {Type: "all", ID: "all"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasMentionAll(tt.mentions); got != tt.want {
				t.Errorf("HasMentionAll() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParseMentionsBareURL pins the tolerant fallback for mention URLs that
// are not wrapped in the canonical markdown link form. The 0.5.123 incident:
// a squad leader's dispatch comment used "**@后端工程师**
// ([mention://agent/<uuid>])" for every mention; MentionRe saw none of them,
// so zero sub-agent tasks were enqueued and the delegation silently died.
func TestParseMentionsBareURL(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []Mention
	}{
		{
			name:    "bold label + parenthesized bare URL (incident variant)",
			content: "**@后端工程师** ([mention://agent/877a086e-c05e-461a-9ffa-862aceb3c32f]) fix the backend",
			want:    []Mention{{Type: "agent", ID: "877a086e-c05e-461a-9ffa-862aceb3c32f"}},
		},
		{
			name:    "bare URL in plain prose",
			content: "see mention://squad/8a3c64b8-81bb-4dec-b5da-829f7c03506d for details",
			want:    []Mention{{Type: "squad", ID: "8a3c64b8-81bb-4dec-b5da-829f7c03506d"}},
		},
		{
			name:    "two bare URLs",
			content: "([mention://agent/877a086e-c05e-461a-9ffa-862aceb3c32f]) and ([mention://agent/321db75b-2be6-485b-9f43-e07088cef440])",
			want: []Mention{
				{Type: "agent", ID: "877a086e-c05e-461a-9ffa-862aceb3c32f"},
				{Type: "agent", ID: "321db75b-2be6-485b-9f43-e07088cef440"},
			},
		},
		{
			name:    "canonical + bare of the same id dedups to one",
			content: "[@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa) (**@A** ([mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa]))",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			name:    "bare all mention",
			content: "ping (mention://all/all) immediately",
			want:    []Mention{{Type: "all", ID: "all"}},
		},
		{
			name:    "canonical matches first, bare fills the rest (scan order)",
			content: "([mention://agent/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb]) then [@A](mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa)",
			want: []Mention{
				{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
				{Type: "agent", ID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},
			},
		},
		{
			name:    "bare scan is a substring match even after url-like text",
			content: "docs at https://example.com/mention://agent/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
			want:    []Mention{{Type: "agent", ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}},
		},
		{
			// JYF-490 shipped bareMentionRe without a trailing delimiter
			// anchor, so the hex class happily ate the leading "A" of a
			// human name and produced a phantom member "A". Nothing down the
			// line rejects that id — it is dispatched. The uuid-shape branch
			// is what rejects it now.
			name:    "non-uuid id is not truncated into a phantom mention",
			content: "[@Alice](mention://member/Alice) please review",
			want:    nil,
		},
		{
			name:    "bare non-uuid id yields no mention",
			content: "contact (mention://agent/backend-team) about the outage",
			want:    nil,
		},
		{
			name:    "uuid prefix of a longer token is not a mention",
			content: "stale ref mention://agent/877a086e-c05e-461a-9ffa trailing junk",
			want:    nil,
		},
		{
			name:    "non-uuid prose never suppresses the real uuid next to it",
			content: "[@Alice](mention://member/Alice) and (mention://agent/321db75b-2be6-485b-9f43-e07088cef440)",
			want:    []Mention{{Type: "agent", ID: "321db75b-2be6-485b-9f43-e07088cef440"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseMentions(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("ParseMentions() returned %d mentions, want %d\ngot:  %+v\nwant: %+v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i].Type != tt.want[i].Type || got[i].ID != tt.want[i].ID {
					t.Errorf("mention[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
