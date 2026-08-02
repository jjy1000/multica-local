# 0.5.5.2 Release Notes — Labs tab UI 补完(隐藏产品级 flag)

**日期**: 2026-08-02
**版本**: 0.5.5.2

## 修复:0.5.5.1 漏改的 Labs tab UI 入口

0.5.5 + 0.5.5.1 后端逻辑已经让 `agent_creation_studio` + `agent_self_optimization` 完全脱离实验域,但 **Settings → Labs tab 仍然显示这两个 flag**(截图证据)。

## 用户视角

- **Settings → Labs tab**: 只显示其他 6 个真正 opt-in 的 flag(claude_science_lab / pythia_oracle / mythos_swarm / llm_wiki_bridge / code_canvas / chat_pin_ui)
- **不再显示**:"智能体自优化"、"智能体创建"(这两个已升格为产品级,无 toggle)

## 跟 0.5.5 阶段对齐

0.5.5 已经在 `LabPicker`(issue detail 里的 lab 选择器)硬编码了同样两个 key;0.5.5.2 把同样的"产品级黑名单"延伸到 Settings Labs tab —— **两个 UI 层防御 catalog drift**。

## 改动

- `packages/views/settings/components/labs-tab.tsx`:
  - 加 `useMemo` import
  - 加 `PRODUCT_LEVEL_LAB_KEYS` Set
  - 加 `displayedFlags` 过滤
  - `flags.map` → `displayedFlags.map`

## 验证

- `pnpm typecheck`: 6/6 子包过
- 冷启动三查: Info.plist 0.5.5.2 = package.json 0.5.5.2、5432/8090 LISTEN、/health ok
- 纯 FE 修复,Go binary 未动

## 后续 0.5.6

catalog 完全移除 flag 条目 + 删 install handler + RecentLabsPanel + 删 `PRODUCT_LEVEL_LAB_KEYS` / `HIDDEN_LAB_KEYS` 集合(那时 catalog 不再返回这些 key,客户端黑名单也无必要)
