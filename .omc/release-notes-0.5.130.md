# Release 0.5.130 (2026-09-30)

**主题：上游 v0.6.0 时代移植批第一波 + 高价值改写移植**。40 个上游提交（`12f8f3f31`→`e31da86c9`）全量 triage，12 个 commit 落地（`5c940ca98..f66755692`）。零 schema 破坏性变更；**一个新迁移 293**（agent_task_queue 历史分页部分索引，随包自动应用）。账本：`.omc/upstream-sync-2026-09-30.md`。

## 用户可感修复

1. **chat 不再因 MCP 工具崩溃**（upstream 82847e275）：工具入参含对象（如 URL 参数对象）时展开步骤折叠会白屏整窗——现在只字符串字段作摘要。
2. **一条坏 mention 不再炸掉全部通知**（upstream 04cdd4857）：非 UUID 的 member mention（"deadbeef"、裸 "all"）此前会 panic 掉 comment:created 通知监听，同批有效接收者一起丢；现在坏 ID 被跳过。
3. **mermaid 图不再随弹窗开关重渲染**（upstream 32a396fd5）：dialog scroll lock 写 body style 曾被当成主题切换，关闭预览时全页图重渲染导致卡顿+闪屏。
4. **全选+发送后不再残留高亮** / **Ctrl+A 删除后光标回到正文**（Tiptap 3.22.1→3.31.3，upstream 400791262）。
5. **截图不再拉满整页**（upstream 3125bd2ea 端态）：评论附件列表的图片改为自然尺寸+36rem 高度上限，竖长手机截图保持形状。
6. macOS TCC 升级后卡死排障文档（zh/ja/ko，按 fork 实情改写）。

## 特性

7. **agent 任务历史分页**（MUL-7685 改写移植）：详情页 Activity tab 从"打开拉全部 run"改为 keyset 分页（200/页，Show more 拉下页）；30 天均值耗时改服务端全窗聚合；CLI `multica agent tasks --limit/--before`。**行为变更**：escalation 占位行（deferred/cancelled 且未启动）此前在历史里原样返回，现在按上游语义隐藏。
8. mobile 项目列表不再被单个 WS 事件播种成残缺列表（upstream 6328e8183）；mobile vitest 测试线扩展到 data/ 层。

## 门禁

- `pnpm typecheck` 6/6
- `go test ./internal/... ./pkg/agent/...` rc=0（Codex 2 测在全量负载下单次计时 flake，单跑+整包均绿）；`./cmd/server/` 仅 2 条在案 baseline 红
- vitest：views 1915+33 skip / core 968 / mobile 36
- 迁移 293 已应用到本地库；bundle-cli 镜像已随源同步 commit

## 注意

- Mimosa hook 在一次提交前报 `apps/mobile/data/api.ts:244/:1218` SSRF-high：实为 mobile 端调自身后端的 `fetch(API_URL+path)` 模式误报，未修改，供后续裁决。
- 本批未改 root CLAUDE.md（发版说明在 .omc，账本已入 git）。
