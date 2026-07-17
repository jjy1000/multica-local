package experimental

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// withTempBlacklist installs a per-test blacklist path inside t.TempDir()
// and restores the package default when the test ends.
//
// IMPORTANT: do NOT combine this helper with t.Parallel(). The safety
// package uses process-local state (the blacklistPath variable, the
// writeMu mutex). Parallel tests would race the package-level path
// pointer and the file-level mutex. The tests below run serially for
// this reason — see the comment on writeMu in safety.go.
func withTempBlacklist(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "experimental-blacklist.json")
	SetBlacklistPath(path)
	t.Cleanup(func() { SetBlacklistPath(defaultBlacklistPath()) })
	return path
}

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	path := withTempBlacklist(t)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be missing before Load, got err=%v", path, err)
	}
	bl, err := Load()
	if err != nil {
		t.Fatalf("Load on missing file should not error: %v", err)
	}
	if bl.Version != blacklistVersion {
		t.Errorf("Version = %d, want %d", bl.Version, blacklistVersion)
	}
	if len(bl.Entries) != 0 {
		t.Errorf("Entries = %d, want 0", len(bl.Entries))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	withTempBlacklist(t)

	in := &Blacklist{
		Version: blacklistVersion,
		Entries: []BlacklistEntry{
			{FlagKey: "mythos_swarm", Reason: ReasonPanic, BrokenAt: time.Now().UTC()},
			{FlagKey: "pythia_oracle", Reason: ReasonInitTimeout, BrokenAt: time.Now().UTC().Add(-time.Minute)},
		},
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("len(Entries) = %d, want 2", len(out.Entries))
	}
	if out.Entries[0].FlagKey != "mythos_swarm" {
		t.Errorf("Entries[0].FlagKey = %q, want mythos_swarm", out.Entries[0].FlagKey)
	}
}

func TestMarkBrokenCreatesEntry(t *testing.T) {
	withTempBlacklist(t)

	if err := MarkBroken("claude_science_lab", ReasonPanic, "main.go:142"); err != nil {
		t.Fatalf("MarkBroken: %v", err)
	}
	entry, ok := IsBroken("claude_science_lab")
	if !ok {
		t.Fatal("IsBroken returned false after MarkBroken")
	}
	if entry.Reason != ReasonPanic {
		t.Errorf("Reason = %q, want %q", entry.Reason, ReasonPanic)
	}
	if entry.Context != "main.go:142" {
		t.Errorf("Context = %q, want main.go:142", entry.Context)
	}
}

func TestMarkBrokenReplacesExisting(t *testing.T) {
	withTempBlacklist(t)

	if err := MarkBroken("flag", ReasonPanic, "first"); err != nil {
		t.Fatalf("first MarkBroken: %v", err)
	}
	time.Sleep(2 * time.Millisecond) // ensure BrokenAt differs
	if err := MarkBroken("flag", Reason5xxBurst, "second"); err != nil {
		t.Fatalf("second MarkBroken: %v", err)
	}
	bl, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bl.Entries) != 1 {
		t.Fatalf("Entries len = %d, want 1 (replaced, not appended)", len(bl.Entries))
	}
	if bl.Entries[0].Reason != Reason5xxBurst {
		t.Errorf("Reason = %q, want %q", bl.Entries[0].Reason, Reason5xxBurst)
	}
	if bl.Entries[0].Context != "second" {
		t.Errorf("Context = %q, want second", bl.Entries[0].Context)
	}
	if bl.Entries[0].StackHint == "" {
		t.Error("StackHint should record the prior BrokenAt timestamp")
	}
}

func TestClearBrokenRemovesEntry(t *testing.T) {
	withTempBlacklist(t)

	if err := MarkBroken("a", ReasonPanic, ""); err != nil {
		t.Fatalf("MarkBroken a: %v", err)
	}
	if err := MarkBroken("b", ReasonInitTimeout, ""); err != nil {
		t.Fatalf("MarkBroken b: %v", err)
	}
	if err := ClearBroken("a"); err != nil {
		t.Fatalf("ClearBroken: %v", err)
	}
	bl, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bl.Entries) != 1 {
		t.Fatalf("Entries len = %d, want 1", len(bl.Entries))
	}
	if bl.Entries[0].FlagKey != "b" {
		t.Errorf("remaining entry = %q, want b", bl.Entries[0].FlagKey)
	}
	// idempotent
	if err := ClearBroken("a"); err != nil {
		t.Fatalf("ClearBroken idempotent: %v", err)
	}
}

func TestMarkBrokenRejectsUnknownReason(t *testing.T) {
	withTempBlacklist(t)

	if err := MarkBroken("x", Reason("garbage"), ""); err == nil {
		t.Fatal("expected error for unknown reason, got nil")
	}
}

func TestIsKnownReason(t *testing.T) {
	for _, r := range []Reason{ReasonPanic, Reason5xxBurst, ReasonInitTimeout} {
		if !IsKnownReason(r) {
			t.Errorf("IsKnownReason(%q) = false, want true", r)
		}
	}
	for _, r := range []Reason{"", "5xx", "panic_recover"} {
		if IsKnownReason(r) {
			t.Errorf("IsKnownReason(%q) = true, want false", r)
		}
	}
}

func TestSaveSortsEntries(t *testing.T) {
	withTempBlacklist(t)

	in := &Blacklist{
		Entries: []BlacklistEntry{
			{FlagKey: "z", Reason: ReasonPanic, BrokenAt: time.Now()},
			{FlagKey: "a", Reason: ReasonPanic, BrokenAt: time.Now().Add(-time.Hour)},
			{FlagKey: "a", Reason: ReasonPanic, BrokenAt: time.Now().Add(-2 * time.Hour)},
		},
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	bl, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bl.Entries) < 3 {
		t.Fatalf("Entries len = %d, want 3", len(bl.Entries))
	}
	if bl.Entries[0].FlagKey != "a" || bl.Entries[1].FlagKey != "a" || bl.Entries[2].FlagKey != "z" {
		t.Errorf("entries not sorted: %+v", bl.Entries)
	}
}

func TestSaveIsAtomic(t *testing.T) {
	path := withTempBlacklist(t)

	if err := Save(&Blacklist{Entries: []BlacklistEntry{
		{FlagKey: "first", Reason: ReasonPanic, BrokenAt: time.Now()},
	}}); err != nil {
		t.Fatalf("Save 1: %v", err)
	}
	if err := Save(&Blacklist{Entries: []BlacklistEntry{
		{FlagKey: "second", Reason: ReasonPanic, BrokenAt: time.Now()},
	}}); err != nil {
		t.Fatalf("Save 2: %v", err)
	}
	// After the second save, no leftover .tmp files should remain in
	// the directory.
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".experimental-blacklist.*.tmp"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("leftover tmp files: %v", matches)
	}
	bl, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bl.Entries) != 1 || bl.Entries[0].FlagKey != "second" {
		t.Errorf("Entries = %+v, want only 'second'", bl.Entries)
	}
}

func TestSaveRejectsSchemaMismatchOnLoad(t *testing.T) {
	withTempBlacklist(t)
	if err := os.WriteFile(ResolveBlacklistPath(), []byte(`{"version":99,"entries":[]}`), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "version mismatch") {
		t.Fatalf("Load should fail on version mismatch, got err=%v", err)
	}
}

func TestConcurrentMarkBrokenSafe(t *testing.T) {
	withTempBlacklist(t)

	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			// All goroutines write to the same flag key; the
			// writeMu serialises them and the file ends up with
			// one entry (last write wins).
			if err := MarkBroken("concurrent", Reason5xxBurst, "from-goroutine"); err != nil {
				t.Errorf("MarkBroken: %v", err)
			}
		}()
	}
	wg.Wait()
	bl, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(bl.Entries) != 1 {
		t.Errorf("Entries = %d, want 1", len(bl.Entries))
	}
	if bl.Entries[0].FlagKey != "concurrent" {
		t.Errorf("FlagKey = %q, want concurrent", bl.Entries[0].FlagKey)
	}
}

func TestJSONShape(t *testing.T) {
	withTempBlacklist(t)
	entry := BlacklistEntry{
		FlagKey:  "shape_test",
		Reason:   Reason5xxBurst,
		BrokenAt: time.Date(2026, 7, 14, 10, 0, 0, 0, time.UTC),
		Context:  "ctx",
	}
	bl := &Blacklist{Version: blacklistVersion, Entries: []BlacklistEntry{entry}}
	if err := Save(bl); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(ResolveBlacklistPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	entries, ok := raw["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("entries shape wrong: %+v", raw["entries"])
	}
	first, _ := entries[0].(map[string]any)
	if first["flag_key"] != "shape_test" {
		t.Errorf("flag_key = %v", first["flag_key"])
	}
	if first["reason"] != "5xx_burst" {
		t.Errorf("reason = %v", first["reason"])
	}
}
