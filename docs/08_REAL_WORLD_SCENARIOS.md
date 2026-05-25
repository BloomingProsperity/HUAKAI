This file is agent-facing and authoritative.

# Real-World Scenarios

Scenarios protect full feature parity by proving that local behavior covers real reference-derived outcomes.

## Purpose

Scenarios define what the platform must handle in production. They are the bridge between reference evidence and acceptance tests.

## Scenario Groups

### Gateway Operations

- Route traffic across multiple providers when a preferred provider is healthy.
- Fail over when a provider account is disabled, exhausted, rate-limited, or unhealthy.
- Preserve streaming behavior across compatible providers.
- Normalize provider errors into operator-actionable responses.

### Account Hub

- Rotate a provider credential without downtime.
- Disable a compromised account and remove it from routing.
- Assign accounts to channels, users, groups, or route policies.
- Detect expired, invalid, or quota-exhausted credentials.

### Quota And Billing

- Enforce quota before provider spend occurs.
- Record token and request usage accurately.
- Reconcile user balance, provider cost, and admin adjustments.
- Prevent negative balance abuse unless explicitly configured.

### Admin Operations

- Search, filter, sort, paginate, and inspect users, keys, channels, providers, routes, accounts, logs, usage, billing records, and audit events.
- Perform bulk operations with confirmation and audit trail.
- Investigate a failed request from user to route to provider account to billing record.

### Security

- Redact secrets in logs and UI.
- Require permissioned access for dangerous operations.
- Preserve audit history for admin changes.
- Block leaked credentials and unsafe configuration.

### User And Permission (§1 Remediation)

- AT-S1-GROUP-001 (P4; requires P3): 管理员需要能创建用户组、分配用户，并让 route/API key policy 按 group entitlement 解析；禁用、缺失或跨 tenant group 必须在上游 dispatch 前失败，并允许 operator 修复 membership 后恢复。
- AT-S1-TENANT-001 (P2): tenant operator 只能管理自己 tenant 的 pool；platform admin 做 pool 管理必须显式指定 tenant，避免 default tenant hardcode 造成跨租户读写。
- AT-S1-REG-001 (P5): 新用户注册后必须获得 tenant default group entitlement，并留下 audit/log 证据；系统不能在未批准的默认流程中发放 plaintext API key。
- AT-S1-ABUSE-001 (P6): 注册和登录的 challenge、IP throttle、email throttle 必须在 session issuance 和 provider spend 前阻断 abuse，同时支持误拦后的 operator 恢复。
- AT-S1-OTP-001 (P7): 密码验证成功后只进入 email OTP pending-login 状态，不签发 session；只有正确 code 才创建 session，过期、锁定和限流都必须无 session 失败。
- AT-S1-KEY-001 (P8): 用户可自助创建、查看和撤销自己的 API keys，但不能看到或操作其他用户的 key；plaintext key 只在创建响应出现一次。
- AT-S1-POLICY-001 (P9): token IP、model、group deny 必须在所有兼容 endpoint 上一致生效，并在 upstream dispatch、billing claim、provider spend 前阻断。
- AT-S1-QUOTA-001 (P9): per-token quota 必须经 billing/claim gate reserve/claim；并发、失败和 retry 场景不能超扣、漏扣或双扣。

## Scenario Rule

Every material feature must have at least one scenario before release readiness can be claimed.
