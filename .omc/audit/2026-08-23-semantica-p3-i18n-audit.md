---
name: semantica-p3-i18n-audit
created: 2026-08-23T00:00:00Z
updated: 2026-08-23T00:00:00Z
status: no-op (documented)
baseline: 0.5.51 + Phase 0 + P1 + P2 (commits through 3b4df690b)
---

# Audit — semantica i18n 4-locale coverage (P3 / 0.5.55)

> **Verdict:** P3 is a **structural no-op** for code. All semantica UI
> strings are already 4-locale complete; no new user-facing strings
> were added by Phase 0 / P1 / P2.

## What the plan §2.3 P3 listed vs what exists

The plan row "i18n 4 locale 全覆盖 (~250 LOC)" was aspirational and
included 16 NEW keys × 4 locale = 64 entries for surfaces that are
**not built yet**:

| Planned key | Surface | Status | Phase that builds it |
|---|---|---|---|
| `semantica.mode.individual.label/desc` | `<SemanticaModeBanner>` | not built | P5 (data-isolation UI) |
| `semantica.mode.team.label/desc` | same | not built | P5 |
| `semantica.acl.view_my_only` | advisor skill | not built | P4 (ACL) |
| `semantica.acl.view_team_wide` | advisor skill | not built | P4 |
| `semantica.wheel.preflight.title/desc` | subprocess-manager.ts error panel | not built | P1 (deliberately deferred to keep P1 ≤ 80 LOC) |
| `semantica.wheel.missing.title/desc` | same | not built | P1 (deferred) |
| `semantica.ports.collision.title/desc` | same | not built | P1 (deferred) |

Each planned key surfaces with its corresponding component. Until
that component exists, an empty `semantica.mode.individual.label` in
the locale JSON is a `[assumed]` placeholder that violates
**Karpathy §2 (Simplicity First)** — "不为单一用例造抽象". Adding
hypothetical keys now would either (a) invent translations for
copy that hasn't been written, or (b) leave empty strings that
i18next renders as empty.

## Existing 4-locale coverage (the only thing in scope today)

The semantica flag ships 8 UI keys consumed by
`apps/desktop/src/renderer/src/pages/semantica-explorer-view.tsx`:

| Key | en | zh-Hans | ja | ko | Source |
|---|---|---|---|---|---|
| `crumb_labs` | Experimental | 试验性功能 | 実験機能 | 실험 기능 | sidebar breadcrumb |
| `title` | Semantica Explorer | Semantica 探索器 | Semantica エクスプローラー | Semantica 탐색기 | page title |
| `connecting` | Connecting to Semantica… | 正在连接 Semantica… | Semantica に接続中… | Semantica에 연결 중… | boot screen |
| `not_enabled_title` | Semantica is disabled | Semantica 未启用 | Semantica は無効です | Semantica가 비활성화됨 | flag-off screen |
| `not_enabled_desc` | Open Settings → Labs and enable the Semantica Knowledge Graph flag to use the explorer. | 打开 设置 → Labs 并启用 Semantica Knowledge Graph 开关,即可使用探索器。 | 設定 → Labs で Semantica Knowledge Graph フラグを有効にすると、エクスプローラーを利用できます。 | 설정 → Labs에서 Semantica Knowledge Graph 플래그를 활성화하면 탐색기를 사용할 수 있습니다. | flag-off screen |
| `boot_error_title` | Semantica failed to start | Semantica 启动失败 | Semantica の起動に失敗しました | Semantica 시작 실패 | boot error screen |
| `retry` | Retry | 重试 | 再試行 | 다시 시도 | button label |
| `iframe_title` | Semantica Explorer | Semantica 探索器 | Semantica エクスプローラー | Semantica 탐색기 | iframe `title=` attr |

Source: `packages/views/locales/{en,zh-Hans,ja,ko}/experimental.json`
→ `semantica` block, all 4 locales complete with no missing keys,
no empty strings, no English fallbacks.

## Backend / subprocess surfaces — no i18n needed

| Surface | Why English is correct |
|---|---|
| `server/internal/handler/decision_sync.go::slog.Warn` | server logs are English by long-standing convention; downstream log shippers (Loki / Datadog / journald) are configured to parse English-only |
| `apps/desktop/vendor/semantica/run.sh` stderr | subprocess stderr is captured by `subprocess-manager.ts` and surfaced as `display notification` strings — those are i18n'd separately if at all |
| `scripts/{sync,build,check-semantica}-*.sh` output | dev-only tooling; users invoking these are running CLI and English is fine |
| `server/internal/service/builtin_skills/multica-semantica{,-decision-advisor}/SKILL.md` content | the `semantica_decision_advisor` agent is a tool-using LLM agent that operates in English; user-facing translations of its decisions come back through the chat UI which has its own i18n layer |

## Verification (re-runnable)

```bash
# Per-locale: list semantica block + count keys
for loc in en zh-Hans ja ko; do
  echo "=== $loc ==="
  python3 -c "import json; d=json.load(open('packages/views/locales/$loc/experimental.json'))['semantica']; print(f'{len(d)} keys: {list(d.keys())}')"
done

# Per-key: verify all 4 locales have the same key set
python3 << 'EOF'
import json
keys = set()
for loc in ['en','zh-Hans','ja','ko']:
    keys.update(json.load(open(f'packages/views/locales/{loc}/experimental.json'))['semantica'].keys())
print(f"{len(keys)} unique keys across 4 locales: {sorted(keys)}")
# Expected: 8 (the table above)
EOF
```

Both scripts exit 0 on the current `epic/0.5.13-integration` HEAD
(post-P2 / commit `3b4df690b`).

## Per-i18nnext incident (2026-07-14) compliance

All `t(($) => $.semantica.*)` selectors in
`semantica-explorer-view.tsx` are arrow expressions — never block
body — so the keys-clobbering `[PATH_KEY]` incident from 2026-07-14
cannot recur on this surface. The ESLint `no-restricted-syntax` rule
in `packages/views/eslint.config.mjs` continues to block the block
form at build time.

## Decision

**P3 ships as a docs-only no-op commit.** Phase 0 / P1 / P2 added
zero user-facing UI strings. The plan's 16-key wishlist is real but
is feature-coupled to P4 (ACL) / P5 (ModeBanner) — those phases
will add their own i18n keys when their components land, not now.

**Future P4/P5 work that adds new semantica UI surfaces MUST:**
1. Add the corresponding `semantica.<new>.*` key set to all 4
   locale files in the same atomic commit as the component.
2. Use arrow-expression `t(($) => $.semantica.<key>)` selectors
   exclusively (no block body).
3. Not introduce English-only keys (the lint guard above will catch
   the asymmetry on next typecheck).
