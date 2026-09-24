---
name: code-review
version: "1.0.0"
min_engine: "1.0.0"
description: "Review diffs for correctness, security, maintainability with graded findings. Use when reviewing PRs or code changes."
operators:
  - read
  - exec
capabilities:
  - cap.reviewer.methodology
metadata:
  enabled: true
  execution_mode: guided
  tags: [code-review, defect-detection, quality, best-practices]
---

# Code Review

Systematically find defects and improvement opportunities in code changes, producing actionable graded feedback.

## When to Use

- Reviewing a PR, diff, or set of changed files.
- Checking code against project conventions.
- Hunting potential bugs, security issues, or performance problems.
- For a quick, no-questions-asked deterministic review pipeline, use `/code-review-dispatch` instead.

## When NOT to Use

- Need to actually fix the code → use `clean-code`.
- Need to run tests for verification → use `test-engineering`.

## Execution

Follow these steps strictly in order.

### Step 1: Identify the Scope

Determine what to review:

- **If a git diff is available**: Run `exec git diff HEAD~1` (or the appropriate range) to get the changeset.
- **If specific files are named**: Read those files directly.
- **If reviewing a PR**: Run `exec git log --oneline main..HEAD` to understand the commit scope, then `exec git diff main...HEAD` for the full diff.

### Step 2: Per-file Review

For each changed file, read the full file (not just the diff) to understand context. Check against these dimensions in order:

1. **Correctness** — Logic errors, unhandled edge cases, resource leaks, race conditions.
2. **Security** — Injection vectors, unvalidated input, leaked credentials, missing auth checks.
3. **Maintainability** — Naming clarity, single responsibility, duplication, test coverage.
4. **Performance** — N+1 queries, unnecessary allocations, missing pagination.

### Step 3: Produce the Review Report

Output findings in this exact format:

```
## Review Summary

| # | Level | File:Line | Issue | Suggestion |
|---|-------|-----------|-------|------------|
| 1 | Must Fix | path/to/file.go:42 | ... | ... |
| 2 | Should Fix | ... | ... | ... |
| 3 | Nice to Have | ... | ... | ... |

## Positive Observations
- <good practice worth noting>
```

Severity levels:
- **Must Fix** — Blocks merge: bug, security hole, data loss risk.
- **Should Fix** — Should address in this change: hidden tech debt, potential issue.
- **Nice to Have** — Optional improvement, can be a follow-up.

### Step 4: Verify Completeness

Before finishing, self-check:
- Every finding has a concrete suggestion (not just "this is bad").
- Severity is calibrated (not everything is Must Fix).
- At least one positive observation is included.
- The review covers ALL changed files, not just the first one.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Modify code during a review | Review produces a report only — never edit files |
| Review only the diff lines, ignoring surrounding context | Read the full file to understand the change in context |
| Mark everything as Must Fix | Calibrate severity: Must Fix = blocks merge, Should Fix = tech debt, Nice to Have = optional |
| Produce findings without file:line references | Every finding must cite exact file and line number |
| Skip positive observations | Include at least one thing done well — balanced reviews are more effective |

## House Rules

1. **Review, don't modify**: Never edit code in review mode. Only produce the report.
2. **Specific over vague**: Each finding references exact file and line.
3. **Assume good intent**: Explain *why* something is a problem, not just *that* it is.
4. **Scope discipline**: Only review the diff. Don't audit the entire codebase.
