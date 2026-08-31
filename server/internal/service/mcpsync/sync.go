// Package mcpsync mirrors the user's Claude Code MCP servers into the
// mcp_sync_server table so Multica agents can use the same MCP set without
// hand-copying config, and so the settings "MCP 管理" tab can display it.
//
// Contract (0.5.92):
//   - One-way, source-of-truth = the source file (default ~/.claude.json,
//     key `mcpServers`). The mirror is read-only everywhere else: no API
//     edits or deletes rows; a server dropped from the source flips to
//     status='removed' and stops taking part in the claim-time merge.
//   - Change detection hashes ONLY the mcpServers subtree (canonical JSON,
//     sorted keys). The whole ~/.claude.json file is rewritten by Claude
//     Code on every startup (numStartups, feature caches, project history),
//     so mtime or whole-file hashes would thrash.
//   - A missing or malformed source never destroys the mirror: the last
//     good snapshot stays as the merge source and the error is recorded in
//     mcp_sync_state.last_error for the settings tab to surface.
package mcpsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/pkg/db/generated"
)

// DefaultInterval is the poll cadence. The file is local and tiny; the
// canonical-subtree hash makes each tick a read + hash + single indexed
// query, so one minute is well within budget while keeping mirror staleness
// imperceptible for the settings tab.
const DefaultInterval = time.Minute

// MinInterval floors the configured cadence — a misconfigured env override
// must not turn the worker into a busy loop against the DB.
const MinInterval = 10 * time.Second

type Config struct {
	// SourcePath is the Claude Code config file to mirror. Empty falls back
	// to ~/.claude.json.
	SourcePath string
	// Interval is the poll cadence. Zero falls back to DefaultInterval.
	Interval time.Duration
}

func (c Config) withDefaults() (Config, error) {
	cfg := c
	if cfg.SourcePath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, fmt.Errorf("mcpsync: resolve home dir: %w", err)
		}
		cfg.SourcePath = filepath.Join(home, ".claude.json")
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Interval < MinInterval {
		cfg.Interval = MinInterval
	}
	return cfg, nil
}

// Summary reports what one SyncOnce pass did.
type Summary struct {
	// Changed is true when the source subtree hash differed from the last
	// applied snapshot (or the previous pass had errored) and the mirror was
	// rewritten. A false Changed pass still refreshes last_synced_at.
	Changed bool
	Added   int
	Updated int
	Removed int
	// Skipped counts servers dropped by validation (never expected with a
	// strict parser — see ExtractMcpServers — but carried for observability).
	Skipped int
	// ServerCount is the number of servers in the source snapshot.
	ServerCount int
}

// Syncer is the background worker mirroring the Claude Code MCP config into
// the DB. Lifecycle mirrors causalgraph.CausalMaintenance: Start is
// idempotent, Stop owns the lifetime, Run blocks until Stop.
type Syncer struct {
	pool    *pgxpool.Pool
	queries *db.Queries
	cfg     Config
	logger  *slog.Logger

	// mu serializes SyncOnce: the ticker loop and the settings-tab manual
	// refresh endpoint must not interleave half-applied snapshots. The sync
	// itself is transactional, but serializing keeps summaries accurate.
	mu       sync.Mutex
	running  atomic.Bool
	stopped  chan struct{}
	stopOnce sync.Once
}

func NewSyncer(pool *pgxpool.Pool, queries *db.Queries, cfg Config, logger *slog.Logger) (*Syncer, error) {
	if pool == nil || queries == nil {
		return nil, errors.New("mcpsync: pool and queries are required")
	}
	withDefaults, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		pool:    pool,
		queries: queries,
		cfg:     withDefaults,
		logger:  logger,
		stopped: make(chan struct{}),
	}, nil
}

// SourcePath reports the resolved source file path (for boot logging).
func (s *Syncer) SourcePath() string { return s.cfg.SourcePath }

// Start launches the worker loop. Idempotent via CAS — a second Start is a
// no-op, matching the causal-maintenance contract.
func (s *Syncer) Start() {
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	go s.Run()
}

// Stop ends the worker loop. Safe to call multiple times.
func (s *Syncer) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
	})
}

// Run syncs once immediately (boot anchor — a server restarting every few
// minutes must not stretch the effective cadence to interval-per-restart),
// then ticks until Stop. Mirrors causalgraph.CausalMaintenance.Run.
func (s *Syncer) Run() {
	ctx := context.Background()
	s.syncAndLog(ctx)

	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopped:
			return
		case <-ticker.C:
			s.syncAndLog(ctx)
		}
	}
}

func (s *Syncer) syncAndLog(ctx context.Context) {
	summary, err := s.SyncOnce(ctx)
	if err != nil {
		s.logger.Warn("mcpsync: sync pass failed", "source", s.cfg.SourcePath, "error", err)
		return
	}
	if summary.Changed {
		s.logger.Info("mcpsync: mirror updated",
			"added", summary.Added,
			"updated", summary.Updated,
			"removed", summary.Removed,
			"servers", summary.ServerCount,
		)
	}
}

// SyncOnce reads the source file and applies it to the mirror. Safe to call
// concurrently with the ticker loop (the settings-tab refresh endpoint uses
// this); calls are serialized.
func (s *Syncer) SyncOnce(ctx context.Context) (Summary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	servers, err := ReadSourceFile(s.cfg.SourcePath)
	if err != nil {
		// Keep the mirror and its hash untouched — the last good snapshot
		// remains the merge source; only the error is surfaced.
		if stateErr := s.queries.RecordMcpSyncStateError(ctx, err.Error()); stateErr != nil {
			s.logger.Warn("mcpsync: record state error failed", "error", stateErr)
		}
		return Summary{}, err
	}

	hash := CanonicalHash(servers)

	state, err := s.queries.GetMcpSyncState(ctx)
	if err == nil && state.LastSourceHash == hash && state.LastError == "" {
		// Nothing changed since the last applied snapshot. A previous pass
		// with last_error set deliberately falls through to a full re-apply
		// so the mirror heals without waiting for the next source edit.
		if touchErr := s.queries.TouchMcpSyncState(ctx); touchErr != nil {
			s.logger.Warn("mcpsync: touch state failed", "error", touchErr)
		}
		return Summary{ServerCount: len(servers)}, nil
	}
	// GetMcpSyncState erroring on a seeded singleton table means a DB hiccup —
	// fall through to the apply (idempotent) rather than skipping the pass.

	summary, err := s.apply(ctx, servers, hash)
	if err != nil {
		return Summary{}, err
	}
	summary.Changed = true
	summary.ServerCount = len(servers)
	return summary, nil
}

func (s *Syncer) apply(ctx context.Context, servers map[string]json.RawMessage, hash string) (Summary, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("mcpsync: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.queries.WithTx(tx)

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	var summary Summary
	for _, name := range names {
		row, err := q.UpsertMcpSyncServer(ctx, db.UpsertMcpSyncServerParams{
			Name:       name,
			Definition: servers[name],
			SourceHash: hash,
		})
		if err != nil {
			return Summary{}, fmt.Errorf("mcpsync: upsert server %q: %w", name, err)
		}
		if row.Inserted {
			summary.Added++
		} else {
			summary.Updated++
		}
	}

	removed, err := q.MarkMcpSyncServersRemoved(ctx, names)
	if err != nil {
		return Summary{}, fmt.Errorf("mcpsync: mark removed: %w", err)
	}
	summary.Removed = int(removed)

	if err := q.UpsertMcpSyncState(ctx, db.UpsertMcpSyncStateParams{
		SourceHash: hash,
		LastError:  "",
	}); err != nil {
		return Summary{}, fmt.Errorf("mcpsync: upsert state: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Summary{}, fmt.Errorf("mcpsync: commit: %w", err)
	}
	return summary, nil
}

// ReadSourceFile reads and parses the Claude Code config file, returning its
// mcpServers map. A missing file is an error (reported via state.last_error),
// not an empty snapshot — Claude Code not being installed yet must not wipe
// the mirror.
func ReadSourceFile(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("mcpsync: source file not found: %s", path)
		}
		return nil, fmt.Errorf("mcpsync: read source file: %w", err)
	}
	return ExtractMcpServers(data)
}

// ExtractMcpServers pulls the mcpServers map out of a Claude Code config
// document. Validation is strict: every name must be non-empty and every
// definition must be a JSON object. A malformed entry fails the whole pass
// (mirror untouched, error recorded) rather than silently half-applying a
// snapshot the user can't see.
func ExtractMcpServers(data []byte) (map[string]json.RawMessage, error) {
	var doc struct {
		McpServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("mcpsync: parse source json: %w", err)
	}
	if doc.McpServers == nil {
		// Valid config with no mcpServers key = zero servers. Legitimate
		// state (Claude Code installed, nothing configured) — unlike a
		// missing FILE, this marks everything removed.
		return map[string]json.RawMessage{}, nil
	}
	for name, raw := range doc.McpServers {
		if name == "" {
			return nil, errors.New("mcpsync: source has an empty mcp server name")
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
			return nil, fmt.Errorf("mcpsync: mcp server %q is not a JSON object", name)
		}
	}
	return doc.McpServers, nil
}

// CanonicalHash hashes the mcpServers subtree with nested keys sorted and
// whitespace normalized, so hash equality means semantic equality regardless
// of how Claude Code happened to serialize the file this startup.
func CanonicalHash(servers map[string]json.RawMessage) string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	h := sha256.New()
	for _, name := range names {
		nameJSON, _ := json.Marshal(name)
		h.Write(nameJSON)
		h.Write([]byte(":"))
		h.Write(canonicalJSON(servers[name]))
		h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalJSON re-encodes raw with sorted object keys and no insignificant
// whitespace. Numbers keep their source literals via json.Number.
func canonicalJSON(raw json.RawMessage) []byte {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// ExtractMcpServers already validated every definition as a JSON
		// object, so this is unreachable in practice; fall back to the raw
		// bytes rather than erroring the hash.
		return raw
	}
	return mustMarshalSorted(v)
}
