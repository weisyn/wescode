---
name: documentation-engineering
version: "1.0.0"
min_engine: "1.0.0"
description: "Documentation methodology: audience analysis, API surface extraction, runnable examples, structure validation."
operators:
  - read
  - grep
  - glob
  - exec
  - write
  - edit
  - memory
metadata:
  enabled: true
  tags: [documentation, docs, readme, api-reference, changelog]
---

# Documentation Engineering

Write documentation that serves its audience. Internal details stay internal; examples must run; structure follows convention.

## When to Use

- User asks to write, update, or generate documentation
- Task output naturally includes documentation (README for new project, API docs for new module)
- User asks to explain code for others (not just for themselves — that's exploration)

## When NOT to Use

- User asks to explain code to themselves ("what does this do?") → use exploration mode
- Inline code comments only → use clean-code skill
- Architecture/design documents → use system-design skill

## Protocol

### Phase 1 — Audience & Type Analysis

Before writing anything, classify:

| Question | Options |
|----------|---------|
| Document type? | API reference / README / Design doc / Changelog / Tutorial / Inline docs |
| Target audience? | End user / Developer (external) / Developer (internal) / Operator / Reviewer |
| Existing docs? | None / Partial / Comprehensive (follow existing style) |

**Gate**: Do not start writing until type and audience are identified.

Rules by type:
- **API Reference**: Only public surface. Never expose internal implementation. Include type signatures, parameters, return values, errors, and one example per endpoint/function.
- **README**: Quick Start first (user should be running in 30 seconds). Then architecture overview. Then development setup.
- **Changelog**: User-facing impact, not code-level changes. Group by: Added / Changed / Deprecated / Removed / Fixed / Security.
- **Tutorial**: Step-by-step with runnable checkpoints. Each step builds on the previous.
- **Design doc**: Problem → Options (2+) → Decision → Consequences. Use system-design skill's Mermaid requirement.

### Phase 2 — Information Extraction

Gather information from source of truth:

| Source | What to extract |
|--------|----------------|
| Source code | Public API surface (exported functions, types, constants) |
| Test files | Usage examples (real call sites that compile) |
| Git log | Change history (for changelogs) |
| Existing docs | Style, structure, terminology to maintain consistency |
| Comments/docstrings | Design intent the author already expressed |

**Tools**: `grep` for API surface, `read` for implementations, `glob` for doc file patterns, `exec` for git log.

### Phase 3 — Writing

Rules:
1. **Examples must be runnable**: Every code example must include complete imports, variable declarations, and error handling. No `...` elision in critical setup.
2. **Internal stays internal**: If a function/type is not exported, it does not belong in API docs. If an implementation detail is needed for context, explain the "what" not the "how".
3. **Match existing conventions**: If project uses JSDoc → use JSDoc. If project has a `docs/` folder with Markdown → put new docs there. If project uses Sphinx → use RST.
4. **Headings create navigation**: Use consistent heading levels. H1 = document title, H2 = major section, H3 = subsection.
5. **Link, don't repeat**: Reference related docs instead of duplicating content.

### Phase 4 — Verification

| Check | Method |
|-------|--------|
| Examples compile | Extract code blocks, write to temp file, run build/test |
| Links valid | `grep` for referenced file paths, verify they exist |
| Markdown valid | If markdownlint available: `exec(command="npx markdownlint docs/...")` |
| Consistent style | Compare heading pattern with existing docs |

**Gate**: Example code that does not compile = documentation is not done. Fix before delivering.

### Phase 5 — Phase L Integration

After completing documentation, L4 only:

```
memory(action="note", source="docs-conventions",
  content="Docs: audience={who}; style={JSDoc|md}; tools={one sentence}.")
```

Do not write L2/L3. User doc preferences that Settlement should own stay out of `save`.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Expose internal implementation details in API docs | Only document public/exported surface |
| Write example code with `...` elision in setup | Complete, runnable examples with full imports |
| Start writing before identifying audience | Phase 1 first: who reads this? |
| Ignore existing documentation style | Read existing docs first, match their conventions |
| Skip example verification | Extract and compile every code block (Phase 4) |
| Write a changelog listing code changes | Write user-facing impact: what changed for the user |
| Mix tutorial and reference styles | Pick one structure and commit |
