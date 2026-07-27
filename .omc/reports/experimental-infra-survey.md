---
name: experimental-infra-survey
created: 2026-07-22T12:27:24Z
updated: 2026-07-22T12:27:24Z
---

# Backend Experimental Infrastructure Survey

Source of truth for the Labs platform server side. Package `experimental` = `server/internal/experimental/` (4226 lines total across ~20 files). Router wiring in `server/cmd/server/router.go`; proxy + guards in `server/internal/handler/`.

## 1. `catalog.go` — static Flag catalog

The developer-only constant list. Users cannot create flags.

```go
type LocalizedString struct { En string `json:"en"`; Zh string `json:"zh"` }

type SidebarRow struct {
    Key      string `json:"key"`
    LabelKey string `json:"label_key"`
    Route    string `json:"route"`
}

type Flag struct {
    Key             string          `json:"key"`
    DefaultVal      bool            `json:"default_enabled"`
    Title           LocalizedString `json:"title"`
    Description     LocalizedString `json:"description"`
    ManifestPath    string          `json:"manifest_path,omitempty"`
    RuntimeKind     string          `json:"runtime_kind,omitempty"`      // none|inline|subprocess|headless
    ProxyPrefix     string          `json:"proxy_prefix,omitempty"`
    LoopbackService string          `json:"loopback_service,omitempty"`
    HideFromIssueLabPicker          bool `json:"hide_from_issue_lab_picker,omitempty"`
    HidesDeliverableInIssueTimeline bool `json:"hides_deliverable_in_issue_timeline,omitempty"`
    Sidebar []SidebarRow `json:"-"`   // loaded from manifest at boot, not serialized from catalog
}

var Catalog = []Flag{ ... }   // package-level slice literal
```

8 flags registered: `chat_pin_ui, claude_science_lab, pythia_oracle, mythos_swarm, llm_wiki_bridge, code_canvas, agent_self_optimization, agent_creation_studio`. (`constitution_agent` retired 0.3.57.)

Helper funcs (all range over the global `Catalog` slice):
```go
func IsKnownKey(key string) bool
func AllFlagKeys() []string
func DefaultFor(key string) bool   // chokepoint: returns false if IsBroken(key) [blacklist], else f.DefaultVal, else false
```

`DefaultFor` body:
```go
func DefaultFor(key string) bool {
    if _, broken := IsBroken(key); broken { return false }  // on-disk safety blacklist
    for _, f := range Catalog { if f.Key == key { return f.DefaultVal } }
    return false
}
```

## 2. `registry.go` — singleton runtime registry

Built once from `Catalog` at boot. Collapses 4 legacy parallel registries into one read-side object.

```go
type InstallHandler   func(userID, workspaceID string) error
type UnregisterHandler func() error

type ProxyRoute struct { Prefix, LoopbackService, FlagKey string }

type SidebarEntry struct {
    Key      string `json:"key"`
    FlagKey  string `json:"flag_key,omitempty"`
    LabelKey string `json:"label_key"`
    Route    string `json:"route"`
}

type Registry struct {
    mu       sync.RWMutex
    flags    map[string]Flag              // catalog snapshot
    loopback map[string]string            // service -> loopback URL
    install  map[string]InstallHandler    // flagKey -> handler
    rollback map[string]UnregisterHandler
}
```

Methods:
```go
func NewRegistry() *Registry                       // copies Catalog into flags map
func (r *Registry) Flags() []Flag
func (r *Registry) Flag(key string) (Flag, bool)
func (r *Registry) IsInstallable(key string) bool
func (r *Registry) RegisterInstallHandler(flagKey string, h InstallHandler)
func (r *Registry) RunInstall(flagKey, userID, workspaceID string) error  // ErrNoInstallHandler if absent
func (r *Registry) RegisterUnregisterHandler(flagKey string, h UnregisterHandler)
func (r *Registry) RunRollback(flagKey string) error
func (r *Registry) SetLoopbackURL(service, url string)
func (r *Registry) LoopbackURL(service string) string
func (r *Registry) ProxyRoutes() []ProxyRoute       // filters RuntimeKind=="subprocess" && prefix/service non-empty
func (r *Registry) SidebarEntries(flagKey string) []SidebarEntry  // lazy-loads manifest entry_points.sidebar, caches into Flag.Sidebar
func NormalizeProxyPrefix(p string) string
```

Constructed at `handler.go:278` — `ExperimentRegistry: experimental.NewRegistry()` inside `handler.New(...)`. Stored as `Handler.ExperimentRegistry *experimental.Registry` (`handler.go:132`). **No other `NewRegistry()` call exists** — single instance.

## 3. `visibility.go` — resource hiding gate

Drives which agents/autopilots/skills/squads a flag hides when off. Backed by the `experimental_resource_visibility` DB table.

```go
type HideableResource string
const (
    HideAgent     HideableResource = "agent"
    HideAutopilot HideableResource = "autopilot"
    HideSkill     HideableResource = "skill"
    HideSquad     HideableResource = "squad"   // 0.3.31
)

func IsKnownHideableResource(r HideableResource) bool
func AgentSelfOptimizationAgentID() uuid.UUID          // hardcoded known IDs (self-opt lab)
func AgentSelfOptimizationAutopilotIDs() []uuid.UUID
func AgentSelfOptimizationSkillID() uuid.UUID
func HiddenResourceIDsByFlag(ctx, q *db.Queries, flagKey string, r HideableResource) ([]uuid.UUID, error)  // q.ListHiddenResourceIDs
func UUIDsToPgtype(ids []uuid.UUID) []pgtype.UUID
```

Consumer (in `handler/`, NOT experimental/): `labs_visibility_filter.go`
```go
func filterLabsHiddenByDefault[T any](
    ctx context.Context, q *db.Queries, items []T, flagKey string,
    kind experimental.HideableResource, extractID func(T) pgtype.UUID, logOnErr string,
) []T
// short-circuits to `items` unchanged if DefaultFor(flagKey)==true or !IsKnownKey(flagKey)
func labManagedSet(ctx, q *db.Queries, kind experimental.HideableResource) map[[16]byte]struct{}  // 0.3.56 lab_managed DTO marker
```
List handlers (`agent.go::ListAgents`, `autopilot.go`, `skill.go`, `squad.go::ListSquads`) call `filterLabsHiddenByDefault`; `ListAgents`/`ListSquads` also stamp `lab_managed` from `labManagedSet`.

## 4. `lock.go` — resource locking / lifecycle markers

Claims resources into `experimental_resource_lock` keyed by a `Source`; a per-flag lifecycle marker UUID.

```go
type LockQuerier interface { ... }   // subset of db.Queries used here
type Source string
type ResourceType string

type ErrLocked struct { Source Source; Type ResourceType; ResID pgtype.UUID }
type LockCounts struct { Type ResourceType; Total, Visible int }

// Source constants (must stay in sync with the SQL CHECK on experimental_resource_lock.experimental_source)
const (
    SourceClaudeScience           Source = "claude_science"      // deprecated 0.3.22
    SourceClaudeScienceLab        Source = "claude_science_lab"
    SourceMythosSwarm             Source = "mythos_swarm"
    SourceAgentSelfOptimization   Source = "agent_self_optimization"
    SourcePythiaOracle            Source = "pythia_oracle"
    SourceCodeCanvas              Source = "code_canvas"
)
const (
    LockWorkspace ResourceType = "workspace"; LockSkill = "skill"; LockAgent = "agent"
    LockSquad = "squad"; LockMember = "member"; LockMCPServer = "mcp_server"
)
const MarkerIDNamespace byte = 0xEC

func (s Source) Valid() bool
func LifecycleMarker(flagKey string) pgtype.UUID   // SHA-256("multica-labs-marker:"+flagKey)[:16], out[0] ^= 0xEC
func Claim(ctx, q LockQuerier, src Source, rt ResourceType, id pgtype.UUID) error
func Hide(ctx, q, src Source) (int, error)
func Restore(ctx, q, src Source) (int, error)
func RestoreOne(ctx, q, src, rt, id) (int, error)
func IsHidden(ctx, q, src, rt, id) (bool, error)
func Lookup(ctx, q, src, rt, id) (db.ExperimentalResourceLock, error)
func CountByType(ctx, q, src Source) ([]LockCounts, error)
```

## 5. `manifest.go` — manifest parsing (260 lines)

Parses `apps/desktop/resources/experiments/<flagKey>/manifest.json` (under `MULTICA_RESOURCES_DIR`, dev fallback `apps/desktop/resources`).

```go
type Manifest struct {
    APIVersion string           `json:"apiVersion"`
    Kind       string           `json:"kind"`
    Metadata   ManifestMetadata `json:"metadata"`
    Spec       map[string]any   `json:"spec"`
    Raw        map[string]any   `json:"-"`
    SourcePath string           `json:"-"`
    mu         sync.Mutex       `json:"-"`
}
type ManifestMetadata struct {
    Name, Flag   string
    Title, Description LocalizedString
    DefaultEnabled bool
}
const ManifestResourceDirEnv = "MULTICA_RESOURCES_DIR"
const DevManifestFallback    = "apps/desktop/resources"

func SetManifestRoot(path string)
func ManifestRoot() string
func LoadManifest(flagKey string) (*Manifest, error)    // guards IsKnownKey, reads+unmarshal+validateManifestSchema
func validateManifestSchema(doc map[string]any, flagKey string) error
func mapToLocalizedString(m map[string]any) LocalizedString
```

Note: `Spec` is an untyped `map[string]any` — the desktop subprocess-manager (`apps/desktop/.../subprocess-manager.ts`) mirrors `spec.runtime` in TS (`ManifestRuntimeSpec`); the Go side does NOT strongly type runtime/surface/capabilities.

## 6. `router.go` — route mounting (`server/cmd/server/router.go`)

- `h := handler.New(...)` at line 175 → this constructs `h.ExperimentRegistry = experimental.NewRegistry()`.
- Line 473: `r.Use(middleware.ExperimentalFlagBurst(...))` — safety burst breaker (reads `X-Experimental-Flag`).
- Line 505: `handler.MountExperimentalProxies(r, h)` — mounts all subprocess proxies.
- Lines 531-563: install handlers bound per-flag (manual, one `RegisterInstallHandler` call each):
  ```go
  h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceClaudeScienceLab), func(userID, ws string) error { return hh.InstallClaudeScience(ctx, experimental.SourceClaudeScienceLab, userID, ws) })
  h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceMythosSwarm), ...)       // InstallMythos
  h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceAgentSelfOptimization), ...) // InstallAgentSelfOptimization
  h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourcePythiaOracle), ...)      // InstallPythia
  h.ExperimentRegistry.RegisterInstallHandler(string(experimental.SourceCodeCanvas), ...)        // InstallCodeCanvas
  ```
- Lines 567-611: Mythos supervise service wiring (`mythossvc.NewService`, `ResumeSupervision`).
- Lines 712: `r.Get("/api/experimental/claude-science/skills", h.ClaudeScienceSkills)` (unconditional, public to signed-in).
- Lines 783-849: **per-request, per-user gated** lab routes via `h.RequireExperimentalFlag(key)` middleware (returns 404 when off):
  - `claude_science_lab` (797) → claude-science-runtime routes + forecast SSE (799)
  - `llm_wiki_bridge` (813)
  - `mythos_swarm` (821) → `/api/experimental/mythos-swarm/run`, `/supervise/{runID}`, `/supervise/{runID}/tick`
  - `pythia_oracle` (836) → per-issue forecast
  - `self-opt` (849) → no guard (own gating)
- Lines 1292-1313 (auth group): flag + resource management API:
  - `/api/experimental-flags` GET `h.ListExperimentalFlags`, PATCH `/{key}` `h.UpdateExperimentalFlag`
  - `/api/experimental-resources` GET `/{key}/status`, POST `/{key}/install`, POST `/{key}/rollback`, POST `/install-all`

Per-user resolution: `experimentalFlagEnabled(ctx, q, userID, key)` (`experimental_guard.go`) checks `experimental_pref` override then falls back to `DefaultFor`.

## 7. `experimental_proxy.go` — proxy mounting (`handler/`)

Functions:
```go
func SetExperimentalLoopbackURL(service, u string)              // legacy thin wrapper -> registry.SetLoopbackURL
func AttachExperimentalRegistry(reg *experimental.Registry)     // binds package-level loopback store
func (h *Handler) experimentalClaudeScienceProxy(w, r)          // legacy hardcoded
func (h *Handler) experimentalPythiaProxy(w, r)                 // legacy hardcoded
func reverseProxyTo(w, r, upstream, prefix string)
func MountExperimentalProxies(r chi.Router, h *Handler)
func mountExperimentalProxy(exp chi.Router, h *Handler, p experimental.ProxyRoute)
func lookupLoopbackURL(h *Handler, service string) (string, bool)
func injectExperimentalFlagHeader(reg *experimental.Registry) func(http.Handler) http.Handler
func (h *Handler) upstreamRegister(w, r)                         // POST /__experimental/upstream (desktop managers report readiness)
func (h *Handler) upstreamUnregister(w, r)                       // DELETE /__experimental/upstream/{service}
func isLoopbackUpstreamURL(raw string) bool
func isAllowedUpstreamService(h *Handler, service string) bool
```

`MountExperimentalProxies`: nil-handler guard → child mux group → `injectExperimentalFlagHeader` middleware → iterates `h.ExperimentRegistry.ProxyRoutes()` calling `mountExperimentalProxy` for each subprocess flag (auto-mount). Legacy fallback registers the 2 hardcoded claude-science/pythia routes only when the registry is nil. Then registers the internal `/__experimental/upstream` register/unregister endpoints.

`injectExperimentalFlagHeader`: snapshots `ProxyRoutes()` at mount time into `prefixByService map[string]string`, sets `X-Experimental-Flag` header by path-prefix match → feeds the burst breaker.

## Cross-file wiring summary

```
handler.New() → NewRegistry() [copies Catalog]                (handler.go:278)
router.go    → RegisterInstallHandler per flag                (531-563, MANUAL)
router.go    → MountExperimentalProxies → ProxyRoutes()       (505, AUTO)
router.go    → RequireExperimentalFlag(key) per route group    (783-849, MANUAL per route)
experimental_proxy → injectExperimentalFlagHeader → ProxyRoutes (snapshot at mount)
ListAgents/Squads/Autopilots/Skills → filterLabsHiddenByDefault(flagKey)  (visibility, MANUAL per handler)
install handlers → Claim/LifecycleMarker/Hide/Restore          (lock.go)
manifest.json → LoadManifest → SidebarEntries (lazy)           (sidebar rows)
DefaultFor(key) = blacklist? Catalog.DefaultVal : false        (featureflag priority chain bottom)
```

## What must change for DYNAMIC plugin registration

Static/hardcoded points that block true dynamic registration:

1. **`var Catalog = []Flag{...}` is a compile-time slice literal.** No `Register(Flag)` mutation API; `Registry.NewRegistry` snapshots it once at boot. → Need a `RegisterFlag`/`RegisterPlugin` on Registry + thread-safe insertion, and decouple `DefaultFor`/`IsKnownKey`/`AllFlagKeys` from the package global (make them registry methods or point them at a live table).
2. **Install handlers are bound by 5 manual `RegisterInstallHandler` calls in router.go** keyed to hardcoded `Source*` constants. → A dynamic plugin would need to self-register its install/rollback handler + its `Source` value; the `experimental_resource_lock.experimental_source` SQL CHECK constraint must be widened per source (migration), which is fundamentally static.
3. **Route groups are hand-written per flag** (`RequireExperimentalFlag("claude_science_lab")` blocks at 797/813/821/836). Proxy routes auto-mount from `ProxyRoutes()`, but the per-lab API routes (run/supervise/forecast/skills) are bespoke Go handlers — not manifest-driven.
4. **`LifecycleMarker` / `Source` enum** are closed Go constants mirrored by a DB CHECK — dynamic sources require either dropping the CHECK or an allowlist table.
5. **Visibility** seeds `experimental_resource_visibility` rows from install handlers; the `lab_managed` marker + `filterLabsHiddenByDefault` are already flag-key-parameterized and would survive dynamic flags unchanged, but each new list surface must still opt in by calling the helper.
6. **Manifest `Spec` is `map[string]any`** — loosely typed; enough to drive sidebar (already dynamic via `SidebarEntries`) and desktop subprocess, but server-side capabilities/safety are not parsed into a schema that could drive auto route/handler registration.
7. **`injectExperimentalFlagHeader` + `MountExperimentalProxies` snapshot at boot** — a plugin registered after boot would need a router rebuild or a dynamic dispatch layer.

Already-dynamic (would NOT need changes): proxy auto-mount (`ProxyRoutes`), sidebar rows (`SidebarEntries`/manifest), per-user flag gating (`experimental_pref` + `DefaultFor`), and visibility filtering helpers.
