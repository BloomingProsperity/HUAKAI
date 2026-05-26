# Deferred Review Finding: Hermes Runner Hash Lock

| Field | Value |
| --- | --- |
| Severity | S2 |
| Source | codex Slice 1.2 Round 1 review |
| Status | Owner-local follow-up after Slice 2.5 sandbox check |

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
blocked in this lane. This is not closed; it remains Owner-pending after
Slice 2.5.

## Slice 2.5 Sandbox Check

Still open. The Slice 2.5 cleanup lane did not change
`backend/deploy/hermes-runner/requirements.txt` or the Docker install path because
this sandbox still lacks `pip-compile`/`piptools` and cannot fetch PyPI metadata.
Owner should generate the transitive hash lock in a local networked environment
before production runner release.

## 2026-05-26 Slice 2.7 Sandbox Prep

Slice 2.7 把 sandbox 可做的部分先落:

1. 新增 `backend/deploy/hermes-runner/requirements.in` —— 直接依赖的 source-of-truth(内容与现 requirements.txt 一致,4 个直接 pin)。
2. 新增 `backend/deploy/hermes-runner/scripts/regen-hashlock.sh` —— 用 `python3.12 -m piptools compile --generate-hashes --resolver=backtracking` 把 requirements.in 编译为完整传递依赖 + sha256 哈希的 requirements.txt。脚本对 Python 版本(必须 3.12)+ pip-tools 缺失自动安装做了卫语句。
3. **`requirements.txt` 与 `Dockerfile` 当前不动** —— 防止沙箱误改后 Docker 构建直接挂掉。

### Owner 本机执行步骤

```bash
cd backend/deploy/hermes-runner
./scripts/regen-hashlock.sh
```

脚本输出末尾会报"pinned package lines"和"--hash lines"两个计数,正常情况后者应大于等于前者(每个包至少 1 个 sha256 哈希)。

### 验收门

Owner 跑完脚本后人工 diff `requirements.txt`,确认:
- 4 个直接依赖锁定版本未变(hermes-agent 0.14.0 / fastapi 0.136.3 / uvicorn 0.48.0 / sse-starlette 3.4.4)
- 所有包(含传递依赖)都带 `--hash=sha256:...` 行
- 没有意外引入 GPL / AGPL / SSPL 等不兼容许可证(`pip-licenses` 或人工抽查)

通过后,在 **repo root**(`cd ../..` 回到 `/path/to/HUAKAI`)继续:
1. `git add backend/deploy/hermes-runner/requirements.{in,txt}` + commit "hermes-runner Slice 2.7 hash-lock 生成 + 传递依赖哈希锁定"
2. 紧跟一 commit 切 Dockerfile 改为 `pip install --require-hashes --no-cache-dir -r requirements.txt`,跑一遍 `docker compose -f backend/docker-compose.dev.yml build hermes-runner`(从 repo root)或 `docker compose build hermes-runner`(从 `backend/`)确认通过。

### 仍然未闭合的部分

- pip-compile 是否引入 stale-transitives:依赖 Owner 周期性 rerun(推荐写进 release checklist)。
- 跨 OS 哈希一致性:Owner 本机和 CI 应该同为 linux x86_64;若 macOS/Windows 跑会生成不一致的 sha256(wheel 平台标记差异)。脚本第一行已锁 `python3.12` 版本对齐 Dockerfile,后续如需多平台再加 `--platform-tag` 控制。
