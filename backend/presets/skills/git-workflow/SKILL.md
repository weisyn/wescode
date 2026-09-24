---
name: git-workflow
version: "1.0.0"
min_engine: "1.0.0"
description: "Feature branch workflow: branch, commit, verify, merge. Guards unsafe ops and enforces conventional commits."
operators:
  - read
  - exec
  - edit
  - write
metadata:
  enabled: true
  tags: [Git, branch, commit, merge, workflow]
---

# Git Workflow

Structured guide for feature branch development. Follow the workflow phases in order.

## Feature Branch Workflow

### Phase 1: Branch Setup

```
1. Check current status:           exec(git status)
2. Ensure clean working tree:      exec(git stash) if dirty
3. Fetch latest:                   exec(git fetch origin)
4. Create feature branch:          exec(git checkout -b feat/description origin/main)
```

### Phase 2: Implementation

```
1. Make changes using edit/write tools
2. After each logical unit of work:
   a. Verify:  exec(go build ./...) or equivalent
   b. Stage:   exec(git add <specific-files>)  — never git add .
   c. Commit:  exec(git commit -m "type(scope): description")
```

### Phase 3: Pre-merge Verification

```
1. Run full test suite:    exec(go test ./...)
2. Run linter:             exec(go vet ./...)
3. Check diff:             exec(git diff main..HEAD --stat)
4. Review commit history:  exec(git log --oneline main..HEAD)
```

### Phase 4: Merge (user-initiated)

The Agent does NOT push or create PRs autonomously. Instead:
- Summarize the changes and commit history
- Ask the user if they want to push and create a PR
- The user executes push/PR manually or gives explicit permission

## Commit Convention

Format: `type(scope): description`

| type | Use when |
|------|----------|
| `feat` | New feature |
| `fix` | Bug fix |
| `refactor` | Code restructuring (no behavior change) |
| `test` | Adding or fixing tests |
| `docs` | Documentation only |
| `style` | Formatting, no code change |
| `chore` | Build, tooling, dependencies |

## Safety Rules

| Operation | Classification | Agent Behavior |
|-----------|---------------|----------------|
| `git add`, `git commit` | Safe (Routine) | Execute without asking |
| `git branch`, `git checkout` | Safe (Routine) | Execute without asking |
| `git push` | Routine | Execute if user has given task-level autonomy; otherwise ask |
| `git push --force` | Escalated | ALWAYS ask user first |
| `git reset --hard` | Escalated | ALWAYS ask user first |
| `git rebase -i` | Escalated | ALWAYS ask user first |

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| `git add .` without reviewing | `git status` first, then `git add <specific-files>` |
| Commit without build passing | Verify with `go build ./...` / `npm run build` first |
| Rebase without fetching | `git fetch origin` then `git rebase origin/main` |
| Force push to main/master | Only force-push to personal feature branches |
| Vague commit messages | Conventional: `fix(auth): handle expired token refresh` |
| Push without user confirmation | Summarize changes, ask user to confirm push |
| Multiple concerns in one commit | One logical change per commit |
