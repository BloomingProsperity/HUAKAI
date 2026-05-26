# Deferred Review Finding: Hermes Runner Hash Lock

| Field | Value |
| --- | --- |
| Severity | S2 |
| Source | codex Slice 1.2 Round 1 review |
| Status | Owner-pending blocker, escalated to Slice 2.5 hard fix |

Deferred review findings:
- [S2] Hermes runner currently uses direct dependency pins without a complete transitive hash lock — source: codex Slice 1.2 Round 1 review; rationale: deploy hardening is required for the production path, but Slice 1.2 is a dev compose runner and direct pins keep the increment closed; follow-up: Slice 2 production Hermes build path must generate a full `pip-compile --generate-hashes` lock and restore `pip install --require-hashes`; Owner decision: none for this commit.
- [S1] Slice 2.2.a added `sse-starlette==3.4.4` while the runner still lacks a full transitive hash lock — source: Slice 2.2.a Round 2 review; rationale: the previous "deferred to Slice 2" commitment has reached its due point, but the current sandbox has no `pip-compile`/`piptools` and cannot fetch PyPI metadata to generate hashes; follow-up: Slice 2.5 cleanup is a hard blocker to create `backend/deploy/hermes-runner/requirements.lock`, switch Docker install to `--require-hashes`, and remove warning comments; Owner decision: pending before any production Hermes runner release.

## Problem

`pip install --require-hashes` requires hashes for every package in the resolved
dependency graph. Slice 1.2 only pinned the direct Hermes runner dependencies, so
the build path omitted required hashes for transitive dependencies such as the
FastAPI and Uvicorn runtime stack.

## Why This Does Not Block Slice 1.2

Slice 1.2 uses the Hermes runner in the dev compose path. The immediate blocker is
that `docker compose build hermes-runner` cannot complete when `--require-hashes`
is enabled without a full transitive hash lock. Direct version pins are retained
for the dev path, and production dependency hardening is deferred rather than
dropped.

## Required Follow-Up

Slice 2.5 must generate a complete hash-locked requirements file with all
transitive dependencies included before the Hermes runner is treated as
production-release ready. Until then, `backend/deploy/hermes-runner/requirements.txt`
and `backend/deploy/hermes-runner/Dockerfile` carry explicit warnings.

Suggested template:

```bash
python -m pip install pip-tools
pip-compile --generate-hashes --output-file requirements.txt requirements.in
docker compose build hermes-runner
```

After the full lock exists, restore the Docker build command to use
`pip install --require-hashes --no-cache-dir -r requirements.txt`.

## Round 2 Escalation Note

Attempted in Slice 2.2.a Round 3 sandbox:

```bash
cd backend/deploy/hermes-runner && python3 -m piptools compile --version
cd backend/deploy/hermes-runner && pip-compile --version
```

Both commands were unavailable (`No module named piptools`, `pip-compile: command
not found`). With network access restricted, generating trustworthy hashes is
blocked in this lane. This is not closed; it is explicitly Owner-pending for
Slice 2.5.
