# k6 Runtime Scripts

This directory contains worker-side runtime files and generated script/config artifacts used by the platform.

Stable files that belong in the repository:

- `entrypoint.sh`
- `placeholder.js`
- `README.md`

Generated runtime files such as `current-test.js`, `config.json`, and `k6-env.sh` are created per test run and should exist locally at runtime, but should not be committed to the repository.

## Controller Fetch Mode

The worker entrypoint supports a fetch mode for deployments where workers do not share the controller's `/scripts` volume.

- Enable the fetch with `SHIVA_FETCH_SCRIPTS_FROM_CONTROLLER=true` (or `1`).
- Point the worker at the controller with `CONTROLLER_URL`, for example `http://controller:8080`.
- The versioned `docker-compose.yml` now enables fetch mode by default for workers.

When fetch mode is enabled, the worker downloads:

- `current-test.js` as a required file with retry logic
- `config.json` as an optional file
- `k6-env.sh` as an optional file

The worker's local `/scripts` path must be writable in fetch mode.

## Local Fetch Smoke Test

The default compose already runs in fetch mode. The versioned override at `.local/docker-compose.fetch.override.yml` is useful when you want a smaller single-worker smoke setup.

The override file is intentionally kept in the repository so the fetch-test setup is reproducible across machines. Only the writable runtime directory `.local/fetch-worker-scripts/` is ignored by Git.

Override contents:

```yaml
services:
  controller:
    environment:
      K6_WORKERS: "worker1:6565"

  worker1:
    environment:
      SHIVA_FETCH_SCRIPTS_FROM_CONTROLLER: "true"
      CONTROLLER_URL: "http://controller:8080"
    volumes:
      - ./.local/fetch-worker-scripts:/scripts
      - k6-output:/output
```

Suggested local test flow:

1. Create the writable local scripts directory, for example `mkdir -p .local/fetch-worker-scripts`.
2. Start a minimal stack with the override:

```sh
docker compose -f docker-compose.yml -f .local/docker-compose.fetch.override.yml up -d mysql controller worker1 target-lb dummy1
```

3. Run a smoke test against the platform.
4. Stop the stack when done.

## Optional Shared-Volume Compatibility Mode

If you need to validate legacy behavior with a shared `/scripts` volume, use `.local/docker-compose.shared-volume.override.yml`.

That override explicitly disables fetch mode (`SHIVA_FETCH_SCRIPTS_FROM_CONTROLLER=false`) and mounts `./k6-scripts` into each worker.
