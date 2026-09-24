---
name: clean-code
version: "1.0.0"
min_engine: "1.0.0"
description: "SOLID, naming, dedup, error handling with minimal diffs. Use when implementing, refactoring, or fixing review findings."
operators:
  - read
  - write
  - exec
metadata:
  enabled: true
  tags: [SOLID, refactoring, naming, error-handling]
---

# Clean Code

Write maintainable code with minimal, reviewable diffs. Guard against unread-before-write, unnecessary abstractions, and silent verification gaps.

## When to Use

- Implementing a new feature or fixing a bug
- Refactoring existing code for clarity
- Addressing code review feedback
- Cleaning up tech debt

## Procedure

1. **Read before write** — understand the calling context:
   - Read the target file end-to-end
   - Identify callers and consumers of the function you're changing
   - Understand the existing naming convention, import order, error handling style
   - Only then plan your change

2. **Minimal diff discipline**:
   - Change only what is necessary to fulfill the request
   - Match existing code style (indentation, naming, import grouping)
   - Do not rename unrelated variables, reformat adjacent code, or reorganize imports
   - One logical change per commit

3. **SOLID priorities** (ordered by impact):
   - **Correctness** > **Single Responsibility** > **Naming** > **Duplication** > **Error Handling**
   - Don't introduce abstractions for single-use cases (wait for 2+ consumers)
   - Extract a helper only when it reduces real duplication, not speculative reuse

4. **Error handling**:
   - Wrap errors with context: `fmt.Errorf("loadUser(%s): %w", id, err)`
   - Never swallow errors silently (`_ = fn()` needs a comment explaining why)
   - Use early returns for error paths; keep the happy path un-indented
   - Match the error handling style of the surrounding code

5. **Naming**:
   - Functions: verb + noun (`LoadUser`, `parseConfig`, `validateInput`)
   - Booleans: `is`/`has`/`can` prefix (`isValid`, `hasPermission`)
   - Avoid abbreviations unless universally understood (`id`, `url`, `ctx`)
   - Name length proportional to scope: short in tight loops, descriptive at package level

6. **Verify after change**:
   - `exec(go build ./...)` or `exec(tsc --noEmit)` after every modification
   - Run existing tests that cover the changed path
   - Zero compilation errors and zero new test failures before delivering

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Edit a file without reading it first | Read the file + callers, understand context, then edit |
| Produce a large diff when a small one suffices | Targeted fixes; keep the diff reviewable |
| Deliver without running verification | Build/typecheck after every change |
| Rewrite entire functions for cosmetic improvements | Surgical edits; cosmetics in a separate commit |
| Introduce abstractions for single-use cases | Abstractions must justify themselves with 2+ consumers |
| Swallow errors with `_ = fn()` | Handle or propagate; if intentionally ignored, add a comment |
| Mix functional changes with formatting changes | One logical concern per diff |

## Verification

- [ ] Target file was read before any edit
- [ ] Diff contains only lines traceable to the request
- [ ] `go build ./...` / `tsc --noEmit` passes with zero errors
- [ ] Existing tests still pass
- [ ] No new abstractions without 2+ consumers
