---
name: pattern-consistency
version: "1.0.0"
min_engine: "1.0.0"
description: "After writing code, compare patterns (error handling, logging, naming) with sibling functions in the same package. Flag inconsistencies."
operators:
  - read
  - grep
  - search_symbols
metadata:
  enabled: true
  tags: [consistency, patterns, R7b]
---

# Pattern Consistency (R7b)

Ensure newly written code follows the same patterns as existing code in the same package/directory. This skill implements R7b (implicit pattern conventions) through real-time comparison rather than static rules.

## When to Activate

After completing a `write` or `edit` tool call, before moving to the next task step:

1. Identify the package/directory of the modified file
2. Find 2-3 sibling functions of the same kind (function/method) in that package
3. Compare patterns across these dimensions:
   - **Error handling**: wrap style (`fmt.Errorf("...: %w", err)` vs bare `return err`)
   - **Logging**: structured logging format (`slog.Info` vs `log.Printf`)
   - **Naming**: parameter naming conventions, receiver names
   - **Return patterns**: early return vs single return at end
   - **Context propagation**: `ctx context.Context` as first param

## Process

```
1. grep or search_symbols to find 2-3 exported functions in the same package
2. read those functions (first 30 lines each)
3. Extract the dominant patterns:
   - Error wrapping: does the package use %w consistently?
   - Logging: structured (slog) or fmt-style?
   - Context: is ctx always first param?
4. Compare with the code just written
5. If mismatch found → self-correct silently (edit to match)
6. If uncertain → no action (don't over-constrain — false constraints are worse than missing ones)
```

## Constraints

- **Do NOT** flag style differences that are intentional (test files, generated code, main functions)
- **Do NOT** spend more than 3 tool calls on pattern checking (budget: ~500ms total)
- **Do NOT** modify code that was explicitly requested by the user to differ from patterns
- Prefer silent self-correction over warning messages (R7b is implicit, not a hard rule)

## Examples

### Error Wrapping Mismatch
```
Package pattern: return fmt.Errorf("create user: %w", err)
Your code:      return err
Action:         Edit to wrap: return fmt.Errorf("<context>: %w", err)
```

### Logging Style Mismatch
```
Package pattern: slog.Info("[module] action", "key", value)
Your code:      log.Printf("action: %v", value)
Action:         Edit to use slog with structured fields
```

### No Mismatch
```
Package pattern: mixed (some %w, some bare return in helpers)
Your code:      return err (in a small helper)
Action:         No change (matches the exception pattern)
```
