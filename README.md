# Shiva Platform
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/AutschPotato/shiva-platform)

Future standalone platform repository for the distributed k6 runtime.

## License and Legal Notice

This repository is proprietary and is not released under an open source license.

- Copyright (c) 2026 AutschPotato. All rights reserved.
- No permission is granted to use, copy, modify, merge, publish, distribute, sublicense, sell, or create derivative works from this source code without prior written permission from the copyright holder.
- Access to the repository, source visibility, or the ability to open pull requests does not grant any separate license or usage right.

See [LICENSE.md](./LICENSE.md) for the repository-wide notice.

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

## Optional Local Fetch Test

The default local Docker Compose setup continues to use the shared `/scripts` volume and does not enable the worker fetch mode.

If you want to smoke-test the opt-in worker fetch flow locally, use the versioned override at `.local/docker-compose.fetch.override.yml` together with the normal stack:

```powershell
docker compose -f .\docker-compose.yml -f .\.local\docker-compose.fetch.override.yml up -d mysql controller worker1 target-lb dummy1
```

The override file is kept in the repository on purpose so the fetch-test setup stays reproducible across machines. Only the writable runtime directory `.local/fetch-worker-scripts/` is ignored by Git. Detailed fetch-mode notes live in [`k6-scripts/README.md`](./k6-scripts/README.md).

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

