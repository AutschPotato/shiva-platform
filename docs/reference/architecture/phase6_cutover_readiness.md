# Phase 6 Cutover Readiness

This document captures the current readiness for executing the final frontend/platform repository cutover.

## Cutover Assets Added

Phase 6 now provides concrete export assets instead of only a high-level split plan:

- [frontend_repo_manifest.json](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/frontend_repo_manifest.json)
- [platform_repo_manifest.json](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/platform_repo_manifest.json)
- [export-split-repo.ps1](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/scripts/export-split-repo.ps1)
- [prepare-split-staging.ps1](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/scripts/prepare-split-staging.ps1)
- [initialize-split-staging-repos.ps1](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/scripts/initialize-split-staging-repos.ps1)
- [frontend_backend_cutover_runbook.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/plans/platform/frontend_backend_cutover_runbook.md)

## What These Assets Solve

- the frontend repo can be exported from a single manifest-driven subtree definition
- the platform repo can be exported from a manifest that includes runtime assets and split-critical docs
- the export process is repeatable and inspectable before writing anything
- the staging trees can be refreshed and bootstrapped into standalone Git repositories without ad-hoc manual steps
- the operational cutover sequence is documented with validation gates

## Current Readiness Boundary

Phase 6 is prepared, but not yet fully executed:

- export manifests exist
- export tooling exists
- cutover runbook exists
- validation gates for frontend/platform are already green from earlier phases
- staging exports have been generated inside the workspace:
  - [shiva-frontend](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/artifacts/split-staging/shiva-frontend)
  - [shiva-platform](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/artifacts/split-staging/shiva-platform)
- transient local artifacts were excluded from those staging trees:
  - frontend `.env`, `.next`, `node_modules`
  - controller `.next`, `server`, `server.exe`

Still intentionally not executed here:

- creating the real external Git repositories
- pushing extracted trees to their final remotes
- wiring production CI/CD secrets in the new repositories
- freezing or archiving the monorepo

## Practical Next Step

Refresh the staging trees, initialize them as standalone Git repositories, validate them there, and then create and push the final external repositories.
