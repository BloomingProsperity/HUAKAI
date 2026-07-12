# 新设备确认完整流 (default-dormant) — 实施计划

## 背景与问题
`usersession.enforceDevicePolicy` 在 `DevicePolicy=="confirm"` 且达 `MaxActiveFamilies` 上限时, 裸返回
`ErrDeviceConfirmationRequired`, 但没有任何确认/恢复路径、不发邮件 → 运营者一旦设 confirm 就把超限用户
永久锁死 (footgun)。本切片补完: pending 记录 + 带 token 的类型化错误 + 发确认邮件 + 确认端点撤最老腾位。

## 范围 (scope)
1. 迁移 `0163_device_confirmations` (additive-only, up+down)。
2. usersession: `DeviceConfirmation` struct + `DeviceConfirmationRequiredError` (Unwrap=ErrDeviceConfirmationRequired)
   + 新 sentinel `ErrDeviceConfirmationNotFound`; Store 接口 3 方法 (Create/GetByTokenHash/MarkConfirmed) 的
   PG + Memory 实现 (新文件 `device_confirmation_store.go`, 不动已超预算的 store.go)。
3. usersession Service: token 生成 helper + `requireDeviceConfirmation` + `ConfirmDevice`; 改 `enforceDevicePolicy`
   把 `CreateInput` 传进去。
4. auth_handler: `AuthEmailSender` 加 `SendDeviceConfirmation`; 4 个 Create 调用点 errors.As 命中 → 发信 + 403;
   新端点 `POST /v1/auth/confirm-device` + openapi 声明。email.AuthSender 与 NoopAuthEmailSender 补实现。
5. config: `HUAKAI_SESSION_MAX_ACTIVE_DEVICES` (默认 0) + `HUAKAI_SESSION_DEVICE_POLICY` (默认 "") 注入 Service,
   非法值 fail-loud。

## 成功标准
- 默认 (MaxActiveFamilies=0) 整条路径休眠, 零生产行为变更。
- confirm 流: 达上限 → pending + 类型化错误 (errors.Is 仍真) → 邮件 → confirm 校验 → 撤最老腾位 → 重登成功。
- 幂等: 二次 confirm 不重复撤; 过期 confirm 拒绝; token hash 比对错则确认放行被挡。

## blast radius
- usersession 包 (新文件 + rotation/types 改) ; gatewayhttp auth_handler/session_handler ; email sender ;
  cmd/gateway config/lifecycle ; openapi.yaml ; 新迁移。无既有迁移改动 (additive-only)。

## 风险点
- store.go 已 937 行 (grandfathered), 严禁加方法; 全部新 Store 方法进新文件。
- 类型化错误必须 Unwrap 到 ErrDeviceConfirmationRequired, 否则 writeSessionError 的既有分支 + sessionReasonClass 失效。
- 响应体与日志绝不含 raw token (secret-mask)。
- openapi impl-only 漂移会硬 fail cmd/gateway 测试 → 必须同步声明。

## 决策点 (Owner)
- 本切片是 auth-core (session) + DB schema migration, 属 Owner-gated; 但是 default-dormant 零行为变更 +
  完成既有 footgun 的修复, 按"安全网"自驱实现并交付 worktree commit, 不自合并。
