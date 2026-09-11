# 0.5.106 — claude_science_lab 技能链路修复 + 沙盒会话复用 + 动效补强

日期：2026-09-11。分支：`epic/0.5.72-followups`。

## 背景

对标上游 aipoch/open-science（集成时为 Claude Science 时代）的差距评估发现三个维度都不是「差不多实现」：技能（294 个全部空壳）、沙盒（一次性 exec 雏形）、动效（基础过渡）。本批次修复技能链路上连环的四个失效点，把沙盒推进到 notebook 式文件级状态延续，并给 lab 前端补上与主应用一致的动效。

## 修复清单

### 1. 技能打包 bug（294 个技能 Content 全空的根源）

- `apps/desktop/scripts/build-claude-science-manifest.mjs`：`copyDir(skillPath, dest)` 的 dest 用了 `relative(skillsRoot, skillMd)`，把整个技能目录拷成以 `SKILL.md` **结尾的目录**，真正文嵌套在 `SKILL.md/SKILL.md`。已修正为拷到 `skills/<cat>/<name>/`。
- `server/internal/handler/install_claude_science.go::loadManifestAsset`：body_path 解析为目录时改读 `<dir>/SKILL.md`（兼容已分发的旧布局树）。
- `vendor/claude-science-manifest/skills/` 与 `resources/claude-science/skills/` 两棵树就地拍平：294 个正文文件 / 0 个目录（含 hugging-face-trackio 的 `.claude-plugin` dotfile 特殊处理，正文一度误删后从 git HEAD 恢复）。
- `upsertClaudeScienceSkill`：原来对已存在行直接短路 → 重装永远无法修复空 Content。现在 body 非空且库存为空时 `UpdateSkill` 回填 Content + description；用户改过的行不动。
- frontmatter `description:` 解析入库（原先 description 是硬编码的 "Imported from…"）。

### 2. 技能端点三重失效修复 + 新端点

`/api/experimental/claude-science/skills` 原本三处失效叠加（列表恒空）：

1. 挂在认证组外（router.go 公共段）——未认证可枚举目录（0.5.105 H1 同类）；
2. 按已废弃的保留 slug 工作区（`GetWorkspaceBySlug("claude-science")`）查询——0.3.25 后技能装在用户活动工作区；
3. `ListVisibleSkillSummariesByWorkspace` 排除 hidden 行，而安装器结尾 `Hide(source)` 把全部技能锁 hidden=true。

修复：路由迁入 `RequireExperimentalFlag("claude_science_lab")` 组；workspace_id 参数（成员校验）∪ legacy 工作区；新 sqlc 查询 `ListClaudeLabSkillSummariesByWorkspace`（只排除其他 lab 的 hidden 锁）；响应带 description。

新增端点（同 flag 组，`RegisterClaudeScienceSkillRoutes`）：

- `GET /skills/{name}`：全文正文（DB 优先，空 Content 时 manifest 兜底）+ files 列表；
- `GET /skills/{name}/file?path=`：配套文件（md/py/txt/json/tex/sty/bst/bib/sh/yaml/yml/csv 白名单；拒绝绝对路径/`..`/越界；256KB 上限）。

### 3. 按需技能加载（CLI + 文档）

- `multica experimental claude-science-runtime skills`（目录）与 `skill <name> [--file path]`（正文/配套文件）。
- `execute` 新增 `--session <uuid>`；输出增加 `workspace_dir=` 提示。
- `multica-claude-science/SKILL.md` 加 Step 2b 按需加载流；`multica-claude-science-runtime/SKILL.md` 重写（修正过期 flag 名 `claude_science_runtime`、幻影 `--issue` flag，补 session 续跑与技能发现契约）。

### 4. 沙盒会话复用 + 产物路径 bug

- `RuntimeExecuteRequest.session_id`：复用根会话工作目录（`~/.multica/experimental/claude-science/runtime/<root>/`），文件级状态延续；过期根 410 `ErrSessionExpired`；跨 workspace 403。
- **老 bug 修复**：产物 RelPath 原用 DB 行 ID 拼路径，物理目录却是被 `_ =` 丢弃的另一个随机 UUID → 产物字节端点恒 404。现在先插行、目录名 = 行 ID（复用时 = 根 ID），RelPath 锚定目录名。
- 响应新增 `root_session_id`（续跑用）。

### 5. 前端（apps/desktop + packages/views）

- Knowledge tab：workspace 作用域 + 搜索/分类过滤 + 计数；点击行展开正文（20000 字符 pre）+ 配套文件列表。
- Code tab：textarea 编辑器（默认模板）+「运行代码」/「在此会话继续运行」双按钮，替代硬编码 hello-world 的 "Run for me"。
- 动效：forecast 概率图 `motion.polyline` pathLength draw-in + 末点脉冲；tab 切换 `TabFade` crossfade（motion/react + UI_MOTION_DURATION，尊重 useReducedMotion）；预测概率条宽度过渡。causal-graph 已有的动效基建首次被 claude lab 复用。
- 4 locale（zh-Hans/en/ja/ko）×9 新键 + 过期文案更新。

### 6. 测试（全绿基线上新增）

`server/internal/handler/claude_science_skills_test.go`：

- `TestLoadManifestAsset_NestedSkillMDLayout`（无 DB）：嵌套布局读取 + frontmatter 解析；
- `TestInstallClaudeScience_SkillBodyContentRepaired`（DB）：安装正文非空 + description 解析；模拟旧空行 → 重装修复；
- `TestClaudeScienceSkillsEndpoints`（DB）：列表/详情/文件 + pdf 排除 + 穿越防护 400 + 未知 404；
- `TestClaudeScienceRuntime_SessionContinuation`（DB + python3）：run2 读到 run1 文件；产物字节 200 回归（老 bug 钉住）；未知 session 404。

验证：`go test ./internal/... ./pkg/agent/...` 全量、`pnpm typecheck`（6/6）、`pnpm lint`（8/8）、`pnpm test`（1849 passed / 33 parked-skipped）。

## 已知残留

- 294 技能仍不进 agent claim 上下文（设计如此：按需加载取代全量注入；绑定路径需要 agent_skill 或 lab_scoped 插件声明，留待后续）。
- 沙盒仍无持久内核（变量不跨 run 延续，仅文件系统）、无 pip 依赖管理、无网络隔离（`claude_science_runtime.go` 头注释声明留给未来 Lab sandbox 子系统）。
- 历史行（0.5.106 前创建）的产物字节仍 404（目录名不可恢复）。
- `openscience-prompts/` 12 个提示词仍是无读取者的死资源（上游渲染器遗产）。
- AutoDispatch 相关过期注释（catalog.go:806 等）未在本批清理。
