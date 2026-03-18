# Publishing Shiva Platform

This file is intended to live in the root of the extracted `shiva-platform` repository.

## Before First Push

Validate the exported tree:

```bash
docker compose -f docker-compose.yml config
```

From `controller/`:

```bash
go test ./...
go build ./cmd/server
```

## Suggested Git Bootstrap

```bash
git init -b main
git add .
git commit -m "Initial platform split"
git remote add origin <your-platform-remote>
git push -u origin main
```

## After First Push

1. Configure repository secrets and variables
2. Enable the platform CI workflow
3. Wire controller/runtime deployment targets
4. Re-run separate deployment validation against the published frontend/backend pair

## Ownership Reminder

This repository should own:

- controller API
- worker/runtime assets
- dummy service and load balancer
- platform CI/CD
- backend API contract maintenance for frontend consumers
