# 0.5.138 — 签名授权启用闸：默认关闭 + 自担风险声明（2026-10-04）

一句话:0.5.137 交付的签名授权功能此前**装机即存在**;本批把它改为**默认关闭**——设置中先阅读用途声明(典型场景:调用 API 模型时创建红蓝对抗/渗透测试类攻击智能体或技能、执行危险任务行为)、勾选风险自担确认后才可启用;停用立即暂停签署/验证/注入。服务端同闸强制,纯前端隐藏不算数。

## 改动

### 服务端(真正的闸)

- `signing.SettingsKey = "signature_authorization_enabled"`(workspace.settings jsonb 键):`EnabledFromSettings` 只认布尔 true——字符串 "true"/1/null/缺键一律视为关闭,马虎写入无法误触发;`WithEnabled` 合并写入保留未知键(append-only 法),停用写显式 false(可审计)。
- `requireSignatureArmed` 挂满**全部十个签名面**:上传/列表/图片/退役/工作区历史/签署/issue 历史/撤销/按 id 验证/**按指纹验证(CLI 通道)**。未启用一律 `403` + 指引文案("open Settings → Signatures, read the purpose and risk notice")。
- claim 注入点:`loadActiveSignatureForClaim` 增加工作区 arm 检查——**停用后即使存在活跃签名行也不再注入 attestation**;重新启用后无需重签即恢复(行与内容快照未变)。
- 开发中测试抓到一处漏网:按指纹验证的内联路径(不走共用 helper)未挂闸,`TestSignatureDisarmSuspendsExistingCoverage` 立即红,已补。

### 前端

- Settings → 签名 tab 重构为两态:**未启用 = 声明卡**(用途说明 + 三条能力点 + 风险自担段落 + 勾选确认 → 启用按钮在勾选前禁用);**已启用 = 管理面** + 顶部"停用"按钮(两次点击确认)。启用写合并 settings(`default_runtime_id` 等未知键保留),并失效 workspace 列表缓存——issue 标题行 pill 随 arm 状态即时消失/出现。
- issue-detail:pill 与仪式 dialog 都在 `signatureArmed` 为 false 时不渲染(默认装机 = 功能完全不存在)。
- 四语言文案修正:tab 描述明确用途(红蓝对抗/渗透测试类攻击智能体或技能的创建、危险任务行为,调用 API 模型场景);新增启用流 12 键 ×4。
- `useSignatureEnabled`/`signatureEnabledFromSettings`/`setSignatureEnabled` 进 `@multica/core/signature/hooks`,与服务端同语义(布尔 true 才算开)。

## 验证

- Go `./internal/... ./pkg/agent/...` 40 包全绿;新增签名 settings 单测(11 子测:布尔-only 语义/合并保留未知键/显式 false)与两个 handler 测试组(**未启用全面 403**——上传/列表/仪式/issue 历史;**停用即暂停既有覆盖**——活跃签名行在停用后 claim 不注入、verify 403,重启用恢复)。
- `pnpm typecheck`、`pnpm lint` 绿;views vitest **1990 通过**(+3:声明卡勾选前禁用启用钮/启用写合并键且保留 default_runtime_id/管理面两击停用写显式 false),core 969 通过。
