# Separate Deployment Validation Readiness

This document captures the current readiness for validating frontend and backend as separately started deployments.

## Frontend-Side Deployment Artifact

The frontend tree now contains:

- [docker-compose.frontend.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/docker-compose.frontend.yml)

This allows the frontend to be started independently from the backend/platform stack while still pointing to the controller over an explicit runtime URL.

## Backend-Side Deployment Artifact

The platform side now contains:

- [docker-compose.platform.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docker-compose.platform.yml)

This allows the controller/runtime stack to be started without depending on the frontend source tree.

## Validation Guidance

The frontend-side validation guide is documented in:

- [frontend/docs/separate-deployment-validation.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/docs/separate-deployment-validation.md)

## Expected Validation Pair

- Backend/platform deployment:
  - controller on `localhost:8080`
- Frontend deployment:
  - frontend on `localhost:3001`

## Intended Outcome

If both compose entrypoints start successfully and the documented flows work, the split is validated operationally rather than only structurally.

## Current Validation Status

As of 2026-03-18, the following separate-deployment smoke path has been validated successfully:

- backend/platform started independently via `docker-compose.platform.yml`
- frontend started independently via `frontend/docker-compose.frontend.yml`
- direct controller health on `http://localhost:8080/api/health`
- frontend login page on `http://localhost:3001/login`
- login through the frontend proxy
- profile summary through the frontend proxy
- result list through the frontend proxy
- template list through the frontend proxy
- schedule list through the frontend proxy
- admin system-template export through the frontend proxy
- admin worker-dashboard listing through the frontend proxy
- admin user listing through the frontend proxy

Observed smoke output from the live validation pair:

- controller health: `ok`
- frontend login page: `HTTP 200`
- login user: `admin`
- profile user: `admin`
- dashboard workers visible: `10`

The live dataset used for validation was still mostly empty, so zero-count responses for results, templates, schedules, and system-template export were considered valid smoke outcomes for this phase.

## Write-Flow Validation

To close the remaining Phase 4 gap, the frontend repo now also contains a dedicated write-flow validator:

- [validate-separate-deployment-write-flows.mjs](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/scripts/validate-separate-deployment-write-flows.mjs)

It is exposed via:

- `pnpm validate:separate-deployment:writes`

That validator uses the frontend proxy against the separately deployed controller and covers:

- builder run creation
- result-derived re-run payloads
- template creation plus template-derived run payloads
- schedule create/update/run-now plus execution history verification
- cleanup of validation templates and schedules

## Phase 4 Completion Status

As of 2026-03-18, the write-heavy split flows were also validated successfully against the separate deployment pair:

- builder run creation
- result re-run
- template creation from a saved result
- template-based run
- schedule create
- schedule update
- schedule run-now
- schedule execution history linkage
- cleanup of temporary validation schedules/templates

Observed write-flow output included these completed test IDs:

- builder run: `dd0095be-2ddc-4d77-b14b-fe7169222b3f`
- result re-run: `9166327e-36a5-45dd-b6bc-73dffa61e1c2`
- template-based run: `39fc9070-f0da-4c29-aa4e-dacf76117c34`
- schedule run-now: `8a84aabb-b049-490e-873d-57abc4acdbc3`

With those results, Phase 4 is now operationally complete for the current split plan.
