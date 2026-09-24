---
name: context-calibration
version: "1.0.0"
min_engine: "1.0.0"
description: "Infer user-project relationship from git signals; write L4 notes. Use on first encounter with a workspace."
operators:
  - exec
  - memory
  - read
  - grep
metadata:
  enabled: true
  tags: [calibration, flywheel, memory]
---

# Context Calibration

Bootstrap a durable L4 note about how the current actor relates to this workspace. Model writes cannot target L2 or L3 (`kind` does not change layer). L3 is CognitiveSettlement; L2 is admin.

## When to Use

- First interaction in a workspace, or the user switched projects.
- `memory(action="search", query="user-project relationship")` returns nothing useful.

## When NOT to Use

- A matching L4 note already exists — skip.
- Trivial one-line edit or a syntax question.
- Every turn. This skill is **not** always-on.

## Protocol

### 1. Search first

`memory(action="search", query="user-project relationship")`. If a note already answers "who is this actor here", stop.

### 2. Gather git signals

```
exec(command="git log --format='%an <%ae>' | sort -u | head -20")
exec(command="git log --since='6 months ago' --oneline | wc -l")
exec(command="git log -1 --format='%ci'")
```

Cross-check the current actor (system context) with the committer list. Optional extras — skip if missing:

- `.gitignore` has `*.idea` / `.vscode` → IDE preference
- >3 committers → shared repo
- lockfile mtime older than 6 months → stale deps

### 3. Write L4 only

```
memory(action="note", source="workspace-profile",
  content="Actor is a {role} of {project} ({evidence}). Repo signals: {one sentence}.",
  tags=["user-profile", "context-calibration"])
```

Same `source` again is a no-op. To replace: `memory(action="delete", id)` then note.

Do **not** call `memory(action="save")` claiming L2/L3. `save`/`note` land in L4.

### 4. Low confidence only

If git is empty / uninitialized, ask once in the same reply: daily / occasional / first time. Then note. Do not block the user's task on a separate turn.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| `always: true` / git log every Run | Search memory; calibrate once |
| `memory(save)` as "L3 profile" or "L2 fact" | `memory(note)` L4 |
| Dump raw git log into memory | One-sentence relationship |
| Ask when git already identifies the actor | Passive signals first |
