# wescode + DeepSeek Chat — SWE-bench Verified Submission

## System Description

**wescode** is an AI-powered coding tool built on the **wesgine** engine (v1.0), a server-side multi-tenant AI Agent engine. wescode integrates with VS Code as a fork, providing code intelligence through Code Knowledge Graph (CKG), Constraint Satisfaction Engine (CSE), and a multi-layer verification system.

### Architecture

- **Engine**: wesgine v1.0 — Hypervisor + Cell architecture with cognitive loop (Cognitive / Context / Execution / Memory / Govern)
- **Model**: DeepSeek Chat (deepseek-v4-flash) via OpenAI-compatible API
- **Tools**: Standard agentic tools (read, write, edit, exec, grep, search_files) + code intelligence tools
- **Strategy**: Best@1 single attempt, no multi-rollout or ensemble

### Key Features

- **Zero tool schema footprint for IDE capabilities**: Editor buffer overlay, terminal routing, and diff preview are transparently injected via `CellSpec.HostEnvironment`
- **Write Verification Nudge (ADR-311)**: Engine-level mechanism that detects "wrote files but didn't verify" at end_turn
- **Three-layer verification**: L0 syntax → L1 targeted test → L2.5 behavioral baseline

## Performance

| Metric | Value |
|--------|-------|
| % Resolved | **79.2%** (396/500) |
| Attempts | 1 (Best@1) |
| Model | DeepSeek Chat |
| Avg. cost per instance | ~$0.19 |
| Total cost | ~$95 |

## How to Reproduce

```bash
# Build wescode
cd backend && go build -o bin/wescode ./cmd/wescode/

# Run SWE-bench evaluation
bin/wescode bench \
  --dataset tests/bench/swebench/batches \
  --runs 1 \
  --predictions predictions.jsonl

# Score with swebench
swebench eval verified -p predictions.jsonl --run-id wescode -j 2
```

## Authors

weisyn team (TODO: + academic co-authors)

## Links

- wescode: https://www.weisyn.com
- wesgine engine: server-side multi-tenant AI Agent engine
