---
name: security-audit
version: "1.0.0"
min_engine: "1.0.0"
description: "OWASP Top 10, dependency CVEs, permissions, credential leaks, risk report. Use when auditing security or auth."
operators:
  - read
  - exec
capabilities:
  - cap.security.audit
metadata:
  enabled: true
  execution_mode: guided
  tags: [security, OWASP, vulnerability, audit, permissions]
---

# Security Audit

Systematically discover security vulnerabilities in code and dependencies, producing a graded risk report.

## When to Use

- Auditing code for security issues (injection, auth bypass, data exposure).
- Scanning dependencies for known CVEs.
- Reviewing authentication/authorization logic.
- Checking for hardcoded secrets or credential leaks.

## When NOT to Use

- Functional bugs → use `code-review`.
- Performance issues → not a security concern.

## Execution

**Critical constraint: This skill is READ-ONLY. Never modify source code during a security audit.**

### Step 1: Scope the Audit

Determine what to audit:
- Run `exec find . -name "*.go" -o -name "*.ts" -o -name "*.py" | head -50` to understand the codebase.
- Read `go.mod` / `package.json` / `requirements.txt` for dependency inventory.
- Identify entry points: HTTP handlers, CLI commands, message consumers.

### Step 2: Dependency Vulnerability Scan

Run the appropriate scanner:
- Go: `exec go list -json -m all | head -100` then check against known CVEs.
- Node: `exec npm audit --json 2>/dev/null | head -50`.
- Python: `exec pip audit 2>/dev/null || echo "pip-audit not installed"`.

Record findings with CVE IDs when available.

### Step 3: Code-Level OWASP Scan

Read each entry point file. Check in this order:

1. **Injection** — Is user input parameterized? Look for string concatenation in SQL/commands.
2. **Broken Auth** — Password storage (bcrypt/argon2?), session management, token validation.
3. **Sensitive Data Exposure** — HTTPS enforcement, credentials in logs, API keys in responses.
4. **Broken Access Control** — Per-endpoint permission checks, IDOR vulnerabilities.
5. **Security Misconfiguration** — Default passwords, debug mode in production, overly permissive CORS.
6. **XSS** — User input rendered without escaping in templates/responses.
7. **Insecure Deserialization** — Untrusted data deserialized without validation.
8. **Known Vulnerable Components** — (covered in Step 2).
9. **Insufficient Logging** — Critical operations without audit trail.

### Step 4: Credential Leak Scan

```bash
exec grep -rn "password\|secret\|api_key\|token\|private_key" --include="*.go" --include="*.ts" --include="*.env" . | grep -v "_test\." | grep -v "node_modules" | head -30
```

Flag any hardcoded values that look like real credentials.

### Step 5: Produce Risk Report

Output in this exact format:

```markdown
## Security Audit Report

### Critical (Remote exploitable)
| # | Location | Vulnerability | Impact | Remediation |
|---|----------|--------------|--------|-------------|

### High
| # | Location | Vulnerability | Impact | Remediation |

### Medium
| # | Location | Vulnerability | Impact | Remediation |

### Low
| # | Location | Vulnerability | Impact | Remediation |

### Dependencies
| Package | Version | CVE | Severity | Fix Version |

### Summary
- Total findings: N
- Critical: N | High: N | Medium: N | Low: N
- Hardcoded credentials: Yes/No
```

### Step 6: Self-Check

- Every finding has a specific location (file:line).
- Every finding has a concrete remediation suggestion.
- Severity is calibrated (not everything is Critical).
- No false positives from test files or comments.

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Fix vulnerabilities during the audit | Audit is read-only — report findings with remediation suggestions |
| Mark everything as Critical severity | Critical = remotely exploitable without auth; calibrate severity accurately |
| Report findings from test files or comments as real vulnerabilities | Verify the finding is in production code paths before reporting |
| Skip dependency vulnerability scanning | Always run `go/npm/pip audit` even if code looks clean — transitive deps matter |
| Produce findings without remediation steps | Every finding must include a concrete fix suggestion |
| Claim "no authentication" without reading the middleware chain | Read the full request path: router → middleware → handler. Auth is often in middleware, not the handler itself |
| Assert a vulnerability exists based on common patterns without tool-call verification | Always `read` or `grep` the actual code before reporting. In long sessions, reference TaskMemory checkpoints for previously verified facts |

## House Rules

1. **Never modify code** — audit produces reports only.
2. **Zero tolerance for hardcoded credentials** — always Critical.
3. **Calibrate severity** — Critical means remotely exploitable without authentication.
4. **Minimize false positives** — one real finding > ten noisy ones.
5. **Verify before assert** — every finding in the report must have a corresponding tool call or TaskMemory checkpoint that verified it. See `code-audit-discipline` skill for the full verification protocol.
6. **Record verified facts** — after verifying authentication, authorization, or input validation mechanisms, record as `memory(action="checkpoint", content="<what was verified>", source="<evidence: file:line + what the code does>")` to prevent contradictory conclusions in later turns.
