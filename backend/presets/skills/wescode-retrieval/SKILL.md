---
name: wescode-retrieval
version: "1.0.0"
min_engine: "1.0.0"
description: "CKG/LSP retrieval on top of engine drill-down: project_map, search_symbols, find_references, find_callers."
operators:
  - project_map
  - search_symbols
  - find_references
  - find_callers
  - impact_analysis
  - read
  - grep
  - glob
  - memory
depends_on:
  - name: agentic-retrieval
    co_activate: true
metadata:
  enabled: true
  tags: [retrieval, ckg, lsp, wescode]
---

# Wescode Retrieval

Product increment on engine `agentic-retrieval`. Engine owns three-tier drill-down, coverage declaration, `summarize` via `tool_search`, and `memory(note)` without extra fields. This skill only adds tools this Cell actually has.

## When to Use

- Exploring a codebase in this workspace (CKG index / LSP may be available).
- Tracing callers, definitions, or change impact.

## When NOT to Use

- Exact file:line already known — `read` it.
- Graph tools missing from the schema (LSP down / index empty) — fall back to engine `grep` / `glob` / `read`. Do not invent calls.

## Product tools (this Cell)

These tools are wescode `codeintel`. They are **Configurable** (`domain=graph`): they appear only when the backend is up. If a name is not in the tool list, do not call it.

| Tool | Use for | Do not use for |
|------|---------|----------------|
| `project_map` | Unfamiliar layout. `project_map(depth=2)` or `project_map(focus="internal/auth")` | Looking up one symbol by name → `search_symbols` |
| `search_symbols` | Functions / types / interfaces by name | Text in comments or strings → `grep` |
| `find_references` | Symbol at a **known** `path` + **0-based** `line`/`column`. `kind`: `references` (default) / `definition` / `callers` | Name-only lookup → `search_symbols` or `find_callers` |
| `find_callers` | Who calls `symbol` (supports `Type.Method`) | Full transitive impact → `impact_analysis` |
| `impact_analysis` | Downstream fan-out of a change | A single caller list → `find_callers` |

## Placement in the engine tiers

- Before engine Tier 1, if the layout is unknown: `project_map`.
- Engine Tier 1 (`grep` / `glob`): add `search_symbols` for structural names.
- Engine Tier 2 (narrow `grep`): add `find_references` / `find_callers` instead of grepping callers.
- Engine Tier 3 (`read` + `memory(note)`): after `search_symbols` hits a function, `read(source, segment="FuncName")`. Notes: `memory(action="note", source="path", content="...")` — no `segments` field. Same `source` again is a no-op; `memory(action="delete", id)` first to replace.

## Wiring trace

When you see `if h.field != nil`, `Set*()`, or `With*()`:

1. `search_symbols` the setter, then `find_references(kind="references")` on that file:line (0-based).
2. Note the chain with `memory(action="note")`. Conditional injection is not "always returns Y".

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Call `find_references` with a symbol name and no file position | `search_symbols` or `find_callers` |
| Treat `line` as 1-based | 0-based, per schema |
| `grep` all callers of a function when `find_callers` is in schema | `find_callers` |
| Call graph tools that are not in this turn's schema | `grep` / `glob` / `read` |
| `memory(note, segments=[...])` | Schema has no `segments` |
| Teach `summarize(...)` here | Engine skill: `tool_search` first |
