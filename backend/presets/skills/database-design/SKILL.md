---
name: database-design
version: "1.0.0"
min_engine: "1.0.0"
description: "ER modeling, schema, indexes, migrations, query tuning. Use when designing tables, migrations, or slow queries."
operators:
  - read
  - write
  - exec
metadata:
  enabled: true
  tags: [database, SQL, schema, migration, index]
---

# Database Design

Design correct schemas, write safe migrations, and optimize queries. Guard against migration irreversibility, missing indexes, and schema drift.

## When to Use

- Designing new tables or modifying existing schema
- Writing database migrations (UP + DOWN)
- Investigating slow queries or N+1 patterns
- Reviewing ER models or data access patterns

## Procedure

1. **Schema design principles**:
   - Normalize to 3NF by default; denormalize only with measured evidence
   - Every table needs a primary key (prefer `TEXT` UUIDs or `INTEGER` auto-increment)
   - Foreign keys with explicit `ON DELETE` behavior (CASCADE/SET NULL/RESTRICT)
   - `NOT NULL` by default; nullable columns need justification
   - `snake_case` for all table and column names

2. **Migration discipline**:
   - Every migration MUST have both UP and DOWN scripts
   - Migrations are idempotent: re-running N times produces the same result
   - Test both directions: `exec` UP then DOWN without error
   - Never modify a deployed migration — create a new one
   - For SQLite: `ADD COLUMN` is safe; `DROP COLUMN` needs table rebuild

3. **Index strategy**:
   - Every foreign key column gets an index
   - Composite indexes: most selective column first
   - Every new index must cite the query it accelerates
   - Verify with `EXPLAIN ANALYZE` (or `EXPLAIN QUERY PLAN` for SQLite)
   - Remove unused indexes — they slow writes for zero read benefit

4. **Query optimization**:
   - Identify N+1 patterns: prefer JOINs or batch loading
   - Use `EXPLAIN` before and after optimization to prove improvement
   - Avoid `SELECT *` in production — list needed columns explicitly
   - Use prepared statements for repeated queries
   - Pagination: keyset (`WHERE id > ?`) over offset (`LIMIT/OFFSET`)

5. **Data integrity**:
   - CHECK constraints for domain rules (`CHECK (status IN ('active','inactive'))`)
   - UNIQUE constraints for business-level uniqueness
   - Triggers only for cross-row invariants (prefer application logic)
   - Validate column types match application types (e.g. `TEXT` vs `VARCHAR(N)`)

6. **SQLite-specific**:
   - WAL mode for concurrent reads (`PRAGMA journal_mode=WAL`)
   - `PRAGMA mmap_size=0` to avoid mmap-related I/O errors (INV-RESIL-08)
   - `PRAGMA busy_timeout=5000` for lock contention
   - No VACUUM during normal operation — only in backup scripts (INV-RESIL-09)

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Write UP migration without DOWN | Always pair: CREATE needs DROP; ADD COLUMN needs rebuild |
| Add indexes without EXPLAIN evidence | Run EXPLAIN on the target query before and after |
| Hardcode environment-specific values in migrations | Use configuration or environment variables |
| Ignore N+1 query patterns | Use JOINs or batch loading; verify with EXPLAIN |
| Use `SELECT *` in production queries | Explicitly list needed columns |
| Modify already-deployed migrations | Create a new migration for changes |
| Use OFFSET pagination on large tables | Use keyset pagination (`WHERE id > last_seen_id`) |

## Verification

- [ ] Every migration has UP + DOWN scripts
- [ ] `exec` both UP and DOWN without errors
- [ ] New indexes cite the query they accelerate + EXPLAIN proof
- [ ] No N+1 patterns in the changed data access layer
- [ ] Column types match application-side types
