---
name: refactoring
version: "1.0.0"
min_engine: "1.0.0"
description: "Incremental refactor: impact analysis, stepwise changes, compile-verify. Use when renaming, migrating, or restructuring."
operators:
  - read
  - grep
  - glob
  - exec
  - write
  - edit
  - search_symbols
  - find_references
  - memory
metadata:
  enabled: true
  tags: [refactoring, migration, rename, API-migration, dependency-upgrade]
---

# Refactoring

A five-phase protocol for large-scale code changes that span multiple files. Designed to prevent the most common model failure: applying all changes at once and producing cascading compilation errors.

## When to Use

- Renaming a symbol (function, type, variable) used across many files.
- Migrating from one API to another (e.g. old SDK to new SDK).
- Upgrading a dependency with breaking changes.
- Moving code between packages or restructuring module boundaries.
- Changing a shared type's fields or method signatures.

## When NOT to Use

- Single-file refactoring within one function (use `clean-code` skill).
- Performance optimization (use `performance-optimization` skill).
- Adding new features (no existing code to refactor).

## Protocol

### Phase 1 — Impact Analysis

**Goal**: Enumerate EVERY location affected by the change before modifying anything.

**Tools**: `grep`, `glob`, `search_symbols`, `find_references`, `memory(action="note")`

**Steps**:
1. Identify the symbol(s) being changed.
2. Use `find_references` to get all usages (definitions, references, implementations).
3. Use `grep` as a cross-check for string-based references (config files, comments, documentation, generated code that `find_references` may miss).
4. Use `glob` to find related files (test files, mocks, fixtures).
5. Record the complete impact list with `memory(action="note")`:

```
memory(action="note", source="impact-analysis", content="Rename getUser → fetchUser affects 23 locations in 12 files:
  - internal/user/service.go: definition L45, L78 (2 methods)
  - internal/user/service_test.go: 8 call sites
  - internal/handler/auth.go: 3 call sites
  - internal/handler/profile.go: 2 call sites
  - ...", tags=["evidence", "refactoring"])
```

**Gate**: You MUST have a complete list of affected locations before proceeding. If `find_references` and `grep` counts disagree, investigate the discrepancy (e.g. string references in configs, generated code, or comments).

### Phase 2 — Plan Increments

**Goal**: Group the changes into ordered increments that can each be independently compiled.

**Tools**: `read`, `memory(action="note")`

**Rules**:
- Group by package/module boundary (one increment per package).
- Order by dependency direction: **leaf packages first, root packages last**.
  - If package A imports package B, change B first, then A.
  - This ensures each increment compiles against already-updated dependencies.
- For interface changes: update the interface definition first, then all implementations, then all callers.
- Record the plan:

```
memory(action="note", content="Refactoring plan (4 increments):
  1. internal/user/ (definition site, 2 files)
  2. internal/handler/ (callers, 3 files)
  3. internal/middleware/ (callers, 1 file)
  4. tests + mocks (5 files)", tags=["refactoring", "plan"])
```

### Phase 3 — Apply Incrementally

**Goal**: Execute the plan one increment at a time, verifying compilation after each.

**Tools**: `read`, `write`, `edit`, `exec`

**Steps** (repeat for each increment):
1. Apply the change to all files in this increment.
2. Run compilation check: `exec(command="go build ./...")` or `exec(command="tsc --noEmit")`.
3. If compilation fails: fix the error within this increment before proceeding.
4. Move to the next increment.

**Gate**: Each increment MUST compile successfully before starting the next. Do NOT batch all increments into one step.

**Rules**:
- Use `edit` for precise replacements (old_string/new_string).
- When renaming, update both the declaration and all references in the same increment.
- Update import paths if the symbol moved to a different package.
- Do NOT leave temporary compatibility aliases unless explicitly requested.

### Phase 4 — Update Dependents

**Goal**: Catch changes that are not direct code references but still need updating.

**Tools**: `grep`, `read`, `write`

**Checklist**:
- [ ] Import statements (added/removed/renamed)
- [ ] Configuration files (YAML, JSON, TOML) referencing the old name
- [ ] Documentation and comments mentioning the old name
- [ ] Generated code (protobuf, OpenAPI, SQL migrations)
- [ ] Build scripts and Makefiles
- [ ] CI/CD pipeline references

Use `grep` with the OLD name across the entire workspace to find any residuals.

### Phase 5 — Final Verification

**Goal**: Confirm zero residuals and full test passage.

**Tools**: `exec`, `grep`

**Steps**:
1. Full compilation: `exec(command="go build ./...")` or equivalent.
2. Full test suite: `exec(command="go test ./...")` or equivalent.
3. Zero-residue check: `grep(pattern="oldSymbolName")` across the entire workspace.
   - Expected: 0 matches (excluding this refactoring plan note and git history).
   - If any matches remain, go back and fix them.

**Output format**:
```
Refactoring: getUser → fetchUser
Impact: 23 locations in 12 files
Increments: 4 (all compiled successfully)
Residue check: 0 matches for "getUser" (clean)
Tests: all passing (147 passed, 0 failed)
```

## Migration Mode

When wsintel detects migration signals (dependency version bump, deprecated API replacement, framework upgrade), this skill activates Migration Mode — an extended protocol that wraps the standard 5-phase refactoring.

### When Migration Mode Activates

- Major version change detected in `go.mod` / `package.json` / `Cargo.toml`
- User explicitly mentions "migrate", "upgrade", "replace X with Y"
- wsintel `IsMigration` signal is true

### Phase 0 — Migration Context (before standard Phase 1)

**Goal**: Establish the old → new mapping before any changes.

**Steps**:
1. Identify the migration scope: What is changing? (dependency, framework, language version, API)
2. Read the migration guide / changelog / breaking changes documentation:
   - `exec(command="cat CHANGELOG.md")` or `exec(command="cat MIGRATION.md")`
   - If no local docs, search for the library's official migration guide
3. Build an **Old → New API mapping table**:
   ```
   memory(action="note", content="Migration map: react-router v5→v6:
     - <Switch> → <Routes>
     - <Route component={X}> → <Route element={<X/>}>
     - useHistory() → useNavigate()
     - ...", source="migration-plan")
   ```
4. Classify each mapping:
   | Type | Description | Example |
   |------|-------------|---------|
   | Direct replacement | 1:1 text substitution | `useHistory()` → `useNavigate()` |
   | Structural change | Needs code restructuring | `<Switch>` → `<Routes>` wrapping |
   | Behavioral change | Same API, different semantics | Route matching now exact by default |
   | Removed | No direct replacement | `<Prompt>` component removed |

**Gate**: Do not start Phase 1 without a complete mapping table.

Then proceed with standard Phase 1-5, where Phase 1 (Impact Analysis) uses the mapping table to find all affected locations.

### Migration Phase L

After completing migration, L4 only:

```
memory(action="note", source="migration",
  content="{library} v{old}→{new} completed. Key changes: {summary}")
```

Same `source` is a no-op; `memory(action="delete", id)` first to replace. Model writes do not land in L2.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Apply all changes in one step across all files | Increment by package, compile-verify after each (Phase 3) |
| Start changing code before listing all affected locations | Complete impact analysis first (Phase 1 Gate) |
| Rename in source but forget tests, mocks, configs | Use Phase 4 checklist for non-code dependents |
| Leave old-name compatibility wrappers without being asked | Clean break unless user explicitly requests gradual migration |
| Trust `find_references` alone for completeness | Cross-check with `grep` for string references in configs and docs |
| Skip final zero-residue grep check | Phase 5: grep the old name to confirm nothing was missed |
| Start migrating without a mapping table | Build Phase 0 mapping first, then use it to drive Phase 1 |
