# S2-048 重修实现计划(Claude 独立稿)

平行计划 CLAUDE.md#10:本稿由 Claude 独立起草,未参考 codex 稿。Decision 已由 Owner 拍定:**HUAKAI 融合三层登录限流(argon2 前)+ 状态枚举并入 generic**。本稿只定实现。

## 背景与目标

S2-048 首版(work/s2-048 @ 141c272)的「三路径等价 argon2」修复被独立审认可并保留,但打回两道门:
- **门1 [S1]**:等价 argon2 让任意不存在邮箱也跑满 64MiB argon2,而 `/v1/auth/login` 无限流 → 未认证 CPU 放大 DoS。
- **门2**:`disabled/locked/unverified/reset` 返回不同 403 码且在 argon2 前 early-return → 时序+状态码双 oracle;spec `docs/specs/user-authentication.md` §"Invalid Password Or Locked User" 要求登录失败统一 generic,审计侧才见真 reason。

目标:在保留等价 argon2 的前提下,(A) argon2 前置限流杀 DoS,(B) 登录失败对外统一 generic、审计仍带真 reason、时序拉平。

## 设计

### 新包 `backend/internal/loginthrottle`(非冻结,按职责命名)
三层防御,纯内存(Redis 留后),时钟注入便于判别测:

1. **每 IP 滑动窗口(new-api 借鉴)**:`Allow(key) (ok bool, retryAfter time.Duration)`,窗口内请求数超阈即拒。默认 `MaxPerWindow=30 / Window=15min`(比 new-api 20/20min 略宽,留正常用户余量;可配)。
2. **失败计数封禁(CLIProxyAPI 借鉴)**:`RecordFailure(key)` 累计;同 key 连续失败 ≥ `MaxFailures=8` → 封 `BanFor=15min`(`Allow` 期间命中封禁直接拒)。`RecordSuccess(key)` 清零(成功不计窗口、清失败)。
3. **账号级锁定**:复用 `userauth` 既有 `FailedLoginCount>=5`(不动),作为第三层(账号维度,前两层是 IP 维度)。

公共 API(草签):
```
type Limiter struct{ ... 内部分片 map + mutex; now func() time.Time }
func New(cfg Config) *Limiter
func (l *Limiter) Check(key string) Decision   // pre-KDF 闸:封禁中/超窗 → Decision{Allow:false, RetryAfter}
func (l *Limiter) OnResult(key string, ok bool) // ok=true 清零;false 累计失败
type Decision struct{ Allow bool; RetryAfter time.Duration }
type Config struct{ MaxPerWindow int; Window time.Duration; MaxFailures int; BanFor time.Duration; Now func() time.Time }
```
并发安全:分片(shard by hash(key))+ 每片 mutex;后台/惰性清理过期窗口与封禁项(惰性:Check 时丢弃过期时间戳,避免独立 goroutine)。

### 接入点:**handler 内,argon2 之前**(不是全局中间件)
- 为什么 handler 不是全局中间件:限流只该套在 `/v1/auth/login`(及可选 register),不能误伤聊天/admin 热路径;且需要拿到归一化后的 key 与登录结果(成功/失败)来驱动失败计数。中间件只能看请求不知登录结果。
- key:用 **client IP**(`d.ClientIPResolver.ClientIP(r)`,信任代理感知)。tenant 来自 body 不可信(攻击者可乱填)→ 不进 key。(可选加 tenant 仅作 metrics,不进限流 key。)
- 顺序(关键 delta = pre-KDF):
  1. decode 请求(便宜)。
  2. `dec := limiter.Check(ip)`;`!dec.Allow` → 记审计 `user_login_throttled` + 返回 **429** `too_many_attempts`(带 `Retry-After`,值粒度粗化到固定档位防泄露精确剩余,见风险7)。**在调用 Authenticate(argon2)之前**。
  3. `Authenticate(...)`(内部:查用户→状态分支→等价 argon2,见下)。
  4. `limiter.OnResult(ip, err==nil)`(成功清零,失败累计)。
  5. 失败 → 记审计真 reason + 对外 generic;成功 → 建 session。
- 成功登录**不消费**窗口配额(`OnResult` ok=true 清失败计数);仅失败累计。窗口计数对所有尝试累计(防成功穿插绕过纯失败封禁)——即「窗口总量」+「失败封禁」双闸。

### 门2:枚举并入 generic(仅 password-login 路径)
- **不改共享 `writeAuthError`**(它被 register/oauth/reset 复用,改了会误伤 invite_required 等正常语义)。
- 在 `newAuthLoginHandler` 失败分支:`recordAuthEvent(ReasonClass: authReasonClass(err))` 保留真 reason(审计),然后调用**新的 login 专用** `writeLoginFailureGeneric(w, err)`:把 `ErrInvalidCredentials/ErrUserDisabled/ErrUserLocked/ErrPasswordResetRequired/ErrEmailUnverified` 一律 → **401 `invalid_credentials`**「email or password is invalid」;`ErrInvalidInput`→400(请求格式错,非枚举泄露,保留);后端/未配置→503。
- **时序拉平**:`Authenticate` 里 `disabled/locked/unverified/reset` 分支当前在 argon2 前 early-return → 改为在返回各自 sentinel **之前**跑一次等价 argon2(`verifyPasswordFn(timingEqualizationHash 或 user.PasswordHash, in.Password)`,结果丢弃),与 not-found/social-only/wrong-password 等工。保留既有副作用语义(locked 仍 MarkLoginFailure;disabled/unverified/reset 不 mark)。service 仍返回具体 sentinel(供 handler 审计),对外 generic 由 handler 负责。

### 与已保留修复共存的最终顺序(Authenticate)
limiter 在 handler 已挡 → Authenticate 内:查用户 → (not-found: 等价 argon2 → ErrInvalidCredentials) → 状态分支各自(等价 argon2 → 具体 sentinel) → social-only(等价 argon2 → ErrInvalidCredentials) → 真 argon2 verify(wrong→MarkLoginFailure+ErrInvalidCredentials)→ 成功 MarkLoginSuccess。每条失败路径恰好 1 次 argon2。

## 文件清单
- 新建 `backend/internal/loginthrottle/limiter.go`(+ `// HUAKAI · iKun`)— 三层 limiter。
- 新建 `backend/internal/loginthrottle/limiter_test.go` — 判别测(见矩阵)。
- 编辑 `backend/internal/gatewayhttp/auth_handler.go`(冻结包既有文件,bug-fix 编辑允许):login handler 接 limiter pre-KDF + `writeLoginFailureGeneric`;`AuthHandlerDeps` 加 `*loginthrottle.Limiter` 字段。
- 编辑 `backend/internal/userauth/service.go`(work/s2-048 基础上):状态分支补等价 argon2。
- 编辑 `backend/cmd/gateway/`(非冻结)wiring:构造 limiter 注入 AuthHandlerDeps;config 读默认值/env。
- 无 schema 变更(失败计数纯内存,不持久化)。

## 测试矩阵(#14,每条可变红 + mutation)
1. **超窗拒**:同 IP 连发 > MaxPerWindow → 第 N+1 个 `Check` Allow=false。mutation:窗口计数不累加 → 永远 Allow → 红。
2. **阈内过**:窗口内 ≤ 阈值全 Allow=true。防过度拦截(去掉会误判)。
3. **失败封禁**:连续 MaxFailures 次 OnResult(false) → Check Allow=false 持续 BanFor;时钟推进过 BanFor → 恢复 Allow。mutation:不封 → 红;封不解除 → 时钟测红。
4. **成功清零**:失败数次未达阈 → OnResult(true) → 失败计数清零,后续不误封。mutation:成功不清 → 红。
5. **pre-KDF 顺序(核心)**:用注入的 `verifyPasswordFn` 计数 + limiter 预置成封禁态;打 login → 断言 **argon2 调用次数 == 0**(被限流挡在 argon2 前)且返回 429。mutation:把 limiter.Check 移到 Authenticate 之后 → argon2 跑了(calls==1) → 红。这条直接钉「pre-KDF」delta。
6. **枚举对外 generic**:disabled/locked/unverified/reset 四态各打 login → HTTP 全是 401 `invalid_credentials`(不再 403 各码);mutation:任一保留具体码 → 红。
7. **审计仍带真 reason**:同上四态 → recordAuthEvent 的 ReasonClass 仍是各自真 reason(用 spy EventSink 断言)。mutation:reason 也被抹成 generic → 红。判别 fixture:对外 generic 与审计 reason 必须**不同来源**。
8. **状态分支时序等工**:扩展既有 timing_equalization_test,disabled/locked/unverified/reset 各跑恰好 1 次等价 argon2。mutation:某分支漏等工 → calls==0 → 红。

## 风险与坑
1. **共享 NAT 误伤**:阈值放宽(30/15min)+ 仅失败累计封禁,降低正常用户误判;文档注明可配。
2. **分布式多 IP 绕过**:per-IP 限流的固有局限(new-api 同此);账号级锁定(第三层)兜部分;真正抗分布式需 CAPTCHA/全局预算,记 follow-up,不在本切片。
3. **内存增长**:惰性清理 + 分片;高基数 IP 下设上限/LRU(记 follow-up 若需);本切片用惰性过期足够。
4. **限流器自身被 DoS**(海量唯一 IP 撑爆 map):分片 + 条目 TTL;极端情况 follow-up 加全局上限。
5. **时钟**:Now 注入,绝不 time.Now() 当随机/测试源(memory 全量验证规则)。
6. **限流 key 用 body tenant 不可信** → 只用 IP(已规避)。
7. **Retry-After 泄露**:429 的 Retry-After 粒度粗化到固定档(如统一返回 window 级),不暴露精确剩余,避免侧信道;且 429 对「存在/不存在用户」一视同仁(限流在查用户前)。
8. **熔断顺序回归**:确保 limiter 不影响成功登录路径性能(成功仅一次 map 操作)。

## commit 切分(一 commit 一模块)
- C1:`loginthrottle 三层登录限流器 + 判别测`(新包,自含)。
- C2:`userauth 登录状态分支补等价 argon2 拉平时序`(service.go + 扩展 timing 测)。
- C3:`gatewayhttp 登录接限流闸 + 失败响应并入 generic`(auth_handler.go + handler 测)+ `cmd/gateway` wiring(handler+wiring 衔接可同 commit,main.go-wire 例外)。
- 三者都过 codex 自审无 S0/S1 才落;push work/s2-048 重标 review。

## 与 Owner 既定方向一致性
完全一致(融合三层 + argon2 前限流 + 状态并入 generic)。最大风险点:**测试5(pre-KDF 顺序)的判别性** —— 必须真证明限流在 argon2 之前,否则等于没修 S1;用 verifyPasswordFn 计数 + 预置封禁态断言 argon2 calls==0 来钉死。
