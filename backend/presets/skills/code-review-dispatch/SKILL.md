---
name: code-review-dispatch
version: "1.0.0"
min_engine: "1.0.0"
description: "Slash-triggered deterministic code review pipeline. Use only when the user explicitly types /code-review-dispatch."
operators:
  - read
  - exec
metadata:
  enabled: true
  execution_mode: guided
  tags: [code-review, dispatch, slash]
  disable_model_invocation: true
---

# Code Review Dispatch

Deterministic entry point for code review via `/code-review-dispatch`. A single slash command triggers a complete, structured review workflow without requiring LLM routing decisions.

## When to Use

- User types `/code-review-dispatch` to trigger an immediate structured code review.
- User wants a quick, no-questions-asked review of current changes.

## When NOT to Use

- User asks conversationally about code quality → use `code-review` directly.
- User wants to fix code → use `clean-code`.

## Execution

This is a deterministic pipeline — follow every step without deviation.

### Step 1: Detect Changes

```bash
exec git status --porcelain
```

Determine the review scope:
- **If uncommitted changes exist**: Review the working tree diff (`git diff` + `git diff --cached`).
- **If on a feature branch**: Review branch diff against main (`git diff main...HEAD`).
- **If neither**: Ask the user what to review and stop.

### Step 2: Collect the Diff

Based on Step 1:
```bash
exec git diff main...HEAD
```
(or the appropriate variant)

### Step 3: Identify Changed Files

```bash
exec git diff --name-only main...HEAD
```

### Step 4: Execute Full Review

For each changed file:
1. Read the complete file (not just diff) for context.
2. Apply the review checklist:
   - Correctness: logic errors, unhandled edges, resource leaks.
   - Security: injection, leaked credentials, missing auth.
   - Maintainability: naming, SRP, duplication.
   - Performance: N+1, unnecessary allocations.

### Step 5: Output Structured Report

```markdown
## Code Review Report

**Scope**: `<branch>` vs `main` | <N> files changed

### Findings

| # | Severity | File:Line | Issue | Fix |
|---|----------|-----------|-------|-----|
| 1 | Must Fix | ... | ... | ... |
| 2 | Should Fix | ... | ... | ... |
| 3 | Nice to Have | ... | ... | ... |

### Positive Observations
- ...

### Summary
- Files reviewed: N
- Must Fix: N | Should Fix: N | Nice to Have: N
```

### Step 6: Completion

Report is complete. Do NOT modify any files. The review is read-only.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Modify any file during the review | This is a read-only pipeline — report findings, never fix them |
| Review only diff lines without reading full file context | Step 4 requires full-file reads for each changed file |
| Skip files because "the diff is small" | Every changed file must be reviewed regardless of diff size |
| Run the pipeline when there are no changes | Step 1 Gate: if no diff scope exists, STOP and ask the user |

## Design Note

This skill uses the **slash-only activation** pattern (`disable_model_invocation: true`).
For conversational code review, use `code-review` instead.
