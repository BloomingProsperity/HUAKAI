# DEFERRED — Hermes Slice 2.1 Round 3 Review Tail

**Created**: 2026-05-25
**Slice**: Hermes phase-1 Slice 2.1 (JWT 模块)
**Trigger**: Round 3 codex review 暴露 4 个 finding，全部归类 S2（defense-in-depth / future-deployment edge / 设计选择）。按 CLAUDE.md #8 "review 反复发现新需求,停 commit 扩张"，当前 commit no-S0/S1 闭合，4 finding 延后处理。

## Round Trail

- Round 1 review: 3 S1 (Dockerfile copy / issuer-audience env / refresh lead window) → 已修
- Round 2 review: 2 S1 (legacy HMAC fallback / audit identity required) → 已修
- Round 3 review: 4 S2 → defer (本文件)

## Slice 2.5 status update (2026-05-26)

- [DEFERRED-1] **Closed in Slice 2.5**: runner entrypoint is JWT-only and no longer accepts `HUAKAI_HERMES_SHARED_SECRET` as startup auth material.
- [DEFERRED-2] **Closed by policy in Slice 2.5**: partial JWT config now fails closed because HMAC fallback is removed; falling through to HMAC is no longer a valid target behavior.
- [DEFERRED-3] **Closed in Slice 2.5**: JWT verifier rejects future `iat` even when `nbf` would otherwise allow the token.
- [DEFERRED-4] **Still deferred**: refresh key rotation needs the future operator key-rotation/runbook path; changing it here would be a broader rotation feature.

## Findings 详情

### [DEFERRED-1] runner entrypoint.sh 强制 HMAC secret

- **位置**: `backend/deploy/hermes-runner/entrypoint.sh` (require `HUAKAI_HERMES_SHARED_SECRET`)
- **Codex**: P1
- **HUAKAI Severity**: S2
- **Slice 2.5 处理**: Closed. `entrypoint.sh` now requires `HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR` or `HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH` + `HUAKAI_HERMES_JWT_KID`; legacy HMAC startup is removed.

### [DEFERRED-2] JWT credential 校验在 mode resolver 之前 hard-fail

- **位置**: `backend/internal/hermes/runner_client.go:86-92`
- **Codex**: P2
- **HUAKAI Severity**: S2
- **Slice 2.5 处理**: Closed by policy. JWT-only cleanup makes fail-closed partial JWT config mandatory; there is no HMAC fallback target to fall through to.

### [DEFERRED-3] JWT verifier 未校验 iat 与 now/nbf 一致

- **位置**: `backend/internal/hermes/jwt.go:130-132` (validateClaimsAt — 只检查 `Exp-Iat ≤ DefaultJWTTTL`)
- **Codex**: P2
- **HUAKAI Severity**: S2
- **Slice 2.5 处理**: Closed. `validateClaimsAt` now rejects `iat > now`, with a regression test where `nbf <= now` but `iat` is future-dated.

### [DEFERRED-4] Refresh 在私钥旋转期保留旧 kid

- **位置**: `backend/internal/hermes/runner_bootstrap.go:92` (RefreshJWT)
- **Codex**: P2
- **HUAKAI Severity**: S2
- **理由**: 当前 deployment 无生产 private key rotation 路径 (无 rotation 工具、无 ops runbook)；旋转期 transient kid mismatch 仅在未来 rotation feature 上线后才相关。当前 RefreshJWT 用旧 token 的 kid 是正确的 (verifier 用同一 kid 查同一 public key)。
- **建议归属**: 与 admin key rotation operator UI 同切片处理 (后续 admin 切片)

## 处理决定

按 CLAUDE.md #8 §"S2/S3 handling: compliance polish, ... 是 Round 2 后可延后。记录到 commit body 或 docs/process/reviews/DEFERRED-<topic>.md"。本文件即记录文件。

**当前 commit no-S0/S1 闭合**：Round 1+2 共 5 个 S1 全部已修 + GREEN tests。Round 3 4 个 S2 不阻塞 commit。

下游处理：
- Slice 2.5 (Hermes cleanup) closed [DEFERRED-1], [DEFERRED-2], and [DEFERRED-3].
- Admin key rotation 切片闭合 [DEFERRED-4]
