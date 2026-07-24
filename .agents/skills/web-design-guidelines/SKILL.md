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

1. Fetch the latest guidelines from the source URL below
2. Read the specified files (or prompt user for files/pattern)
3. Check against all rules in the fetched guidelines
4. Output findings in the terse `file:line` format

## Guidelines Source

Fetch fresh guidelines before each review:

```
https://raw.githubusercontent.com/vercel-labs/web-interface-guidelines/main/command.md
```

Use WebFetch to retrieve the latest rules. The fetched content contains all the rules and output format instructions.

## Usage

When a user provides a file or pattern argument:
1. Fetch guidelines from the source URL above
2. Read the specified files
3. Apply all rules from the fetched guidelines
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

- Guidelines were fetched fresh from the source URL this run (not from memory).
- Every file in the requested file/pattern was read and checked.
- Each finding names a concrete `file:line` and the specific rule it breaks.
- The summary line is present and its counts match the listed findings.

If any check fails (e.g. the fetch failed or a file was unreadable), say so explicitly instead of returning a partial review as if it were complete.
