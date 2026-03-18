# Frontend / Backend Split Execution Plan

## Goal

Execute the prepared split so that:

- the frontend can live in its own repository and directory tree
- the backend/platform can live in its own repository and directory tree
- the frontend can be deployed independently
- the frontend can be tested against a separately deployed backend
- the existing proxy/BFF model remains intact

This plan assumes the contract groundwork already exists in:

- [frontend/docs/controller-integration.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/docs/controller-integration.md)
- [controller/docs/frontend-api-contract.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/docs/frontend-api-contract.md)

## Execution Status

- Phase 1: completed
- Phase 2: completed
- Phase 3: completed
- Phase 4: completed
- Phase 5: completed
- Phase 6: in_progress

---

## Target End State

### Future Repo A: `shiva-frontend`

Owns:

- `frontend/`

Contained tree after extraction:

- application source
- frontend-local docs
- frontend-local Playwright config and tests
- frontend-local artifact folders
- frontend-local CI and deployment config

Deployment target:

- standalone Next.js deployment
- configured with `CONTROLLER_URL`
- uses its internal Next proxy for controller access

### Future Repo B: `shiva-platform`

Owns:

- `controller/`
- `dummy-service/`
- `loadbalancer/`
- `k6-scripts/`
- `docker-compose.yml`
- backend/platform docs

Deployment target:

- controller API
- local runtime/integration stack
- workers, dummy targets, and target load balancer

---

## Phase 1 - Freeze and Verify the Split Boundary

Before moving anything again, confirm that the split-critical boundary stays stable.

### Deliverables

- Frontend consumes controller only through:
  - `CONTROLLER_URL`
  - `CONTROLLER_API_KEY`
  - `/api/backend/...` in the Next proxy
- Backend remains source of truth for:
  - auth endpoints
  - test control
  - live metrics
  - results
  - templates
  - schedules
  - admin worker dashboards

### Validation

- Frontend build works from `frontend/`
- Playwright discovery works from `frontend/`
- Backend tests work from `controller/`
- Docs mention the same env names and same route groups on both sides

### Acceptance Criteria

- No frontend feature depends on importing backend code directly
- No backend feature depends on frontend code or build output
- Split-critical env names are consistent across docs and config

### Phase 1 Completion Notes

Phase 1 is complete based on the current repository state:

- Frontend-owned Playwright now runs from `frontend/`
- split-critical env names are aligned on `CONTROLLER_URL` and `CONTROLLER_API_KEY`
- frontend and backend contract documents exist on both sides
- frontend build and Playwright discovery were re-validated from `frontend/`
- backend tests were re-validated from `controller/`

Verification details are captured in:

- [split_boundary_verification.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/split_boundary_verification.md)

---

## Phase 2 - Extract the Frontend Directory Tree

Create the future frontend repository from the current `frontend/` subtree.

### Source of Truth

Primary contents to extract:

- `frontend/`

No root-level Playwright or root-level npm artifacts should be required anymore.

### Required Repo Contents

The new frontend repo root must contain:

- Next.js app source
- `package.json`
- `pnpm-lock.yaml`
- `playwright.config.cjs`
- `tests/e2e`
- `artifacts/README.md`
- `docs/controller-integration.md`
- `README.md`
- `.env.example`
- frontend Dockerfile

### Required Adjustments in the Extracted Repo

- Ensure path references are repo-local, not monorepo-local
- Ensure Playwright output paths remain local to the frontend repo
- Ensure README describes running against an external backend deployment
- Ensure no documentation still references monorepo root Playwright commands

### Acceptance Criteria

- `pnpm install`
- `pnpm build`
- `pnpm test:e2e --list`

all work from the extracted frontend repo root alone

### Phase 2 Completion Notes

Phase 2 is complete based on the current repository state:

- Playwright ownership lives fully under `frontend/`
- frontend-local `.gitignore` and CI workflow scaffolding exist
- frontend docs were rewritten to be repo-local instead of monorepo-root oriented
- the frontend Docker build no longer depends on copying a local `.env` file into the image
- the frontend tree now contains an explicit extraction checklist

Verification details are captured in:

- [frontend_extraction_readiness.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/frontend_extraction_readiness.md)

---

## Phase 3 - Extract the Platform Directory Tree

Create the future platform repository from the controller/runtime assets.

### Source of Truth

Primary contents to extract:

- `controller/`
- `dummy-service/`
- `loadbalancer/`
- `k6-scripts/`
- `docker-compose.yml`

Optional docs to copy with it:

- relevant plan/reference docs under `docs/`
- top-level platform README content as needed

### Required Repo Contents

The new platform repo root must contain:

- controller source and Dockerfile
- runtime assets
- local compose stack
- backend docs
- `.env.example` for controller-side deployment

### Required Adjustments in the Extracted Repo

- Ensure compose paths still resolve relative to the new repo root
- Ensure backend docs do not assume frontend source is present locally
- Keep the worker/dashboard and local target stack behavior unchanged

### Acceptance Criteria

- `go test ./...` in `controller/`
- `go build ./cmd/server` in `controller/`
- local compose stack still starts from the platform repo root

### Phase 3 Completion Notes

Phase 3 is complete based on the current repository state:

- platform-owned runtime directories are explicitly documented
- controller now has repo-local extraction docs and ignore rules
- a platform-only compose candidate exists that no longer depends on the frontend source tree
- the backend/platform side is documented as a future standalone repository tree

Verification details are captured in:

- [platform_extraction_readiness.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/platform_extraction_readiness.md)

---

## Phase 4 - Separate Deployment Validation

Prove the split works operationally, not only structurally.

### Backend Deployment

Deploy the platform/backend first and expose:

- controller base URL
- reachable auth endpoints
- reachable dashboard proxy endpoints through the controller

### Frontend Deployment

Deploy the frontend separately with:

- `CONTROLLER_URL` pointing at the deployed backend
- optional `CONTROLLER_API_KEY` if used in the environment

### Validation Scenarios

Run these against the separately deployed pair:

1. Login
2. Run test from builder
3. Re-run an old result
4. Run from template
5. Result list and result detail
6. Schedule create/update/run-now
7. Admin user management
8. Admin system-template promote/export/import
9. Admin worker dashboard launcher and proxied dashboard access

### Acceptance Criteria

- Frontend can be deployed without bundling backend code
- Backend can be deployed without bundling frontend code
- End-to-end behavior remains unchanged for users

### Phase 4 Completion Notes

Phase 4 is complete and now has both a reproducible smoke-validation path and a separate write-flow validator:

- the platform/backend side starts independently via `docker-compose.platform.yml`
- the frontend side starts independently via `frontend/docker-compose.frontend.yml`
- the frontend tree now contains a repo-local smoke checker:
  - `pnpm validate:separate-deployment`
- the frontend tree also contains a repo-local write-flow checker:
  - `pnpm validate:separate-deployment:writes`
- live smoke validation on 2026-03-18 confirmed:
  - controller health
  - frontend login page reachability
  - login through the frontend proxy
  - profile summary through the frontend proxy
  - result list through the frontend proxy
  - template list through the frontend proxy
  - schedule list through the frontend proxy
  - admin system-template export through the frontend proxy
  - admin worker-dashboard listing through the frontend proxy
  - admin user listing through the frontend proxy
- live write-flow validation on 2026-03-18 confirmed:
  - builder run creation
  - result re-run
  - template creation from a saved result
  - template-based run
  - schedule create
  - schedule update
  - schedule run-now
  - schedule execution history linkage
  - cleanup of temporary validation templates and schedules

The write-flow validator intentionally runs real distributed tests through the frontend proxy, waits for final result persistence, and verifies schedule execution history before cleaning up its own temporary template/schedule artifacts.

---

## Phase 5 - CI/CD and Ownership Split

Once both extracted trees work independently, align ownership and pipelines.

### Frontend Repo Responsibilities

- frontend build pipeline
- Playwright pipeline
- frontend deployment pipeline
- frontend docs and env contract maintenance

### Platform Repo Responsibilities

- Go test/build pipeline
- runtime/integration validation
- controller deployment pipeline
- compose/local infrastructure ownership
- API contract maintenance for frontend consumers

### Required CI Checks

Frontend:

- dependency install
- build
- Playwright discovery
- optional selected E2E smoke set

Backend:

- Go tests
- build
- optional compose smoke checks

### Acceptance Criteria

- Each repo can be validated independently in CI
- No pipeline depends on files from the other repo

### Phase 5 Completion Notes

Phase 5 is complete based on the current repository state:

- frontend ownership is documented in repo-local docs and keeps its own workflow:
  - `frontend/.github/workflows/frontend-ci.yml`
- platform ownership is now documented in repo-local docs
- an extraction-ready platform workflow now exists:
  - `.github/workflows/platform-ci.yml`
- the platform workflow validates:
  - `go test ./...` from `controller/`
  - `go build ./cmd/server` from `controller/`
  - `docker compose -f docker-compose.platform.yml config`
- the Phase 5 readiness summary is captured in:
  - [phase5_ci_ownership_readiness.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/phase5_ci_ownership_readiness.md)

---

## Phase 6 - Controlled Cutover

After both repos are live and validated, move the team to the new structure.

### Cutover Steps

1. Freeze active feature work briefly
2. Create both new repositories from the prepared trees
3. Push the extracted histories or initial snapshots
4. Wire CI/CD and deployment secrets
5. Run separate deployment validation
6. Announce the new source-of-truth repos
7. Archive or freeze the old monorepo

### Monorepo Handling

Recommended:

- keep the monorepo only as a temporary migration artifact
- mark it read-only after successful cutover
- do not continue feature work in parallel across old and new structures

### Phase 6 Progress Notes

Phase 6 is now prepared with concrete cutover assets:

- frontend export manifest:
  - [frontend_repo_manifest.json](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/frontend_repo_manifest.json)
- platform export manifest:
  - [platform_repo_manifest.json](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/platform_repo_manifest.json)
- manifest-driven export helper:
  - [export-split-repo.ps1](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/scripts/export-split-repo.ps1)
- staging refresh helper:
  - [prepare-split-staging.ps1](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/scripts/prepare-split-staging.ps1)
- staging repo bootstrap helper:
  - [initialize-split-staging-repos.ps1](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/scripts/initialize-split-staging-repos.ps1)
- operational cutover runbook:
  - [frontend_backend_cutover_runbook.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/plans/platform/frontend_backend_cutover_runbook.md)
- Phase 6 readiness summary:
  - [phase6_cutover_readiness.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docs/reference/architecture/phase6_cutover_readiness.md)
- staging exports were also generated locally for inspection:
  - [shiva-frontend](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/artifacts/split-staging/shiva-frontend)
  - [shiva-platform](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/artifacts/split-staging/shiva-platform)

Phase 6 is intentionally still `in_progress`, because the external actions are not performed inside this workspace:

- creating the real remote repositories
- exporting into final staging directories outside the monorepo
- pushing the two extracted trees
- wiring CI/CD secrets in the new repositories
- freezing or archiving the monorepo

---

## Testing Matrix

### Frontend-only readiness

- build from frontend root
- Playwright discovery from frontend root
- `.env.example` matches actual runtime variable names

### Backend-only readiness

- Go tests from controller root
- build from controller root
- compose startup from platform repo root

### Cross-repo regression

- auth login and session persistence
- builder runs with and without auth
- template flows
- schedule flows
- metrics/result pages
- admin dashboards
- result exports

### Split-specific regression risks

- broken proxy path assumptions
- stale env names
- dashboard proxy path drift
- DTO drift between backend responses and frontend expectations
- old docs/scripts still pointing to monorepo root

---

## Explicit Decisions

- Use **2 repositories**, not 3
- Keep the **Next proxy/BFF**
- Keep **dummy-service**, **loadbalancer**, **k6-scripts**, and **compose** with the platform repo
- Keep **Playwright** with the frontend repo
- Do not introduce a shared contracts package in this phase
- Do not redesign the controller/runtime architecture during the split

---

## Definition of Done

The split can be considered complete when:

1. The frontend tree can be copied to a new repo and built/tested on its own
2. The backend/platform tree can be copied to a new repo and built/run on its own
3. The frontend can be deployed against a separately deployed backend
4. The main authenticated, admin, metrics, template, schedule, and dashboard flows still work
5. CI and deployment ownership are cleanly separated
6. The monorepo is no longer needed as the active source of truth
