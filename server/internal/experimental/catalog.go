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
		RuntimeKind: "inline",
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
		RuntimeKind:     "subprocess",
		ProxyPrefix:     "/experimental/pythia",
		LoopbackService: "pythia_oracle",
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
		RuntimeKind: "headless",
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
	},
	{
		// code_canvas: 0.3.19 P9 internal lab. A subprocess-style
		// experiment that spawns a tiny stub binary so the desktop
		// IPC pipeline gets end-to-end exercised without depending
		// on a real external service. The 30-line stub run.sh is
		// enough to verify the manifest → catalog → registry →
		// dispatcher → manager-factory → IPC chain; future labs
		// replace the stub with the actual binary.
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
		ManifestPath:    "experiments/code_canvas/manifest.json",
		RuntimeKind:     "subprocess",
		ProxyPrefix:     "/experimental/code-canvas",
		LoopbackService: "code_canvas",
	},
	{
		// agent_self_optimization: hides the 「智能体优化专家」 agent
		// and its 2 autopilots (per-3-workday bulk optimization +
		// daily SkillOpt-Multica self-evolution loop) plus the
		// `skillopt-multica` Skill behind an opt-in toggle. Off by
		// default — these features run autonomous edits to agents /
		// skills / autopilots across the workspace, so the flag exists
		// to keep them out of the visible agent team + automation list
		// until the user explicitly opts in. Visibility is enforced
		// via experimental_resource_visibility rows + autopilot
		// scheduler skip; the agent row stays in the squad so its
		// leader briefs still resolve.
		Key:        "agent_self_optimization",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Agent Self-Optimization Loop",
			Zh: "智能体自优化循环",
		},
		Description: LocalizedString{
			En: "Exposes the 智能体优化专家 agent, its 2 autopilots (SkillOpt-Multica daily self-evolution loop + per-3-workday bulk optimization), and the skillopt-multica Skill as an opt-in plugin. Off by default — these features run autonomous edits across the workspace, so keep them hidden until you opt in.",
			Zh: "将「智能体优化专家」智能体与其 2 条 autopilot(SkillOpt-Multica 每日自进化循环 + 每3工作日批量优化)以及 skillopt-multica 技能作为可选用插件暴露。默认关闭 —— 这些功能会在工作区内自动修改智能体 / 技能 / 自动化,请在明确启用前保持隐藏。",
		},
		ManifestPath: "experiments/agent_self_optimization/manifest.json",
		// inline: the flag is purely a visibility gate (HideableResource
		// rows hide the agent / 2 autopilots / skill from list queries
		// and the autopilot scheduler). No subprocess; no proxy.
		RuntimeKind: "inline",
	},
	{
		// constitution_agent: hides the 宪法智能体 agent, its 3
		// autopilots (CTR 三周评审 / CSIL 宪章自优化循环 / TAOL 任务
		// 智能体优化循环), and the multica-constitution-agent Skill
		// behind an opt-in toggle. Off by default — this agent enforces
		// the workspace 《智能体宪章 v6》(CSIL + CTR + TAOL 三轨制)
		// and runs autonomous review cycles, so the flag exists to
		// keep it out of the visible agent team / automation / skill
		// list until the user explicitly opts in. The agent row, the
		// autopilot rows, and the SKILL.md content (charter body +
		// CSIL/CTR/TAOL protocols) stay in the DB / resources tree so
		// toggling the flag on restores everything without re-
		// provisioning. Visibility is enforced via
		// experimental_resource_visibility rows + autopilot scheduler
		// skip — same pattern as agent_self_optimization.
		Key:        "constitution_agent",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Constitution Agent (Charter Guardian)",
			Zh: "宪法智能体（宪章守护）",
		},
		Description: LocalizedString{
			En: "Hides the 宪法智能体 agent and its 3 autopilots (CTR 三周评审 / CSIL 宪章自优化 / TAOL 任务-智能体优化) behind an opt-in toggle. Off by default — this agent enforces the workspace constitution (charter v6, CSIL + CTR + TAOL tracks) and runs autonomous review cycles, so keep it hidden until you explicitly enable it. The agent row, autopilots, and SKILL.md content stay in the DB / resources tree so toggling on restores them without re-provisioning.",
			Zh: "将「宪法智能体」与其 3 条 autopilot(CTR 三周评审 / CSIL 宪章自优化 / TAOL 任务-智能体优化)以及配套 skill 作为可选用插件暴露。默认关闭 —— 该智能体负责执行工作区宪章(charter v6,CSIL + CTR + TAOL 三轨制)并周期性自动评审,请在明确启用前保持隐藏。原始数据 / 资源 / 自动化保留在 DB 与 resources 中,启用 flag 即恢复,不重新创建。",
		},
		ManifestPath: "experiments/constitution_agent/manifest.json",
		// inline: pure visibility gate (HideableResource rows hide the
		// agent / 3 autopilots / skill). Same shape as
		// agent_self_optimization — no subprocess, no proxy.
		RuntimeKind: "inline",
	},
	{
		// agent_creation_studio (0.3.45): action-type lab distinct
		// from the visibility-gated flags above. Instead of hiding or
		// installing hidden agents, it surfaces an in-Labs editor
		// ("studio") where the user explicitly drafts agent / skill /
		// squad resources, then submits the existing POST /api/agents,
		// /api/skills, /api/squads endpoints. Created resources are
		// normal user-visible rows (no HideableResource rows seeded)
		// so they participate in the main product immediately — but
		// the entry point itself lives behind the lab toggle, keeping
		// the "build a team" affordance out of the main product chrome.
		//
		// Unlike agent_self_optimization / constitution_agent (which
		// are visibility gates with zero new HTTP routes), this flag
		// exposes no install handler, no HideableResource, no
		// experimental_resource_lock rows. The only thing it ships is
		// entry_points.issue_panel_action on the manifest and a
		// pre-workspace /experimental/agent-creation-studio route.
		//
		// 0.3.45 hard constraint: the action entry point fires through
		// LabPicker's `onAction` callback, NOT through issue.lab_source
		// — so the lab/assignee mutex, assignDefaultLabAgentOnUpdate,
		// and the IssueLabsSection sidebar link all stay oblivious to
		// the studio. This keeps the studio orthogonal to the existing
		// 8-flag inline / visibility-gate pair.
		Key:        "agent_creation_studio",
		DefaultVal: false,
		Title: LocalizedString{
			En: "Agent Creation Studio",
			Zh: "智能体创建",
		},
		Description: LocalizedString{
			En: "Draft and create agent, skill, or squad resources from inside Labs. Created resources are normal user-visible rows; they appear in the main product immediately. Off by default — the studio sits behind an opt-in toggle so the main product chrome stays focused.",
			Zh: "在实验室内编写并创建智能体 / 技能 / 团队资源。新建资源为普通用户可见行, 立即在主产品中出现。默认关闭 —— 编辑器隐藏在 opt-in 开关后, 保持主产品界面简洁。",
		},
		ManifestPath: "experiments/agent_creation_studio/manifest.json",
		// inline: no subprocess, no proxy, no install handler. The
		// studio runs purely inside the renderer (ChatWindow-shaped
		// orchestrator) and round-trips through existing REST APIs.
		RuntimeKind: "inline",
	},
}

// IsKnownKey reports whether key matches a Catalog entry. The HTTP
// handler uses this to reject PATCH calls for unknown keys, preventing
// users from accidentally creating a typo-bound row in experimental_pref
// that the Labs UI then has no way to surface or clear.
func IsKnownKey(key string) bool {
	for _, f := range Catalog {
		if f.Key == key {
			return true
		}
	}
	return false
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
	for _, f := range Catalog {
		if f.Key == key {
			return f.DefaultVal
		}
	}
	return false
}
