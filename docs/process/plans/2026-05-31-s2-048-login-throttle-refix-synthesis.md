# S2-048 重修 — Claude×Codex 平行计划综合(执行版)

两稿独立成文(`-claude.md` / `-codex.md`)。**无冲突**;codex 稿是我稿的超集+四处实质补强,全部采纳。本文件 = 实际执行的合并方案。

## 双稿一致(强信号,直接定)
- 新建非冻结包 `backend/internal/loginthrottle`(限流逻辑不进冻结 gatewayhttp)。
- 限流闸在 **handler 内、`Authenticate`(argon2)之前**(非全局中间件:只套 login,不误伤热路径;需登录结果驱动失败计数)。
- 限流 key = **client IP only**(两稿独立都拒绝用 body `tenant_id` —— 未认证可伪造,会绕过 CPU 防护)。
- **不动共享 `writeAuthError`**(它被注册/验证/重置/OAuth 复用);新增 login 专用 generic writer。
- service 层保留 typed error 供审计;handler 记真 `authReasonClass` 后对外统一 **401 `invalid_credentials`**。
- 状态分支(disabled/locked/unverified/reset)取消 argon2 前 early-return,改为跑一次等价 argon2 再返回(拉平时序),保留既有副作用(locked 仍 MarkLoginFailure)。
- 无 schema 变更;纯内存;Redis/多实例横向绕过 = follow-up(列 mandatory hardening)。
- 时钟注入;核心判别测 = 预置 throttle 满载后断言 `GetUserByEmail`/argon2 调用 0 次 + 429(证 pre-KDF)。

## 采纳 codex 的四处补强(我稿缺/弱)
1. **in-flight reservation + `IPInFlightLimit` 并发闸(关键)**:纯滑动窗口有竞态——N 个并发请求可在任一记录失败前全部通过窗口检查、同时进 argon2。改用 `Begin→Lease(Success/Failure/Cancel)`:Begin 占一个 in-flight 槽,超 `IPInFlightLimit`(默认 4)直接拒。这才真正卡住并发 CPU 放大。
2. **`Lease.Cancel` + `defer`**:handler 拿到 lease 后 `defer lease.Cancel()`(commit 后 no-op),panic/早退也释放 in-flight 槽。
3. **`MaxKeys` + key 压力 fail-closed**:限流器自身内存有界(prune 过期 + evict + 满则 429),防唯一-IP 把 map 撑爆。
4. **docs 同步(防 OpenAPI 一致性闸红)**:login 响应码变了(403 状态码族 → 401 generic,新增 429)→ 必须同步 `docs/openapi/openapi.yaml`(否则 `TestOpenAPI_ImplementationConsistency` 全量测红,memory 教训),并更 `docs/specs/user-authentication.md` + 验收矩阵。
5. 附带:`go test -race` 跑 loginthrottle;config 非法值 fail-fast;session 创建失败 ≠ 登录失败(不计 quota)。

## 合并后的三层模型(执行)
- **第1层 IP pre-KDF 闸**(loginthrottle):`Begin(IP)` 检查 ①封禁中? ②in-flight ≥ IPInFlightLimit? ③窗口内(失败+in-flight)≥ IPWindowLimit? ④key 压力? 任一命中 → Decision.Allowed=false → handler 记 `login_rate_limited` + 429(Retry-After 粗粒度),**不查用户、不跑 argon2**。
- **第2层 等价 argon2 + typed error**(userauth.Authenticate):查用户→算 reason→真 hash 或 dummy 常量跑一次 verifier→按结果更新失败计数→返回 user 或 typed error。每条失败路径恰一次 argon2。
- **第3层 账号锁定**(既有 userauth `FailedLoginCount>=5`,不动):账号维度兜底,对外仍 generic。
- 结果回灌:`lease.Success()`(auth 成功,不消耗窗口/清 in-flight)或 `lease.Failure()`(累计失败、可能触发封禁)。

## 默认参数(可 env 覆盖,温和偏 NAT 友好)
`IPInFlightLimit=4`(每 IP 并发 argon2 上限)、`IPWindow=1m / IPWindowLimit=10`(失败+in-flight 计数)、`IPBanWindow=10m / IPBanAfter=20 / IPBanDuration=15m`、`MaxKeys=100000 / CleanupInterval=1m`。成功登录不计窗口。

## commit 切分(一 commit 一模块,push work/s2-048)
- C1 `loginthrottle 三层登录限流器(pre-KDF reservation)+ 判别测`(新包,含 -race)。
- C2 `userauth 登录状态分支补等价 argon2 拉平时序`(service.go + service/timing 测)。
- C3 `gatewayhttp 登录接限流闸 + 失败响应并入 generic`(auth_handler.go + 既有 handler 测)+ `cmd/gateway` wiring/config(handler-wire 例外可同 commit)。
- C4 `docs auth 登录限流 + generic 失败同步`(openapi.yaml + spec + 验收矩阵)。
- 每个过 codex 自审无 S0/S1;全量 `go test ./...`(含 OpenAPI 一致性闸)。

## 残余风险(surface Owner 知会,非阻塞)
- 内存限流单进程:多副本生产可横向绕过 → 需 sticky LB 或 Redis 集中式(mandatory hardening follow-up,本切片不做)。
- 共享 NAT 误伤:默认偏宽 + 仅失败/in-flight 计数 + 成功不累计缓解;可 env 调。
