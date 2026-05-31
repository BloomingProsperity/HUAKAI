# S2-048 重修实现计划(Codex 独立草案)

> 留底:codex(gpt-5.5 xhigh, read-only)独立起草,未参考 Claude 稿。CLAUDE.md#10 平行计划。原文如下(轻度排版)。

## 目标与边界
- 修两个 S1:`/v1/auth/login` 未认证 Argon2 CPU 放大 DoS;登录状态错误码/时序账号枚举 oracle。
- 范围内:内存型登录限流包、登录 handler 接入、`userauth.Authenticate` 状态分支等价 KDF、登录失败 public generic、判别测试、OpenAPI/验收矩阵同步。
- 范围外:DB schema、Redis、全站限流、验证码、认证核心大重构。
- 高风险点:跨实例共享失败计数需 Redis/DB(schema/infra gate),本次不做。

## 1. loginthrottle 新包
非冻结包 `backend/internal/loginthrottle`。模型:IP 滑动窗口(失败+in-flight reservation)+ 失败封禁(blockedUntil)+ 成功只释放 reservation 不清历史失败 + 账号锁定留在 userauth。

public API(草签):`Config{IPWindow=1m, IPWindowLimit=10, IPInFlightLimit=4, IPBanWindow=10m, IPBanAfter=20, IPBanDuration=15m, MaxKeys=100000, CleanupInterval=1m, Now}`;`Gate.Begin(ctx, Attempt{TenantID,ClientIP}) (Lease, Decision, error)`;`Lease.Success/Failure/Cancel`;`Decision{Allowed, Reason(allowed/ip_window/ip_in_flight/ip_banned/key_pressure), RetryAfter}`。

内存实现:`sync.Mutex` 护 `map[string]*bucket`;bucket={failures []time.Time, inFlight map[uint64]time.Time, blockedUntil, lastSeen, nextID}。Begin: prune→查 ban/并发/窗口/key 压力→写 in-flight reservation。Success 删 reservation;Failure 删 reservation+追加失败时间+达阈设 blockedUntil;Cancel 只释放(中途返回/panic 恢复)。reservation 用 sync.Once/原子防重复 commit。MaxKeys 超限先清过期无 in-flight 的 bucket,腾不出则 fail-closed 返回 429(保护 limiter 自身内存)。

## 2. 接入点与顺序
handler 内(非全局 middleware):只影响 /v1/auth/login;需 decode body 后拿 IP+登录结果。流程:依赖检查→decode(无效 400 不进 throttle/KDF)→取可信 IP→`Begin`→deny 记 reason_class=login_rate_limited + 429 不调 Authenticate→allow 调 Authenticate→成功 `lease.Success()` 再建 session(session 失败不计登录失败)→失败 `lease.Failure()` + 记真 authReasonClass + login generic writer。key=IP-only(body tenant 不可信,只作审计维度)。429 带 Retry-After(粗粒度,不暴露邮箱/状态/剩余次数)。三层:IP pre-KDF→Authenticate(等价 argon2)→账号锁定(generic 对外)。

## 3. 枚举 oracle 并入 generic
service 保留 typed error(审计);handler 新增 login 专用 writer 把 disabled/locked/reset/unverified/invalid 全映射 401 `invalid_credentials`;不动共享 writeAuthError(注册/验证/重置/OAuth 不受影响)。audit 在 writer 前记真 reason。时序:四态不再 early-return,通过基本校验且未被 throttle 拦截就跑一次等价 verifier(有真 hash 用真 hash,否则用保留的合法 dummy argon2id 常量)。

## 4. 与等价 Argon2 共存顺序
限流 Begin→查用户→算真实 reason→选真 hash 或 dummy→跑一次 verifier→按结果更新失败计数→返回 typed error/user→handler 审计真 reason→public generic。(not-found/social-only/wrong=dummy 或真 hash→ErrInvalidCredentials;locked 保留 failure mark;disabled/deleted/reset/unverified KDF 后返回对应 typed error 但对外 generic;成功 MarkLoginSuccess + throttle Success 不消耗。)

## 5. 判别测试矩阵(节选)
loginthrottle:窗口超阈拒/阈内过、成功不写失败窗口、**in-flight 并发超 IPInFlightLimit 被拒(只在 KDF 后计数会放过并发 CPU 放大)**、ban 窗口过期但 ban 未过仍拒、MaxKeys/prune、`-race` 无竞争。userauth:四态 KDF 后才返回(early-return→verifier call 0→红)、not-found/social-only/wrong 各跑一次、locked 仍保留账号锁定 mark。gatewayhttp:四态+wrong public 全 401 invalid_credentials、AuthEvent ReasonClass 仍各自真 reason、**throttle 命中时 GetUserByEmail 计数 0 且 429(证 pre-KDF)**、wrong 累计后下次 pre-KDF 429 且成功不增 quota、可信 IP 解析。cmd/gateway:env 默认/非法 fail-fast/override、production route 注入非 nil throttle。

pre-KDF 关键 fixture:Store.GetUserByEmail 一被调就计数;先打满 throttle 再请求 login,断言 429 且 store 调用 0 → 证拒绝发生在 Authenticate 前。

## 6. 文件清单
新建:loginthrottle/throttle.go(+test)、cmd/gateway/login_throttle_config.go(+test)、可选 userauth/service_login_oracle_test.go。编辑:gatewayhttp/auth_handler.go(冻结既有文件 bug-fix)、gatewayhttp/auth_session_handler_test.go(既有测试文件)、userauth/service.go(+service_test.go)、cmd/gateway/{wiring.go,routes.go}、docs/openapi/openapi.yaml(login 去状态型 403、加 429)、docs/specs/user-authentication.md、docs/11_ACCEPTANCE_TEST_MATRIX.md。无 migration。

## 7. 风险
共享 NAT(IP-only 必须,用温和默认+短 Retry-After 缓解);多实例绕过(内存单进程,生产需 sticky/Redis,列后续 mandatory);唯一 IP 内存 DoS(MaxKeys+prune+evict+fail-closed);并发瞬时放大(必须 pre-KDF reservation + IPInFlightLimit);Retry-After 只基于 IP 不基于状态;时钟注入;mutex 热点(单 bucket 小切片);审计不记密码/hash/token/body;handler defer Cancel。

## 8. commit 切分
1 loginthrottle 包;2 userauth 去时序 oracle;3 gatewayhttp login generic+throttle 接入;4 cmd/gateway wiring+config;5 docs(OpenAPI/spec/matrix)。验证 `go test ./internal/loginthrottle ./internal/userauth ./internal/gatewayhttp ./cmd/gateway` + 力所能及 `go test ./...` + `-race`。

> 一致性:与 Owner「融合三层 + argon2 前 + 状态 generic」一致;最大残余风险=内存限流多实例横向绕过(后续 Redis/sticky)。
