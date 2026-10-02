# 0.5.133 (2026-10-03) — 三实验室动态图闲置态常驻:静态也显示为"动画中"

一个 FE-only 修复提交(`df0428789`),零迁移、零 wire、零 Go。**用户装上 0.5.132 后反馈三个动画"未实现"**——实际是闲置态把画布藏起来了:①两个 issue 内嵌有「0-runs 整卡消失」法则;②大脑画布闲置默认折叠;③议会画布没数据连席位都不画;④星图空态是死白盒。修复原则:**闲置 = 等待中的动画,不是空白**。

## 修复面

- **大脑**(claude):`defaultOpen ?? hasLive` → 默认恒展开(闲置大脑本身就是视觉:闲置边慢速漂移 + idle/queued 节点呼吸环 + 中枢轨道照转);点开关仍可折叠。`ClaudeIssueEmbed` 0-runs 法则收窄——闲置 lab 议题渲染大脑卡(仅此卡),获取失败重试条保留 0.5.131 法则。
- **议会**(pythia):0 envelopes 时席位=引擎固定名单(swarm.py Strategist/Economist/Naturalist/Skeptic,未投票态+呼吸环),罗盘加慢速虚线待命轨道环——**呈现"等待中的议会",绝不造数据(verdict 恒 pending)**;`PythiaIssueEmbed` 整卡 null 法则移除(闲置 lab 议题渲染待命议会+embed_idle 提示);live 块从第一帧就挂议会(首封前=待命席);历史折叠内议会默认展开。
- **星图**(causal):焦点轨道环常转(26s/圈);无焦点工作区渲染动画空态(旋转虚线环+提示文案)替代死白盒。

## 安全性

内嵌卡上游已按 `issue.lab_source`(pythia_oracle / claude_science_lab)与路由闸门,闲置常驻不会出现在普通议题上。全部新动画走既有 reduced-motion 闸门(CSS 媒体查询 + useReducedMotion)。

## 门禁与测试

`pnpm typecheck` 6/6;`pnpm lint` 8/8;views vitest **1976 passed**(33 skipped 在案)。两条 0-runs 塌缩钉子测试**反向重写**为闲置常驻契约(claude embed 大脑卡 / pythia embed 待命议会),新增议会固定名单待命测试(断言四席名与 idle 呼吸环,verdict=pending 防造数据)。i18n 新增 `pythia_lab.embed_idle` + `constellation_empty` ×4 语言。
