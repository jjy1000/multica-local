# 0.5.3 Release Notes — Self-Opt 泛化四对象 + 智能体创建 Studio 升级为可分配 Lab

**日期**: 2026-08-02
**版本**: 0.5.3

## 功能一:自进化循环泛化(智能体 + 技能 + 团队 + 自动化)

原 0.5.2 只优化智能体 instructions。0.5.3 泛化为四类可优化对象,并修正信任语义:

| 对象 | 可训练文本 | 自动应用 |
|---|---|---|
| 智能体 `agent` | `agent.instructions` | trust<7 强制 / enroll 可选 |
| 技能 `skill` | `skill.content`(SKILL.md 正文) | 永不(人工确认) |
| 团队 `squad` | `squad.instructions`(迁移 088) | 永不(人工确认) |
| 自动化 `autopilot` | `issue_title_template` | 永不(人工确认) |

### 信任语义修正(对齐用户原话「当信用到8以上,视为可以不用再进化的保留机制」)
- **信任 ≥ 8 → 保留**:不再提议任何编辑(0.5.2 方向反了,已翻转)
- **信任 < 7 + 纠正/审核失败 → trust-scope 强制优化**:系统有义务修复,自动应用免 enroll marker
- 技能/团队/自动化无信任账本 → 全部进「待确认建议」人工确认

### 其余改进
- 验证打分改**增量对比**(只评 delta 行)
- 安全词表收窄(去掉「必须/不得」)
- 低信任优先排序 + deferral 联动(低信任 agent 存在时不因数据不足 defer)
- 删除闭环:skill/squad/autopilot 删除清理账本
- **edits.go 人工 apply/revert 泛化四对象**(含修复:snapshot 曾捕获编辑后文本)

## 功能二:agent_creation_studio 升级为可分配 Lab

- 新增 install handler:provision `agent_creation_expert` leader agent
- LabPicker 选中 → `lab_source` 写入 → 自动分配 leader → 任务派发
- 手动创建器保留,双路径并存

## 迁移

- **232**: `agent_opt_edit` 加 `target_type`/`target_id`/`subject_scope`,`agent_id` 可空+回填
- **233**: 修复 0.5.2 遗留 drift — 缺 `updated_at` 列(查询引用它,hman-confirm 路径从未被真实 DB 走过)

## 测试
- 11 个新 optimizer 测试 + 2 个 handler 集成测试(真实 DB)
- Go 全量测试通过;`go vet` 通过;`pnpm typecheck` 通过

## 验证
- 冷启动三查通过:5432/8090 LISTEN、/health ok、workspace=1 恒定
- 已安装 /Applications/Multica.app(0.5.3)+ 嵌套二进制重签
