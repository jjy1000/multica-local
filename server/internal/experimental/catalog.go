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
		Key:        "chat_pin_ui",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Chat pin button",
			Zh: "聊天置顶按钮",
		},
		Description: LocalizedString{
			En: "Show the pin button in the chat session list. Pinned sessions stay at the top. Off by default — enable to try.",
			Zh: "在聊天会话列表显示置顶按钮。置顶的会话会排在最上方。默认关闭,开启后即可试用。",
		},
		ManifestPath: "experiments/chat_pin_ui/manifest.json",
		RuntimeKind:  "none",
		// 0.5.60 (audit P2-6): chat_pin_ui is a pure UI toggle with no
		// leader agent and no dispatch path — binding it to an issue is
		// a dead lab_source that nothing ever reads. Hide it from the
		// per-issue LabPicker like the other infrastructure flags.
		HideFromIssueLabPicker: true,
	},
	{
		Key:        "claude_science_lab",
		DefaultVal: false,
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
		Key:        "pythia_oracle",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Pythia Multi-Perspective Forecasting",
			Zh: "Pythia 多视角预测",
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
		RuntimeKind:                     "subprocess",
		ProxyPrefix:                     "/experimental/pythia",
		LoopbackService:                 "pythia_oracle",
		HidesDeliverableInIssueTimeline: true,
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
		// mythos_swarm: 0.3.16+ multi-agent topology inspired by OpenMythos
		// Recurrent-Depth Transformer. A prelude agent plans the work,
		// parallel loop agents iterate until convergence (cosine ≥ 0.95
		// between successive turns) or max_loop_iters, and a coda agent
		// synthesizes. Each loop turn may invoke Claude Science skills
		// from the experimental_resource_lock catalogue.
		//
		// Off by default. The Mythos install handler provisions a
		// dedicated `mythos-swarm` workspace + 5 Mythos agents + 1
		// squad; user squads are NOT mutated (preserves the user's
		// existing roster).
		Key:        "mythos_swarm",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Mythos Swarm Topology",
			Zh: "Mythos 蜂群拓扑",
		},
		Description: LocalizedString{
			En: "Multi-agent RDT topology: a prelude agent plans, parallel loop agents iterate until spectral convergence, and a coda agent synthesizes. Off by default — enable to decompose unknown problems through the multica-mythos Skill.",
			Zh: "多智能体 RDT 拓扑:prelude 智能体规划,并行 loop 智能体迭代至谱收敛,coda 智能体汇总。默认关闭 — 通过 multica-mythos 技能将未知问题解耦为子任务。",
		},
		ManifestPath: "experiments/mythos_swarm/manifest.json",
		// headless: Mythos runs entirely inside the agent runtime. The
		// RDT three-stage runner (prelude / loop / coda) is invoked
		// by the Skill adapter, not by a subprocess. No proxy.
		RuntimeKind:                     "headless",
		HidesDeliverableInIssueTimeline: true,
	},
	{
		// 0.5.21 swarm_topology: multi-agent role-graph topology. The
		// orchestrator runs in-process as a server-owned goroutine
		// (Service.StartOrchestrator + runOrchestratorLoop); no
		// subprocess, no proxy. The HTTP surface at
		// /api/experimental/swarm-topology/* is gated by this flag —
		// off-flag callers get a uniform 404 (indistinguishable from a
		// nonexistent route, per experimental_guard.go).
		//
		// 0.5.22: description refreshed to match the orchestrator's
		// 3-phase lifecycle contract (multica-creating-swarms
		// bootstrap → execute → cleanup); HideFromIssueLabPicker
		// stays false so users can pick swarm_topology per issue via
		// the LabPicker (the contract puts the lab in charge of the
		// assignee via the mutex gate, not in a globals-only mode).
		Key:        "swarm_topology",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Swarm Topology",
			Zh: "蜂群拓扑",
		},
		Description: LocalizedString{
			En: "Self-organising multi-agent system. The leader authors role-agents + skills + a coordinating squad on bootstrap, then walks a 5-phase machine. Off by default.",
			Zh: "自组织多智能体系统。leader 在启动时创建角色 agents + skills + 协调 squad,运行 5 阶段机器。默认关闭。",
		},
		ManifestPath: "experiments/swarm_topology/manifest.json",
		RuntimeKind:  "headless",
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
	},
	{
		// code_canvas: 0.3.19 P9 internal lab, graduated to a real
		// service in 0.5.18. A subprocess experiment that spawns
		// vendor/code-canvas/run.sh (stdlib-only GET /health +
		// GET|POST /render syntax-highlight service) so the desktop IPC
		// pipeline exercises a genuine loopback backend end-to-end.
		Key:        "code_canvas",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Code Canvas (internal lab)",
			Zh: "代码画布（内部实验）",
		},
		Description: LocalizedString{
			En: "Internal P9 pilot: a stub subprocess wired through every Labs platform layer. Off by default. Used to verify the manifest → catalog → registry → IPC pipeline end-to-end before real labs are added.",
			Zh: "内部 P9 试点：通过 Labs 平台所有层的 stub 子进程。默认关闭。在真实实验加入前用于端到端验证 manifest → catalog → registry → IPC 链路。",
		},
		ManifestPath:                    "experiments/code_canvas/manifest.json",
		RuntimeKind:                     "subprocess",
		ProxyPrefix:                     "/experimental/code-canvas",
		LoopbackService:                 "code_canvas",
		HidesDeliverableInIssueTimeline: true,
	},
	{
		// semantica: 0.5.22 Semantica × Multica Phase 2 integration.
		//
		// Phase 1 (0.5.22 dev cycle) shipped the catalog entry, vendor
		// run.sh, REST API bridge, and the `multica-semantica` curl skill.
		// Phase 2 adds: semantica_decision_advisor leader agent (installable,
		// hidden when flag is off), terminal-issue decision sync via the
		// events bus, a Labs-tab iframe view at /experimental/semantica-explorer,
		// and `multica lab delegate semantica "<task>"` (resolved by the
		// `resolveLabFlagKey` pass-through fix in cmd_lab.go).
		//
		// The subprocess path is unchanged from Phase 1 — semantica uses the
		// GENERIC subprocess-manager (resolveGenericSubprocessManager)
		// because it needs no env injection beyond SEMANTICA_REPO_PATH,
		// which the run.sh script reads from process.env directly.
		Key:        "semantica",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Semantica Knowledge Graph",
			Zh: "Semantica 知识图谱",
		},
		Description: LocalizedString{
			En: "Local subprocess that bridges Semantica (knowledge graph + decision records) into Multica. Phase 2 adds the semantica_decision_advisor agent for delegation, terminal-issue decision sync, and a Labs-tab iframe. Off by default.",
			Zh: "本地子进程,把 Semantica(知识图谱 + 决策记录)桥接到 Multica。Phase 2 新增 semantica_decision_advisor agent 支持委托、终态 issue 自动同步决策、Labs 标签页 iframe。默认关闭。",
		},
		ManifestPath:    "experiments/semantica/manifest.json",
		RuntimeKind:     "subprocess",
		ProxyPrefix:     "/experimental/semantica",
		LoopbackService: "semantica",
		// Phase 2: opt into the issue LabPicker so users can bind
		// lab_source=semantica per-issue (mirrors pythia_oracle /
		// claude_science_lab). The decision-sync listener and the iframe
		// tab together make semantica a first-class issue-bound lab.
		HideFromIssueLabPicker:          false,
		HidesDeliverableInIssueTimeline: true,
		// Sidebar entry is owned by the manifest's
		// spec.entry_points.sidebar (apps/desktop/resources/experiments/semantica/manifest.json).
		// Route must be /experimental/semantica-explorer — the bare
		// /experimental/semantica path is reserved for the REST proxy
		// used by the agent subprocess. Single source of truth: edit the
		// manifest, not this catalog literal.
	},
	{
		// timesfm: 0.5.82 WL2 — TimesFM 2.5 local forecasting lab.
		// A vendored torch-stack Python engine (source-of-record
		// apps/desktop/vendor/timesfm-src/, spawned generically by the
		// desktop manager-factory from resources/timesfm/run.sh) serves
		// POST /forecast on loopback; the Go server reverse-proxies it
		// at /experimental/timesfm (auto-mounted from this entry — no
		// manual proxy code) and persists per-issue runs into
		// timesfm_forecast_run (migration 275).
		//
		// Issue-task-first: runs fire from issues via the leader agent
		// `timesfm_oracle` + the multica-timesfm skill; the lab view is
		// a read-only record (manifest spec.entry_points.sidebar is
		// EMPTY per ICP-1). Off by default.
		Key:        "timesfm",
		DefaultVal: false,
		Title: LocalizedString{
			En: "TimesFM Forecasting Lab",
			Zh: "TimesFM 预测实验室",
		},
		Description: LocalizedString{
			En: "Local TimesFM 2.5 forecasting engine (offline torch runtime). Agents forecast numeric series from issue data via the multica-timesfm skill; quantile-band results persist per issue. Weights are user-seeded — without them a seasonal-naive fallback answers. Off by default.",
			Zh: "本地 TimesFM 2.5 预测引擎(离线 torch 运行时)。智能体通过 multica-timesfm 技能对 issue 数据中的数值序列做预测,分位数区间结果按 issue 持久化。权重由用户手动放置 — 未放置时以季节性朴素回退应答。默认关闭。",
		},
		ManifestPath: "experiments/timesfm/manifest.json",
		// subprocess: the desktop manager-factory spawns a real Python
		// process on a free loopback port (generic manifest-driven
		// manager, semantica style) and registers the URL with
		// experimental_proxy.go via __experimental/upstream. Agents
		// reach POST /forecast through the same Multica origin.
		RuntimeKind:     "subprocess",
		ProxyPrefix:     "/experimental/timesfm",
		LoopbackService: "timesfm",
		// CPU inference takes seconds-minutes per call (report R5/R6);
		// runs stay manual/retry-driven — pinned by
		// TestCatalogAutoDispatchContract in catalog_test.go.
		AutoDispatch: ptrBool(false),
		// HideFromIssueLabPicker deliberately NOT set: the lab is
		// per-issue bindable (lab_source=timesfm) like pythia_oracle /
		// semantica. HidesDeliverableInIssueTimeline deliberately NOT
		// set: the agent's forecast comment IS the issue-first
		// deliverable and must stay visible in the plain timeline.
	},
	{
		// 0.5.83 WL3: issue causal graph — Tier A/B/C/D decision
		// tracking. Server-NATIVE: causal_node / causal_edge tables +
		// the gated REST surface live in the Go server; there is no
		// subprocess, no loopback proxy prefix, and no manager (the
		// desktop descriptor is kind "none"). The manifest carries the
		// real sidebar entry point (/experimental/causal-graph) — the
		// user asked for a visible full-graph surface, not a
		// settings-toggle-only lab. Off by default; the Tier A/B
		// recorders no-op unless the flag is on.
		//
		// Flag-key literal "causal_graph" is a VERBATIM copy per the
		// duplication law (router gate + lock.go SourceCausalGraph +
		// migration 279 CHECK + this catalog — pinned by
		// TestCatalogAutoDispatchContract + the migration static test).
		Key:        "causal_graph",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Issue Causal Graph",
			Zh: "问题因果图谱",
		},
		Description: LocalizedString{
			En: "Decision tracking across issues: runs, decisions, and outcomes become a typed causal graph (native task hooks + Semantica decision mirrors + Pythia hypothesis closure + LLM-curated proposals behind a human-confirm gate). Adds the /experimental/causal-graph surface. Off by default.",
			Zh: "跨 issue 的决策追踪:运行、决策与结果汇成类型化因果图(原生任务钩子 + Semantica 决策镜像 + Pythia 假设闭环 + 人工确认门控的 LLM 提议)。新增 /experimental/causal-graph 页面。默认关闭。",
		},
		ManifestPath: "experiments/causal_graph/manifest.json",
		RuntimeKind:  "inline",
		AutoDispatch: ptrBool(false),
		// HideFromIssueLabPicker deliberately NOT set: the graph is
		// issue-bound (the subgraph seeds from issue nodes and the
		// causal icon lives on the issue header), so per-issue binding
		// is meaningful.
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

// userPluginMu guards the dynamic user plugin layer. Built-in flags
// in Catalog are immutable after init; user plugins are loaded at
// boot from the DB and optionally from ~/.multica/plugins/.
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
