# Platform Repo Extraction Checklist

This checklist describes the minimum expectations when the platform-owned tree is lifted into a standalone repository.

## Expected Repo Root Contents

- `.github/workflows/platform-ci.yml`
- `controller/`
- `dummy-service/`
- `loadbalancer/`
- `k6-scripts/`
- `docker-compose.platform.yml`
- documentation for controller/runtime ownership

If this tree becomes the active future platform repository, `docker-compose.platform.yml` can be promoted to the repo's main compose entrypoint.

## Required Commands

- `go test ./...` from `controller/`
- `go build ./cmd/server` from `controller/`
- `docker compose -f docker-compose.platform.yml config` from the platform repo root

## CI Ownership

The extracted platform repo should own:

- controller build and test automation
- runtime compose validation
- deployment automation for controller/runtime assets

The prepared workflow for this split lives at:

- [platform-ci.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/.github/workflows/platform-ci.yml)

When the platform tree is lifted into its own repository, that workflow should move to the new repo root under `.github/workflows/platform-ci.yml`.

## Required Runtime Contract

- controller exposes the API consumed by the frontend contract
- worker/dashboard settings remain configurable
- runtime assets remain sibling directories to `controller/`

## Non-Goals

- No controller/runtime redesign during extraction
- No movement of dummy/LB/worker assets into separate repositories
- No direct browser-to-controller rewrite

## Cutover Support

Phase 6 provides a manifest-driven export path for the actual repo split:

- [platform_repo_manifest.json](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/platform_repo_manifest.json)
- [export-split-repo.ps1](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/scripts/export-split-repo.ps1)
