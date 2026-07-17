---
name: multica-llm-wiki
description: "Use when the agent needs to read from or write to the user's local LLM Wiki knowledge base at /Applications/LLM Wiki.app. Reads call the LLM Wiki desktop API on 127.0.0.1:19828 (the same surface the bundled llm-wiki MCP server exposes as `llm_wiki_search` / `llm_wiki_read_file` / `llm_wiki_graph` / `llm_wiki_files` / `llm_wiki_status`). Writes drop files into ~/Documents/llm wiki/{wiki, skills, agents, squads, sources}/ for the user to manually re-vectorise. The bridge is gated behind the `llm_wiki_bridge` Labs flag — when the flag is off, this Skill refuses and falls back to in-band Multica comments. Do NOT use for issue / chat / runtime operations; that is what the other Skills cover."
user-invocable: true
allowed-tools: Bash(multica *), Bash(git *)
---

# LLM Wiki Local Bridge (0.3.19+)

Bridges Multica agents to `/Applications/LLM Wiki.app`. The
desktop app exposes a vector + keyword search over a user-managed
knowledge vault; the bridge surfaces five verbs the Skill calls,
plus two write verbs for the agent to add notes.

## Hard rule — flag check before any call

Before issuing any verb, **check the flag**:

```
multica experimental flags list 2>/dev/null
```

If `llm_wiki_bridge` is `false` (or the flag is missing), **refuse**:

> LLM Wiki 桥接未启用。请在 Settings → Labs 打开「LLM Wiki Local
> Bridge」开关。关闭后请按一般 Multica 评论的方式继续。

Do NOT fall back to reading files directly with
`Bash(ls ~/Documents/llm wiki/...)` — that's the on-disk execution
path 0.3.18's safety net was designed to keep out of the labs.

## Step 1 — probe the bridge

```sh
multica experimental llm-wiki status --output json
```

Three outcomes:

- `{"ok": true, "health": {...}}` — desktop app is online
- `{"ok": false, "reason": "..."}` — desktop app offline; ask the
  user to launch `/Applications/LLM Wiki.app`
- 403 with `llm_wiki_bridge` — flag is off (see the rule above)

If desktop app is offline, stop here and surface the diagnostic to
the user. Do NOT continue; reads return empty and writes land in
files the desktop app cannot pick up until it is running.

## Step 2 — read verbs

```sh
multica experimental llm-wiki projects --output json         # list known + active project
multica experimental llm-wiki files --root wiki --recursive  # tree dump
multica experimental llm-wiki read --path wiki/biology.md    # single text file
multica experimental llm-wiki search \
  --query "细胞分裂期 G2 期检验点蛋白" --top-k 8 \
  --include-content --output json                            # vector + keyword
multica experimental llm-wiki graph --q protein --limit 50   # knowledge graph
```

The response shapes mirror the LLM Wiki desktop API JSON. The
vector + keyword search returns hits ordered by descending
relevance; prefer `include_content=true` only for small result
sets because the response balloons quickly.

## Step 3 — write verbs (file drop only)

Writes drop files into `~/Documents/llm wiki/<vaultPath>`:

```sh
multica experimental llm-wiki write \
  --path sources/experiment-2026-07-14.md \
  --content-file /tmp/note.md \
  --output json
```

Constraints:

- `path` is relative to the vault root. Must not contain `..`.
- `--content-file` reads bytes from disk; pass `-` for stdin.
- The desktop app's Source Watch picks the file up on the next
  rescan. The agent **must not** assume the index sees the file
  immediately; surface a "需手动向量化" hint to the user.

There is **no delete verb in 0.3.19** — the desktop API does not
expose a write-side delete yet. Drop a `.tombstone.md` marker
file and ask the user to remove the original via the desktop
app's UI.

## Step 4 — narration back to the issue

After each verb that returned useful data, post the relevant
excerpt to the issue thread as a Multica comment so the user can
audit the research chain without leaving Multica:

```sh
multica issue comment \
  --slug "$WORKSPACE_SLUG" \
  --issue "$ISSUE_ID" \
  --body "## LLM Wiki 查询: <query>

\$FIRST_3_HITS

来源: llm_wiki_search (top_k=$TOPK)" \
  --output json
```

For writes, include the absolute path the bridge wrote to so the
user can find the file:

```sh
multica issue comment \
  --slug "$WORKSPACE_SLUG" \
  --issue "$ISSUE_ID" \
  --body "## LLM Wiki 写入

- 路径: $ABS_PATH
- 字节: $BYTES
- 请手动触发 LLM Wiki 桌面端的 Sources Rescan 以加入向量索引" \
  --output json
```

## Hard rules

- **Always check the flag first.**
- **Never write outside the vault root.** `..`-bearing paths
  return 400; do not try to bypass.
- **Never assume the index is fresh after a write.** The user
  vectorises; the bridge does not.
- **Never loop forever.** Vector search is bounded by the
  upstream topK; the Skill should not retry with broad queries
  when the first search returned enough hits.
- **Do not re-implement the bridge.** The MCP-style surface is
  the bus; the Go handler in
  `server/internal/handler/llm_wiki_bridge.go` is the executor.

## Where to look

- Catalog flag: `llm_wiki_bridge` (server/internal/experimental/catalog.go)
- HTTP routes: `server/internal/handler/llm_wiki_bridge.go`
- HTTP client: `server/internal/llmwiki/client.go`
- Vault writer: `server/internal/llmwiki/writer.go`
- Desktop app: `/Applications/LLM Wiki.app`
- Vault dir: `/Users/jiangjianyan/Documents/llm wiki/{wiki, skills, agents, squads, sources}/`
- Desktop API port: `127.0.0.1:19828/api/v1/` (fallback: 19827)
- 0.3.19 platform blueprint: `.omc/plans/multica-labs-platform-blueprint-0.3.19.html`
  (P5 "Skill 适配器" — this Skill is the bridge Adapter)
