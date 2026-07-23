# Lab-builder (user plugins) API source map

Reference for `multica-lab-builder`. The user-plugin CRUD surface (0.3.60+) is
served by the local backend at `http://localhost:8090`. Auth: `Authorization:
Bearer $MULTICA_API_TOKEN`. Paths below are relative to the repo root.

Re-derive before trusting — line numbers drift. To re-anchor a symbol:

```bash
grep -rn "user-plugins"        server/cmd/server/router.go
grep -rn "func (h \*Handler) CreateUserPlugin" server/internal/handler/
grep -rn "user_" server/internal/handler/user_plugin.go
```

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/experimental-flags` | Survey the full lab landscape: built-in catalog flags **and** user plugins, each with `enabled` state. User plugins carry `is_user_plugin: true` and a `user_` `flag_key`. Read-only; auth same as below. |
| `GET` | `/api/user-plugins` | List all user plugins |
| `POST` | `/api/user-plugins` | Create a plugin |
| `PUT` | `/api/user-plugins/{slug}` | Update a plugin by slug |
| `DELETE` | `/api/user-plugins/{slug}` | Delete a plugin by slug |

## Create / update request body

| Field | Type | Notes |
|---|---|---|
| `slug` | string | Create only. Lowercase alphanumeric + hyphens, 2–64 chars. Stable identity. |
| `title` | `{en, zh}` | Bilingual display name. |
| `description` | `{en, zh}` | Bilingual description. |
| `trigger_mode` | `"auto" \| "issue_select"` | `auto` = background/self-driven, hidden from issue LabPicker; `issue_select` = user picks on an issue. |
| `runtime_kind` | `"none" \| "inline" \| "subprocess"` | `inline` = Multica resources only; `subprocess` = external process w/ health check; `none` = UI-only. |
| `manifest` | object | Free-form. See below. |

Derived field (server-set, never sent by the client):

| Field | Value |
|---|---|
| `flag_key` | `"user_" + slug` |

## Manifest shape (conventions, not hard-enforced)

```json
{
  "capabilities": { "skills": [], "agents": [], "squads": [] },
  "ui": {
    "shell": "standard",
    "tabs": [
      { "key": "chat", "kind": "chat", "label": {"en": "Chat", "zh": "对话"} }
    ]
  }
}
```

- `capabilities.*` — IDs/names of provisioned Multica resources (created via the
  `multica` CLI), relevant for `runtime_kind: "inline"`.
- `ui.tabs[].kind` — one of `chat`, `artifacts`, `table`, `iframe`, `code`.
- `runtime_kind: "subprocess"` manifests describe the binary / health path instead.

## Related contracts

- Plugin-owned agents/squads are hidden from regular pickers via the `lab_managed`
  marker (see `multica-lab-builder/SKILL.md` rules and the `lab_managed` DTO
  contract in the project memory).
- Resource provisioning uses the standard CLI: `multica agent create`,
  `multica skill create`, etc. (see `multica-creating-agents`).
