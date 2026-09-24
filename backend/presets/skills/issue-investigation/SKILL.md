---
name: issue-investigation
version: "1.0.0"
min_engine: "1.0.0"
description: "Wescode bug hunt: map editor/webview/backend layers; use CKG tools on top of engine investigation."
operators:
  - search_symbols
  - find_references
  - find_callers
  - read
  - grep
  - glob
  - exec
  - memory
depends_on:
  - name: investigation
    co_activate: true
metadata:
  enabled: true
  tags: [debugging, investigation, wescode]
---

# Issue Investigation (wescode)

Product increment on engine `investigation`. Engine owns the six phases, coverage declaration, and `memory(note)` + `evidence_refs`. This skill only adds this product's layers and graph tools.

## When to Use

- User reports broken / slow / unexpected behavior in this workspace.

## When NOT to Use

- Feature request — nothing is broken.
- Code review — `code-review`.
- General exploration — engine `agentic-retrieval` + `wescode-retrieval`.

## Layers (this product)

Do not assume the symptom lives in one tree. Typical wescode span:

```
Editor extension:  editor/src/vs/workbench/contrib/wescode/
Chat webview:      web/src/
Go backend:        backend/internal/
```

Other workspaces: read `AGENTS.md` / `design/` / `README.md` and list **this** project's layers the same way. Engine Phase 2 is the generic version of this step.

## Graph tools on the engine protocol

- Phase 3 (broad discovery): `search_symbols` for types/funcs; `grep` for visible labels and RPC names. Do not pass `path` on the first round.
- Phase 5 (chain trace) in this product is often:

```
UI render → handler → JSON-RPC / HTTP → Go backend → response → UI update
```

Use `find_references` (known file + **0-based** line) or `find_callers(symbol)` for structural edges; `grep` for stringly RPC method names. If graph tools are absent from the schema, `grep` / `read` only.

Notes stay L4: `memory(action="note", source="path", content="...", evidence_refs=[...])`. Same `source` is a no-op; delete first to replace.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Search only `backend/` because the bug "feels server-side" | Name all layers; search the ones not yet covered |
| Restate engine's six-phase tables here | Engine `investigation` already co-activates |
| Call `find_references` without file:line | `search_symbols` / `find_callers` / `grep` |
| Write L2/L3 | Model notes are L4 |
