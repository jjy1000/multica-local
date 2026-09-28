# 0.5.125 (2026-09-28) — 群体推演实验室可被 agent 调用: pythia skill 认知修复 + 实验室目录页 + mention 正则回归

Commits: `98f864acd`, `137bc0e25` (5 文件, +368/−47). 提案与完整调查证据: `.omc/proposals/pythia-delegation-routing-2026-09-28.md`.

## 起因

Issue #505「关于对拟写方案进行优化和审议」里, 用户要求「委托群体推演实验功能对该方案做假想推定」。
squad leader 回复「工作区里没有名为群体推演实验的插件或功能」, 然后用 squad 内 4 个角色
分头写出了时间 / 外部依赖 / 泄露舆情 / 现场政治表述四组人工角色扮演推演。

**它没有撒谎 —— 能力一直都在, 而且不需要绑定实验室。**

## 用户可见

- **agent 现在能看见并调用群体推演**: 在 issue 上说「推演 / 预演 / 假想 / 情景 / 沙盘」这类请求,
  agent 会走 `multica pythia issue-forecast --issue <id> --wait`, 而不是自己编情境。
- **独立交付 + 结果回传** (既有契约, 本次只是接通了): 引擎跑完后结论报告作为一条
  `pythia_runtime` 评论落在该 issue 上, 该 issue 上所有其他 agent 经正常 inbox 路径读到。
- **新增实验室能力目录页** `multica-labs`: 列出全部四个实验室, 并逐个标注
  **agent 能否自主调用** —— 避免 agent 对 claude_science 也误报「没有这个功能」。
- **不再用角色扮演顶替推演**: skill 里明写禁止 squad 角色扮演替代引擎 (无概率分布 /
  无多轮收敛 / 无反事实权重), 并要求「宣布功能缺失之前先跑命令」。
- **合成降级诚实标注**: 引擎不可用时服务端会降级成合成生成器, 报告带 `synthetic` /
  `synthetic_oracle_failover` 标签; skill 要求 agent 必须向用户声明这是合成结果而非真实预测。
- **mention 假 id 修复**: `[@Alice](mention://member/Alice)` 此前会被解析成
  `{member, "A"}` 并照常派工 —— 一个不存在的 agent 收到任务。现已拒绝。

## 工程

### 根因 (三层, 最后一层此前没人发现)

1. **没有语义路由**: `issue.lab_source` 的唯一写入入口是 UI 的 LabPicker
   (`lab-picker.tsx:42`), 评论文本永远无法设置它。
2. **`AutoDispatch=false`**: pythia 绑上也不会建 agent 任务 (`issue_trigger.go:97-99`),
   且 `multica lab delegate pythia_oracle` 被 CLI 显式拒绝 (`cmd_lab.go:570`)。
   0.5.81 的产品决策, 本批未推翻。
3. **skill 认知层自我阻断** ← 真正的杀手:
   - built-in skill 的 `AgentSkillData.Description` **恒为空** (`loadBuiltinSkill` 只填
     Name + Content); description 是在 agent 机器上从正文重新 parse, 正文写到
     `{workDir}/.claude/skills/<name>/SKILL.md`, 由 provider CLI 原生加载
     (`execenv context.go:177-179, 451`)。**所以 front-matter 就是唯一契约。**
   - 旧 description 全英文 (中文触发词一条没有) + 写着
     `Do not use it for chat / issues` —— 与同文件的 issue-forecast 段落直接冲突。
   - 正文 Step 1 要求先跑 `multica pythia status`, 不是 `ready` 就 **stop here**;
     而该命令**从 CLI 恒返回 `status="unknown"`** (`cmd_pythia.go:100-108`,
     注释自陈 "cannot introspect the desktop-managed subprocess ... by design")。
     **agent 忠实执行自己的 skill 指令 → 拿到 unknown → 按指令放弃 → 如实汇报「服务没跑」。**

### 改动

- `multica-pythia/SKILL.md`: description 中文化 + 触发词 + 直接给出可执行命令 +
  删除自相矛盾排除条款; Step 1 重写 (issue 场景跳过 status 探针, 并写明
  `unknown` 是 CLI 的预期答案而非服务已停); 新增禁止角色扮演顶替、合成降级披露、
  「先跑命令再下结论」三条硬规则。
- `multica-labs/SKILL.md` (新增): 四个实验室的能力目录。
  `loadMainProductSkills` 走目录扫描, **加目录即注入, 零代码改动**。
  记录两个此前未文档化的限制: `claude-science research` / `get-result` 均已 Deprecated
  (pythia 是唯一有 agent 可调用路径的 lab); `brief` / `predict` / `whatif` 强制 `--url`
  而 CLI 永远拿不到 URL, 实为死路。
- `builtin_skills_labs_test.go` (新增): 4 条回归测试, **每条都做过 mutation 验证**
  —— 逐条破坏契约确认测试会红, 4/4 全杀。
- `internal/util/mention.go`: `bareMentionRe` 的 id 分支由 `[0-9a-fA-F-]+` 收紧为完整
  8-4-4-12 UUID 形状。JYF-490 引入该正则时无尾部定界锚, hex 类会吃掉人名 id 的首字母。
  **不能用负向前瞻** —— Go 的 regexp 是 RE2, 无 lookaround, 形状本身必须承担锚定。
  本 fork 所有 id 都是 36 字符 UUID, 真实 mention 不受影响。
- `internal/util/mention_test.go`: 新增 4 条负例 (截断 / 非 uuid slug / uuid 前缀 /
  被拒的散文不影响旁边的真 uuid)。

## 门禁

```
pnpm typecheck                                      → 6/6, EXIT=0
go test ./internal/... ./pkg/agent/...              → 39 包全绿, EXIT=0
```

**在案 baseline 红 (非本批引入)**: `cmd/server` 的 `TestCommentTriggerOnComment` /
`TestCommentTriggerAtAllSuppression` 仍红 (0.5.117/0.5.118 遗留的 comment-trigger
thread-parent 路由问题)。已用 `git stash` 对比验证: 移除本批全部改动后失败模式逐字相同,
且 `cmd/server` 不在 ship gate 列表内。需专门 fix batch。

## 验证方式

在 #505 上重新派工说同一句话。预期 agent 输出 `multica pythia issue-forecast` 相关调用,
而非四组情境角色扮演。若仍不调, 说明还有未发现的层级。

## 未做 (见提案 §5 分批)

- **方案 B2**: agent 调 pythia 前自动 ensure-up (引擎当前未运行, 需先实测引擎未起时
  `issue-forecast` 的行为再定)。
- **方案 C**: 评论语义路由。待权衡: 只要报告评论 (绕开 `lab_source` / `AutoDispatch`),
  还是必须要可视化面板 (则须绑 lab, 须推翻 0.5.81 决策)。
