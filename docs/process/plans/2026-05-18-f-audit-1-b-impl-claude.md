---
plan_id: 2026-05-18-f-audit-1-b-impl-claude
lane: claude (PM)
status: dispatched
spec: docs/specs/user-consumption-transparency.md
prereq: F-AUDIT-1-A (commit 920b54d)
utc: 2026-05-18T11:05:00Z
---

# F-AUDIT-1-B 5 endpoint impl — Claude 平行计划

## 0 缘起

F-AUDIT-1-A 已落 (920b54d): user_cost_receipts 表 + DeriveReceipt + SignReceipt + AppendReceipt + GetReceipt.
现在做 sub-phase B: 5 user-facing verification endpoint.

## 1 5 endpoint (跟 spec §5 一致)

| Method | Path | 用途 |
|---|---|---|
| GET | `/v1/receipts/{request_id}` | 用户拉 cost receipt JSON (含 signed_hash + signer_fingerprint) |
| POST | `/v1/receipts/{request_id}/verify` | detached verify (用户拿 receipt + signed_hash, server 用本机 pubkey 算 canonical hash 对比) |
| GET | `/v1/pricing/rate-table?version=...` | 公开 pricing rate table (按 version 历史不可改) |
| GET | `/v1/pricing/snapshots` | 列出所有历史 rate table version + 切换时间 |
| GET | `/v1/audit/pubkey` | 当前 ed25519 sign pubkey (跟 F-TRUST 共用 endpoint, 但加 cost-receipt 标签) |

## 2 中间件 + 安全

- 全 endpoint 加 chi RequestID middleware (与 F-AUDIT-1-A 的 audit_request_id 链路保持)
- `/v1/receipts/{request_id}` 必须做 cross-tenant 隔离: 看 session.tenant_id 跟 receipt.tenant_id 比对, 不匹配返 404 (不是 403, 避存在性 oracle)
- pricing endpoint 公开 (不需 auth, rate table 是公开信息)
- pubkey endpoint 公开 (用户验证用)

## 3 错误响应

- receipt 找不到 (ErrReceiptInputsNotFound): 404 + `{"error":"receipt_not_found"}`
- receipt 待 DLQ replay (ErrReceiptUnavailable): 202 + `{"error":"receipt_unavailable","retry_after_seconds":60}`
- request_id 过长 (>256B): 400 + `{"error":"request_id_too_long"}` — 同时修 F-AUDIT-1-A 残留 P2
- pricing version 不存在: 404 + `{"error":"rate_table_version_not_found"}`
- verify mismatch: 200 + `{"valid":false,"reason":"signature_mismatch"}`

## 4 AT 要 (15-20 条)

- AT-AUDIT-001-009: GET receipt 命中
- AT-AUDIT-001-010: cross-tenant 拒 (404)
- AT-AUDIT-001-011: receipt 不存在 (404)
- AT-AUDIT-001-012: receipt DLQ 期间 (202)
- AT-AUDIT-001-013: detached verify PASS
- AT-AUDIT-001-014: receipt 被篡改后 verify FAIL
- AT-AUDIT-001-015: pricing rate table 按 version 拉
- AT-AUDIT-001-016: pricing snapshots 列表
- AT-AUDIT-001-017: pubkey endpoint 返当前 fingerprint
- AT-AUDIT-001-018: ingress >256B X-Request-Id 400 (P2 闭环)

## 5 实施步骤 (派给 codex)

1. 加 gateway endpoint route + handler 在 backend/internal/gatewayhttp/
2. 路由跟 F-SESSION-001 已有 middleware 接 (session.tenant_id)
3. 加 ingress 中间件: X-Request-Id 长度限制 (256B 拒) → 修 F-AUDIT-1-A P2
4. 10-18 AT 单测 + integration test
5. 跑 go build / go test PASS

## 6 跟 R-3-A-fix-3-deeper parallel 关系

R-3-A-fix-3-deeper 是 Rust C 路径 (vendor/boring), F-AUDIT-1-B 是 Go gateway. 完全不冲突.
