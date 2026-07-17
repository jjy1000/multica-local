# 0.3.30.2 — Labs 实战修复 + 侧栏命名

**Date**: 2026-07-16T12:10:34Z
**Build**: `/Applications/Multica.app` 0.3.30.2(替换 0.3.30.1)
**Branch**: feat/0.3.29-integration

---

## 0. 触发这次 ship 的两个用户截图

| 截图 | 现象 | 根因 |
|---|---|---|
| #1 Claude Lab → Plan tab | `claude_lab.loading_failed: Failed to fetch`(红的) | renderer asar 仍是 0.3.29 旧 bundle,还在用裸 `fetch(/api/...)`(package 0.3.30.1 跑过 ship,但 renderer 没真打进 asar — 主进程 / Go binary 是新,renderer 是旧)|
| #2 Pythia → 进入时 | `pythia.pythia_unavailable / Error invoking remote method 'pythia:ensure-up': Error: experimental manager pythia: already started` | main 端 manager-factory.ts `pythia_oracle.ensureUp` 没状态守卫,首次 spawn 后的二次 ensureUp 触发 throw at `manager-template.ts:134` |

---

## 1. renderer 必须真打 asar 替换(0.3.30.1 漏的链路)

**根因**:`cp -R dist/mac-arm64/Multica.app/Contents/Resources/app.asar` 这条直接 copy 路径,在 0.3.30.1 ship 时没真重新产 dist(mac-arm64 时间戳 7月 16 18:54,而 source renderer 改文件 7月 16 19:32,**asar 早于 source 修改 38 分钟**)。

**本次 ship 步骤**:
1. `pnpm --filter @multica/desktop bundle-cli`(打 Go + 拷贝 resources → 766M)
2. `pnpm --filter @multica/desktop build`(`electron-vite build` → 重生成 `out/main/out/preload/out/renderer` + `dist/mac-arm64/Multica.app/.../app.asar`)
3. `cp -R dist/mac-arm64/Multica.app /Applications/`
4. `PlistBuddy -c 'Set :CFBundleShortVersionString 0.3.30.2'`(electron-builder fallback ship 模式不自动 bump)
5. 冷启动三-check + grep asar 验证 `api.rawRequest` 字面量存在 + Plan tab 加载空列表

---

## 2. 代码改动(3 文件 + 1 locale + Info.plist)

### 2.1 `apps/desktop/src/main/pythia-manager.ts:242`
`pythia:ensure-up` IPC handler 加 idempotent guard。sharedManager 是 Electron session 全局单例,proxy warm-up / 上次 Pythia tab / `pythia:proxy` 调过一次 `start()` 后,二次 ensureUp 会走到 `manager-template.ts:134` 抛 "already started"。

```ts
ipcMain.handle("pythia:ensure-up", async () => {
  if (sharedManager === null) {
    sharedManager = new PythiaManager({ resourceSubdir: "pythia" });
  }
  const cur = sharedManager.status();
  if (cur === "ready") return cur;            // 已就绪,直接返回
  if (cur === "starting") {                    // 别人在起,等一下
    const deadline = Date.now() + 10_000;
    while (sharedManager.status() === "starting" && Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 100));
    }
    return sharedManager.status();
  }
  await sharedManager.start();                 // 真启动
  return sharedManager.status();
});
```

注:ExperimentalStatus type 是 `"idle"|"starting"|"ready"|"stopping"|"stopped"|"error"`(无 "running" literal),typecheck 必须用 `"ready"`。

### 2.2 `apps/desktop/src/main/experimental/manager-factory.ts`
两处 `ensureUp` 加同款 idempotent guard:
- `pythia_oracle` 分支(原 174-177)
- `llm_wiki_bridge` 分支(原 195-198)— **同样 bug**,顺手修

generic subprocess 分支 215-220 已有 `idle|stopped` guard,**不动**(它 spawn 完的状态字面量为 "ready",语义对齐)。

### 2.3 `server/internal/handler/claude_lab_forecast.go`
删 5 行死代码:`var _ = experimental.DefaultFor` import-keeper + 同步删 experimental import。

### 2.4 `packages/views/locales/zh-Hans/layout.json:42`
- `"experimental_pythia": "Pythia 预测神谕"` → `"Pythia 多视角推演"`

> Pythia 是希腊德尔菲神谕,API 设计不是"算命",而是"世界观察 + 多视角辩论台"(swarm.py)—— 让本地 LLM 戴几个 persona 帽子同时对一个 scenario 出共识+分歧(Brief / Oracle+Swarm / WhatIf)。侧栏命名回归设计语义。

en / ja / ko 已是 `Pythia Oracle / Pythia オラクル / Pythia 오라클`,不动。其它 locale(Pythia 等)是上游字段,flag key `pythia_oracle` / engine 路径 / DB `lab_source` 列 / IPC 通道名全不动。

### 2.5 `apps/desktop/package.json`
- `0.3.30.1` → `0.3.30.2`

---

## 3. 不在本次 ship 范围(已知未修,继续记账)

| 项 | 位置 | 影响 |
|---|---|---|
| `lab_section.*` 8 key + `lab.picker_none` 缺失 | 4 个 locale `issues.json` | IssueLabsSection 在带 lab_source 的 issue 右侧栏抛 `lab_section does not exist`(TS 在 0.3.30 + 现 build 都报;前端 `t(...)` 兜底返回 undefined 因此**用户看不见崩溃**,但 toast / 详情渲染仍坏)|
| `mythos_enabled_badge` 缺失 | layout.json | sidebar Mythos 行的 hexagon 徽章 fallback("Mythos 蜂群已启用" 显示 undefined)|
| `mythos` json 在 base locale 不存在 | locales/index.ts:151/182/213/244 | 4 个 TS 错 |
| AutopilotListFilters `'triggers'` key | issue-detail.tsx:1532 | list 过滤 UI 微 bug |
| `__pycache__/` 随 engine 递归打进 resources | bundle-cli.mjs | DMG 体积浪费 ~tens KB |
| `vendor/pythia-src/runs/ledger.jsonl` 被打包 | bundle-cli.mjs | fixture 数据泄漏 |
| bundle-cli fixture manifest 旧 `schema_version:1` | server/internal/experimental/experiments/ | 与生产 `apiVersion: multica.dev/experiment/v1` 不一致(仅测试)|
| `llm_wiki_bridge` catalog RuntimeKind split | catalog.go vs manifest/desktop | 标 `inline` 但行为是 subprocess,需决策 |
| 8 manifest 缺 `installable` / `resources` 字段 | experiments/*/manifest.json | 渲染端无法独立观察 installability |
| CLI 无 `experimental install|rollback|gc` 子命令 | cmd/multica/cmd_experimental.go | CLAUDE.md 提到但未实现 |
| `SourceClaudeScience` 是否窄化为 `LegacySources` | experimental/lock.go | 历史 lock 行兼容,新代码不会产生 |

---

## 4. Cold start 验证(待 ship 后填)

- [ ] `dist/mac-arm64/Multica.app/Contents/Resources/app.asar` 时间戳 ≥ source renderer 修改时间(本次 ship 必重新产)
- [ ] `grep -c "api\\.rawRequest" /Applications/Multica.app/Contents/Resources/app.asar ≥ 15`(替换 14 处 + 1 文档)
- [ ] `lsof -nP -iTCP:5432 -sTCP:LISTEN` 6s 内 LISTEN
- [ ] `lsof -nP -iTCP:8090 -sTCP:LISTEN` 6s 内 LISTEN
- [ ] `curl http://localhost:8090/health` → `{"status":"ok"}`
- [ ] row parity:`workspace=1 / issue=170 / comment=977 / agent=85`(基线 0.3.29 + 0.3.30.1 零漂移)
- [ ] osascript Multica → Claude Lab → Plan tab → 列表(非 loading_failed)
- [ ] osascript Multica → Pythia 标签(显示"Pythia 多视角推演",非"预测神谕")→ 不再 pythia_unavailable
- [ ] 再点一次 Pythia 标签(同 session 双开)→ 不再抛 already started
- [ ] `multica pythia whatif --help` 返回 ok(IPC 命名/spawn 未坏)

---

## 5. 数据安全 contract(本次 unchanged)

- ✅ `runMigrate` 仍拒 `backend="external"`(P0 guard 0.3.0)
- ✅ 0.3.30.1 → 0.3.30.2 forward-only migration:`server/migrations/*` **未新增任何 .sql**,本次不动 schema
- ✅ asarUnpack `resources/**` 不动
- ✅ AppData / PG volume / config / workspace 路径不动
- ✅ Pre-update snapshot before 0.3.30.2 build(见 ship 命令清单)
