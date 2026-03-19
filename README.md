# Shiva Platform
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/AutschPotato/shiva-platform)

Future standalone platform repository for the distributed k6 runtime.

## Quick Start For Windows

Prerequisites:

- Docker Desktop

From the repo root in PowerShell:

```powershell
docker compose -f .\docker-compose.yml up -d --build
```

Open after startup:

- Controller API: `http://localhost:8080`
- Target load balancer: `http://localhost:8090`

Stop the platform stack again:

```powershell
docker compose -f .\docker-compose.yml down
```

This starts the local integrated platform stack with MySQL, controller, workers, dummy targets, and the target load balancer.

## Working Model

- This repository is the platform single source of truth for controller, workers, and runtime infrastructure.
- Do not continue backend or runtime feature work in the legacy monorepo or in any exported staging directory.
- Use short-lived feature branches such as `feature/...`, `bugfix/...`, or `chore/...`; keep `main` releasable.
## Owns

- `controller/`
- `dummy-service/`
- `loadbalancer/`
- `k6-scripts/`
- `docker-compose.yml`
- platform-specific documentation and CI

## Primary Validation Commands

From the repo root:

```bash
docker compose -f docker-compose.yml config
```

From `controller/`:

```bash
go test ./...
go build ./cmd/server
```

## Runtime Scope

This repository is intended to own:

- controller API deployment
- worker/runtime orchestration
- local integration stack for MySQL, workers, LB, and dummies
- backend-facing API contract maintenance for the frontend

## Source Documents

Key split and readiness references:

- `controller/README.md`
- `controller/docs/frontend-api-contract.md`
- `controller/docs/deployment.md`
- `docs/plans/platform/frontend_backend_split_execution_plan.md`
- `docs/reference/architecture/separate_deployment_validation.md`

