# Phase 5 CI / Ownership Readiness

This document captures the current Phase 5 readiness for splitting CI/CD ownership between the future frontend and platform repositories.

## Frontend Side

The frontend tree already contains repo-local CI scaffolding:

- [frontend-ci.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/.github/workflows/frontend-ci.yml)

Current frontend-owned validation scope:

- dependency installation
- production build
- Playwright test discovery

Frontend ownership is documented in:

- [README.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/README.md)
- [repo-extraction.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/docs/repo-extraction.md)

## Platform Side

Phase 5 adds an extraction-ready platform workflow:

- [platform-ci.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/.github/workflows/platform-ci.yml)

Current platform-owned validation scope:

- `go test ./...` from `controller/`
- `go build ./cmd/server` from `controller/`
- `docker compose -f docker-compose.platform.yml config` from the repo root

Platform ownership is documented in:

- [README.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/README.md)
- [repo-extraction.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/docs/repo-extraction.md)

## Remaining Phase 5 Gaps

Phase 5 intentionally stops short of live deployment automation. The following remain Phase 6 concerns:

- wiring repository secrets in the extracted repos
- binding deployment targets to the new repositories
- retiring the monorepo as the active source of truth

## Readiness Summary

With the current repository state:

- frontend and platform validation responsibilities are explicitly separated
- both sides now have extraction-ready CI definitions
- repo-local docs point to the correct owner for build, test, and deployment concerns

That is sufficient to treat Phase 5 as structurally complete for the split plan.
