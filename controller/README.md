# Shiva Platform

This directory is the future `shiva-platform` repository core.

It owns:

- controller API and orchestration logic
- k6 worker integration
- local runtime stack integration
- backend-facing API contract for the frontend

Related runtime assets that stay with the platform split:

- sibling `dummy-service/`
- sibling `loadbalancer/`
- sibling `k6-scripts/`
- platform compose files at the future repo root

## Build

```bash
go test ./...
go build ./cmd/server
```

Runtime config validation from the future platform repo root:

```bash
docker compose -f docker-compose.platform.yml config
```

## CI / Ownership

This platform side owns:

- controller build and Go test automation
- runtime compose validation
- controller/runtime deployment automation
- backend contract maintenance for frontend consumers

The prepared CI workflow for the future platform repo is:

- [.github/workflows/platform-ci.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/.github/workflows/platform-ci.yml)

## Contract

The frontend-facing controller contract is documented in:

- [docs/frontend-api-contract.md](./docs/frontend-api-contract.md)
- [docs/deployment.md](./docs/deployment.md)
- [docs/repo-extraction.md](./docs/repo-extraction.md)
- [.env.example](./.env.example)
