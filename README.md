# Shiva Platform

Future standalone platform repository for the distributed k6 runtime.

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
