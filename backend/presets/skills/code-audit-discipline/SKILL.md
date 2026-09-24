---
name: code-audit-discipline
version: "1.0.0"
min_engine: "1.0.0"
description: "Prevents false findings in code audits via evidence-backed claims. Uses report_finding tool for mechanical verification, checkpoint recording, segmented review, and optional role-separated delegation."
operators:
  - read
  - grep
  - glob
  - exec
  - memory
  - report_finding
capabilities:
  - cap.audit.discipline
metadata:
  enabled: true
  execution_mode: guided
  tags: [audit, discipline, verification, long-session, quality, grounded-claims]
---

# Audit Discipline

Cross-cutting discipline for any code audit, security review, or codebase-wide analysis. Built on the **Grounded Claims Architecture**: findings must be evidence-backed, conclusions must go through structured tools, and high-stakes audits use role separation to make hallucinated evidence structurally impossible.

## When to Use

This skill is activated alongside domain-specific audit skills (`security-audit`, `code-review`, `integration-audit`). It does not replace them — it provides the verification discipline layer.

- Any audit spanning more than 10 files.
- Security reviews, architecture assessments, migration audits.
- Investigation tasks that accumulate findings across many modules.

## When NOT to Use

- Quick single-file fixes (overhead of checkpoint recording not warranted).
- Implementation tasks (use `agentic-execution` instead).

## Core Rules (Mandatory)

### Rule 1: Findings Go Through `report_finding`

**All audit findings must be submitted via the `report_finding` tool, not free text.** The tool requires `evidence_refs` that reference actual tool calls (grep/read/exec) from this Run. The engine mechanically verifies that each `tool_call_id` exists.

```
✅ Correct:
  grep("admin.*auth", path="internal/gateway/") → tool_call_id: "tc_0012"
  read("internal/gateway/middleware_chain.go", lines=344-360) → tool_call_id: "tc_0013"
  report_finding(severity="info", claim="admin chain authenticates via bearer token",
    evidence_refs=[
      {"tool_call_id": "tc_0012", "observation": "grep found buildAdminChain"},
      {"tool_call_id": "tc_0013", "observation": "read confirmed Verify(token) at line 344"}
    ])

❌ Incorrect:
  (no tool call)
  → Free text: "admin handler lacks authentication — P0 security issue"
```

Free-text conclusions in the chat response do NOT enter the structured audit report. Only `report_finding` entries (in TaskMemory as `[verified]` findings) constitute the official report.

### Rule 2: Record Verified Facts as Checkpoints

After verifying a significant fact, immediately record it as a checkpoint:

```
memory(action="checkpoint",
       content="admin chain authenticates via bearer token",
       source="buildAdminChain:344 calls store.Verify(token, scope, cellID)")
```

Checkpoints are persisted in TaskMemory and survive context compression. They prevent contradictory conclusions in later turns when context has been compacted.

For ongoing notes (not yet ready as a finding), use `memory(action="note", evidence_refs=[...])` to link to evidence.

### Rule 3: Severity Requires Evidence Depth

| Severity | Evidence requirement |
|----------|---------------------|
| **P0** (critical) | >= 2 evidence refs, must include `read` of the actual code |
| **P1** (important) | >= 1 evidence ref |
| **P2** (minor) | >= 1 evidence ref |
| **info** | >= 1 evidence ref |

### Rule 4: Segment Long Audits

For audits spanning many modules:

1. **Plan first**: Create a plan with one step per module/concern area.
2. **Checkpoint between segments**: After each module, record a summary checkpoint.
3. **30-file threshold**: After auditing ~30 files without a checkpoint summary, pause and record progress.
4. **Self-check at segment boundaries**: Before moving to the next module, verify that recent findings are consistent with earlier checkpoints.

### Rule 5: Contradiction Detection

Before outputting a finding that contradicts an existing checkpoint:

1. **Re-read the code** — the checkpoint may be outdated if code was modified.
2. If the code hasn't changed, **trust the checkpoint** — it was verified with a tool call.
3. If you cannot determine whether the code changed, **explicitly state the uncertainty**.

## Role Separation (High-Stakes Audits)

For security audits or compliance checks where hallucinated conclusions carry significant risk, delegate to two sub-agents:

1. **Investigator** (`tools_allow: [read, grep, glob, exec]`): searches the codebase. Cannot produce conclusions — it has no `report_finding` tool. Its output is objective tool call history.

2. **Analyst** (`tools_allow: [report_finding, memory]`): receives the Investigator's results. Can submit findings but cannot search — it has no grep/read. Its findings must reference the Investigator's actual tool calls.

The Investigator cannot hallucinate conclusions (no tool). The Analyst cannot hallucinate evidence (no search tools). Both are LLM-driven — the engine limits tool access via `DelegationTask.ToolsAllow`.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Write audit conclusions in free text without `report_finding` | Use `report_finding(evidence_refs=[...])` for all findings |
| Assert security vulnerabilities without reading the actual handler code | Read the handler, check middleware chain, then `report_finding` |
| Report "missing authentication" because you don't see it in the handler | Check middleware chain, decorator patterns, framework-level auth |
| Claim "no input validation" without checking request parsing code | Read the full request handling path including shared middleware |
| Skip checkpoint recording in sessions longer than 20 turns | Record at least one checkpoint per 10-turn segment |
| Contradict an earlier checkpoint without re-reading the code | Re-verify before overriding a checkpoint conclusion |
| Use same agent for both searching and concluding in security audits | Use role separation: Investigator (search only) + Analyst (conclude only) |

## Integration with TaskMemory

This skill relies on wesgine TaskMemory for persistence across context compressions:

- **Findings (via `report_finding`)**: evidence-backed, mechanically verified, marked `[verified]` in TaskMemory.
- **Checkpoints**: verified safe areas — "admin has auth", "knowledge has sandbox".
- **Decisions**: classification decisions — "context.Background() in defer is intentional".

All three survive context compaction because TaskMemory is injected as a separate `<task-memory>` section, outside the compressed conversation history.

## Report Template

The final report is rendered from TaskMemory `[verified]` findings:

```markdown
## Audit Report: {scope}

### Verified Findings ({N} — evidence-backed via report_finding)
| # | Severity | Location | Issue | Evidence Refs |
|---|----------|----------|-------|---------------|

### Verified Safe ({N} checkpoints)
| # | Area | Conclusion | Evidence |
|---|------|------------|----------|

### Audit Coverage
- Files examined: {N}
- Findings recorded: {N} (all verified)
- Checkpoints recorded: {N}
```
