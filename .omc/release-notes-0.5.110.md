# 0.5.110(2026-09-22)— 上游价值移植批:OpenCode 2.x + 仓库起始分支 UI

上游 `multica-ai/multica` 45 提交移植(13 代码 port + 1 docs,5 SKIP 有据)。
账本:[`.omc/upstream-sync-2026-09-22.md`](upstream-sync-2026-09-22.md)。

## 用户可见

- **OpenCode 2.x runtime 可用**(MUL-7520):此前 2.x 下每个任务 ~130ms 即死
  (GH #8586)。现 2.x 正确去掉 `--dir`、thinking level 折叠进 model;取消时
  会真正打断后台 session(否则客户端死了任务还在跑);携带 MCP 配置的任务会
  明确拒跑并解释原因(2.x 只能把凭据写进工作区文件,不安全),而不是静默
  丢掉所有 MCP server。1.x 行为零变化。
- **仓库起始分支可在 UI 设置**(MUL-7504):创建项目时每个仓库可填起始分支;
  资源面板每个已附加仓库有分支编辑(改动只影响新开的任务);mobile attach
  表单同款。粘贴 `.../tree/<分支>` 地址自动拆成 URL + 分支。服务端保存前做
  git ref 形状校验(之前拼错的分支要到任务里 500 才暴露)。设置了分支的仓库
  会在任务简报里告知 agent:从这里开始、PR 也提回这条分支。
- **评论单条 copy link**(MUL-7528)、**inbox 通知文案改进**(MUL-7521)。
- **UI 性能**:文字 shimmer / border-beam 降重绘(MUL-7466);web 路由 barrel
  imports 减载(8feab0abe 部分)。
- **编辑器**:图片粘贴保留已知类型预览(MUL-7518)。
- **杂项**:本地目录资源不再显示误导性 rename pencil(MUL-7525);desktop
  启动 toolbar 遮挡修复;skill 文档与 CLI 实际行为同步(MUL-7577/5850)。

## 工程

- 双 brief 路径(legacy 默认 + slim)同步 MUL-7504 简报语义,fork 专属 slim 钉子测试。
- `useUpdateProjectResource` 回归(0.5.109 批删的孤儿现在有了真实消费者)。
- localization 冲突零命中、零 migration。
