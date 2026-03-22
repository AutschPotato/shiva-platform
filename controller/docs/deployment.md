# Platform Deployment Notes

This backend/platform side is intended to be deployable without the frontend repository.

## Core Runtime Pieces

- controller API
- MySQL
- k6 workers
- k6 runtime scripts
- dummy service
- target load balancer

For local integrated usage, these are currently wired together in the current repo-root `docker-compose.yml`.

For platform-only extraction readiness, use the current repo-root `docker-compose.platform.yml` as the compose candidate that does not require the frontend source tree.

## Required Configuration

Use [../.env.example](../.env.example) as the baseline for controller-side deployment configuration.

Worker startup readiness tuning:

- `K6_WORKER_READY_TIMEOUT_SEC=0` keeps adaptive timeout mode (recommended default).
- Set a value `> 0` only if you need a fixed readiness timeout override for specific environments.

Frontend deployments consume this backend through:

- controller base URL
- optional controller API key
- JWT-based authentication flows
- admin dashboard proxy endpoints
