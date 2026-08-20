// Package pluginruntime is a minimal stub added in 0.5.47 to
// unblock the MUL-6310 cherry-pick. The fork's task.go (MUL-6310
// port) imports CompiledEntry and ParseEntries from this package,
// but upstream's pkg/pluginruntime does not exist on main — these
// symbols are referenced in upstream's task.go but never defined
// anywhere in the upstream tree.
//
// This stub provides the minimum surface for the tasks fork to
// compile. Full plugin runtime semantics (manifest parsing,
// contribution ordering, etc.) are not ported — fork's plugin
// infrastructure remains CLAUDE.md SKIP-DEAD-CASE (MUL-6350 plugin
// rebuild series).
//
// When pkg/pluginruntime is later defined upstream (or fork
// diverges further), update this stub to match the real contract
// or remove it if the dependency is dropped.
package pluginruntime

// CompiledEntry is a placeholder type for the upstream plugin
// runtime manifest entry. The real type lives in a future
// plugin-runtime extraction that has not been ported.
type CompiledEntry struct {
	// Fields will be filled in when the upstream pkg/pluginruntime
	// is defined. For now, the struct exists only so that task.go
	// can declare fields of this type.
}

// ParseEntries parses a serialized contribution list into compiled
// entries. Stub implementation: returns an empty slice for any
// input. The upstream real implementation has not been ported.
func ParseEntries(_ []string) ([]CompiledEntry, error) {
	return nil, nil
}
