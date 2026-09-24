---
name: autonomous-workflow
version: "1.0.0"
min_engine: "1.0.0"
description: "Unattended verify-fix loop. Use when the user asked you to proceed without pausing on routine build/test failures."
operators:
  - exec
  - edit
  - write
  - read
  - grep
  - glob
metadata:
  enabled: true
  tags: [autonomous, verify, lint, build]
---

# Autonomous Workflow

Self-directed verify-and-fix. Routine build/test failures are yours to close; architecture and irreversible git are not.

## When to Use

- User said to proceed unattended: "just do it", "don't ask", "keep going until it builds".
- A multi-step implementation where pausing on every compile error would stall the task.

## When NOT to Use

- Ordinary coding until the user asked for this loop.
- User did not ask to commit or land — do not `git commit`. Use `git-workflow` when they ask for git.
- Force push, hard reset, drop table, or any irreversible op.

## Core Loop

After every meaningful code change (`edit` / `write` / `apply_patch`):

1. **Build**: `exec` the project's real command (`go build ./...`, `npx tsc --noEmit`, `cargo check`, …). Match files you changed; do not invent a toolchain.
2. **Fix**: If build fails, fix it. Do not report and wait.
3. **Repeat** until build passes, then the next task step.

## Verification Escalation

When the code changes for this task are complete:

1. `exec` static analysis (`go vet ./...` or equivalent)
2. `exec` tests for the affected packages (`go test ./...`, `npm test`, …)
3. If tests fail, diagnose and fix. Ask only when the failure is ambiguous (flake, missing fixture, unclear expected behavior).

## Git

`exec` can run `git commit`. That is not permission.

- User asked to commit / land / "commit when done" → `git-workflow` (conventional message, no `git add .`, no push unless asked).
- User did not → stop after tests pass. Do not commit.

## Decision Authority

| Decision | Action |
|----------|--------|
| Need a new file for this task | Create it |
| Need a dependency the project already uses | Install it |
| Test fails after your edit | Fix it |
| Lint error in code you touched | Fix it |
| Architectural direction unclear | Ask |
| Commit / push / force push / hard reset | Only commit if the user asked to land; push and irreversible ops always ask |

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Ask "should I run the build?" | Run it |
| Report a compile error and wait | Fix it |
| Ask "should I create this file?" | Create it |
| Commit because verification passed | Commit only if the user asked to land |
| Skip verification because "it looks correct" | Run the actual toolchain |
