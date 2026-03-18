# Split Boundary Verification

This document captures the current verified boundary between the future `shiva-frontend` and `shiva-platform` repositories.

## Verified Contract Facts

- The frontend consumes the backend through the Next proxy under `/api/backend/...`.
- The frontend runtime contract uses:
  - `CONTROLLER_URL`
  - `CONTROLLER_API_KEY`
- The frontend-side contract is documented in:
  - [controller-integration.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/docs/controller-integration.md)
- The backend-side contract is documented in:
  - [frontend-api-contract.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/docs/frontend-api-contract.md)

## Verified Ownership Split

Frontend-owned:

- [frontend](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend)
- [frontend/playwright.config.cjs](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/playwright.config.cjs)
- [frontend/tests/e2e](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/tests/e2e)
- [frontend/docs](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend/docs)

Platform-owned:

- [controller](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller)
- [dummy-service](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/dummy-service)
- [loadbalancer](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/loadbalancer)
- [k6-scripts](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/k6-scripts)
- [docker-compose.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docker-compose.yml)

## Verified Validation Checks

### Frontend

- `pnpm build` from [frontend](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend): successful
- `playwright test --config .\\playwright.config.cjs --list` from [frontend](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/frontend): successful

### Backend

- `go test ./...` from [controller](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller): successful

## Boundary Conclusion

The repository is now in a state where:

- the frontend tree can be treated as a self-contained future repository root
- the controller/runtime tree can be treated as a self-contained future repository root
- the remaining split work is mainly extraction, CI/deployment setup, and cross-repo validation rather than architectural discovery
