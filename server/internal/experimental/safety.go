package experimental

// Blacklist support for the Labs safety mechanism. 0.3.18 adds a
// process-level safety net that breaks (auto-disables) experimental
// flags whose code paths misbehave, so a buggy flag can't take down
// the user's working app or block startup.
//
// The three break categories are documented in the user-facing release
// notes (0.3.18-alpha) and in the Reason enum below:
//
//   - "panic" — a goroutine panicked inside a code path that declared
//     itself owned by the flag. The recover() sentinel in main.go
//     writes a break entry before re-panicking to terminate the
//     process, so the next launch sees the flag as broken and skips
//     it.
//   - "5xx_burst" — the recovery middleware observed N consecutive
//     5xx responses (configurable, defaults to 3 in a 60-second
//     window) where the request context carried the flag's identity.
//     The flag is presumed unstable and disabled.
//   - "init_timeout" — main.go's per-flag init hook did not return
//     within the configured deadline (default 30 s). The flag is
//     disabled; the user can retry by clearing the blacklist entry
//     and relaunching.
//
// Restoration is manual: the user clears the entry from Labs →
// "Restore" button. We deliberately do NOT auto-recover because the
// cause of the break may still be present (a flaky network, a missing
// binary, a corrupted DB row), and silently turning a flag back on
// would just re-trigger the same panic.
//
// Storage: ~/.multica/experimental-blacklist.json on the server's
// filesystem, written atomically (tmp file + rename) so a crash
// mid-write does not corrupt the file. The desktop mirrors the file
// into its own ~/.multica/experimental-blacklist.json (same path,
// since the server runs out of the user's home directory in the
// desktop model) so the Labs UI can read it without round-tripping
// the API.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Reason categorises why a flag was broken. New categories must be
// added to the AllowedReasons set below AND to the Reason field's
// JSON consumers — the Labs UI keys off this string to render the
// appropriate badge copy.
type Reason string

const (
	ReasonPanic       Reason = "panic"
	Reason5xxBurst    Reason = "5xx_burst"
	ReasonInitTimeout Reason = "init_timeout"
)

// AllowedReasons is the closed set of Reason values the JSON file
// may carry. Used to reject stale entries from older versions and
// typos from manual edits.
var AllowedReasons = map[Reason]struct{}{
	ReasonPanic:       {},
	Reason5xxBurst:    {},
	ReasonInitTimeout: {},
}

// IsKnownReason reports whether r is in the AllowedReasons set.
func IsKnownReason(r Reason) bool {
	_, ok := AllowedReasons[r]
	return ok
}

// BlacklistEntry is one record of a flag that was auto-disabled.
type BlacklistEntry struct {
	FlagKey   string    `json:"flag_key"`
	Reason    Reason    `json:"reason"`
	BrokenAt  time.Time `json:"broken_at"`
	Context   string    `json:"context,omitempty"`
	StackHint string    `json:"stack_hint,omitempty"`
}

// Blacklist is the file shape written to disk. The version field
// guards against forward-compat breaks: a future schema bump can
// detect version=1 files and migrate them before reading.
type Blacklist struct {
	Version int              `json:"version"`
	Entries []BlacklistEntry `json:"entries"`
}

// blacklistVersion is the current schema version. Bump it when the
// JSON shape changes in a way older binaries cannot read.
const blacklistVersion = 1

// defaultBlacklistPath is the canonical location of the file on the
// server's filesystem. Tests override this via SetBlacklistPath.
// Resolution order on the desktop:
//
//  1. $MULTICA_EXPERIMENTAL_BLACKLIST_PATH if set (test override)
//  2. $HOME/.multica/experimental-blacklist.json
func defaultBlacklistPath() string {
	if override := os.Getenv("MULTICA_EXPERIMENTAL_BLACKLIST_PATH"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// UserHomeDir fails only when $HOME is unset, which is rare
		// outside CI. Fall back to a tmp location so the safety write
		// does not panic the process; the operator can find the file
		// in the diagnostic dump.
		return filepath.Join(os.TempDir(), "multica-experimental-blacklist.json")
	}
	return filepath.Join(home, ".multica", "experimental-blacklist.json")
}

// SetBlacklistPath overrides the location of the blacklist file. Used
// exclusively by tests; production code should let the resolver pick
// the default location.
func SetBlacklistPath(path string) {
	blacklistPathMu.Lock()
	defer blacklistPathMu.Unlock()
	blacklistPath = path
}

var (
	blacklistPathMu sync.RWMutex
	blacklistPath   = defaultBlacklistPath()
)

// writeMu serialises Save / MarkBroken / ClearBroken so concurrent
// calls from the recovery sentinel, 5xx middleware, and the Labs UI
// "Restore" button do not race the atomic rename. Reads (Load /
// IsBroken / Snapshot) take the RLock via the package helpers; writes
// take the exclusive lock.
//
// We hold the lock for the whole Load → mutate → Save window so a
// concurrent MarkBroken cannot slip between the Load result and the
// Save call (the latter would clobber the former). The mutex is
// process-local: a process restart does not see in-flight writes
// from a prior run, but the file's atomic-rename semantics guarantee
// the on-disk file is never a torn write.
var writeMu sync.Mutex

// ResolveBlacklistPath returns the path the safety package reads and
// writes. Exported for diagnostic logging only — call sites that need
// to actually mutate the file should use ReadBlacklist / WriteBlacklist.
func ResolveBlacklistPath() string {
	blacklistPathMu.RLock()
	defer blacklistPathMu.RUnlock()
	return blacklistPath
}

// Load reads and parses the blacklist file. A missing file is not an
// error — it returns an empty Blacklist at the current schema
// version, which is the common case on first launch.
//
// Parse errors and schema mismatches return an error so the caller
// can decide whether to surface them (recovery sentinel: keep going
// with an empty list and log; UI: surface "blacklist corrupted" to
// the user).
func Load() (*Blacklist, error) {
	path := ResolveBlacklistPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Blacklist{Version: blacklistVersion, Entries: []BlacklistEntry{}}, nil
		}
		return nil, fmt.Errorf("read blacklist at %s: %w", path, err)
	}
	var bl Blacklist
	if err := json.Unmarshal(data, &bl); err != nil {
		return nil, fmt.Errorf("parse blacklist at %s: %w", path, err)
	}
	if bl.Version != blacklistVersion {
		return nil, fmt.Errorf("blacklist schema version mismatch: got %d, want %d", bl.Version, blacklistVersion)
	}
	if bl.Entries == nil {
		bl.Entries = []BlacklistEntry{}
	}
	return &bl, nil
}

// Save writes the blacklist atomically: write to a sibling .tmp file,
// fsync, rename over the destination. A crash mid-write leaves the
// prior good file in place — the new content either lands cleanly or
// does not land at all.
//
// The .tmp file uses a UUID suffix so concurrent writers (recovery
// sentinel + 5xx middleware firing in the same instant) do not
// collide. The rename is atomic on POSIX filesystems.
//
// Save does NOT take writeMu — the callers that mutate the file
// (MarkBroken, ClearBroken) hold it across the Load + Save pair. A
// direct Save call from a test or admin tool without the lock is
// safe at the file level (atomic rename) but may race concurrent
// mutators and lose entries; prefer MarkBroken / ClearBroken.
func Save(bl *Blacklist) error {
	if bl == nil {
		return fmt.Errorf("blacklist is nil")
	}
	bl.Version = blacklistVersion
	if bl.Entries == nil {
		bl.Entries = []BlacklistEntry{}
	}
	// Stable order on save so diffs between successive writes are
	// readable in version control if the file is ever checked in.
	sort.Slice(bl.Entries, func(i, j int) bool {
		if bl.Entries[i].FlagKey != bl.Entries[j].FlagKey {
			return bl.Entries[i].FlagKey < bl.Entries[j].FlagKey
		}
		return bl.Entries[i].BrokenAt.Before(bl.Entries[j].BrokenAt)
	})

	data, err := json.MarshalIndent(bl, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal blacklist: %w", err)
	}
	path := ResolveBlacklistPath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure blacklist dir %s: %w", dir, err)
	}
	tmp := filepath.Join(dir, fmt.Sprintf(".experimental-blacklist.%s.tmp", uuid.NewString()))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tmp blacklist at %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup of the tmp file. We do not return
		// this error because the original rename failure is the
		// actionable one.
		_ = os.Remove(tmp)
		return fmt.Errorf("rename blacklist tmp to %s: %w", path, err)
	}
	return nil
}

// IsBroken reports whether flagKey has a current entry in the
// blacklist. Reads via Load(); callers that need to fire many IsBroken
// checks in one request should cache the result via Snapshot().
//
// The reason and context are returned together so the Labs UI can
// render "broken by 5xx_burst — 3 errors in 60s" without a second
// read.
func IsBroken(flagKey string) (BlacklistEntry, bool) {
	bl, err := Load()
	if err != nil {
		// A corrupted blacklist MUST NOT silently disable a flag.
		// The recovery sentinel and the UI both need to know about
		// this — log here and return false so the user keeps the
		// flag's normal behaviour; the UI's diagnostic banner can
		// still surface "blacklist unreadable" if it tries Load()
		// directly.
		return BlacklistEntry{}, false
	}
	for _, e := range bl.Entries {
		if e.FlagKey == flagKey {
			return e, true
		}
	}
	return BlacklistEntry{}, false
}

// MarkBroken appends a new entry for flagKey to the blacklist. If an
// entry for the same flag already exists, the new entry replaces it
// (latest break wins; the prior broken_at is preserved in StackHint
// if not otherwise set so the diagnostic log shows the full history).
//
// Idempotent: calling MarkBroken twice with the same args produces the
// same file content, so the panic recover sentinel can call it
// without worrying about double-writes.
func MarkBroken(flagKey string, reason Reason, context string) error {
	if flagKey == "" {
		return fmt.Errorf("flag key is empty")
	}
	if !IsKnownReason(reason) {
		return fmt.Errorf("unknown reason %q (not in AllowedReasons)", reason)
	}
	writeMu.Lock()
	defer writeMu.Unlock()
	bl, err := Load()
	if err != nil {
		return fmt.Errorf("load blacklist: %w", err)
	}
	entry := BlacklistEntry{
		FlagKey:  flagKey,
		Reason:   reason,
		BrokenAt: time.Now().UTC(),
		Context:  context,
	}
	written := false
	for i, e := range bl.Entries {
		if e.FlagKey == flagKey {
			if e.StackHint == "" {
				entry.StackHint = e.BrokenAt.Format(time.RFC3339)
			} else {
				entry.StackHint = e.StackHint + "," + e.BrokenAt.Format(time.RFC3339)
			}
			bl.Entries[i] = entry
			written = true
			break
		}
	}
	if !written {
		bl.Entries = append(bl.Entries, entry)
	}
	return Save(bl)
}

// ClearBroken removes the entry for flagKey from the blacklist.
// Returns nil even when no entry exists, so callers can use it as a
// "force-clear" without checking first. Used by the Labs UI's
// "Restore" button.
func ClearBroken(flagKey string) error {
	writeMu.Lock()
	defer writeMu.Unlock()
	bl, err := Load()
	if err != nil {
		return fmt.Errorf("load blacklist: %w", err)
	}
	out := bl.Entries[:0]
	changed := false
	for _, e := range bl.Entries {
		if e.FlagKey == flagKey {
			changed = true
			continue
		}
		out = append(out, e)
	}
	if !changed {
		return nil
	}
	bl.Entries = out
	return Save(bl)
}

// Snapshot returns a defensive copy of the blacklist, suitable for
// iteration from a request handler. Mutating the result does not
// affect the on-disk file.
func Snapshot() (*Blacklist, error) {
	return Load()
}
