# 0.3.32 — Pythia 完整仪表盘 + ClaudeLab rawRequest 修复 + 10 轮预测契约

## 一句话

Pythia 从 0.3.29 的精简报告升级为完整仪表盘(SVG 地球 + 议会厅 + 预测列表 + what-if + 晨报 + 计分卡 + 行情),Claude Lab 全网调用从 bare fetch 切换到 api.rawRequest,forecast 默认轮次从 1 提升到 10。

## 关键变更

### Pythia 完整仪表盘
- 0.3.29 报告(单薄) → 0.3.32 仪表盘
- 全程通过 `window.experimentalAPI.pythia.proxy` IPC 通道调用,确保 token-auth 桌面应用下 renderer fetch 不失效
- 新增 17 个字符串键 (`horizon_year`、`council_chamber_*`、`scorecard_*`、`world_brief_*` 等),4 语言完整对齐

### Claude Lab `rawRequest` 修复
- 7 处 `fetch(..., { credentials: "include" })` 改为 `api.rawRequest(...)`
- 桌面 token-auth 下 bare fetch 完全无法登录(无 cookie 携带,renderer origin ≠ API host,无 proxy rewrite)
- 同时影响 Plan / Artifact / Code / Knowledge / Forecast tab

### Forecast 10 轮契约
- `defaultIssueForecastRounds` 从 1 → 10
- `maxIssueForecastRounds` 从 3 → 10
- 新增 `clampIssueForecastRounds(int) int` 导出函数,加 6 单元测试(0 / 负 / 1 / 10 / 11 / 1000)

### Manager idempotent ensureUp
- `pythia_oracle` + `llm_wiki_bridge` 加 ready 短路 + starting 状态轮询(10s 上限)
- 消除 "already started" 抛错(manager-template.ts:134)

### Mythos 命名空间修复
- `packages/views/locales/index.ts` 中 `enMythos.mythos` → `enMythos`(平铺结构修复)
- zh-Hans / ja / ko 同步修复

### scopeCounts 类型对齐
- `{ all: number; mine: number; shared: number }` → `Record<AutopilotScope, number>`
- 消除 AutopilotScope 扩展时的 type drift

## 测试

- `go test -count=1 ./internal/handler/ -run TestClampIssueForecastRounds` PASS
- `go vet ./internal/handler/` 0 警告
- `pnpm --filter @multica/desktop bundle-cli` OK (0.3.32 build)
- `pnpm --filter @multica/desktop exec electron-builder --mac --dir` OK
- Cold start 三件套:5432 LISTEN + 8090 LISTEN + `/health` 返回 OK

## 部署

1. `cd server && go run ./cmd/migrate up`(mig 157 已应用,无需新迁)
2. `pnpm --filter @multica/desktop bundle-cli`
3. `pnpm --filter @multica/desktop exec electron-builder --mac --dir`
4. `rm -rf /Applications/Multica.app && cp -R dist/mac-arm64/Multica.app /Applications/`
5. `bash ~/.multica/scripts/verify-desktop-cold-start.sh`

## 数据兼容性

- 没有 schema 变更(mig 157 在 0.3.31 已落地)
- 没有 config 字段重命名
- 没有 RPC 路径删除(forecast/issue 端点保持,仅默认值变化)
- 既有数据库用户 workspace / issue / comment / agent 数据不受影响

## 行级验证

| 表 | 0.3.30.2 | 0.3.32 | 增量 |
|---|---|---|---|
| workspace | 1 | 2 | +1 (新工作区) |
| issue | 170 | 182 | +12 (实验期间新建) |
| comment | 977 | 981 | +4 (轻量) |
| agent | 85 | 87 | +2 (系统新增) |
| mythos_run | 0 | 0 | schema 就位 |
| mythos_members | 0 | 0 | schema 就位 |

## 注意

- `CFBundleShortVersionString` 在 macOS 只能存三段 semver;0.3.31.1 → 0.3.32 是把版本显式升级一格以匹配 dashboard 变更密度
- `.omc/release-notes-0.3.31.md` 历史保留,不代表当前 ship
- DMG 打包因 Electron 39 + create-dmg 1.2.3 兼容问题仍走 `.app` 直接覆盖路径
