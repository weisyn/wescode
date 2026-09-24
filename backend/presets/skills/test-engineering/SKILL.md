---
name: test-engineering
version: "1.0.0"
min_engine: "1.0.0"
description: "TDD workflow, table-driven tests, mocks, coverage. Use when writing tests, regressions, or improving coverage."
operators:
  - read
  - write
  - exec
capabilities:
  - cap.test.methodology
metadata:
  enabled: true
  execution_mode: guided
  tags: [testing, unit-test, integration-test, TDD, mock]
---

# Test Engineering

Write comprehensive tests using a disciplined red-green-refactor loop with immediate execution feedback.

## When to Use

- Writing tests for new or existing functionality.
- Fixing a bug (write failing test first, then fix).
- Refactoring — ensure test coverage before restructuring.
- Reviewing test quality and coverage gaps.

## When NOT to Use

- Throwaway scripts or prototypes.
- Pure UI style tweaks (visual regression is a different discipline).

## Execution

### Step 1: Understand the Target

Read the code under test. Identify:
- Public API surface (exported functions/methods).
- Edge cases: nil inputs, empty collections, boundary values, error paths.
- External dependencies that need mocking (DB, HTTP, filesystem).

### Step 2: Write a Failing Test (Red)

Create or open the test file (`*_test.go` / `*.test.ts`).

Write **one** test case that exercises the behavior you want to verify. Use table-driven pattern for Go:

```go
tests := []struct {
    name    string
    input   InputType
    want    OutputType
    wantErr bool
}{
    {"valid case", validInput, expectedOutput, false},
    {"empty input", "", "", true},
}
for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got, err := FunctionUnderTest(tt.input)
        // assertions
    })
}
```

Run it: `exec go test ./path/to/package -run TestName -v`

**Gate**: The test MUST fail. If it passes immediately, the test is not testing anything useful — revise it.

### Step 3: Make it Pass (Green)

If implementing new code: write the minimum code to make the test pass.
If testing existing code: the test should already pass (skip to Step 4).

Run again: `exec go test ./path/to/package -run TestName -v`

**Gate**: The test MUST pass. If it fails, fix the implementation (not the test).

### Step 4: Add Edge Cases

Extend the table with:
- Boundary values (0, -1, max int, empty string).
- Error conditions (nil pointer, timeout, permission denied).
- Concurrent access (if applicable).

Run the full test suite: `exec go test ./...`

### Step 5: Verify Coverage (if requested)

```bash
exec go test -coverprofile=cover.out ./path/to/package
exec go tool cover -func=cover.out | grep -v "100.0%"
```

Report uncovered lines as potential risk areas.

## Feedback Loop

If any test run fails unexpectedly:
1. Read the error message carefully.
2. Determine: is this a test bug or an implementation bug?
3. Fix the appropriate side.
4. Re-run. Do NOT proceed until green.

## Mock Strategy

- Only mock external boundaries (DB, HTTP, file I/O).
- Never mock the module under test's internal functions.
- Prefer interface injection over monkey-patching.
- Mock behavior must match real behavior (same error types, same nil semantics).

## Naming Convention

```
Test_<Function>_<Scenario>_<Expected>

Test_ValidateEmail_EmptyInput_ReturnsError
Test_CreateUser_DuplicateName_ReturnsConflict
```

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Write a test that passes immediately (skip Red phase) | The test MUST fail first — revise until it fails meaningfully |
| Fix the test assertion to make Green instead of fixing implementation | Fix the implementation, not the assertion |
| Mock internal functions of the unit under test | Only mock external boundaries (DB, HTTP, filesystem) |
| Share mutable state between test cases | Each test is independent and repeatable — no execution order dependency |
| Name tests generically (`TestHandler`, `TestService`) | Use `Test_Function_Scenario_Expected` pattern |

## House Rules

1. **Test first, implement second** — TDD is the default workflow.
2. **Tests are documentation** — a reader should understand behavior from test names alone.
3. **Independent and repeatable** — no shared state between tests, no execution order dependency.
4. **Test files only** — write tests in `*_test.go` / `*.test.ts`, never in production files.
