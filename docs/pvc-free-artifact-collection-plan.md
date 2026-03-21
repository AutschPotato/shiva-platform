# PVC-Free Artifact Collection and Intelligent Completion Plan

## Status

This document is a planning artifact only. No implementation is described as already merged into `main`.

Current branch basis for the next implementation wave:

- Platform: `codex/pvc-free-artifact-collection`
- Frontend: `codex/pvc-free-artifact-collection`

Both branches were created from the current `codex/fix-rerun-prefill` feature branches because those changes are not merged into `origin/main` yet.

## Goals

We want to achieve all of the following:

1. Remove the production dependency on a shared RWX/PVC for worker result artifacts.
2. Replace the current fixed retry loop for summary collection with an intelligent, state-aware completion barrier.
3. Make incomplete worker artifact collection explicit and observable instead of silently accepting partial data.
4. Preserve a simple local smoke-test path with Docker Compose.
5. Keep a local optional shared-volume fallback only as a temporary debug/smoke aid, not as the target production architecture.

## Current Architecture and Pain Points

### What is already improved

Worker input distribution can already work without a shared script volume:

- workers can fetch `current-test.js`, `config.json`, and `k6-env.sh` from the controller
- this path is implemented in `k6-scripts/entrypoint.sh`
- the controller serves those files through `controller/internal/handler/scripts.go`

### What still depends on shared storage

Completion artifacts still depend on `/output` being shared between controller and workers:

- `summary-*.json`
- `payload-*.json`
- `auth-summary-*.json`

The controller currently finalizes runs by reading files from shared output storage.

### Why the current completion flow is brittle

The current implementation in `controller/internal/handler/test.go`:

- polls for summary files with a fixed retry loop
- accepts partial availability too easily
- does not validate artifact completeness against the expected worker set
- can persist results where only a subset of worker summaries is present

This is especially risky for:

- high-load runs
- delayed `handleSummary` writes
- slow worker shutdown and flush behavior
- temporary I/O or container scheduling stalls

## Target Architecture

## Overview

Move from:

- controller reads worker artifacts from shared filesystem

to:

- workers fetch runtime inputs from controller
- workers push completion artifacts back to controller
- controller finalizes only after an explicit run completion barrier is satisfied

## High-Level Flow

1. Controller prepares a run and records the expected worker set for `test_id`.
2. Workers fetch the run inputs from controller as they already can today.
3. Workers execute the run.
4. Each worker emits completion artifacts and uploads them back to the controller.
5. Controller tracks per-worker artifact receipt and terminal worker state.
6. Controller finalizes the run only when:
   - all expected workers have submitted required artifacts, or
   - a state-aware timeout policy expires and the run is explicitly marked partial

## Production Storage Model

Recommended production model:

- no RWX shared PVC between controller and workers
- controller receives artifacts via HTTP
- controller persists:
  - merged result data in the database
  - raw worker artifacts either
    - in database tables for phase 1, or
    - in object storage such as S3/MinIO for phase 2

Recommended sequence:

- Phase 1: controller-managed artifact persistence in DB or local controller disk
- Phase 2: optional object storage backend behind a small abstraction

## Local Docker Compose Model

Recommended local default:

- use the same controller-fetch plus worker-push architecture as production

Optional local fallback:

- keep the current shared-volume artifact path available behind a Compose override for debugging only

Reasoning:

- local smoke tests should exercise the same architecture as Kubernetes by default
- the shared-volume mode remains useful temporarily for diagnostics and migration comparison

## Detailed Design

## 1. Run Completion Registry in the Controller

Add a controller-side run completion registry keyed by `test_id`.

Each run should track:

- expected worker IDs
- expected worker count
- run start time
- execution mode and executor type
- whether each worker has:
  - reached terminal runtime state
  - uploaded summary artifact
  - uploaded auth summary artifact, if auth is configured
  - uploaded payload artifact, if applicable
- per-worker upload timestamps
- per-worker validation errors
- completion state:
  - `running`
  - `collecting`
  - `complete`
  - `partial`
  - `timed_out`

Recommended implementation location:

- new controller package, for example `controller/internal/completion`

This keeps the new state machine out of `handler/test.go` and avoids growing that file into a larger god-file.

## 2. Internal Artifact Upload API

Add internal controller endpoints for worker artifact upload.

Recommended endpoints:

- `POST /api/internal/runs/{test_id}/workers/{worker_id}/summary`
- `POST /api/internal/runs/{test_id}/workers/{worker_id}/auth-summary`
- `POST /api/internal/runs/{test_id}/workers/{worker_id}/payload`
- optional combined endpoint:
  - `POST /api/internal/runs/{test_id}/workers/{worker_id}/artifacts`

Recommended payload fields:

- `worker_id`
- `artifact_type`
- `content_type`
- `content`
- `checksum`
- `size_bytes`
- `finished_at`

Behavior requirements:

- idempotent uploads
- same worker may retry safely
- controller rejects artifacts for unknown `test_id`
- controller rejects uploads from unknown worker IDs for the run
- controller logs duplicates and mismatches clearly

Authentication between worker and controller:

- Phase 1 local/simple: internal network trust plus unguessable test-scoped token
- Phase 2 hardened: internal shared secret or signed upload token

## 3. Worker Artifact Publisher

The worker runtime must stop relying on shared `/output` as the primary result handoff.

Recommended approach:

- keep `handleSummary` generating the artifact content in the worker
- add a small post-processing/publish step in the worker startup/runtime path
- upload artifacts back to controller after k6 exits

Possible implementation options:

1. shell-based uploader in `k6-scripts/entrypoint.sh`
2. lightweight helper binary or script inside `k6-worker`

Recommended choice:

- a small dedicated helper in `k6-worker` or a robust shell uploader wrapper

Reason:

- upload retries, validation, and better error reporting become much easier than with ad-hoc shell only

## 4. Intelligent Completion Barrier

Replace the fixed `45 x 1s` retry loop with a state-aware barrier.

The controller should finalize a run based on:

- expected worker set
- worker terminal state observation
- artifact upload completeness
- bounded grace windows

Recommended logic:

1. Enter `collecting` only after run execution is considered finished by orchestrator.
2. Determine expected workers from the actual worker set assigned to the run.
3. Wait until every expected worker is either:
   - terminal and fully uploaded, or
   - terminal and still missing artifacts
4. Start a grace window only after all workers are terminal or unreachable in a terminal-consistent way.
5. Finalize as:
   - `complete` if all required artifacts arrived
   - `partial` if grace window expired with missing worker artifacts

Recommended grace window formula:

- base: 10 seconds
- plus dynamic component based on observed run duration
- capped upper bound: 90 seconds

Example:

- `grace = max(10s, min(90s, duration * 0.15))`

Optional load-aware extension:

- increase grace modestly when:
  - worker count is high
  - aggregate request volume is high
  - auth metrics indicate retry pressure

Important rule:

- do not finalize merely because at least one summary exists
- finalize only against expected completeness or explicit timeout classification

## 5. Result Quality and Observability

The result model must explicitly report partial worker data.

Add fields or quality metadata such as:

- `expected_worker_count`
- `received_worker_summary_count`
- `received_auth_summary_count`
- `missing_workers`
- `artifact_collection_status`

Recommended statuses:

- `complete`
- `partial`
- `missing`

Metrics quality flags should distinguish:

- no summary available
- partial worker summaries
- full worker summaries available

Example new quality keys:

- `summary_artifact_completeness`
- `workers_incomplete`
- `auth_summary_incomplete`

## 6. Frontend Impact

Frontend changes should remain focused and explicit.

Affected areas:

- result detail page
- worker metrics view
- warnings and quality indicators

Recommended behavior:

- show when worker drilldown is partial
- show expected vs received worker artifacts
- do not imply complete worker coverage when only a subset was received

Potential UI additions:

- small warning badge in result summary
- expandable diagnostics block:
  - expected workers
  - received workers
  - missing workers
  - artifact collection status

## Migration Strategy

## Phase 0: Instrument and Detect

Goal:

- make incompleteness visible before changing the transport model

Tasks:

- compare expected worker count vs parsed summary worker count
- add result quality flags for partial worker summaries
- add logs and diagnostics for missing worker artifacts

Benefits:

- immediate transparency
- lower-risk first step

## Phase 1: Controller Completion Registry

Goal:

- introduce structured completion tracking before changing worker upload transport

Tasks:

- implement run completion registry
- record expected worker set per run
- introduce explicit completion states
- keep current shared-volume reading path temporarily

Benefits:

- decouples barrier logic from file polling
- prepares controller for push-based uploads

## Phase 2: Worker Push Uploads

Goal:

- move artifact handoff from shared storage to controller upload

Tasks:

- add internal artifact upload endpoints
- add worker artifact publishing logic
- make controller barrier consume uploaded artifacts
- keep shared-volume ingestion as temporary fallback

Benefits:

- production no longer depends on RWX shared storage

## Phase 3: Default PVC-Free Runtime

Goal:

- make controller-fetch plus worker-push the default execution model

Tasks:

- switch Kubernetes manifests to HTTP-based artifact flow
- remove production dependency on shared `/output`
- keep local Compose shared-volume mode only as optional override

## Phase 4: Cleanup and Hardening

Goal:

- simplify the codebase after successful rollout

Tasks:

- remove legacy shared-volume completion path from production code
- tighten upload authentication
- optionally move raw artifacts to object storage abstraction

## Repository Breakdown

## Platform Repository

Expected main work:

- completion state package
- internal artifact upload handlers
- worker runtime upload path
- result persistence changes
- quality flag enrichment
- local and Kubernetes runtime toggles

Likely affected areas:

- `controller/internal/handler/test.go`
- `controller/internal/handler/result.go`
- `controller/internal/handler/metrics_v2.go`
- `controller/internal/scriptgen/...`
- `controller/internal/orchestrator/...`
- `k6-scripts/entrypoint.sh`
- `k6-worker/...`
- `docker-compose.yml`
- Kubernetes deployment manifests if present outside the split repo

## Frontend Repository

Expected focused work:

- result detail completeness warning
- worker metrics completeness indicators
- optional diagnostics display for missing artifacts

Likely affected areas:

- result detail page
- API typings
- Playwright result coverage

## Test Strategy

## Platform Tests

Add targeted tests for:

- completion registry state transitions
- artifact upload idempotency
- invalid worker upload rejection
- complete vs partial finalization
- expected worker count mismatch detection
- legacy fallback behavior during migration phase

Add integration-style tests for:

- all workers upload successfully
- one worker uploads late but inside grace window
- one worker never uploads and run finalizes as partial
- auth summary present for only subset of workers

## Frontend Tests

Add UI coverage for:

- result page displays partial collection warning
- worker diagnostics show expected vs received count
- no warning shown for complete runs

Add Playwright coverage for:

- completed run with simulated partial worker summaries
- completed run with complete worker summaries

## Local Smoke Tests

Support two Compose modes:

1. default pvc-free smoke mode
2. optional shared-volume fallback mode

Smoke test expectations:

- run completes with full worker artifacts in default mode
- partial artifact scenario is visible and classified correctly
- rerun and result views continue to work

## Risks and Mitigations

## Risk: controller becomes artifact bottleneck

Mitigation:

- keep uploads small
- use per-worker compressed payloads if needed
- make raw artifact persistence abstractable for future object storage

## Risk: duplicate or retried uploads corrupt state

Mitigation:

- idempotent artifact writes
- checksum and worker/test scoping

## Risk: worker crash after run end but before upload

Mitigation:

- explicit partial run classification
- missing worker diagnostics
- bounded grace window

## Risk: local and production drift

Mitigation:

- make pvc-free mode the local default
- keep shared-volume mode only as named override

## Recommended Immediate Next Steps

1. Implement Phase 0 first to expose completeness gaps without changing transport.
2. Build the completion registry before introducing new worker upload behavior.
3. Move artifact transport to controller push in a backward-compatible phase.
4. Keep the current shared-volume mode only as a temporary fallback while validating the new flow.
5. Do not merge this work directly from `main`; continue from the current `codex/pvc-free-artifact-collection` branches in both repos.
