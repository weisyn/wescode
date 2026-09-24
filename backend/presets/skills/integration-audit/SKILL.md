---
name: integration-audit
version: "1.0.0"
min_engine: "1.0.0"
description: "Audit Set*/With*/Init* wiring and injection chains. Use when verifying integrations or tracing nil-guard branches."
operators:
  - read
  - grep
  - find_references
  - search_symbols
  - memory
metadata:
  enabled: true
  always: false
  tags: [audit, integration, wiring, injection]
---

# Integration Audit

A systematic workflow for auditing structs with runtime-injected dependencies. Prevents the common error of concluding a feature is absent because the constructor shows default/nil state, when the feature is actually injected at wiring time via Set*/With*/Init* methods.

## When to Use

- Reviewing a struct that has `Set*`, `With*`, or `Init*` methods (runtime injection pattern).
- Reviewing a method body that contains `if h.field != nil` conditional branches.
- Auditing interface-to-implementation completeness across multiple files.
- Asked to verify whether a feature or integration exists in a codebase.

## When NOT to Use

- Simple structs with all fields set in the constructor.
- Single-file implementations with no cross-file wiring.

## Workflow

### Phase 0 — Confirm Audit Target

Identify the specific struct, interface, or feature being audited. Read the struct definition.

**Gate**: You must be able to name the target struct and the question being answered (e.g. "Does BufferOverlayHost have VSCode terminal integration?") before proceeding.

### Phase 1 — Interface Definition + All Implementations

- Search for the target interface: `search_symbols("TargetInterface")`
- Use `find_references(kind="references")` on the interface to find all implementations
- For each implementation, confirm which methods are implemented

### Phase 2 — Injection Point Enumeration

- For each implementation struct, list all unexported fields
- For each field, search for setters: `search_symbols("Set*")` or `grep(pattern="Set\w+|With\w+")`
- Use `find_references(kind="references")` on each setter to find ALL call sites
- Record the wiring chain: caller -> intermediate -> target

**Gate**: Every unexported field must have a traced injection source (constructor param, Set*, With*, or "confirmed unset").

### Phase 3 — Conditional Branch Check

For each method in the implementation:
- Check for `if h.field != nil` conditional branches
- Record: what does the conditional branch return? What is the fallback?
- **Critical**: seeing the fallback line does NOT prove the conditional path is absent — you must read the FULL method body

**Gate**: For every conditional branch, you must document BOTH paths (injected behavior + fallback behavior).

### Phase 4 — Conclusion Formula

- **WRONG**: "Method X returns Y" (when the method has conditional injection)
- **CORRECT**: "Method X returns Z when field is injected (via SetField at caller.go:57), Y otherwise (fallback)"
- Every claim must cite the specific file:line evidence

**Gate**: Every claim in the conclusion must have a corresponding `memory(action="note", tags=["evidence"])` with file:line citation. Zero unverified claims.

## Output Format

```
Target: BufferOverlayHost
├─ Shell()
│  ├─ notifier != nil → vscodeShellProvider
│  ├─ notifier == nil → LocalShellProvider  (fallback)
│  └─ notifier wiring chain:
│       NewBufferOverlayHost(bs, notifier) ← pendingNotifier ← Service.SetNotifier ← handler.go ✓
├─ Files()
│  └─ overlayFileProvider (always, no conditional)
└─ Conclusion: Shell is conditionally integrated via constructor-injected notifier
```

## Evidence Recording

Use `memory(action="note")` to record each finding as structured evidence:

```
memory(action="note", 
  content="Shell() has 2 paths: (1) notifier!=nil → vscodeShellProvider, (2) fallback → LocalShellProvider. Notifier injected via constructor: NewBufferOverlayHost(bs, pendingNotifier)",
  source="wescode/internal/engine/host.go",
  tags=["evidence", "wiring", "integration-audit"]
)
```

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Read a method's last line and conclude "returns X" | Read the FULL method body — conditional branches come before the fallback |
| See `NewFoo(bar)` with no field and conclude "field is nil" | Check all constructor parameters AND search for `SetField`/`WithField` — injection may happen via constructor args or post-construction |
| Trust your mental model over grep evidence | If grep finds `VSCodeShellProvider` but you thought "no VSCode integration", re-read |
| Skip tracing the setter's call chain | Knowing `SetNotifier` exists is not enough — trace WHO calls it and WHEN |
| Report findings without `memory(action="note", tags=["evidence"])` | Every claim needs a recorded note with file:line before concluding |
| Audit only the constructor, skip post-construction `Set*` calls | Scan for ALL injection methods — some fields are set after construction via lifecycle hooks |
