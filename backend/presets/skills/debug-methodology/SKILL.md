---
name: debug-methodology
version: "1.0.0"
min_engine: "1.0.0"
description: "Systematic runtime debugging: crash analysis, breakpoint strategy, log correlation, profile interpretation."
operators:
  - debug_context
  - debug_stacktrace
  - set_breakpoint
  - evaluate
  - analyze_crash
  - suggest_breakpoint
  - debug_continue
  - read
  - grep
  - exec
requires_operators:
  - debug_context
metadata:
  enabled: true
  tags: [debugging, runtime, profiling, crash-analysis]
---

# Debug Methodology

## When to Use

- User reports a runtime crash, exception, or panic
- Tests are failing with unexpected errors
- User asks to investigate a bug or unexpected behavior
- Performance issue needs profiling
- User explicitly requests debugging help

## When NOT to Use

- Compilation errors (use get_diagnostics instead)
- Code style or review questions (not a runtime issue)
- Architecture questions without runtime symptoms

## Four-Phase Debugging Protocol

### Phase 1: Reproduce

Before investigating, confirm the failure:

1. Check `debug_context` for any captured crashes or exceptions
2. If no crash data, ask the user how to reproduce, or run the failing command:
   ```
   exec(command="go test ./failing/package/ -v -run TestName")
   ```
3. If a crash is captured, proceed to Phase 2

### Phase 2: Isolate

Narrow down where the problem occurs:

1. Run `analyze_crash` to correlate stack trace with source code
2. Run `suggest_breakpoint` to get recommended breakpoint locations
3. Read the source at crash frames using `read`
4. If the crash point is clear, set a breakpoint:
   ```
   set_breakpoint(action="set", file="/path/to/file.go", line=42)
   ```
5. For intermittent bugs, use conditional breakpoints:
   ```
   set_breakpoint(action="set", file="...", line=42, condition="x > 100")
   ```

### Phase 3: Identify

Find the root cause:

1. When the debugger stops at a breakpoint, inspect state:
   ```
   debug_stacktrace  # full call stack + variables
   evaluate(expression="len(items)")  # check specific values
   ```
2. Step through execution to find where state diverges from expectation:
   ```
   debug_continue(action="step_over")  # next line
   debug_continue(action="step_into")  # enter function
   ```
3. For performance issues:
   ```
   exec(action="start_bg", command="go test -cpuprofile=cpu.prof -bench .")
   exec(command="go tool pprof -text -top cpu.prof")
   ```

### Phase 4: Fix

Apply and verify the fix:

1. Edit the source to fix the identified issue
2. Re-run the failing test/command to verify
3. Clean up breakpoints:
   ```
   set_breakpoint(action="remove", file="...", line=42)
   ```

## Tool Reference

| Tool | When to use |
|------|-------------|
| `debug_context` | First step: check what runtime events have been captured |
| `debug_stacktrace` | After crash/breakpoint: see full call stack and variable values |
| `analyze_crash` | Parse and correlate a stack trace with source code |
| `suggest_breakpoint` | Get AI-recommended breakpoint locations for a bug |
| `set_breakpoint` | Set/remove breakpoints in the IDE debugger |
| `evaluate` | Evaluate expressions at a breakpoint |
| `debug_continue` | Step through code: continue, step over/into/out |

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Guess the root cause without runtime evidence | Check debug_context first, then analyze_crash |
| Set dozens of breakpoints blindly | Use suggest_breakpoint for targeted locations |
| Read entire files looking for bugs | Use stack trace frames to pinpoint locations |
| Skip the reproduce step | Always confirm the failure is reproducible |
| Forget to remove breakpoints after fixing | Clean up with set_breakpoint(action="remove") |
