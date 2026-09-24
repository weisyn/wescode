---
name: system-design
version: "1.0.0"
min_engine: "1.0.0"
description: "Architecture: modules, interfaces, trade-offs, Mermaid, ADRs. Use when designing systems or decomposing work."
operators:
  - read
  - exec
  - write
  - edit
  - glob
  - memory
metadata:
  enabled: true
  tags: [architecture, module-decomposition, trade-off, Mermaid, ADR, bootstrap, scaffolding]
---

# System Design

Quick reference for architecture decisions. The model already handles system design well; this skill guards against single-option bias and missing impact analysis.

## Key Gates

- **Two-alternative minimum**: Every design decision must present at least two alternatives with a trade-off comparison table. Gate: no single-option proposals.
- **Mermaid visualization**: Architecture must include at least one Mermaid diagram showing component boundaries and data flow.
- **Impact assessment**: Every proposal must list: affected files, breaking interface changes, migration path, and rollback strategy.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Propose a single architecture without alternatives | Present 2+ options with pros/cons/trade-offs table |
| Skip visualization, describe architecture in prose only | Include Mermaid diagram with component boundaries and data flow |
| Design without impact assessment | List affected files, breaking changes, migration, rollback |
| Define implementation before interfaces | Define module interfaces (Input/Output/Errors) first, implement second |

## Bootstrap Mode

When wsintel detects an empty or near-empty project (`IsEmpty` signal), this skill switches to Bootstrap Mode — a specialized protocol for creating new projects from scratch.

### When Bootstrap Mode Activates

- Workspace has < 5 source files
- Only a manifest file exists (go.mod / package.json) with no source code
- User says "create", "new project", "scaffold", "initialize"

### Protocol

#### Phase B1 — Technology Selection

If user has not specified technology choices:

1. Infer from context (manifest file type, user message keywords)
2. Present **2+ alternatives** with trade-off table (inherits the two-alternative minimum gate):
   ```
   | Option | Pros | Cons |
   |--------|------|------|
   | Standard library | No dependencies, fast compile | More boilerplate |
   | Echo framework | Less boilerplate, middleware | External dependency |
   ```
3. Wait for user selection before proceeding

If user has specified choices: skip to Phase B2.

#### Phase B2 — Scaffold Generation

1. Create directory structure following language/framework conventions:
   - Go: `cmd/{name}/main.go`, `internal/`, `go.mod`
   - Node: `src/`, `package.json`, `tsconfig.json`
   - Python: `src/{name}/`, `pyproject.toml`, `tests/`
2. Generate entry point file with minimal working code
3. Generate build/config files (.gitignore, Makefile/Taskfile, CI template)
4. Install dependencies: `exec(command="go mod tidy")` or `exec(command="npm install")`

#### Phase B3 — Verification

1. Compile/build: `exec(command="go build ./...")` or equivalent
2. Run: verify the app starts (brief smoke test)
3. Present the created structure to the user:
   ```
   exec(command="find . -not -path './.git/*' -not -path './node_modules/*' | head -30")
   ```

#### Phase B4 — Memory Write

After scaffolding, L4 only (`memory(action="note")`). `kind` does not change layer.

```
memory(action="note", source="scaffold",
  content="Scaffold: {language}/{framework}. User choices: {stack}. Conventions: {one sentence}.")
```

### Anti-Patterns (Bootstrap)

| Do NOT | Instead |
|--------|---------|
| Start writing code without asking about technology choices | Phase B1: present alternatives |
| Generate a massive scaffold with every possible feature | Minimal viable scaffold — user adds features incrementally |
| Skip compilation verification | Phase B3: always verify the scaffold builds and runs |
| Forget to create .gitignore | Always include .gitignore appropriate for the language/framework |
