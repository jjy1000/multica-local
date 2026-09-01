# 0.5.94 (2026-09-01) — 技术债务审计修复批:MUL-6749 移植 + 中文状态名 + 门禁加固

独立审计(不依赖文档记录,全部代码级复核)后的问题修复批。除注明外均含回归测试。

## 中文命名的自定义状态(MUL-6749,移植上游 d6ecf4bc8)

此前状态名的机器 key 只接受 ASCII 字符:纯中文名(如「客户确认」)会派生出空 key 直接报错,而设置表单又没有 key 输入框 —— **中文工作区从界面上完全无法创建中文命名的状态**。现在:

- 名称无可提取 ASCII 时,key 回退为「分类 + 序号」:`客户确认` → `in_review_2`,仍能看出它继承哪个平台行为;
- 两个不同名称坍缩到同一 slug(「待客户 Review」/「待供应商 Review」→ `review`)时,后者自动消歧为 `review_2`,不再误报冲突;
- 派生 key 的"读目录 + 插入"在事务内于独占目录锁下完成,且**所有**创建(含显式 key)都走该锁,消除并发创建的半开竞态;
- 派生 key 不可读,显示名随行:`status_name` 贯穿 HTTP 响应、后台事件与前端 schema;未知状态 400 错误列出 `key (显示名)`;创建成功的 toast 会告知实际生成的 key;`multica issue status --help` 明确参数是 key 而非名称。

## 收件箱优先级词条(MUL-6835 残余收尾)

`priority_changed` 通知此前直接渲染静态英文(中文界面出现「设优先级为 Urgent」),现走统一的 issues 语言包。这是已知最后一处硬编码英文状态/优先级文案。

## 门禁诚信

- `TestThinkingCacheKeyDistinct` 偶发红(全量跑负载下复现一次):三个并行测试共享包级 thinking 缓存且各自 reset→put→get 无测试级互斥 —— 三个测试已移出并行波次;
- `check.sh` 的 TS 三步(typecheck/lint/test)加 `--force`:turbo 缓存曾对 `@multica/core#test` 服务过假绿,可从缓存应答的门不是门;
- 清理 5 处失效的 eslint-disable 指令;退役 `TestAutopilotTickFlagOffShortCircuits`(其断言的门在 0.5.6 已随 flag 移出目录而删除)。

## 仓库安全

- origin 重新指向新建私有镜像 `jjy1000/multica-exploration-dev`(原 `jjy0` owner 已不存在,远端 404,888 个提交此前仅有本机单拷贝);24 个分支已推送。322 个陈旧快照 tag 被GitHub fsck 拒收,仅存本地(非负载件)。

## 本版同时带上 0.5.93 bump 之后的 9 个未发布提交

此前 `/Applications` 的 0.5.93 未包含:bump 后当日的门禁诚信修复批 —— issue 页白屏崩溃修复、composer 发送/停止按钮无障碍名、auth 会话失效恢复、30 个 lint 错误清理(含 `shell.openExternal` 安全放行绕过)。

## 仍开放的账本(有意不动)

`CompleteTask` 对 dispatched 任务静默空操作(需与 WS4 同做);Timesfm/causal 安装测试的全局 `experimental_resource_lock` 计数(该表本无 workspace 列,结构性);mythos reaper 与 t=0 心跳的竞态窗口;ClaimTask causal-brief 端到端 + 并发去重测试覆盖。
