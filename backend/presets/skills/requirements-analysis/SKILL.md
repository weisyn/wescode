---
name: requirements-analysis
version: "1.0.0"
min_engine: "1.0.0"
description: "Turn vague needs into PRDs with stories, acceptance criteria, scope, open questions. Use when scoping features."
operators:
  - read
metadata:
  enabled: true
  tags: [requirements, PRD, user-story, acceptance-criteria]
---

# Requirements Analysis

Turn ambiguous requests into actionable specifications. Guard against incomplete acceptance criteria, scope creep, and untestable goals.

## When to Use

- User says "add a feature" but hasn't defined success criteria
- Scoping a new module, API, or workflow
- Reviewing a PRD or spec for completeness
- Breaking an epic into implementable stories

## Procedure

1. **Extract the three essentials** — every requirement must define:
   - **Goal**: What problem does this solve? For whom?
   - **Happy Path**: The primary user journey, step by step
   - **Success Metric**: Quantifiable outcome (latency target, error rate, conversion %)

2. **Write user stories with Given-When-Then**:
   - Each story needs ≥2 acceptance criteria (happy path + error case)
   - Format: `Given [context], When [action], Then [expected outcome]`
   - Include edge cases: empty input, concurrent access, permissions boundary

3. **Define scope boundaries**:
   - In Scope: exhaustive list of deliverables
   - Out of Scope: explicitly name what is NOT included (prevents creep)
   - Dependencies: what must exist before this can start

4. **Identify open questions** before implementation:
   - Data model assumptions that need validation
   - Integration points with unclear contracts
   - Performance constraints not yet quantified

5. **Prioritize with impact matrix**:
   - Effort (S/M/L) × Impact (Low/Med/High)
   - Ship the high-impact/low-effort items first

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Write user stories without acceptance criteria | Every story needs 2+ Given-When-Then criteria |
| Leave Out of Scope empty or missing | Explicitly list what is NOT included |
| Include implementation details in requirements | Requirements describe WHAT and WHY, not HOW |
| Accept vague success metrics ("make it better") | Quantify: latency target, error rate, conversion % |
| Treat requirements as immutable once written | Requirements evolve — version them, track changes |
| Skip stakeholder sign-off before implementation | Get explicit "yes, this is what I want" before coding |

## Verification

- [ ] Every requirement has Goal + Happy Path + Success Metric
- [ ] Every user story has ≥2 Given-When-Then acceptance criteria
- [ ] In Scope and Out of Scope sections are both non-empty
- [ ] Open questions list exists (even if empty with justification)
- [ ] No implementation details leaked into requirement descriptions
