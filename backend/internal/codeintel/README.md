# codeintel: Code Intelligence Subsystem

Code indexing, retrieval, LSP integration, and context assembly
for the wescode AI programming assistant.

**Design doc**: [design/code-intelligence.md](../../../design/code-intelligence.md)

---

## Architecture Overview

```
User Message
     │
     ▼
┌─────────────────┐
│    Classifier    │ ─── TaskType (Explain/Fix/Implement/Review/Refactor)
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────────────────────┐
│                 Context Assembler                    │
│  ┌──────────────────────────────────────────────┐   │
│  │              Retriever                        │   │
│  │  Stage 1: deterministicBase (zero LLM,<20ms) │   │
│  │  Stage 2: heuristicExpand (rules,<100ms)     │   │
│  │  Stage 3: model→search_symbols (agentic)     │   │
│  └──────┬───────────────────────────────────────┘   │
│         ▼                                           │
│  ┌──────────────────────────────────────────────┐   │
│  │           BudgetAllocator                     │   │
│  │  P0→P1→P2→P3 Waterfall (ValueScore packing)  │   │
│  └──────────────────────────────────────────────┘   │
└────────┬────────────────────────────────────────────┘
         │
         ▼
┌─────────────────┐
│ ContextSnapshot  │ ─── Pushed to Agent context (Turn Overlay)
└─────────────────┘

                   Index Sources
         ┌───────────────────────┐
         ▼                       ▼
    CodeIndex                LSPBridge
    (sqlite)                 (gopls)
    symbols + FTS5           diagnostics
    CKG edges                definitions
    references
```

## Module Map

| File | Lines | Role |
|------|-------|------|
| `types.go` | 153 | Core types: CodeFragment, EditorState, TaskType, FragmentKind, ContextSnapshot |
| `classify.go` | 33 | Task classification from user message text |
| `retrieval.go` | 940 | 3-stage context retrieval with TaskType-routed expansion |
| `budget.go` | 101 | P0-P3 token budget allocation (waterfall model) |
| `assembler.go` | 267 | ContextAssembler: orchestrates Retriever → Budget → Snapshot |
| `index.go` | 448 | CodeIndex: sqlite-backed symbol index with FindSymbol/FindReferences |
| `capability_provider.go` | 107 | CodeIntelCapabilityProvider: capability surface + strategy guidance |
| `lspbridge/` | — | IDELSPBridge（问扩展）+ NoopLSP；后端不 spawn 语言服务器 |
| `langs/*.json` | — | CKG / 编译测试 / 拓扑配置（无 `lsp.command`） |
| `tool_search.go` | 146 | search_symbols tool for wesgine Agent |
| `metrics.go` | 179 | RetrievalMetrics: timing, hit rate, token cost tracking |
| `watcher.go` | 163 | FileWatcher: fsnotify-based index invalidation |
| `capability_test.go` | — | Integration test: CodeCapability (Assembler→Retrieval→Index) |
| `index_test.go` | — | Unit tests for CodeIndex |

## Key Types

### TaskType (classify.go)

6 classifications matched by regex patterns:

| TaskType | Pattern | Stage 2 Behaviour |
|----------|---------|-------------------|
| TaskExplain | explain/describe/what is/how does | Symbol definitions by name |
| TaskFixBug | fix/bug/error/panic/incorrect | LSP diagnostic context (±3 lines each error/warning) |
| TaskImplement | implement/add/create/new feature | Call graph chain (inward 2-level) |
| TaskReview | review/audit/check/look over | 1-hop reference chain |
| TaskRefactor | refactor/rename/extract/move | Both directions (inward + outward, 1-level each) |
| TaskDebug | debug/diagnose/trace/runtime | LSP diagnostics + outward call chain |

### CodeFragment (types.go)

| Field | Description |
|-------|-------------|
| Path | File path |
| StartLine/EndLine | Line range |
| Content | Raw code content |
| Kind | FragmentKind: FocusFile/RelevantCode/Diagnostic/Sibling/SymbolRef |
| Symbol | Associated symbol name (if any) |
| Reason | Why this fragment was included, human-readable |
| TokenCost | Estimated token consumption |
| ValueScore | 0.0-1.0 priority score for budget allocation |

### ContextSnapshot (types.go)

```
ContextSnapshot
├── Fragments     []CodeFragment  (sorted by ValueScore, budget-limited)
├── FocusFile     string          (current editor file)
├── FocusLine     int             (current cursor line)
├── TaskType      TaskType        (classified intent)
├── TotalTokens   int             (sum of TokenCost)
└── AssemblyTime  time.Duration   (performance metric)
```

## Retrieval Pipeline Details

### Stage 1: deterministicBase (zero LLM, <20ms)

Always includes:
- **Focus file** (cursor context: ±10 lines)
- **Selected code** (if any, full range)
- **Active diagnostics** (per TaskType matrix)
- **Call chain injection**:
  - Inward (callees): `FindReferences → callee definitions` (1-level, expand declared symbols)
  - Outward (callers): `HasReferences → caller fragments` (2-level for FixBug/Refactor, 1-level for Explain)
  - Call depth: `callDepth(map, sym, 0)` — TaskExplain/TaskReview depth=1, FixBug/TaskRefactor depth=2

### Stage 2: heuristicExpand (rule-driven, <100ms)

TaskType-routed:

| TaskType | Expansion Strategy | Cross-Language Guard |
|----------|-------------------|---------------------|
| TaskExplain | Message identifier → FindSymbol | ✅ (contextLanguageKey filter) |
| TaskFixBug | LSP diagnostic context (±3 lines each) | N/A (same file) |
| TaskImplement | Def-use chain: FindReferences → callee expansion | Via callDepth+callee |
| TaskRefactor | Both call chain directions | Via call graph |
| TaskReview | Same-package sibling functions | Within same extension |

### Stage 3: tools (model-driven, via Agent)

- `search_symbols` tool: FTS5 symbol search + L1 file content search
- Called by model when it decides more context is needed
- Results automatically added to exploration path memory

### Budget Allocation (budget.go)

P0→P1→P2→P3 waterfall model:

| Priority | Alloc | Condition |
|----------|-------|-----------|
| P0 | Unlimited | Focus file + selection |
| P1 | 40% of remaining | Diagnostics + call chain |
| P2 | 35% of remaining | Heuristic expansion |
| P3 | 25% of remaining | All other fragments |

Fragments within each tier sorted by `ValueScore / TokenCost` descending.

## LSP Integration

- **LSPBridge**：窗口下 `IDELSPBridge` 问编辑器扩展；无窗口 `NoopLSP`。后端不 spawn `gopls` / `typescript-language-server`
- 扩展空结果下一跳是 CKG，不是第二份语言服务器进程
- Used by: Stage 1 deterministic expansion (diagnostics), reverse reference injector

## Cross-Language Support

- `contextLanguageKey(path)`: infers language from file extension (.go, .py, .js/.ts/.tsx/.jsx, .rs, .java)
- Stage 2 `TaskExplain` guards against cross-language symbol match (e.g. Go `HandleRequest` vs JS `handleRequest`)
- `LanguageContext` in types.go tracks per-fragment language metadata
- Fail-open: if language is indeterminate, no filtering (inclusive default)

## Key Design Decisions

1. **Precision over recall** (CI-01): Better to miss a fragment than to bloat context with noise
2. **Deterministic base first, LLM only for enrichment**: Stage 1+2 guarantee no silent context loss
3. **Transparent context** (CE-09): ContextSnapshot is pushed to user as tool result, model and user see same context
4. **No staging storage**: Direct write disk, consistency via QualityGate catch-and-fix cycle (edit-engine.md EE-01)

## Dependencies

- **External**: go/ast, go/types (Go analysis)
- **Internal**: wesgine engine, editengine (memory anchor for knowledge binding)
- **Data store**: sqlite (via mattn/go-sqlite3) for symbol index + embeddings
- **File watching**: fsnotify for index invalidation

## Performance Targets

| Operation | Target | Measured |
|-----------|--------|----------|
| Stage 1 (deterministic) | <20ms | — |
| Stage 2 (heuristic) | <100ms | — |
| Full Assembly (Stages 1+2) | <150ms | — |
| Symbol search (local) | <50ms | — |
| Semantic search (1000 files) | <100ms | — |
| LSP diagnostics | <200ms | — |
| Index build (1000 files) | <30s | — |

## Testing

- `capability_test.go`: End-to-end Assembly→Retrieval→Index chain (mock-based)
- `index_test.go`: CodeIndex CRUD + FindSymbol + FindReferences

Run: `go test ./backend/internal/codeintel/...`
