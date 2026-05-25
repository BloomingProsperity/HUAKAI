# DEFERRED — Hermes Slice 2.1 Round 3 Review Tail

**Created**: 2026-05-25
**Slice**: Hermes phase-1 Slice 2.1 (JWT 模块)
**Trigger**: Round 3 codex review 暴露 4 个 finding，全部归类 S2（defense-in-depth / future-deployment edge / 设计选择）。按 CLAUDE.md #8 "review 反复发现新需求,停 commit 扩张"，当前 commit no-S0/S1 闭合，4 finding 延后处理。

## Round Trail

- Round 1 review: 3 S1 (Dockerfile copy / issuer-audience env / refresh lead window) → 已修
- Round 2 review: 2 S1 (HMAC fallback in transition / audit identity required) → 已修
- Round 3 review: 4 S2 → defer (本文件)

## Findings 详情

### [DEFERRED-1] runner entrypoint.sh 强制 HMAC secret

- **位置**: `backend/deploy/hermes-runner/entrypoint.sh` (require `HUAKAI_HERMES_SHARED_SECRET`)
- **Codex**: P1
- **HUAKAI Severity**: S2
- **理由**: 当前 transition 期 default `HUAKAI_HERMES_AUTH_MODE=hmac`，HMAC secret 是必需的；将来 JWT-only 部署上线时 entrypoint.sh 需要放宽 require 检查到 `secret OR jwt-key-path`。当前不阻塞。
- **建议归属**: Slice 2.5 (Hermes transition cleanup) — 同时清掉 HMAC 后兼容代码

### [DEFERRED-2] JWT credential 校验在 mode resolver 之前 hard-fail

- **位置**: `backend/internal/hermes/runner_client.go:86-92`
- **Codex**: P2
- **HUAKAI Severity**: S2
- **理由**: 当前实现：JWT_PRIVATE_KEY_PATH 设置后 KID 必须同时设置，否则启动失败。这是 fail-fast on partial config (设计选择 — 防止 silent 漂移)。Codex 建议改为：partial JWT config 时 fall through 到 HMAC mode。两种 design 都可，当前选 fail-fast 更安全。
- **建议归属**: 视 transition deployment 反馈再定 — 若 ops 实际遇到 partial config 后回退到 HMAC 的真实需求才放宽

### [DEFERRED-3] JWT verifier 未校验 iat 与 now/nbf 一致

- **位置**: `backend/internal/hermes/jwt.go:130-132` (validateClaimsAt — 只检查 `Exp-Iat ≤ DefaultJWTTTL`)
- **Codex**: P2
- **HUAKAI Severity**: S2
- **理由**: Defense-in-depth。攻击场景需要攻击者已掌握 gateway signing private key (它直接 sign Iat=now)；攻击者拿到 key 后该 token TTL 限制已经无意义。当前 gateway-issued token 永远 Iat=Nbf=now，无问题。仅在第三方 issuer 加入后才相关。
- **建议归属**: Slice 2.5 / 或 multi-issuer 引入时

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
- Slice 2.5 (Hermes transition cleanup) 闭合 [DEFERRED-1] + [DEFERRED-3] + 视情况 [DEFERRED-2]
- Admin key rotation 切片闭合 [DEFERRED-4]
