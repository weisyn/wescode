---
name: pr-guardian
version: "1.0.0"
min_engine: "1.0.0"
description: "Keeps PRs merge-ready: resolve conflicts, address review comments, fix CI in a loop. Use when babysitting a PR or fixing CI."
operators:
  - read
  - exec
  - write
  - edit
metadata:
  enabled: true
  tags: [PR, merge, CI, review-comments, conflict]
---

# PR Guardian

A three-phase loop that drives a pull request toward merge-ready state: resolve conflicts, address review comments, fix CI. Repeat until all green or blocked on human input.

## When to Use

- User asks to "babysit this PR" or "make this PR merge-ready".
- User reports CI failures on a PR and wants them fixed.
- User wants review comments addressed automatically.

## When NOT to Use

- The PR has not been created yet (use `git-workflow` to create it first).
- The user wants a code review (use `code-review` skill).

## Protocol

### Phase 1 — Status Check

```
exec(command="gh pr view --json number,state,mergeable,reviewDecision,statusCheckRollup,reviewRequests")
```

Classify the PR state:
- `mergeable: CONFLICTING` → go to Phase 2A (conflicts)
- Unresolved review comments → go to Phase 2B (comments)
- CI checks failing → go to Phase 2C (CI)
- All green + approved → report "PR is merge-ready"

**Gate**: After 3 loop rounds with no progress on any dimension, STOP and report the blocking issues to the user instead of continuing indefinitely.

### Phase 2A — Resolve Conflicts

1. `exec(command="git fetch origin && git rebase origin/main")`
2. If rebase succeeds: `exec(command="git push --force-with-lease")` → return to Phase 1
3. If rebase has conflicts: read conflicted files, resolve automatically if the intent is clear
4. If the conflict involves competing logic changes (intent conflict): abort and report to user

**Gate**: Never silently resolve intent conflicts. Only auto-resolve formatting/import order conflicts.

### Phase 2B — Address Review Comments

1. `exec(command="gh pr view --json comments,reviews")`
2. Filter: skip resolved threads, skip bot comments unless from a review tool
3. For each actionable comment: read the referenced code, make the fix, push
4. After all comments addressed: `exec(command="git push")` → return to Phase 1

**Gate**: If a comment requires a design decision (not just a code fix), ask the user.

### Phase 2C — Fix CI Failures

1. `exec(command="gh pr checks --json name,state,detailsUrl")`
2. For each failing check: read the failure log
3. Only fix failures caused by code in this PR's diff scope — do not modify CI workflow files
4. After fixes: `exec(command="git push")` → return to Phase 1

**Gate**: If a CI failure is unrelated to PR changes (infra/flaky), report it instead of trying to fix.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Auto-resolve logic conflicts during rebase | Only auto-resolve formatting/import conflicts; report intent conflicts to user |
| Blindly trust all review comments | Verify comment validity against the code before acting |
| Modify CI workflow files to make checks pass | Fix the code, not the pipeline |
| Loop indefinitely without progress | After 3 rounds with no improvement, report blocking issues and stop |
