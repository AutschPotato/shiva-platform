# Platform Extraction Readiness

This document captures why the current platform-owned tree is now ready to be lifted into a standalone repository.

## Platform-Owned Areas

- [controller](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller)
- [dummy-service](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/dummy-service)
- [loadbalancer](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/loadbalancer)
- [k6-scripts](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/k6-scripts)
- [docker-compose.platform.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docker-compose.platform.yml)

## Key Improvement for Extraction

The platform side now has a compose candidate that does not depend on the frontend source tree:

- [docker-compose.platform.yml](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/docker-compose.platform.yml)

This removes the biggest structural blocker for extracting the platform side into its own repository.

## Repo-Local Preparation

The controller tree now contains:

- [README.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/README.md)
- [.env.example](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/.env.example)
- [.gitignore](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/.gitignore)
- [docs/frontend-api-contract.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/docs/frontend-api-contract.md)
- [docs/deployment.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/docs/deployment.md)
- [docs/repo-extraction.md](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller/docs/repo-extraction.md)

## Verified Checks

- `go test ./...` from [controller](/C:/Dev/CLAUDE/PROJECTS/K6-ADOPTION/k6-enterprise-suite-codex/controller): successful

## Extraction Conclusion

The remaining work for the platform split is now primarily:

- copying the platform-owned tree into a new repository
- wiring CI and deployment there
- validating the separately deployed backend against the separately deployed frontend
