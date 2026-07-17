---
name: 0.2.93 username-only login 跨重启丢失 workspace
created: 2026-06-27T19:46:20Z
updated: 2026-06-27T19:46:20Z
status: closed
severity: P1
affected: local desktop fork 0.2.89+
scope: server/internal/handler/auth, packages/views/login
---

# 0.2.93 username-only login 跨重启丢失 workspace

## 现象

桌面 0.2.93 重启后 GUI 看不到已建工作区（slug=jyf）。检查发现：

- DB `workspace` 表里仍有 1 个 workspace `jyf`（owner=user `3f2d577f…`）
- 用户重启时输入 `jydf` 登录 → `POST /auth/login` 创建一个全新 user
- 全新 user 没有任何 member 记录 → `GET /api/workspaces` 返回空 → GUI 工作区列表为空
- 用户感知："重启后工作区丢了"

## 根因

fork 的 `POST /auth/login` 是 **username-only upsert**：

```go
// server/internal/handler/auth.go (post-localization)
func UsernameLogin(w http.ResponseWriter, r *http.Request) {
    var req struct{ Name string `json:"name"` }
    body.Decode(&req)
    user := db.UpsertUserByName(req.Name)  // 不存在则新建
    token := jwt.Sign(user)
    json.NewEncoder(w).Encode({token, user})
}
```

每次重启桌面 / 换名字登录 = 新 user_id + 零 workspace_member。

历史累积 5 个 orphan user：

```
jyf       3f2d577f-…  06-26 04:17  owner of workspace jyf
alice     a06fd189-…  06-26 04:22  (no member)
testuser  f4314930-…  06-26 04:09  (no member)
jxiao     08231a31-…  06-26 06:49  (no member)
jydf      1899f715-…  06-27 19:31  (current login, no member)
```

## 修复

短期 hot-fix（本次执行）：在 PG 直接给 `jydf` 加 member 关系：

```sql
INSERT INTO member (workspace_id, user_id, role)
VALUES ('283d3de3-…', '1899f715-…', 'member')
ON CONFLICT (workspace_id, user_id) DO NOTHING;
```

立即生效。daemon 30s workspace sync 后 `count=1`。

## 治本（待办）

需要 server handler 改造，**任选其一**：

1. **持久化 user_id 到 desktop config**：登录时如果同名 user 已存在则复用其 ID，不创建新 user。改 `apps/desktop/src/main/auth-flow.ts` + `UsernameLogin` 在 user 存在时直接返回原 token。
2. **single-user auto-bind**：UsernameLogin 检测 user 数量，如果 DB 里只有 1 个 user 就自动给新建的 user 添加 workspace member=owner。
3. **明确的 owner claim 流程**：用户名登录只在没有同名 user 时创建；存在时必须二次确认（防止名字撞车）。

**优先级 P1**：本地单用户场景下反复踩这个坑，等 0.2.94 修。

## 复现

```
1. 启动桌面 0.2.93，输入 "alice" 登录，建一个 workspace
2. 重启桌面，输入 "bob" 登录
3. GUI 工作区列表为空
4. SELECT * FROM "user";  → 多了 bob
5. SELECT * FROM member WHERE user_id = (bob's id); → 0 rows
```

## 检测方法

PG 查询定位：

```sql
-- 列出没有 workspace 关联的 user（孤儿）
SELECT u.id, u.name, u.created_at FROM "user" u
LEFT JOIN member m ON m.user_id = u.id
WHERE m.user_id IS NULL ORDER BY u.created_at DESC;
```

如果数量 > 1 且最新一行 created_at 是最近 7 天 = 触发了这个 bug。

## 数据安全

- 工作区数据从未丢失，DB `workspace` 表完好。
- 用户感知"丢失"实际是"看不见"，通过补 member 关系立即恢复。
- 历史 orphan user 不会自愈，需要手动清理或保留作为审计。