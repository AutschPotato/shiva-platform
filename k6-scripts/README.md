# k6 Runtime Scripts

This directory contains worker-side runtime files and generated script/config artifacts used by the platform.

Stable files that belong in the repository:

- `entrypoint.sh`
- `placeholder.js`
- `README.md`

Generated runtime files such as `current-test.js`, `config.json`, and `k6-env.sh` are created per test run and should exist locally at runtime, but should not be committed to the repository.
