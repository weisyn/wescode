---
name: wescode-execution
version: "1.0.0"
min_engine: "1.0.0"
description: "Language-specific verify after edits: go build/test, pytest, tsc. Complements engine agentic-execution."
operators:
  - exec
  - read
depends_on:
  - name: agentic-execution
    co_activate: true
metadata:
  enabled: true
  tags: [execution, verify, wescode]
---

# Wescode Execution

Product increment on engine `agentic-execution`. Engine owns act-first, when to `plan`, and "verify after every change". This skill only names commands this Cell can run with `exec`.

## When to Use

- After `write` / `edit` / `apply_patch` in this workspace.

## When NOT to Use

- Engine already forbids planning for trivial steps — do not restate that here.
- Do not invent a verifier the project does not have. If `go.mod` is absent, do not `go build`.

## Verification (this Cell)

Pick the row that matches files you actually changed. Commands run via `exec`.

| Change | Verify |
|--------|--------|
| Go (`*.go`, `go.mod`) | `exec(command="go build ./...")` then `exec(command="go test ./...")` scoped to the package when the repo is large |
| Python | `exec(command="python3 -m pytest ...")` or `exec(command="python3 <script.py>")` |
| TypeScript / frontend | `exec(command="npx tsc --noEmit")` when a tsconfig exists |
| Shell script | `exec(command="chmod +x script.sh && ./script.sh")` only if the user asked to run it |
| Config / YAML | `read` the file; tool-specific validate only if that binary is on PATH |

Multi-file change: verify once after the batch, not after every file.

`fetch_url` is for the network. Do not download what `exec` can produce locally.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Skip verify because the edit "looks right" | Run the matching row above |
| `go build` in a non-Go workspace | Match the project's real toolchain |
| Copy engine act-first / plan rules into this file | Engine `agentic-execution` already always-on |
