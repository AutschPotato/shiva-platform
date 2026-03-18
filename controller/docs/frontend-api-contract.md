# Frontend-Facing API Contract

This document defines the controller responsibilities toward the future `shiva-frontend` repository.

## Environment Contract

The controller deployment must expose a stable HTTP base URL consumable by the frontend via `CONTROLLER_URL`.

Relevant controller/platform settings include:

- JWT/auth configuration
- optional API key configuration
- worker list and worker dashboard settings
- CORS origins for non-proxied access paths

## Supported API Areas

The controller must continue to provide the API groups currently wired in `internal/server/server.go`:

- auth and profile
- test execution and live metrics
- results
- templates
- schedules
- admin user management
- admin worker dashboards

## Stability Expectations

- Existing frontend-visible route groups should remain stable unless a documented contract update is made.
- Frontend DTOs are currently mirrored manually, so backend changes to response shapes must be intentional and documented.
- Admin worker dashboard proxy routes are split-critical and should be treated as externally consumed behavior.

## Local Integrated Deployment

For local and integrated environments, the controller remains the owner of:

- runtime orchestration
- worker communication
- summary/result collection
- dummy and load-balancer-backed local test setup
