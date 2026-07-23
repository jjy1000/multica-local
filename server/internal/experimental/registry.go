package experimental

// ExperimentRegistry (0.3.19 P2) — single source of truth for the
// runtime wiring of every experiment flag.
//
// Before P2, the four parallel registries (installableSources map in
// experimental_resources.go, the fixed {claudeScience, pythia} struct
// in experimental_proxy.go, the per-flag IPC setups in apps/desktop,
// and the hard-coded if-else chains in LabsTab / app-sidebar) each
// carried a slice of the same knowledge. Adding a fifth flag meant
// editing all four. The registry collapses the read-side registry
// into a single object the boot phase constructs once from Catalog +
// Manifest data.
//
// Scope (P2 only):
//
//   - installableSources: replaced by Registry.IsInstallable() /
//     Registry.InstallHandler().
//   - experimental_proxy.go loopback map: replaced by Registry.
//     SetLoopbackURL / GetLoopbackURL / ProxyRoutes(). The mount
//     site in cmd/server/router.go stays the same; it just iterates
//     the registry instead of hard-coding two routes.
//   - MountExperimentalProxies: now iterates registry.ProxyRoutes()
//     so adding a subprocess manifest auto-registers the proxy.
//
// Out of scope (deferred to later PRs):
//
//   - Desktop IPC consolidation: PR 4 (P2.4) handles the renderer
//     side; the registry exposes the metadata it needs but does not
//     itself spin up Electron IPC.
//   - Sidebar / LabsTab: PR 3.
//   - Skill loader boot-time scan: PR 5.

import (
	"strings"
	"sync"
)

// InstallHandler is the per-source install function the dispatcher
// invokes when POST /api/experimental-resources/{key}/install fires.
// Concrete handlers live in install_claude_science.go and
// install_mythos.go and are bound at boot via RegisterInstallHandler.
//
// The contract: handler receives the caller's user id (may be empty
// for unauthenticated fixture callers), the caller's currently active
// workspace id (the workspace the lab resources are written into —
// labs no longer create a dedicated reserved workspace), and the
// Handler receiver. On success it returns nil; on a soft error
// (manifest missing, dependency not staged) it returns
// ErrManifestUnavailable; on any other error the dispatcher maps it
// to 500.
type InstallHandler func(userID, workspaceID string) error

// UnregisterHandler is invoked when the source is being rolled back.
// The handler can choose to do nothing (current Claude Science path)
// or actively clean domain rows (e.g. removing the lab workspace).
// Default is no-op — the lock table's hide=true is the rollback.
type UnregisterHandler func() error

// ProxyRoute describes one same-origin reverse-proxy route the
// registry wants mounted. ProxyPrefix is the public path on the
// Multica origin (e.g. "/experimental/pythia"); LoopbackService is
// the key the desktop main process POSTs to /__experimental/upstream
// to register the loopback URL.
type ProxyRoute struct {
	Prefix          string
	LoopbackService string
	FlagKey         string
}

// Registry is the runtime wiring table for every catalog flag. The
// boot phase (cmd/server/main.go after the catalog is loaded) calls
// NewRegistry; the resulting value is then passed to the install
// dispatcher, the proxy mount, and (in P3) the sidebar.
//
// Concurrent use: Registry is read-mostly after boot. InstallHandler
// and UnregisterHandler may be added during boot only — runtime
// mutation is not supported and would race. The mutex guards the
// loopback URL map (SetLoopbackURL / GetLoopbackURL) and the flags
// map, which gains user-plugin entries at runtime via
// MergeUserPlugins / RemoveUserPlugin (0.3.60). All flags readers
// (Flags / Flag / ProxyRoutes / SidebarEntries) take the read lock.
type Registry struct {
	mu sync.RWMutex

	// catalog snapshot — used for capability / installability lookups.
	flags map[string]Flag

	// loopback URLs keyed by service. Populated by SetLoopbackURL
	// (called from the upstream register handler) and read by the
	// proxy handlers. The map replaces the fixed {claudeScience,
	// pythia} struct in experimental_proxy.go.
	loopback map[string]string

	// install handlers keyed by flag key. Populated by
	// RegisterInstallHandler at boot. The install dispatcher
	// (experimental_resources.go PR 2 wire) consults this map to
	// dispatch by flag.
	install map[string]InstallHandler

	// rollback handlers keyed by flag key. Same lifecycle as install.
	rollback map[string]UnregisterHandler
}

// NewRegistry builds a registry from the current Catalog. Unknown
// flags are not iterated — Catalog is the source of truth. Handlers
// are bound separately via RegisterInstallHandler / RegisterUnregisterHandler.
func NewRegistry() *Registry {
	r := &Registry{
		flags:    make(map[string]Flag, len(Catalog)),
		loopback: make(map[string]string),
		install:  make(map[string]InstallHandler),
		rollback: make(map[string]UnregisterHandler),
	}
	for _, f := range Catalog {
		r.flags[f.Key] = f
	}
	return r
}

// Flags returns the catalog snapshot the registry was built from.
// Returned slice is a defensive copy; callers may iterate without
// locking.
func (r *Registry) Flags() []Flag {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Flag, 0, len(r.flags))
	for _, f := range r.flags {
		out = append(out, f)
	}
	return out
}

// Flag returns the catalog entry for key, or false when unknown.
// Equivalent to IsKnownKey but returns the full Flag for callers
// that need the manifest fields.
func (r *Registry) Flag(key string) (Flag, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.flags[key]
	return f, ok
}

// MergeUserPlugins adds user-created plugin flags to the registry's
// flag map. Called at boot after RegisterUserPlugins populates the
// catalog's dynamic layer. Proxy routes and sidebar entries for user
// plugins are derived from their Flag fields the same way built-in
// flags are.
func (r *Registry) MergeUserPlugins(flags []Flag) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range flags {
		r.flags[f.Key] = f
	}
}

// RemoveUserPlugin removes a user plugin flag from the registry.
func (r *Registry) RemoveUserPlugin(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.flags, key)
}

// IsInstallable reports whether key has an install handler bound.
// A flag can be in the catalog without being installable — the
// PR 3 install endpoint needs to answer 404 for non-installable
// sources so a renderer typo doesn't bubble up as "no resources".
func (r *Registry) IsInstallable(key string) bool {
	_, ok := r.install[key]
	return ok
}

// RegisterInstallHandler binds an install handler to flagKey. Must
// be called during boot; runtime registration is not supported.
func (r *Registry) RegisterInstallHandler(flagKey string, h InstallHandler) {
	if h == nil {
		return
	}
	r.install[flagKey] = h
}

// RunInstall dispatches the install for flagKey to the bound
// handler. The returned error is the handler's verbatim — the HTTP
// layer maps it to the appropriate status. Returns false when no
// handler is bound so the caller can 404.
func (r *Registry) RunInstall(flagKey, userID, workspaceID string) error {
	h, ok := r.install[flagKey]
	if !ok {
		return ErrNoInstallHandler
	}
	return h(userID, workspaceID)
}

// RegisterUnregisterHandler binds a rollback handler. The default
// (no binding) is "no-op" — the lock table's Hide() is the rollback.
func (r *Registry) RegisterUnregisterHandler(flagKey string, h UnregisterHandler) {
	if h == nil {
		return
	}
	r.rollback[flagKey] = h
}

// RunRollback dispatches the rollback for flagKey. Returns nil when
// no handler is bound — that is the common case; rollback is a
// visibility toggle, not a deletion.
func (r *Registry) RunRollback(flagKey string) error {
	h, ok := r.rollback[flagKey]
	if !ok {
		return nil
	}
	return h()
}

// SetLoopbackURL records a service's loopback URL. Called by the
// /__experimental/upstream POST handler. Safe for concurrent use.
func (r *Registry) SetLoopbackURL(service, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if url == "" {
		delete(r.loopback, service)
		return
	}
	r.loopback[service] = url
}

// LoopbackURL returns the registered loopback URL for service, or
// "" when the manager is not up. Safe for concurrent use.
func (r *Registry) LoopbackURL(service string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.loopback[service]
}

// ProxyRoutes returns one ProxyRoute per catalog entry whose
// RuntimeKind == "subprocess" and whose ProxyPrefix / LoopbackService
// are non-empty. The proxy mount site iterates this slice to register
// routes — adding a new subprocess flag requires only the manifest
// and catalog edit, not a router change.
func (r *Registry) ProxyRoutes() []ProxyRoute {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ProxyRoute, 0)
	for _, f := range r.flags {
		if f.RuntimeKind != "subprocess" {
			continue
		}
		if f.ProxyPrefix == "" || f.LoopbackService == "" {
			continue
		}
		out = append(out, ProxyRoute{
			Prefix:          f.ProxyPrefix,
			LoopbackService: f.LoopbackService,
			FlagKey:         f.Key,
		})
	}
	return out
}

// ErrNoInstallHandler is returned by RunInstall when no handler is
// bound for the requested key. The HTTP layer maps it to 404.
var ErrNoInstallHandler = &registryError{msg: "no install handler registered for flag"}

// registryError is a typed error so the HTTP layer can use
// errors.Is without coupling to a sentinel string.
type registryError struct{ msg string }

func (e *registryError) Error() string { return e.msg }

// NormalizeProxyPrefix enforces "/experimental/..." shape on the
// proxy prefix. The catalog schema (PR 1) already validates this
// in the test, but the helper exists so the proxy mount site can
// defend against a future contributor who hand-edits a manifest.
func NormalizeProxyPrefix(p string) string {
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

// SidebarEntry is one row of a flag's entry_points.sidebar manifest.
// Mirrors the { key, label_key, route } shape in
// apps/desktop/resources/experiments/*/manifest.json. Carried on the
// GET /api/experimental-flags response so the renderer's nav hook
// can render the Experimental sidebar group from the catalog payload
// instead of a hard-coded STATIC_NAV list.
type SidebarEntry struct {
	Key      string `json:"key"`
	FlagKey  string `json:"flag_key,omitempty"`
	LabelKey string `json:"label_key"`
	Route    string `json:"route"`
}

// SidebarEntries returns the sidebar rows for flagKey, or nil when
// the flag is unknown. The list is sourced from the manifest's
// entry_points.sidebar[*]; flags without a manifest (e.g.
// chat_pin_ui) return nil. Loading is lazy and idempotent — the
// first call parses the manifest, subsequent calls reuse the
// in-memory snapshot. A parse failure returns nil so the wire
// response degrades gracefully.
//
// Holds the write lock for the whole call because it may mutate
// r.flags to cache the loaded Sidebar rows; this must not race with
// MergeUserPlugins / RemoveUserPlugin. LoadManifest reads a small
// local file, so the critical section is short.
func (r *Registry) SidebarEntries(flagKey string) []SidebarEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	f, ok := r.flags[flagKey]
	if !ok || f.Sidebar != nil || f.ManifestPath == "" {
		// unknown key, already loaded, or no manifest → return what we have.
		if !ok {
			return nil
		}
		out := make([]SidebarEntry, 0, len(f.Sidebar))
		for _, e := range f.Sidebar {
			out = append(out, SidebarEntry{
				Key: e.Key, FlagKey: flagKey,
				LabelKey: e.LabelKey, Route: e.Route,
			})
		}
		return out
	}

	manifest, err := LoadManifest(flagKey)
	if err != nil {
		return nil
	}
	sp, _ := manifest.Raw["spec"].(map[string]any)
	ep, _ := sp["entry_points"].(map[string]any)
	rows, _ := ep["sidebar"].([]any)

	loaded := make([]SidebarRow, 0, len(rows))
	out := make([]SidebarEntry, 0, len(rows))
	for _, row := range rows {
		m, _ := row.(map[string]any)
		key, _ := m["key"].(string)
		labelKey, _ := m["label_key"].(string)
		route, _ := m["route"].(string)
		if key == "" || route == "" {
			continue
		}
		if labelKey == "" {
			labelKey = "experimental_" + flagKey
		}
		loaded = append(loaded, SidebarRow{Key: key, LabelKey: labelKey, Route: route})
		out = append(out, SidebarEntry{Key: key, FlagKey: flagKey, LabelKey: labelKey, Route: route})
	}
	f.Sidebar = loaded
	r.flags[flagKey] = f
	return out
}
