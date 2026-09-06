package agent

import (
	"testing"
)

// codexRetiredCompactionLiveError is the failure exactly as GH #8000 reported
// it, request id and cf-ray included — the hex ids are part of the fixture on
// purpose, since a status-code match would find digits inside them.
//
// Ported from upstream ca47495fc (MUL-7053 #8032). The second test in the
// upstream file, TestCodexLaunchArgsCanSelectTheRetiredCompactionRoute, is
// SKIPPED in the fork — it depends on NormalizeCodexLaunchArgs,
// codexFastServiceTier, FilterLaunchPrefix and stripCodexFastModeConflicts,
// none of which exist in fork's codex.go yet.
const codexRetiredCompactionLiveError = `Error running remote compact task: unexpected status 404 Not Found: ` +
	`{"detail":"Not Found"}, url: https://chatgpt.com/backend-api/codex/responses/compact, ` +
	`cf-ray: a35507973eee3d57-SJC, request id: 9eed88ee-822d-446e-9b73-df49d56fe7e0`

func TestCodexRetiredCompactionError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		errText string
		want    bool
	}{
		{"live failure", codexRetiredCompactionLiveError, true},
		// The path is what identifies the route, so any status on it counts:
		// a compaction request reaching /responses/compact at all means the
		// legacy route was selected, and the remedy is the same either way.
		{
			"retired path with a non-404 status",
			`Error running remote compact task: unexpected status 502 Bad Gateway, ` +
				`url: https://chatgpt.com/backend-api/codex/responses/compact`,
			true,
		},
		{
			"case insensitive",
			`ERROR RUNNING REMOTE COMPACT TASK: UNEXPECTED STATUS 404 NOT FOUND, ` +
				`URL: HTTPS://CHATGPT.COM/BACKEND-API/CODEX/RESPONSES/COMPACT`,
			true,
		},

		// The reason the path is required rather than the status. Codex wraps
		// v2 compaction failures in the SAME "Error running remote compact
		// task" prefix (compact_remote_v2.rs), and a v2 stream can answer 404
		// too. Firing here would tell a user whose setting is already correct
		// to go turn it off — the exact opposite of the fix.
		{
			"v2 route returning the same status",
			`Error running remote compact task: unexpected status 404 Not Found: {"detail":"Not Found"}, ` +
				`url: https://chatgpt.com/backend-api/codex/responses`,
			false,
		},
		{
			"v2 route failing some other way",
			`Error running remote compact task: remote compaction v2 stream closed before response.completed`,
			false,
		},
		{
			"no url tail",
			`Error running remote compact task: unexpected status 404 Not Found: {"detail":"Not Found"}`,
			false,
		},
		// The path without the compaction marker is some other request.
		{
			"path outside a compaction failure",
			`Error running turn: unexpected status 404 Not Found, ` +
				`url: https://chatgpt.com/backend-api/codex/responses/compact`,
			false,
		},
		{"unrelated codex failure", "codex thread/resume failed: token too long", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := CodexRetiredCompactionError(tc.errText); got != tc.want {
				t.Errorf("CodexRetiredCompactionError(%q) = %v, want %v", tc.errText, got, tc.want)
			}
		})
	}
}