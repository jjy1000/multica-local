---
name: ai-research-writing-pack
description: Curated collection of research-writing prompts distilled from the open-source `awesome-ai-research-writing` repository (Leey21). Use when the user asks for help drafting, polishing, translating, de-AI-ing, or reviewer-checking academic prose in either Chinese or English. Triggers include 论文润色, 学术翻译, AI 味, 审稿人视角, logical flow check, methodology writing, LaTeX paper workflow. This skill is the lab-only prompt library — it is hidden from Multica's main picker via experimental_resource_visibility and only available to claude_science_lab agents (research / ml / write / biology / physics).
category: research-prompts
allowed-tools: [Read, Write, Edit, Bash]
---

# AI Research Writing — Prompt Pack

A condensed reference of the prompts that ship with the `awesome-ai-research-writing`
repo (Leey21), rewritten so each pattern is one paragraph of instruction plus one
example transformation. The lab's `research` leader agent invokes this skill when
the issue title matches the patterns below; `write` uses it for cross-language
drafting.

## When to Use

- 论文 / 学位论文 / 会议论文 任何章节的**中文→英文**或**英文→中文**翻译
- 把口语化研究笔记改成 IMRAD / Nature / IEEE / Vancouver 风格的段落
- 去掉 AI 味的"安全改写"——重写那种一眼像 ChatGPT 写的段落
- 用审稿人视角倒读一遍 user 的 introduction / discussion
- 论文逻辑链检查(claim → evidence → limitation 三段对得上吗)

## Prompt Patterns

### 1. 中英翻译 (zh → en academic)

```
You are a senior researcher translating my Chinese draft into a publication-ready
English paragraph. Preserve every technical term, do NOT paraphrase away the
specificity, and write in the style of Nature Methods / Bioinformatics author
guidelines. Output only the translated paragraph — no preamble.
```

### 2. 英文润色 (en → en, +academic)

```
Rewrite the following paragraph for an academic journal. Tighten hedging,
replace generic verbs with field-specific ones, vary sentence length, and
break any sentence that carries two claims into two. Keep the original
citations. Do NOT change the technical content.
```

### 3. 去 AI 味 (de-AI rewrite)

```
The following paragraph reads as ChatGPT output. Identify the top 5
tells (parallelism, hedge-stacking, "It is important to note",
"Furthermore" chains, generic conclusions). Rewrite removing those tells
while preserving the technical claim set.
```

### 4. 审稿人视角 (reviewer-mode critique)

```
Read the following manuscript section as Reviewer 2. List (a) the strongest
claim that is NOT supported by the presented evidence, (b) the method
detail most likely to be questioned, (c) the result most likely to be
misinterpreted, and (d) one experiment that, if added, would resolve the
biggest concern. Be specific; cite paragraph numbers.
```

### 5. 逻辑链检查 (claim → evidence → limitation)

```
For each claim in the following paragraph, output a 3-tuple:
[claim text] → [evidence paragraph index] → [stated limitation].
Highlight any tuple where the evidence paragraph does not actually back
the claim. Use IMRAD conventions.
```

### 6. LaTeX 双链编译 (LaTeX source → translated LaTeX)

```
Translate the following LaTeX source from English to Chinese (or vice
versa). Preserve every command, every environment, every cite key, and
every label. Translate only the text between commands. Compile-safe —
do not introduce UTF-8 characters that break pdflatex.
```

### 7. arxiv-translator-skill (LaTeX → PDF end-to-end)

This is the long-running version of pattern 6. The agent takes a .tex
file path, translates content in place, recompiles, and returns the
final PDF. See the `arxiv-translator-skill` sibling for the orchestration
detail.

## Lab Contract

This skill is part of `claude_science_lab`. It is loaded by the
`research` leader agent and the `write` agent when an issue matches
the routing rules in `RESEARCH_WRITING_TRIGGERS` (see
`apps/desktop/vendor/claude-science-skills/research-prompts/ai-research-writing-pack.json`).
The skill body MUST stay under 50 KB so the agent context window
isn't blown — split long-tail prompts into sibling skill files
(`ai-research-writing-pack-translation.md`,
`ai-research-writing-pack-reviewer.md`) when the prompt library grows.

## Failure Modes the Lab Lead Should Watch

- A skill call that returns the **same text** unchanged = the user
  already wrote it well; don't rewrite for the sake of rewriting.
- A translation that **adds claims** not in the source = a hallucination;
  report it to the user, do NOT silently keep it.
- A reviewer-mode critique that **invents flaws** = flag and drop; the
  user wants real weaknesses, not a confidence game.

## Related Skills

- `scientific-writing` (writing/) — full IMRAD drafting workflow
- `literature-review` (writing/) — systematic review pipeline
- `peer-review` (research/) — peer-review-specific checks (related but distinct)