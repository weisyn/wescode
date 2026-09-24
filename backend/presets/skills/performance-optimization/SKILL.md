---
name: performance-optimization
version: "1.0.0"
min_engine: "1.0.0"
description: "Profile-first perf work: measure, hotspot, optimize, benchmark. Use when code is slow or latency/memory is high."
operators:
  - read
  - exec
  - search_symbols
  - find_references
  - write
  - memory
metadata:
  enabled: true
  tags: [performance, profiling, optimization, benchmark]
---

# Performance Optimization

A five-phase protocol that enforces measurement-driven optimization. Designed to prevent the most common model failure: guessing the bottleneck instead of profiling first.

## When to Use

- User reports "this is slow" or "optimize this endpoint/function".
- User asks to reduce latency, memory usage, or CPU consumption.
- User wants to improve build time, test execution time, or startup time.
- A benchmark shows regression.

## When NOT to Use

- User wants a code review (use `code-review` skill).
- User wants to refactor for readability, not performance (use `clean-code` skill).
- The code has correctness bugs (fix bugs first, then optimize).

## Protocol

### Phase 1 — Reproduce and Baseline

**Goal**: Establish a reproducible, measurable baseline before touching any code.

**Tools**: `exec`, `read`

**Steps**:
1. Understand what "slow" means: which operation, what input size, what is the expected vs actual time.
2. Create or identify a reproducible test case.
3. Run the baseline measurement and record the numbers.

**Measurement tools by context**:

| Context | Tool |
|---------|------|
| HTTP endpoint | `exec(command="curl -w '%{time_total}' ...")` or `wrk`/`ab` |
| Go function | `exec(command="go test -bench=BenchmarkXxx -benchmem ./...")` |
| Node.js | `exec(command="node --prof script.js")` then `--prof-process` |
| Python | `exec(command="python -m cProfile -s cumulative script.py")` |
| SQL query | `exec` with `EXPLAIN ANALYZE` |
| General | `exec(command="time ...")` |

**Gate**: You MUST have a numeric baseline (e.g. "endpoint returns in 1200ms", "BenchmarkX: 45000 ns/op") before proceeding. Do NOT proceed on "it feels slow".

### Phase 2 — Profile

**Goal**: Identify WHERE the time/memory is spent using profiling tools, not guessing.

**Tools**: `exec`, `read`

**Steps by language**:

**Go**:
```
exec(command="go test -cpuprofile=cpu.prof -memprofile=mem.prof -bench=. ./pkg/...")
exec(command="go tool pprof -top cpu.prof")
exec(command="go tool pprof -top mem.prof")
```

**Node.js**:
```
exec(command="node --prof app.js")
exec(command="node --prof-process isolate-*.log")
```

**Python**:
```
exec(command="python -m cProfile -s cumulative target.py")
```

**SQL**:
```
exec(command="... EXPLAIN ANALYZE SELECT ...")
```

Record the profiling output with `memory(action="note")`:
```
memory(action="note", source="cpu.prof", content="Top 3 hotspots: 1) json.Marshal (35%), 2) db.Query (28%), 3) template.Execute (15%)", tags=["evidence", "profiling"])
```

**Gate**: You MUST have profiling data showing the actual hotspots before proceeding. "I think it might be N+1" is NOT sufficient — prove it with EXPLAIN or profiling output. If profiling tools are unavailable, add timing instrumentation (`time.Since`, `console.time`, `datetime.now`) to narrow down the slow section.

### Phase 3 — Identify Hotspot

**Goal**: From profiling data, select the top bottleneck to address.

**Tools**: `read`, `search_symbols`, `find_references`, `memory(action="note")`

**Rules**:
- Pick the SINGLE largest contributor first (Amdahl's Law: optimizing 35% of time gives more than optimizing 5%).
- Read the hotspot code to understand why it is slow.
- Use `find_references` to check how widely the hotspot is called.
- Classify the bottleneck:

| Type | Examples | Typical fix |
|------|----------|-------------|
| Algorithmic | O(n^2) loop, repeated linear scan | Better data structure, index, cache |
| I/O | N+1 queries, unbatched network calls | Batch, prefetch, connection pool |
| Serialization | Repeated JSON marshal/unmarshal | Reuse encoder, binary format, cache |
| Memory | Excessive allocation, no pooling | sync.Pool, pre-allocate, reduce copies |
| Concurrency | Lock contention, sequential I/O | Parallel, shard locks, async |

### Phase 4 — Optimize

**Goal**: Fix the identified bottleneck with a targeted change.

**Tools**: `read`, `write`, `exec`

**Rules**:
- Change ONE thing at a time. Do not combine multiple optimizations in one step.
- After each change, run the baseline measurement from Phase 1 to confirm improvement.
- If the change does not improve performance, revert it before trying something else.
- Keep the code readable. A 10% speedup that makes code unmaintainable is not worth it.

### Phase 5 — Benchmark and Verify

**Goal**: Confirm the optimization works and does not break anything.

**Tools**: `exec`

**Steps**:
1. Re-run the exact same measurement from Phase 1.
2. Compare: report the before/after numbers and percentage improvement.
3. Run the full test suite to confirm no regressions: `exec(command="go test ./...")` or equivalent.
4. If the improvement is insufficient, return to Phase 3 for the next hotspot.

**Gate**: You MUST show before/after numbers AND confirm all tests pass before reporting "optimized". No numbers = no claim of improvement.

**Output format**:
```
Baseline:  1200ms / 45000 ns/op / 12 allocs/op
After:      340ms / 12000 ns/op /  3 allocs/op
Improvement: 72% latency reduction, 73% fewer allocations
Regression tests: all passing
```

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Guess the bottleneck ("probably N+1") without profiling | Profile first, then identify from data (Phase 2) |
| Optimize multiple things at once | One change at a time, measure after each |
| Optimize code that accounts for <5% of total time | Focus on the top contributor (Amdahl's Law) |
| Skip the baseline measurement | Phase 1 Gate: must have numbers before starting |
| Report "optimized" without before/after numbers | Always show baseline vs result with percentage |
| Rewrite entire functions for minor gains | Targeted fixes; keep code readable |
