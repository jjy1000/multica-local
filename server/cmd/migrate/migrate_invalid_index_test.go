package main

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// MUL-6288 — every concurrent index build has retry cleanup.
//
// Port of upstream #7073 (commit 1e1321a9c). CREATE INDEX CONCURRENTLY
// leaves an INVALID index behind when the build is interrupted (a crash,
// a statement_timeout, a cancelled connection). A retry is then silently
// poisoned: `CREATE INDEX ... IF NOT EXISTS` treats the leftover relation
// as success and records the migration as applied while the index stays
// unusable, and a bare `CREATE` stays wedged on "already exists" forever.
// main.go's concurrentIndexCleanups registry + generated pre-migration
// hook drop the INVALID leftover before the retry rebuilds.
//
// The invariant tests below are pure file scanning (no Postgres): every
// up migration that builds an index concurrently MUST be registered, and
// every registered entry MUST build an index and have a hook. The repair
// test is live-Postgres: it interrupts a real concurrent build, proves
// the hookless retry is a silent no-op, then proves the hook recovers.

var concurrentIndexNamePattern = regexp.MustCompile(
	`(?i)CREATE\s+(?:UNIQUE\s+)?INDEX\s+CONCURRENTLY\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_]+)`)

// stripSQLLineComments drops `--` comment lines so prose that mentions SQL is
// not mistaken for SQL.
func stripSQLLineComments(body []byte) []byte {
	var kept [][]byte
	for _, line := range bytes.Split(body, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("--")) {
			continue
		}
		kept = append(kept, line)
	}
	return bytes.Join(kept, []byte("\n"))
}

// TestConcurrentIndexCleanupsMatchTheirMigrations guards the mapping that wires
// invalid-index cleanup hooks to migrations. A hook that names an index no
// migration creates is a silent no-op: the retry then treats the INVALID
// leftover as success and the index stays unusable. Nothing at runtime would
// report that, so the names are checked against the migration files here.
func TestConcurrentIndexCleanupsMatchTheirMigrations(t *testing.T) {
	assertConcurrentIndexCleanupsMatchTheirMigrations(
		t,
		concurrentIndexCleanups,
		preMigrationHooks,
		"up",
	)
}

// TestEveryConcurrentUpBuildHasCleanup is the MUL-6288 invariant: every up
// migration that builds an index concurrently must be registered, so an
// unregistered build fails here rather than silently poisoning a real retry
// in production. Registration used to be opt-in — concurrent index builds
// shipped without a hook and nothing failed until an interrupted migration
// turned into either a permanently INVALID index recorded as success
// (`IF NOT EXISTS`) or a wedged migrator (bare `CREATE`).
func TestEveryConcurrentUpBuildHasCleanup(t *testing.T) {
	assertEveryConcurrentBuildHasCleanup(t, "up", concurrentIndexCleanups)
}

func assertEveryConcurrentBuildHasCleanup(t *testing.T, direction string, cleanups map[string]string) {
	t.Helper()
	suffix := "." + direction + ".sql"
	paths, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*"+suffix))
	if err != nil {
		t.Fatalf("glob %s migrations: %v", direction, err)
	}
	if len(paths) == 0 {
		t.Fatalf("no %s migrations found", direction)
	}

	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", path, err)
			continue
		}
		matches := concurrentIndexNamePattern.FindAllSubmatch(stripSQLLineComments(body), -1)
		if len(matches) == 0 {
			continue
		}
		version := strings.TrimSuffix(filepath.Base(path), suffix)
		if len(matches) != 1 {
			t.Errorf("%s: has %d concurrent index builds; cleanup registration supports exactly one", version, len(matches))
			continue
		}
		indexName := string(matches[0][1])
		registered, ok := cleanups[version]
		if !ok {
			t.Errorf("%s: builds %q concurrently on %s but has no %s cleanup", version, indexName, direction, direction)
			continue
		}
		if registered != indexName {
			t.Errorf("%s: %s cleanup registers %q, migration builds %q", version, direction, registered, indexName)
		}
	}
}

func assertConcurrentIndexCleanupsMatchTheirMigrations(
	t *testing.T,
	cleanups map[string]string,
	hooks map[string]preMigrationHook,
	direction string,
) {
	t.Helper()
	for version, indexName := range cleanups {
		path := filepath.Join("..", "..", "migrations", version+"."+direction+".sql")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read migration: %v", version, err)
			continue
		}
		// The comment headers on these migrations mention CREATE INDEX
		// CONCURRENTLY in prose, so match statements only.
		match := concurrentIndexNamePattern.FindSubmatch(stripSQLLineComments(body))
		if match == nil {
			t.Errorf("%s: has a cleanup hook but builds no index concurrently", version)
			continue
		}
		if got := string(match[1]); got != indexName {
			t.Errorf("%s: hook cleans %q but the migration builds %q", version, indexName, got)
		}
		if hooks[version] == nil {
			t.Errorf("%s: no pre-migration hook registered", version)
		}
	}
}

// TestRunMigrationsRepairsInvalidRuntimeIDIndex is the live-Postgres
// counterpart of the invariant tests, run against migration 248's real SQL
// shape (`CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_agent_runtime_id ON
// agent (runtime_id)`) and its registered hook.
//
// 248 is the representative case: an interrupted build leaves an INVALID
// index that the retry would otherwise skip past, recording the migration
// as applied while the runtime GC's agent.runtime_id lookup stays on a full
// table scan.
func TestRunMigrationsRepairsInvalidRuntimeIDIndex(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	suffix := fmt.Sprintf("%d_%d", time.Now().UnixNano(), rand.Uint32())
	schema := "migrate_invalid_index_" + suffix
	schemaIdent := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+schemaIdent); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+schemaIdent+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})

	const indexName = "idx_agent_runtime_id"
	const version = "248_agent_runtime_id_index"
	tableName := pgx.Identifier{schema, "agent"}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE TABLE "+tableName+` (
		id BIGSERIAL PRIMARY KEY,
		runtime_id UUID
	)`); err != nil {
		t.Fatalf("create agent table: %v", err)
	}

	qualifiedIndex := pgx.Identifier{schema, indexName}.Sanitize()
	createIndex := "CREATE INDEX CONCURRENTLY IF NOT EXISTS " + pgx.Identifier{indexName}.Sanitize() +
		" ON " + tableName + " (runtime_id)"

	// Interrupt the build the way a real one gets interrupted: an open
	// transaction that has written to the table owns an xid the concurrent
	// build must wait for, and statement_timeout cancels the wait.
	blocker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire blocker conn: %v", err)
	}
	blockerTx, err := blocker.Begin(ctx)
	if err != nil {
		blocker.Release()
		t.Fatalf("begin blocker tx: %v", err)
	}
	if _, err := blockerTx.Exec(ctx, "INSERT INTO "+tableName+" (runtime_id) VALUES (gen_random_uuid())"); err != nil {
		blocker.Release()
		t.Fatalf("blocker insert: %v", err)
	}

	builder, err := pool.Acquire(ctx)
	if err != nil {
		blocker.Release()
		t.Fatalf("acquire builder conn: %v", err)
	}
	if _, err := builder.Exec(ctx, "SET statement_timeout = '2s'"); err != nil {
		builder.Release()
		blocker.Release()
		t.Fatalf("set statement_timeout: %v", err)
	}
	_, buildErr := builder.Exec(ctx, createIndex)
	// pgxpool does not reset session state, so clear the fuse before the
	// connection goes back to the pool.
	if _, err := builder.Exec(ctx, "SET statement_timeout = DEFAULT"); err != nil {
		t.Logf("reset statement_timeout: %v", err)
	}
	builder.Release()
	if buildErr == nil {
		blocker.Release()
		t.Fatal("interrupted build unexpectedly succeeded")
	}
	_ = blockerTx.Rollback(ctx)
	blocker.Release()

	assertIndexValidity(t, pool, schema, indexName, false)

	migrationPath := filepath.Join(t.TempDir(), version+".up.sql")
	if err := os.WriteFile(migrationPath, []byte(createIndex+";\n"), 0o600); err != nil {
		t.Fatalf("write retry migration: %v", err)
	}
	opts := runOptions{
		Direction:             "up",
		Files:                 []string{migrationPath},
		SchemaMigrationsTable: schema + ".schema_migrations",
		AdvisoryLockKey:       int64(rand.Uint64()&0x7fffffffffffffff) | 1,
	}

	// Without the hook the retry is a silent no-op: IF NOT EXISTS sees the
	// invalid relation, reports success, and the migration is recorded.
	if err := runMigrations(ctx, pool, opts); err != nil {
		t.Fatalf("retry without hook: %v", err)
	}
	assertIndexValidity(t, pool, schema, indexName, false)

	// With the production hook — schema-qualified so it resolves inside the
	// test schema — the leftover is dropped and the index is rebuilt.
	if preMigrationHooks[version] == nil {
		t.Fatalf("production hook is not registered for %s", version)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM "+pgx.Identifier{schema, "schema_migrations"}.Sanitize()+" WHERE version = $1", version); err != nil {
		t.Fatalf("reset recorded version: %v", err)
	}
	opts.Hooks = map[string]preMigrationHook{
		version: cleanupInvalidConcurrentIndexHook(qualifiedIndex),
	}
	if err := runMigrations(ctx, pool, opts); err != nil {
		t.Fatalf("retry migration with invalid-index cleanup: %v", err)
	}
	assertIndexValidity(t, pool, schema, indexName, true)
}

func assertIndexValidity(t *testing.T, pool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, schema, index string, want bool) {
	t.Helper()
	var valid bool
	if err := pool.QueryRow(context.Background(), `
		SELECT i.indisvalid
		FROM pg_index i
		JOIN pg_class c ON c.oid = i.indexrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = $2
	`, schema, index).Scan(&valid); err != nil {
		t.Fatalf("read validity for %s.%s: %v", schema, index, err)
	}
	if valid != want {
		t.Fatalf("index %s.%s validity = %v, want %v", schema, index, valid, want)
	}
}
