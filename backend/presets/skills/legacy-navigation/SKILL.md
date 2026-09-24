---
name: legacy-navigation
version: "1.0.0"
min_engine: "1.0.0"
description: "Legacy codebase archaeology: entry-point discovery, mental model building, safe modification patterns."
operators:
  - read
  - grep
  - glob
  - exec
  - memory
  - search_symbols
  - find_references
metadata:
  enabled: true
  tags: [legacy, archaeology, codebase-navigation, onboarding, mental-model]
---

# Legacy Navigation

When to Use: Working with a codebase that shows legacy signals (old language version, deprecated APIs, no tests, long inactive, sparse documentation). Triggered by wsintel `IsLegacy` detection.

When NOT to Use: Greenfield projects, well-maintained projects with comprehensive tests and docs, projects the actor already knows (`memory(action="search")` hits).

## Protocol

### Phase 1 — Panoramic Scan (read-only, no modifications)

Goal: Build a high-level mental model without touching anything.

Steps:
1. Entry point discovery: find main functions, HTTP router registrations, CLI command definitions
   - `grep(pattern="func main\\(", type="go")` / `grep(pattern="createServer|express\\(|app\\.listen", type="js")`
2. Build system: read Makefile / package.json scripts / Cargo.toml — understand how to build and run
3. Dependency audit: read go.mod / package.json / requirements.txt — note versions, identify deprecated deps
4. Test inventory: `glob("*_test.go")` / `glob("*.test.ts")` — count and classify (unit vs integration)
5. Doc inventory: `glob("*.md", path="docs/")` / `glob("README*")` — existing documentation

Output: `memory(action="note", source="project", content="Architecture overview: ...")`

Gate: Do NOT modify any file until Phase 1 is complete.

### Phase 2 — Mental Model Construction

Goal: Trace 3 layers deep from entry points to understand core abstractions.

Steps:
1. From Phase 1 entry points, follow primary call chains (3 levels deep)
2. Identify core interfaces / base classes / middleware patterns
3. Mark "understanding boundaries" — what you understand vs what's unexplored territory
4. Look for hidden conventions: magic numbers, env var dependencies, implicit ordering

Output: `memory(action="note", source="architecture", content="Core abstractions: ...")`

### Phase 3 — Safe Navigation Rules

When modifying legacy code:
1. Never assume you understand all callers — use `find_references` before any change
2. Write a test that captures current behavior BEFORE modifying (lock the behavior)
3. Minimize change scope — one concern per modification
4. If you find a "magic number" or implicit convention — document it with `memory(action="note")` + inline comment
5. Prefer additive changes (new function that wraps old one) over in-place modifications

### Phase 4 — Knowledge Persistence (Phase L)

Write discoveries as L4 notes (`memory(action="note")`). Architecture overview, deprecated APIs, magic values, env vars — one distilled sentence per `source`. Same `source` is a no-op; delete first to replace. Model writes do not land in L2/L3.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Jump straight into editing legacy code | Complete Phase 1 panoramic scan first |
| Assume you understand the full impact of a change | Use find_references + write behavior-locking test |
| Apply agentic-execution "Act first" pattern | Legacy requires "Understand first, act carefully" |
| Ignore deprecated APIs without noting them | `memory(action="note")` the deprecated deps for later migration |
| Make large refactoring changes in legacy code | Small, incremental, tested changes only |
| Forget to document implicit conventions found | memory(action="note") + inline comment for each discovery |
