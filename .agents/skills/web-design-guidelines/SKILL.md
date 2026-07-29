---
name: web-design-guidelines
description: Review UI code for Web Interface Guidelines compliance. Use when asked to "review my UI", "check accessibility", "audit design", "review UX", or "check my site against best practices".
metadata:
  author: vercel
  version: "1.0.0"
  argument-hint: <file-or-pattern>
---

# Web Interface Guidelines

Review files for compliance with Web Interface Guidelines.

## How It Works

1. Read the vendored guidelines from `references/guidelines.md` (in this skill directory)
2. Read the specified files (or prompt user for files/pattern)
3. Check against all rules in the guidelines
4. Output findings in the terse `file:line` format

## Guidelines Source

The rules are vendored locally at `references/guidelines.md`. Always read that file; do NOT fetch guidelines from the network at review time. The review works fully offline.

The vendored copy is pinned to upstream commit `d0a657bfe87e86dd3a4753d7ec28c7e7dd7a88fe` of `vercel-labs/web-interface-guidelines` (`command.md`); provenance is recorded in the header of `references/guidelines.md`.

## Refreshing Vendored Guidelines

Manual refresh only — never done automatically during a review:

1. Find the latest commit touching `command.md`:
   `https://api.github.com/repos/vercel-labs/web-interface-guidelines/commits?path=command.md&per_page=1`
2. Fetch the file at that exact commit hash (never `main`):
   `https://raw.githubusercontent.com/vercel-labs/web-interface-guidelines/<commit-sha>/command.md`
3. Replace the rules content in `references/guidelines.md`, update the pinned commit hash and date in its provenance header, and update the pinned hash mentioned above.

## Usage

When a user provides a file or pattern argument:
1. Read `references/guidelines.md`
2. Read the specified files
3. Apply all rules from the guidelines
4. Output findings using the format specified in the guidelines

If no files specified, ask the user which files to review.

## Output

Report findings as a terse list, one per line, in `file:line — issue (rule)` form:

- `packages/ui/button.tsx:42 — icon button has no accessible label (WCAG 4.1.2)`

Rules:

- Group findings by file; omit files that have no findings.
- Cite the specific guideline each finding violates; do not restate the full guidelines.
- End with a one-line summary of files reviewed and finding counts, or `No issues found` when the code is compliant.

## Validation

Before returning, confirm the review is complete and trustworthy:

- Guidelines were read from `references/guidelines.md` this run (not from memory, not from the network).
- Every file in the requested file/pattern was read and checked.
- Each finding names a concrete `file:line` and the specific rule it breaks.
- The summary line is present and its counts match the listed findings.

If any check fails (e.g. `references/guidelines.md` is missing or a file was unreadable), say so explicitly instead of returning a partial review as if it were complete.
