# Deferred Review Finding: Hermes Runner Key Rotation 路径未接通

| Field | Value |
| --- | --- |
| Severity | S1 |
| Source | 2026-05-26 外部 review (P0/P1 列表第 3 条) |
| Status | Owner-confirmed defer to Slice 2.8;phase-1 merge 前必须 risk register 标 Mandatory Roadmap |

## 问题

Gateway 实现了 HMAC 保护的 internal routes 等 runner 调用:
- `/internal/runner/bootstrap` ([backend/cmd/gateway/routes.go:121](../../../backend/cmd/gateway/routes.go#L121))
- `/internal/runner/refresh` ([backend/cmd/gateway/routes.go:181](../../../backend/cmd/gateway/routes.go#L181))
- `/internal/keys` ([backend/cmd/gateway/routes.go:222](../../../backend/cmd/gateway/routes.go#L222))

但 Python runner 端实际只在 [backend/deploy/hermes-runner/main.py:12](../../../backend/deploy/hermes-runner/main.py#L12) 启动时一次性 `load_public_key_cache_from_env()`,**永不刷新**,**从不调用 gateway 的 internal routes**。grep 整个 `backend/deploy/hermes-runner/` 子树无任何 client 代码 call `/internal/runner/*` 或 `/internal/keys`。

后果:
- JWT 签名公钥**永久 stale**(env 注入版本)
- Gateway 端 key rotation 工作做了但 runner 看不见,等于没做
- 测试覆盖了 gateway endpoint "能被 call",**没证明 runner 真会 call**

## 为什么不阻 phase-1 merge

Phase-1 没有 key rotation 业务依赖:
- 部署模型是 single-pair gateway + runner,JWT 签名密钥 dev 环境从 env 一次注入
- 真正需要 rotation 是生产 multi-replica + 长期 token 场景
- 当前 phase-1 是 MVP,merge 后 rotation 缺失不会立刻导致漏 / 串租户

但**绝不能装作 rotation 工作**:
- commit message 不能写 "key rotation 已实现"
- docs/10_RISK_REGISTER.md 必须明示该路径未接通

## Slice 2.8 范围(待开)

Gateway 端实际 endpoint 契约([backend/cmd/gateway/routes.go:100-102](../../../backend/cmd/gateway/routes.go#L100)):
- `POST /internal/runner/bootstrap` —— HMAC 签名,返 runner JWT token(`sub=runner_id`,短期),
  phase-1 runner 暂未使用此 token,**留作未来 runner→gateway 反向调用**用
- `POST /internal/runner/refresh` —— HMAC 签名,续 runner JWT token,同上 phase-1 未用
- `GET /internal/keys` —— HMAC 签名(**非 Bearer**),返 JWKS,
  routes.go:222 `VerifyRunnerHMACRequest(r, nil, ...)`

1. Python runner 端新增 `bootstrap_client.py` 启动时 **HMAC GET `/internal/keys`** 拿 JWKS,写入
   本地 `JWT_KEYS` 内存 cache。无需先调 bootstrap(那个 endpoint 是给未来 runner→gateway 反向通信用,
   phase-1 不必启用)
2. 后台 task 周期(默认 5 min)HMAC GET `/internal/keys` 拉最新 JWKS,merge 入 cache(替换或合并按 kid)
3. 401/kid-miss 时单次 retry 触发同步 HMAC GET `/internal/keys` 重拉 + retry verify
4. 判别测试:
   - `test_runner_loads_jwks_from_internal_keys_on_startup`:启动调 mock gateway HMAC GET 返 2 keys → JWT_KEYS 命中
   - `test_runner_keys_refresh_picks_up_rotated_jwks`:rotation 后 mock gateway 出新 kid → 5 min 内 verify 新 token 成功
   - `test_runner_kid_miss_triggers_keys_resync`:JWT verify 失败带新 kid → runner 立即重调 /internal/keys → retry verify 成功
   - `test_runner_internal_keys_call_uses_hmac_not_bearer`:断言出站 request 含 HMAC header (sig + ts + nonce),不含 Authorization Bearer
   - mutation:删 bootstrap_client.start() 调用 → 第一个 test 必红;删 refresh task → rotation test 必红;删 kid-miss retry → 第三个 test 必红;改用 Bearer auth → 第四个 test 必红

## 风险登记

- 若 Slice 2.8 落地前已 GA 生产部署,运维 SOP 必明示:**JWT 私钥泄露时只能滚动重启 runner + env 注入新公钥**,不能在线 rotation
- docs/10_RISK_REGISTER.md 项 `R-HERMES-KEYROT` 新增,标 Mandatory Roadmap

## 后续

- Owner 拍板是否 phase-1 后立刻开 Slice 2.8(优先级 vs cursor C2/C3/C4)
- 若 phase-1 实际不需 rotation,可保留本 DEFERRED 至 phase-2 准备阶段再开

---

**报告者**: Claude PM (Opus 4.7) verify 自 Owner 提供的外部 review (2026-05-26)
**确认时间**: 2026-05-26
