# twofa TOTP 防重放(wave-2 审计 wy94u3tn9 S1,auth-core + additive schema)

## 背景 / 来源
审计 wy94u3tn9 确认 S1:`VerifyTOTP` 接受当前 ±1 时间步的码,但 `recordSuccess→MarkSuccess`
只写 `last_used_at`,从不记录"已消费的时间步"。后果:同一个 6 位码在其 ~60-90s 有效窗口内
可被**重复提交且每次都成功**(RFC 6238 §5.2 要求拒绝二次使用)。攻击面:VerifyLogin /
VerifyLoginChallenge(controlhttp/twofa、passkey stepup 都funnel到这里)。

## 真码摸透(已读)
- `internal/twofa/totp.go:VerifyTOTP(secret,code,at,cfg) bool`:offset∈[-Window,+Window] 循环,匹配即 true。counter = at.Unix()/step。
- `service.go:VerifyLogin`:GetSettings→ensureUnlocked→decryptSecret→VerifyTOTP→`recordSuccess(MethodTOTP)`→`MarkSuccess(tenant,user,now)`(唯一调用方)。backup code 走 ConsumeBackupCode(本就单次)。
- `MarkSuccess` sqlc(`sql/queries/twofa.sql` + 生成 `internal/db/twofa`):UPDATE failed=0/locked=NULL/last_used_at,**无 step**。`Settings`/`TwoFactorSetting` 无 step 字段。
- schema `0087_two_factor_auth.up.sql` 的 two_factor_settings 无 step 列。sqlc.yaml schema=sql/migrations,可 `~/go/bin/sqlc generate`。

## #16 三镜像(TOTP 防重放)
- **sub2api**(migrations/044_add_user_totp.sql):仅 totp_secret_encrypted/totp_enabled/totp_enabled_at,**无已消费 step/counter 列** → 同样存在窗口内重放。
- **new-api**:model 层无 TOTP 已用 step/counter 跟踪(无等价)。
- **CLIProxyAPI**:纯 relay,无 2FA(无等价)。
- **HUAKAI delta(算法+生态升级)**:引入单调"已消费时间步"守卫(类似 passkey sign-count 的单调计数),拒绝 step ≤ 已存 step → RFC 6238 §5.2 合规的防重放,**比三家都强**。不照抄任何上游标识符。

## 修复(additive schema,可逆,无默认翻转,auth 从认证身份)
1. **迁移 0150**:`ALTER TABLE two_factor_settings ADD COLUMN last_used_step bigint`(nullable,NULL=从未消费)。UP 加列不丢数据;DOWN 丢该列。**可逆 additive**。
2. **totp.go**:加 `VerifyTOTPStep(secret,code,at,cfg) (step int64, ok bool)`(返回匹配到的 counter);`VerifyTOTP` 改为 `_,ok:=VerifyTOTPStep(...)` 委托,行为不变(既有 totp_test 仍绿)。
3. **types.go**:`Settings` 加 `LastUsedStep *int64`;新错误 `ErrCodeReused`;`Store` 加 `MarkTOTPSuccess(ctx,tenant,user,consumedStep,now) (stored bool, err error)`。
4. **service.go**:VerifyLogin 的 TOTP 分支改 `VerifyTOTPStep`——若 `settings.LastUsedStep!=nil && step<=*LastUsedStep` → `ErrCodeReused`(快速拒绝,**不计失败次数**,避免合法重复提交被锁);否则 `MarkTOTPSuccess`(条件更新原子兜并发竞态,stored=false → ErrCodeReused)。backup 路径不变。
   - **Enable 不消费时间步(最终决定)**:防重放收敛在登录路径(审计点名的攻击面)。启用是一次性初始证明;其码在随后首次登录仍可被消费一次(首次消费),其后重复使用被 VerifyLogin 拒绝。这样既堵住登录重放,又不破坏"启用后立即用同一码登录一次"的合法流程(也避免误伤大量既有 enable+verify 测试)。残留极窄(启用码可在窗口内被用一次登录),可接受。
   - **调用方错误映射**:新错误 `ErrCodeReused` 加进 controlhttp/twofa_handler、gatewayhttp/auth_handler(writeTwoFactorLoginError + twoFactorReasonClass)、passkeyhttp/handler 的错误 switch,统一映射 401 `two_factor_code_reused`,而非落到默认 503 backend_error。
5. **twofa.sql + sqlc generate**:GetTwoFactorSettings SELECT + last_used_step;Upsert RETURNING + last_used_step 且 DO UPDATE 把 last_used_step 重置(re-setup 换新密钥须清旧 step,否则旧 step 误拒新码);新 `MarkTwoFactorTOTPSuccess :execrows`(UPDATE ... last_used_step=$4 WHERE tenant/user AND (last_used_step IS NULL OR last_used_step<$4),返回 rows)。
6. **store_postgres.go**:`MarkTOTPSuccess` 调 :execrows 返 rows>0;`settingsFromDB` 映射 LastUsedStep。
7. **store_memory.go**:cloneSettings 深拷 LastUsedStep;`MarkTOTPSuccess` 内存条件实现。

## 测试(变异可证,MemoryStore 单测为主,无需 DB)
- **重放拒绝**:同一码连提两次 → 第 1 次成功、第 2 次 `ErrCodeReused`(变异:去掉 step≤last 守卫 + 去掉条件更新 → 第 2 次又成功 → RED)。
- **前进**:下一个时间步的码被接受(step 单调推进,不误锁合法用户)。
- **并发兜底**:两请求同 step,条件更新只 1 个 stored=true、另一个 ErrCodeReused(MemoryStore 条件实现 + 变异去条件 → RED)。
- **backup 码不受影响**:backup 成功仍走 MarkSuccess,不动 step。
- **VerifyTOTPStep 返回正确 counter**:断言匹配 step==at/step(+offset),且 VerifyTOTP 委托行为与原一致(既有 totp_test 全绿)。
- 干净基线 `go test ./internal/twofa/... ./internal/db/twofa/... -count=1`;`go build ./...`;codebudget 绿。

## blast radius
auth-core:internal/twofa(+ db/twofa 生成 + 1 迁移)。**不碰** controlhttp/passkey/session 调用方(VerifyLogin 签名不变,新增错误 ErrCodeReused 调用方按既有 error 处理即返 401/invalid)。对抗审查零 S0/S1 后合并。
