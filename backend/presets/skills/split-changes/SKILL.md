---
name: split-changes
version: "1.0.0"
min_engine: "1.0.0"
description: "Split large changes into small reviewable PRs. Use when splitting a branch or breaking up an oversized PR."
operators:
  - exec
  - memory
metadata:
  enabled: true
  tags: [PR, split, review, git, branch]
---

# Split Changes

Split accumulated changes into multiple focused, reviewable PRs. Each PR should be independently mergeable and pass CI.

## When to Use

- User has a branch with many unrelated changes and wants to split into multiple PRs.
- User asks to "break this up" or "split into smaller PRs".
- A code review requests splitting a large PR.

## When NOT to Use

- Changes are already small and focused (just create a single PR).
- User wants to rewrite history interactively (use `git-workflow` with rebase route).

## Protocol

### Phase 1 — Analyze Changes

1. `exec(command="git diff --stat origin/main")` — overview of changed files and scope
2. `exec(command="git log --oneline origin/main..HEAD")` — commits in scope
3. Group changes by module, concern, or dependency order
4. Create a split proposal with `memory(action="note")`:

```
memory(action="note", content="Split proposal (3 PRs):
  PR 1: internal/auth/ (2 files) — auth token refresh fix
  PR 2: internal/handler/ (3 files) — new endpoint + tests
  PR 3: web/src/ (4 files) — UI updates for new endpoint
Order: PR 1 first (no deps), PR 2 depends on PR 1, PR 3 depends on PR 2", tags=["split-plan"])
```

### Phase 2 — User Approval

Present the split plan to the user. Wait for explicit approval before proceeding.

**Gate**: Do NOT create any branches or PRs without user approval of the split plan.

### Phase 3 — Execute Split

For each slice in the approved plan:

1. `exec(command="git stash create")` — create a backup ref
2. Create a new branch: `exec(command="git checkout -b <slice-branch> origin/main")`
3. Cherry-pick or checkout the relevant files: `exec(command="git checkout <source-branch> -- <file1> <file2>")`
4. Commit with a descriptive message
5. Push: `exec(command="git push -u origin <slice-branch>")`
6. Create PR: `exec(command="gh pr create --title '...' --body '...' --base main")`
7. Return to the source branch for the next slice

**Gate**: Each slice MUST compile (`exec(go build ./...)` or equivalent) and pass tests before pushing. Do not push a broken slice.

### Phase 4 — Report

After all PRs are created, report:

```
Split complete:
  PR #101: auth token refresh (internal/auth/) — https://github.com/.../101
  PR #102: new endpoint (internal/handler/) — https://github.com/.../102
  PR #103: UI updates (web/src/) — https://github.com/.../103
Merge order: #101 → #102 → #103
Backup ref: stash@{0}
```

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Create branches before user approves the split plan | Present plan first, execute only after approval (Phase 2 Gate) |
| Use `git add .` when staging slice files | Stage specific files for each slice |
| Create dependent PRs without noting merge order | Document merge order in each PR body and in the final report |
| Forget to create a backup before splitting | `git stash create` before any destructive operation |
| Create a PR that doesn't pass CI independently | Each slice must compile and pass tests on its own |
