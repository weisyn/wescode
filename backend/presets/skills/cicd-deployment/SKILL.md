---
name: cicd-deployment
version: "1.0.0"
min_engine: "1.0.0"
description: "CI/CD, Docker, compose, env separation, rollback. Use when configuring pipelines, Dockerfiles, or deploy automation."
operators:
  - read
  - write
  - exec
  - grep
metadata:
  enabled: true
  tags: [CI/CD, Docker, GitHub-Actions, deployment, containers]
---

# CI/CD Deployment

Build reliable pipelines and containerization. Guard against security mistakes, missing rollback paths, and environment leakage.

## When to Use

- Writing or reviewing Dockerfiles, docker-compose, or CI workflows
- Setting up GitHub Actions, GitLab CI, or similar pipelines
- Configuring environment separation (dev/staging/prod)
- Designing rollback and blue-green deployment strategies

## Procedure

1. **Dockerfile discipline**:
   - Multi-stage builds mandatory: `FROM golang AS builder` then `FROM alpine` (or distroless)
   - Pin base image tags (`golang:1.25-alpine3.21`, not `golang:latest`)
   - Run as non-root user in production stage
   - Use `.dockerignore` to exclude `.git/`, `node_modules/`, test fixtures

2. **Pipeline structure** (ordered stages):
   - lint → build → test → security scan → deploy
   - Each stage fails fast — no downstream execution on failure
   - Cache dependencies between runs (`actions/cache`, Docker layer cache)

3. **Secret management**:
   - Zero hardcoded secrets in Dockerfiles, CI configs, or compose files
   - Use CI secrets / environment variables / vault integration
   - Verify: `grep -rn 'password\|secret\|api_key\|token' Dockerfile .github/` should be empty

4. **Environment separation**:
   - Separate compose files: `docker-compose.yml` (base) + `docker-compose.prod.yml` (override)
   - Environment-specific config via env vars, not build args
   - Never share production credentials with dev/staging

5. **Rollback strategy**:
   - Every deployment must define how to roll back
   - Container: previous image tag; DB: migration DOWN script; Feature: flag toggle
   - Test rollback procedure before going live

6. **Health checks and observability**:
   - Docker `HEALTHCHECK` instruction in every production image
   - CI pipeline reports build duration, test coverage, image size
   - Deployment smoke test after every release

## Anti-Patterns

| Do NOT | Instead |
|--------|---------|
| Single-stage Dockerfile for production | Multi-stage: builder + minimal runtime |
| Hardcode secrets in CI config or Dockerfile | Use CI secrets / environment variables / vault |
| Skip test stage in pipeline | Pipeline order: lint → build → test → security → deploy |
| Deploy without rollback plan | Define rollback: previous tag, migration DOWN, feature flag |
| Use `latest` tag for production deployments | Pin specific version tags for reproducibility |
| Run containers as root | `USER nonroot` in Dockerfile; `--user` in compose |
| Ignore Docker image size | Distroless or alpine base; remove build tools from runtime stage |

## Verification

- [ ] Dockerfile uses multi-stage build with non-root runtime user
- [ ] `grep` finds zero hardcoded secrets in CI/Docker files
- [ ] Pipeline has lint → build → test → deploy stages in order
- [ ] Rollback procedure documented and tested
- [ ] Production images have HEALTHCHECK instruction
