// Package experimental holds the developer-authored catalog of opt-in
// feature flags surfaced through the Settings → Labs tab.
//
// Hard constraints (user-approved 2026-07-12):
//
//  1. Flag = off (default) must completely bypass the new code path. Old
//     callers must not import any code added solely for a flag.
//  2. Users cannot add/edit/delete flags at runtime. New flags land here
//     as a code change; the labs UI only renders what this Catalog returns.
//  3. Surfacing is restricted to the labs tab — no nav bar, no CLI, no API
//     beyond the two HTTP endpoints in this package's companion handlers.
//  4. These are toggle points, not a plugin system. Each flag toggles a
//     single small surface (UI element, copy variant, behavior switch);
//     larger features belong in their own sub-system, not here.
//
// To add a new flag, append a Flag literal to Catalog and wire its
// toggle point in the relevant view. Do not delete a flag entry without
// first removing every Toggle Point call site that reads it — otherwise
// the flag becomes a dead switch and the Labs UI shows a toggle that
// does nothing, which users will rightly treat as a bug.
package experimental

import "sync"

// LocalizedString is a minimal en+zh bilingual pair used for flag titles
// and descriptions. The HTTP API returns both fields so the client can
// pick by its current locale without a server-side i18n hop. Languages
// other than en/zh are not yet supported; add fields here when needed.
type LocalizedString struct {
	En string `json:"en"`
	Zh string `json:"zh"`
}

// Flag describes one opt-in experimental feature.
//
// Key is the stable identifier stored in experimental_pref.flag_key and
// surfaced through the Labs UI. Changing the Key of a live flag breaks
// user preferences silently — treat it as a breaking change.
//
// DefaultVal is the on/off state used when the user has not yet opened
// the labs tab. Per the user constraint, DefaultVal must be false for
// any new flag that ships new code paths; the lab exists precisely to
// hide incomplete work behind an explicit opt-in.
//
// Title and Description are user-visible. Keep them short — they render
// inside a Settings panel card without truncation budget.
//
// ManifestPath (0.3.19+) is the path to the experiment manifest JSON
// describing capabilities / runtime / safety / surface metadata. The
// path is relative to the manifest root (MULTICA_RESOURCES_DIR or the
// dev resources tree). All fields after ManifestPath are optional; if
// ManifestPath is empty the flag is treated as a "plain toggle" with
// no experiment manifest — the loader returns ErrNoManifest and the
// call site falls back to the flag's plain-toggle behavior. This keeps
// existing flags (chat_pin_ui) source-compatible while new flags
// (code_canvas) carry a manifest.
//
// RuntimeKind describes the local process model the experiment needs.
// "none" means no subprocess (default for plain toggles), "inline"
// means a no-op manager that simply reports ready (Claude Science's
// Multica-native path), "subprocess" means a real binary the desktop
// spawns (Pythia), "headless" means the experiment runs entirely
// inside the agent runtime (Mythos Swarm). Loader callers use this to
// decide which ExperimentalManager implementation to wire in PR 4.
//
// ProxyPrefix and LoopbackService are populated for experiments that
// need a same-origin reverse proxy. The handler MountExperimentalProxies
// (PR 2) iterates Catalog and registers the route automatically. Both
// fields are empty for experiments without a subprocess (no proxy
// needed). When set, ProxyPrefix must start with "/" and LoopbackService
// must match the service field in __experimental/upstream POSTs.
type Flag struct {
	Key             string          `json:"key"`
	DefaultVal      bool            `json:"default_enabled"`
	Title           LocalizedString `json:"title"`
	Description     LocalizedString `json:"description"`
	ManifestPath    string          `json:"manifest_path,omitempty"`
	RuntimeKind     string          `json:"runtime_kind,omitempty"`
	ProxyPrefix     string          `json:"proxy_prefix,omitempty"`
	LoopbackService string          `json:"loopback_service,omitempty"`
	// HideFromIssueLabPicker — when true, the flag is NOT offered in the
	// issue-detail LabPicker. Reserved for "infrastructure" or
	// "self-driven" labs whose effect is global (every agent can use it
	// automatically once enabled), not per-issue. Examples:
	//   - llm_wiki_bridge: agents read LLM Wiki automatically via the
	//     llm-wiki MCP server; picking it per-issue is meaningless.
	//   - agent_self_optimization: runs on its own 4-day schedule, no
	//     issue ownership; choosing it per-issue is a UX trap.
	// LabPicker renders the title for these flags in the Labs settings
	// tab (where the user flips the toggle on), but never inside the
	// "实验插件" popover on a specific issue.
	HideFromIssueLabPicker bool `json:"hide_from_issue_lab_picker,omitempty"`
	// AlwaysShowInLabPicker — when true, the flag's entry ALWAYS appears in
	// the issue LabPicker main list regardless of the flag's enabled state.
	// Reserved for action-type labs whose entry is a user action, not a
	// per-issue lab_source binding: agent_creation_studio is the canonical
	// case — the user must be able to reach the creator even before they
	// have opted in via Labs (0.5.3: it also binds lab_source + dispatches
	// to the leader, but the entry point itself stays unconditional).
	// Mutually exclusive with HideFromIssueLabPicker (asserted at boot by
	// the LabCatalogHasFlag test).
	AlwaysShowInLabPicker bool `json:"always_show_in_lab_picker,omitempty"`
	// HidesDeliverableInIssueTimeline — when true, the lab's agent
	// deliverable comments (comments generated by the lab's leader
	// agent once the run finalises) are hidden from the plain issue
	// timeline view. Used by labs that own a workspace-scoped view
	// (`/experimental/<slug>`) where the deliverable belongs (e.g.
	// Claude Lab Artifact tab, Mythos Swarm reflection panel). Without
	// a workspace-scoped view, the deliverable stays visible in the
	// plain timeline because that is the only place the user sees it.
	//
	// 0.3.49.1: split out from the renderer-side hardcoded
	// `VIEW_LAB_SOURCES` set (issue-labs-section.tsx) so the source of
	// truth lives in the catalog rather than a TypeScript const. The
	// two consumers (issue-labs-section.tsx for the workbench link,
	// issue-detail.tsx for the timeline comment filter) now read
	// `(flags ?? []).find(...).hides_deliverable_in_issue_timeline`.
	HidesDeliverableInIssueTimeline bool `json:"hides_deliverable_in_issue_timeline,omitempty"`
	// InteractionModel classifies HOW a lab participates on a bound
	// issue (0.5.86). Two families, mutually exclusive:
	//
	//   - "assignee" (独立工作型): the lab owns a leader agent that
	//     takes the issue as its assignee and works it to a
	//     deliverable (Claude Lab research, Pythia forecast, TimesFM
	//     forecast, Semantica delegate, Mythos/Swarm run). Binding
	//     the lab locks the issue assignee to that leader — the
	//     assignee picker refuses other people/agents and the
	//     server-side create/update gates 400 any non-leader
	//     assignee. The full list of leader names per lab lives in
	//     handler.defaultLabLeaderForKey / service.defaultLeaderAgentForLab.
	//   - "auxiliary" (辅助协作型): the lab works ALONGSIDE the
	//     workspace's normal agents for tracing/visualization and is
	//     never an assignee (causal_graph hidden team, llm_wiki
	//     bridge). These never appear in assignee pickers and never
	//     lock anything.
	//
	// Empty string = legacy/unclassified (chat_pin_ui, code_canvas,
	// user plugins): behavior is unchanged — the picker allows manual
	// assignment and no lock applies. Client mirror:
	// ExperimentalFlag.interaction_model in packages/core/types/experimental.ts.
	InteractionModel string `json:"interaction_model,omitempty"`
	// Frozen (0.5.88) — when true, the lab is FROZEN for new
	// delegation/bindings. Append-only wire field: the entry, its routes,
	// and its toggle stay (forward-only law; legacy bound issues keep
	// resolving) but nothing new may be started against it. First (and
	// only) consumer: swarm_topology, frozen by the 0.5.86 consolidation
	// (mythos_swarm is the single 蜂群 lab from 0.5.86 on). The
	// daemon's "Available Labs (delegation)" briefing skips frozen labs
	// so agents are never pointed at a lab that cannot accept work.
	// Toggle behavior is deliberately unchanged — Frozen is advisory
	// metadata, not a second enable gate.
	Frozen bool `json:"frozen,omitempty"`
	// SuccessorKey (0.5.88) — the catalog key agents should use
	// instead when Frozen is true. Empty when the lab has no
	// successor. Mirrors the deprecation banner direction
	// (swarm_topology → mythos_swarm).
	SuccessorKey string `json:"successor_key,omitempty"`
	// AutoDispatch — when nil (the zero value), the lab follows the
	// standard 0.3.46 contract: lab_source flip → assignee auto-rewrite
	// to the leader → maybeEnqueueOnAssign runs the task queue. When
	// *AutoDispatch == false, the lab still auto-assigns the leader
	// (so IssueLabsSection + the lab workbench view show the right
	// assignee) but the enqueue gate short-circuits — the user must
	// trigger the run explicitly (e.g. via @mention, a workspace-scoped
	// lab UI button, or the daemon CLI). Used by labs whose runs are
	// expensive enough that an automatic 30+ minute background run on
	// every new issue would surprise the user (claude_science_lab).
	// Pointer is required so we can distinguish "not configured =
	// default true" from "explicitly opted out = false"; a plain bool
	// could not encode that without a sentinel. Free function
	// `experimental.AutoDispatch(key)` collapses both layers (built-in
	// catalog + user plugins) for callers; nil entry returns true.
	AutoDispatch *bool `json:"auto_dispatch,omitempty"`
	// Sidebar mirrors entry_points.sidebar[*] from the manifest so
	// GET /api/experimental-flags can ship the nav rows in one call.
	// Loaded at boot from the manifest under MULTICA_RESOURCES_DIR.
	// Code-only flags (no manifest) leave this empty.
	Sidebar []SidebarRow `json:"-"`
}

// SidebarRow is one entry_points.sidebar item. Mirrors the JSON
// shape in apps/desktop/resources/experiments/*/manifest.json so
// callers can copy entries across verbatim.
type SidebarRow struct {
	Key      string `json:"key"`
	LabelKey string `json:"label_key"`
	Route    string `json:"route"`
}

// Catalog is the developer-authored list of experimental flags. Order
// here is the order rendered in the labs UI, so put the safest / most
// stable flags first — they are the ones the user is most likely to try.
//
// The chat_pin_ui flag gates the pin button in the chat session list.
// 0.3.4 PR-6 shipped the pin UI unconditionally; 0.3.6 hides it behind
// this flag so we can decide whether to graduate it to the default UI
// based on user opt-in rate before flipping the default to true.
//
// The claude_science_lab flag (0.3.22) replaces the 0.3.20
// `claude_science` + `claude_science_runtime` pair. One flag, single
// sidebar entry, single view — chat with multi-agent squads, run
// isolated Python, inspect artifacts, visualize forecasts in one
// window. No reserved workspace; everything lives on the user's
// currently active workspace, tagged via lab_id in metadata. The
// runtime handler keeps its existing route prefix
// (/api/experimental/claude-science-runtime/*) for wire-compat with
// the 0.3.20 SKILL.md docs and the Skill adapter.
//
// The pythia_oracle flag gates the bundled Pythia (multi-perspective
// forecasting) FastAPI service. When enabled, the desktop spawns the
// Python service on a free loopback port and exposes it to Multica
// agents through the multica-pythia Skill adapter. No UI is rendered —
// the agent calls the manager via the CLI. Pythia brings MiroFish
// (swarm predictions) and Osiris (live global intelligence) under
// one headless API.
var Catalog = []Flag{
	{
		Key: "claude_science_lab",
		// 0.5.114: flipped false → true by user decision (mirrors the
		// pythia_oracle flip). The runtime is inline/server-side, so
		// default-on only needs the install payload — the desktop
		// ClaudeLabInstallAutoStart ensure-installs it once per
		// session. Explicit-off pref rows are untouched; only
		// never-touched installs get the new default.
		DefaultVal: true,
		Title: LocalizedString{
			En: "Claude Research Lab",
			Zh: "Claude 科研实验室",
		},
		Description: LocalizedString{
			En: "Single-pane research workbench — chat with multi-agent squads, run isolated Python, inspect artifacts, and visualize forecasts in one window. Replaces the 0.3.20 `claude_science` + `claude_science_runtime` flags. No reserved workspace.",
			Zh: "单窗口科研工作台 — 在一处与多智能体 Squad 对话、运行隔离 Python、查看生成的产物并可视化预测结果。不再创建独立工作区,取代 0.3.20 的 `claude_science` + `claude_science_runtime` 两个 flag。",
		},
		ManifestPath: "experiments/claude_science_lab/manifest.json",
		// inline: server-side only. The runtime handler keeps its existing
		// /api/experimental/claude-science-runtime/* route prefix for
		// wire-compat; its DefaultFor("claude_science_lab") gate replaces
		// the old per-flag check.
		RuntimeKind:                     "inline",
		HidesDeliverableInIssueTimeline: true,
		// 0.5.86: 独立工作型 — the research leader owns the issue.
		InteractionModel: InteractionModelAssignee,
		// 0.5.81 (plan §P4): flip from opt-out (0.5.22) back to default
		// auto-dispatch. The standard 0.3.46 contract applies: lab_source
		// flip on a new issue → assignee auto-rewrites to the research
		// leader → maybeEnqueueOnAssign enqueues the research task. The
		// workbench "Run research" button (handler/claude_science_run.go)
		// stays as a manual re-trigger surface for retries / mid-flight
		// kicks — it bypasses both service gates because the endpoint
		// IS the manual opt-in. Leader-rewrite semantics unchanged.
	},
	{
		Key: "pythia_oracle",
		// 0.5.114: flipped false → true by user decision — the lab is
		// stable enough to ship enabled (PythiaEngineAutoStart brings the
		// subprocess up on boot). A user who explicitly turned the flag
		// off keeps their pref row; only never-touched installs get the
		// new default.
		DefaultVal: true,
		Title: LocalizedString{
			En: "Pythia Multi-Perspective Deliberation",
			Zh: "Pythia 群智推演",
		},
		Description: LocalizedString{
			En: "Headless local Python service that fuses a swarm prediction engine with a live global-intelligence feed. Multica agents call it via the multica-pythia Skill for forecasts, what-if scenarios, and global briefs.",
			Zh: "本地无界面 Python 服务,融合群体预测引擎与实时全球情报数据源。Multica 智能体通过 multica-pythia 技能调用,用于预测、情景推演与全球简报。",
		},
		ManifestPath: "experiments/pythia_oracle/manifest.json",
		// subprocess: pythia-manager spawns a real Python process on a
		// free loopback port and registers the URL with
		// experimental_proxy.go via __experimental/upstream. Agents
		// reach the API through the same Multica origin so the
		// renderer can call /experimental/pythia/* same-origin.
		RuntimeKind:     "subprocess",
		ProxyPrefix:     "/experimental/pythia",
		LoopbackService: "pythia_oracle",
		// 0.5.86: flipped from true → false. The forecast run now
		// writes its text report back to the issue as the
		// pythia_runtime leader's comment (handler/forecast_issue.go,
		// report_comment_id dedupe) — that comment IS the issue-first
		// deliverable and must stay visible in the plain timeline
		// (previously the report lived only in /experimental/pythia,
		// which is exactly the gap the 0.5.86 issue-delivery batch
		// closes). The full 推演 view stays reachable via the labs
		// section click-through.
		HidesDeliverableInIssueTimeline: false,
		// 0.5.86: 独立工作型 — pythia_runtime owns the issue assignee
		// slot; binding the lab locks the assignee to the leader.
		InteractionModel: InteractionModelAssignee,
		// 0.5.81 (plan §P3): opt out of the auto-dispatch contract.
		// Pythia's per-issue forecast runs are 10 SSE rounds × ~5s = 50s
		// of blocking work on every lab_source=pythia_oracle create; the
		// user prefers to trigger forecasts explicitly via the per-issue
		// Pythia panel's "Run forecast" button (which posts directly to
		// /api/experimental/pythia-oracle/forecast/issue — unaffected by
		// this gate). Leader-rewrite still applies so the assignee stays
		// = the pythia lab agent and IssueLabsSection keeps rendering.
		AutoDispatch: ptrBool(false),
	},
	{
		// llm_wiki_bridge: connects Multica agents to the locally-installed
		// `/Applications/LLM Wiki.app`. Reads go through the bundled
		// llm-wiki MCP server (stdio JSON-RPC); writes go through the
		// filesystem under `/Users/jiangjianyan/Documents/llm wiki/{wiki,
		// skills, agents, squads, sources}/` following the wiki's
		// directory schema (the user manually re-vectorises after a
		// write). Off by default; the HTTP routes register zero entries
		// on the chi router when the flag is off, matching the
		// claude_science_runtime wire discipline above.
		Key:        "llm_wiki_bridge",
		DefaultVal: false,
		Title: LocalizedString{
			En: "LLM Wiki Local Bridge",
			Zh: "LLM Wiki 本地桥接",
		},
		Description: LocalizedString{
			En: "Bridges Multica agents to your local /Applications/LLM Wiki.app. Reads use the llm-wiki MCP server (vector search, graph queries, file reads); writes drop files into the LLM Wiki vault directory for you to vectorise manually. Off by default — enable to let agents search your local knowledge base.",
			Zh: "将 Multica 智能体桥接到本地的 /Applications/LLM Wiki.app。读走 llm-wiki MCP server(向量搜索、图查询、文件读取);写落文件到 LLM Wiki 仓库目录,向量索引由你手工重建。默认关闭,开启后允许智能体检索本地知识库。",
		},
		ManifestPath: "experiments/llm_wiki_bridge/manifest.json",
		// Server runtime stays inline because the chi adapter is in-process.
		// Desktop manager kind is subprocess because it separately owns the
		// local stdio MCP lifecycle declared by the manifest.
		RuntimeKind: "inline",
		// Infrastructure flag — agents use the bridge automatically once
		// it's enabled. Picking it per-issue has no meaning, so the issue
		// LabPicker does not offer it as a "实验插件" choice.
		HideFromIssueLabPicker:          true,
		HidesDeliverableInIssueTimeline: true,
		// 0.5.86: 辅助协作型 — the bridge works alongside the normal
		// agents (knowledge retrieval); never an assignee.
		InteractionModel: InteractionModelAuxiliary,
	},
	{
		// 0.5.83 WL3: issue causal graph — Tier A/B/C/D decision
		// tracking. Server-NATIVE: causal_node / causal_edge tables +
		// the gated REST surface live in the Go server; there is no
		// subprocess, no loopback proxy prefix, and no manager (the
		// desktop descriptor is kind "none"). The manifest carries the
		// real sidebar entry point (/experimental/causal-graph) — the
		// user asked for a visible full-graph surface, not a
		// settings-toggle-only lab.
		//
		// 0.5.119: renamed 知识图谱决策追溯自动系统 (Knowledge Graph
		// Decision Traceability) and flipped DefaultVal to true — the
		// Tier A recorders are global task-seam hooks (enqueue /
		// complete / sub-issue split), not an issue-bound surface, so
		// the tracing only fulfils its purpose when it captures every
		// task by default. Explicit-off pref rows are untouched; the
		// renderer-side CausalGraphInstallAutoStart ensure-installs the
		// hidden team once per session (claude_science_lab 0.5.114
		// precedent). Flag-key literal "causal_graph" is a VERBATIM
		// copy per the duplication law (router gate + lock.go
		// SourceCausalGraph + migration 279 CHECK + this catalog —
		// pinned by TestCatalogAutoDispatchContract + the migration
		// static test). The KEY does not carry the new display name —
		// renaming it would touch every verbatim site for zero gain.
		Key:        "causal_graph",
		DefaultVal: true,
		Title: LocalizedString{
			En: "Knowledge Graph Decision Traceability",
			Zh: "知识图谱决策追溯自动系统",
		},
		Description: LocalizedString{
			En: "Automatic decision-traceability system: every task run, sub-issue split, delegation, decision and outcome is captured into a typed causal knowledge graph (native task hooks + Semantica decision mirrors + Pythia hypothesis closure + LLM-curated proposals behind a human-confirm gate). Agents consume the graph at claim time and via `multica causal`; the /experimental/causal-graph surface is the workspace-wide overview. On by default.",
			Zh: "决策追溯自动系统:每个任务运行、子任务分裂、委托、决策与结果自动汇成类型化因果知识图谱(原生任务钩子 + Semantica 决策镜像 + Pythia 假设闭环 + 人工确认门控的 LLM 提议)。智能体在 claim 时自动收到因果简报,也可用 `multica causal` 主动追溯;/experimental/causal-graph 页面提供全局总览。默认开启。",
		},
		ManifestPath: "experiments/causal_graph/manifest.json",
		RuntimeKind:  "inline",
		AutoDispatch: ptrBool(false),
		// HideFromIssueLabPicker deliberately NOT set: the graph is
		// issue-bound (the subgraph seeds from issue nodes and the
		// causal icon lives on the issue header), so per-issue binding
		// is meaningful.
		// 0.5.86: 辅助协作型 — the causal team (curator/historian/
		// verifier, all lab_managed-hidden) works ALONGSIDE the
		// workspace's normal agents for decision tracing and
		// visualization; it is never an assignee and never locks the
		// picker.
		InteractionModel: InteractionModelAuxiliary,
	},
	// 0.5.6: `agent_self_optimization` and `agent_creation_studio`
	// are no longer catalog entries. The two flags were promoted to
	// product-level resources in 0.5.5 (boot-provisioned leader
	// agents, autopilot-row `enabled` control, no Labs tab toggle)
	// and 0.5.5.1 decoupled the self-opt control surface; 0.5.6
	// removes the catalog literals entirely so a future
	// `experimental.DefaultFor("agent_creation_studio")` returns
	// `false` instead of relying on a never-queried stub. The runtime
	// (service/agent_self_optimization/*) and the leader agents
	// (agent_creation_expert, 智能体优化专家) live on — they are
	// product-level resources, not catalog entries. See
	// .omc/0.5.6-ship-2026-08-02.md for the full rationale.
	//
	// 0.3.57 catalog cleanup: the 0.3.20 constitution_agent flag is
	// RETIRED (see migration 165). Visibility constants in visibility.go
	// and the SourceConstitutionAgent enum value in lock.go were deleted
	// alongside the flag. The skill, autopilots, and agent row live on
	// past that point only via historical lock rows; existing tables were
	// not dropped because migration data must remain forward-compatible.
}

// Interaction model values for Flag.InteractionModel (0.5.86). See the
// field comment for the full contract.
const (
	// InteractionModelAssignee — 独立工作型: the lab's leader agent
	// owns the bound issue's assignee slot and works it to a
	// deliverable. Binding locks the assignee (picker + server gates).
	InteractionModelAssignee = "assignee"
	// InteractionModelAuxiliary — 辅助协作型: the lab works alongside
	// the workspace's normal agents for tracing/visualization and is
	// never an assignee.
	InteractionModelAuxiliary = "auxiliary"
)

// InteractionModelOf resolves the interaction model for a flag key,
// collapsing the built-in catalog and the dynamic user-plugin layer
// (mirrors AutoDispatch). Empty string = legacy/unclassified — callers
// must treat it as "no lock, no auxiliary semantics" (0.5.86 law).
func InteractionModelOf(key string) string {
	userPluginMu.RLock()
	f, ok := userPlugins[key]
	userPluginMu.RUnlock()
	if ok {
		return f.InteractionModel
	}
	for i := range Catalog {
		if Catalog[i].Key == key {
			return Catalog[i].InteractionModel
		}
	}
	return ""
}

// IsAssigneeModelLab reports whether binding `key` on an issue locks
// the assignee to the lab's leader agent.
func IsAssigneeModelLab(key string) bool {
	return InteractionModelOf(key) == InteractionModelAssignee
}

// IsAuxiliaryModelLab reports whether `key` is an auxiliary
// (trace/visualize-only) lab that must never appear as an assignee.
func IsAuxiliaryModelLab(key string) bool {
	return InteractionModelOf(key) == InteractionModelAuxiliary
}

// userPluginMu guards the dynamic user plugin layer. Built-in flags
// in Catalog are immutable after init; user plugins come from the
// user_plugin table only — loaded at boot (ListActiveUserPlugins) and
// merged incrementally on each CRUD write. There is no on-disk
// discovery path.
var (
	userPluginMu sync.RWMutex
	userPlugins  = map[string]Flag{}
)

// RegisterUserPlugins merges user-created plugin flags into the
// dynamic layer. Called at boot after the DB is available. Flags
// whose keys collide with built-in Catalog entries are silently
// skipped — built-in flags always win.
func RegisterUserPlugins(flags []Flag) {
	userPluginMu.Lock()
	defer userPluginMu.Unlock()
	for _, f := range flags {
		// skip collisions with built-in catalog
		if isBuiltinKey(f.Key) {
			continue
		}
		userPlugins[f.Key] = f
	}
}

// UnregisterUserPlugin removes a single user plugin flag from the
// dynamic layer. Called when a user deletes a plugin.
func UnregisterUserPlugin(key string) {
	userPluginMu.Lock()
	defer userPluginMu.Unlock()
	delete(userPlugins, key)
}

// isBuiltinKey checks only the static Catalog.
func isBuiltinKey(key string) bool {
	for _, f := range Catalog {
		if f.Key == key {
			return true
		}
	}
	return false
}

// UserPluginFlags returns a snapshot of all registered user plugin
// flags. The caller may iterate freely; the returned slice is a copy.
func UserPluginFlags() []Flag {
	userPluginMu.RLock()
	defer userPluginMu.RUnlock()
	out := make([]Flag, 0, len(userPlugins))
	for _, f := range userPlugins {
		out = append(out, f)
	}
	return out
}

// IsKnownKey reports whether key matches a Catalog entry. The HTTP
// handler uses this to reject PATCH calls for unknown keys, preventing
// users from accidentally creating a typo-bound row in experimental_pref
// that the Labs UI then has no way to surface or clear.
func IsKnownKey(key string) bool {
	if isBuiltinKey(key) {
		return true
	}
	userPluginMu.RLock()
	defer userPluginMu.RUnlock()
	_, ok := userPlugins[key]
	return ok
}

// IsFrozen reports whether the catalog marks key as frozen — retired
// for new issue bindings (0.5.105: swarm_topology). Frozen stays
// advisory for toggles and for the daemon delegation briefing; only
// the lab_source BIND path (handler/issue.go, via
// frozenLabSourceBindError) treats it as a hard reject.
func IsFrozen(key string) bool {
	f, ok := FlagByKey(key)
	return ok && f.Frozen
}

// FlagByKey returns a copy of the Flag definition for key, resolving
// the dynamic user-plugin layer first (mirrors InteractionModelOf).
// ok=false when key is unknown to both layers. 0.5.88 added this so
// read-side consumers (the daemon delegation briefing) can consult
// advisory metadata (Frozen / SuccessorKey) without a third lookup
// table — the catalog stays the single source of truth.
func FlagByKey(key string) (Flag, bool) {
	userPluginMu.RLock()
	f, ok := userPlugins[key]
	userPluginMu.RUnlock()
	if ok {
		return f, true
	}
	for i := range Catalog {
		if Catalog[i].Key == key {
			return Catalog[i], true
		}
	}
	return Flag{}, false
}

// AllFlagKeys returns every flag key in the catalog as a fresh slice.
// 0.3.26 added this helper so callers that previously hard-coded the two
// visibility-gated labs (`agent_self_optimization`, `constitution_agent`)
// can iterate the full list without drift. The slice is freshly allocated
// so callers may freely mutate it; the returned values are stable catalog
// identifiers and may be used as map keys.
func AllFlagKeys() []string {
	keys := make([]string, 0, len(Catalog))
	for _, f := range Catalog {
		keys = append(keys, f.Key)
	}
	userPluginMu.RLock()
	defer userPluginMu.RUnlock()
	for k := range userPlugins {
		keys = append(keys, k)
	}
	return keys
}

// DefaultFor returns the catalog default for key, or false when key is
// unknown. The Provider uses this as the final fallback when neither
// the user override nor any higher-priority provider has an opinion.
//
// 0.3.18 Labs safety: if the flag appears in the on-disk blacklist
// (a previous panic / 5xx burst / init timeout auto-disabled it),
// DefaultFor returns false regardless of the catalog default. The
// blacklisted flag's code path is then bypassed by the same code
// that respects DefaultFor elsewhere, which is the whole point of
// the safety net.
//
// IsBroken swallows read errors silently (returns ok=false) so a
// corrupted blacklist file does not lock every flag off. The
// operator can read / fix the file by hand without restart.
func DefaultFor(key string) bool {
	if _, broken := IsBroken(key); broken {
		return false
	}
	if isBuiltinKey(key) {
		for _, f := range Catalog {
			if f.Key == key {
				return f.DefaultVal
			}
		}
	}
	// user plugins default to false (opt-in)
	return false
}

// AutoDispatch reports whether an issue with lab_source=key should
// auto-enqueue a task on the lab leader when created (CreateIssue +
// UpdateIssue paths via IssueService.maybeEnqueueOnAssign / handler
// WillEnqueueRun). Defaults to true so existing labs keep their
// 0.3.46 behaviour; flags that opt out (claude_science_lab) set
// Flag.AutoDispatch = ptrBool(false).
//
// Note: this gate does NOT block the leader-rewrite (assignDefaultLabAgent /
// assignDefaultLabAgentOnUpdate). The assignee still becomes the lab
// leader — only the auto-enqueue is skipped. Users can still trigger a
// run explicitly via @mention, lab UI buttons, or daemon CLI.
//
// Built-in catalog wins on key collision (matches DefaultFor's rule);
// user plugins that omit AutoDispatch fall back to true.
func AutoDispatch(key string) bool {
	if isBuiltinKey(key) {
		for _, f := range Catalog {
			if f.Key == key {
				return f.AutoDispatch == nil || *f.AutoDispatch
			}
		}
	}
	userPluginMu.RLock()
	defer userPluginMu.RUnlock()
	if f, ok := userPlugins[key]; ok && f.AutoDispatch != nil {
		return *f.AutoDispatch
	}
	return true
}

// ptrBool returns a pointer to the literal bool. Helper for filling
// Flag.AutoDispatch at the catalog literal site without making every
// site spell out `b := false; AutoDispatch: &b`.
func ptrBool(b bool) *bool { return &b }
