# 2026-07-07 429 限速冷却下沉到 per-model 格 Codex 独立计划

| Owner directive | “独立起草计划(不看别人计划)——429 限速冷却下沉到 per-model 格”；“只写计划文档,不改代码、不 commit”；“Owner 已拍板 policy B” |
| --- | --- |
| Scope | 只规划后端热路径改动:上游 429 限速从账号级 channelhealth 冷却改为 provider account × upstream model 的 `provider_accounts.model_rate_limits` 写入；配额耗尽、账号封、认证硬失败、529/5xx 过载语义维持账号级处理。 |
| Out of scope | 不改 schema、不改 billing ledger、不改 quota enforcement、不改 auth core、不改前端、不读取或复制 reference 源码、不 commit。 |
| Success criteria | 429 rate-limited 只冷却当前账号的当前 upstream model；同账号其它模型仍可被选择；健康 FSM/分数/RecentReqRing 不被纯 429 限速污染；quota exhausted/account suspended 仍进入账号级禁用或冷却；reset 到期后 per-model gate 自动恢复；测试覆盖判别、并发、跨模块配合。 |
| Time estimate | 代码实施 0.5-1 天；测试补齐 0.5 天；本地检查 0.25 天；总计约 1-1.75 天。 |
| Blast radius | `chat_completions` 热路径、账号池选号、上游失败重试、rate-limit audit、channelhealth 观测语义。不会触碰账本和配额扣减。 |
| Clean-room note | 本计划未读取非 MIT reference 项目源码；reference 只作为 Owner 输入的行为约束。实现必须使用 HUAKAI 现有结构和 HUAKAI 自有命名，不复制 reference 函数名、结构、注释、schema 或测试。 |
| Independence note | 我没有读取同主题 Claude 计划正文。目录/状态检查显示同名 Claude 草案文件存在，但本计划按 HUAKAI 代码独立推导。 |

## 当前 HUAKAI 运行逻辑

1. 错误分类与 SignalClass 映射

- 通用 429 命中 `ErrorClassRateLimited`：`internal/gateway/error_normalize.go:328-331` 的 R-013 把 HTTP 429 归为 `upstream_rate_limited`、`cooldown`、`ambiguous`。
- Bedrock 429 的类型化限流也归 `ErrorClassRateLimited`：`internal/gateway/error_normalize.go:317-323`。
- `SignalFromClassification` 把 `ErrorClassRateLimited` 映射为 `channelhealth.SignalRateLimit`：`internal/gateway/error_normalize.go:59-63`；如果分类没有显式 class，但 status 是 429，也回落为 `SignalRateLimit`：`internal/gateway/error_normalize.go:77-80`。
- 配额/账号封类已有整号信号入口：`KYCRequired`、`OrgDisabled`、`WorkspaceDeactivated`、`CreditExhausted` 映射为 `SignalAccountSuspended`：`internal/gateway/error_normalize.go:71-73`。其中 KYC/org/token/credit 的铁证规则在 `internal/gateway/error_normalize.go:239-270`、`internal/gateway/error_normalize.go:288-299`。
- 认证类不是健康 FSM，而是 auth 降级车道：`ErrorClassTokenRevoked` / `OAuthInvalidGrant` 映射为 `SignalAuthChallenge`：`internal/gateway/error_normalize.go:67-68`，`channelhealth.Service.ApplySignal` 对 `SignalAuthChallenge` 在进入健康存储前短路：`internal/channelhealth/service.go:87-101`。
- 注意:现有 `SignalForbidden` 不在 `isBanSignal` 里，`isBanSignal` 只认 account suspended / token revoked / credential revoked / account disabled / subscription-workspace disabled / policy auto disabled：`internal/channelhealth/window.go:127-135`。本切片不应顺手扩大 403 语义，除非 Owner 另批。

2. 当前信号记录路径

- `ChatHandlerDeps` 已同时接入账号健康和 per-model 写入器：`ChannelHealth` 在 `internal/gatewayhttp/chat_completions_handler.go:144-147`，`ModelCooldowns` 在 `internal/gatewayhttp/chat_completions_handler.go:149-150`；生产装配见 `cmd/gateway/routes.go:752-754` 和 `cmd/gateway/wiring.go:1338-1341`。
- `recordChannelHealthSignal` 会先写 RecentReqRing，再调用 `ChannelHealth.ApplySignal`，并把 `RateLimitResetAt` 传入 `channelhealth.Signal`：`internal/gatewayhttp/chat_completions_error.go:152-175`。
- raw buffered 非 2xx 路径现在对任何上游非 2xx 先只给 404 调 `recordModelCooldownOnUpstream404`，随后把分类结果写入 channelhealth：`internal/gatewayhttp/chat_completions_handler.go:783-794`。因此 429 当前会落到账号级 `SignalRateLimit`，不落 per-model 格。
- canonical/HCSF 错误路径中，真实上游 HTTP 错误会先 `recordModelCooldownOnUpstream404`，再调用 `forceCooldownFromUpstreamRateLimit`，之后记录 channelhealth 信号：`internal/gatewayhttp/chat_completions_dispatch.go:698-727`。429 当前会进入整号 `ForceCooldown`。
- streaming 非 2xx 路径同样只对 404 记 per-model，然后调用 `forceCooldownFromUpstreamRateLimit`，再写 channelhealth：`internal/gatewayhttp/chat_completions_stream.go:222-245`。
- `forceCooldownFromUpstreamRateLimit` 对 429/529/408/5xx 先走 `RateService.HandleUpstreamError`，拿到 cooldown 后直接 `ChannelHealth.ForceCooldown`：`internal/gatewayhttp/chat_completions_dispatch.go:785-804`；候选 status 范围在 `internal/gatewayhttp/chat_completions_dispatch.go:806-818`。

3. per-model 写入器现状

- `ModelCooldownInput` 已具备 `TenantID`、`ProviderAccountID`、`ModelKey`、`ResetAt`、`Reason`、`StatusCode`、`UpstreamRequestID`，可表达 429 限速：`internal/rate/model_cooldown.go:22-30`。
- `RecordModelRateLimit` 是通用写入器：校验 tenant/account/model，默认 reason 为 `ReasonModelLimitExceeded`，默认 reset 为 `now + defaultCooldown`，最终调用 `SetProviderAccountModelRateLimit`：`internal/rate/model_cooldown.go:72-98`。
- 当前不通用的点是命名与审计来源:常量 `modelCooldownSourceLayer = "gateway_upstream_404"` 在 `internal/rate/model_cooldown.go:14-18`，调用 helper 名为 `recordModelCooldownOnUpstream404` 且仅 `statusCode == 404` 时写入：`internal/gatewayhttp/chat_completions_error.go:248-260`。
- SQL 写入是 JSONB 单账号单模型 key 更新，并追加 `model_rate_limit_set` audit：`sql/queries/pool_accounts.sql:179-222`。schema 已有 `provider_accounts.model_rate_limits`：`sql/migrations/0004_rate_limiting.up.sql:55-57`。

4. channelhealth 如何把账号冷却

- `Signal` 带 `RateLimitResetAt` 字段：`internal/channelhealth/types.go:242-249`。
- `addSignalToWindow` 把 `SignalRateLimit` 计入 `RateLimitHits` 和 `FailedAttempts`：`internal/channelhealth/window.go:45-49`。
- `rateLimitDecision` 在样本数达到阈值且命中率超过阈值后进入 `StateCoolingDown`，优先使用 `sig.RateLimitResetAt`，否则用默认冷却：`internal/channelhealth/service.go:555-573`。
- `ForceCooldown` 不看样本阈值，直接把记录改成 `StateCoolingDown` 并设置 `CooldownUntil`：`internal/channelhealth/service.go:301-345`。现有测试也锁定单个 rate-limit signal 不会冷却、但 `ForceCooldown` 会绕过样本地板：`internal/channelhealth/service_test.go:131-156`。
- 选号侧 `PoolGate.Allow` 读取最新账号健康状态，不合格时返回 `GateFailureHealth`：`internal/channelhealth/failover.go:47-92`。冷却到期时 `maybeStartExpiredRamp` 会尝试转 ramp：`internal/channelhealth/failover.go:113-120`；`IsEligible` 对未到期 `StateCoolingDown` 拒绝、到期放行：`internal/channelhealth/failover.go:123-139`。
- ban signal 仍是整号禁用: `evaluate` 对 `isBanSignal` 直接 `StateDisabled`、24h 起步 cooldown、发高危 alert：`internal/channelhealth/service.go:506-525`。

5. 选号侧 per-model gate 与恢复 ETA

- chat selection request 已传 upstream model 作为 `ModelCooldownKey`：`internal/gatewayhttp/chat_completions_dispatch.go:495-502`；`SelectionRequest` 说明该字段对应 `provider_accounts.model_rate_limits`，为空才回退 requested model：`internal/pool/router/types.go:48-57`。
- DB account source 会把 `model_rate_limits` 解进 `AccountSnapshot.ModelRateLimits`：`internal/pool/dispatcher/account_source.go:56-79`，解析 JSON 字段在 `internal/pool/dispatcher/account_source.go:96-125`。
- `DefaultGateChain` 已把 `Model` gate 设为 `modelRateLimitGate`：`internal/pool/router/gates.go:82-86`，且 gate 顺序中 model gate 在 health gate 之前：`internal/pool/router/gates.go:202-212`。
- `modelRateLimitGate` 只检查当前请求的 model key；当前模型 reset 未到期则返回 `GateFailureModel`，其它模型不受影响：`internal/pool/router/gates.go:253-275`。
- 池级无容量错误已经把健康冷却和当前模型冷却合并计算最早恢复时间：`internal/pool/router/default_selector.go:90-102`、`internal/pool/router/default_selector.go:381-408`。
- `channelHealthKey` 不含 model 维，只含 tenant、vendor、provider account、account credential、credential version，并生成 stable channel id：`internal/gatewayhttp/chat_completions_error.go:137-149`。本次不应给 ChannelKey 加 model 维，避免把健康主体和模型限速主体混在一起。

## 最小改动方案

1. 扩展 per-model 记录 helper，而不是新增存储。

- 在 `internal/gatewayhttp/chat_completions_error.go:248-260` 把 `recordModelCooldownOnUpstream404` 重构为更通用的 helper，例如 `recordModelCooldownFromUpstreamError`。
- 仍保留 404 行为:status 404 写 `ReasonModelLimitExceeded`，默认 reset 5 分钟。
- 新增 429 rate-limit 行为:当 `statusCode == 429` 且 classification class 为 `ErrorClassRateLimited` 时，写同一 `ModelCooldowns.RecordModelRateLimit`，scope 为 `(tenantID, providerAccountID, upstreamModelID)`，status code / request id 原样记录。
- reset 选择顺序:优先复用 `RateService.HandleUpstreamError` 算出的 `CooldownUntil` 和 reason；若 RateService 不可用或返回 no-change，则用 `rateLimitResetFromClassification` 解析到的 Retry-After/body reset；仍没有则交给 `ModelCooldownService` 默认 5 分钟。
- reason 建议沿用 `rate.ReasonRateLimitRPM` 或 RateService 返回的 5h/7d/both reason；不要继续用 `ReasonModelLimitExceeded` 表示 429。
- `modelCooldownSourceLayer` 建议从 `"gateway_upstream_404"` 改为 `"gateway_upstream_error"` 或按 input 传入 source layer。若只改常量值，不改 schema；audit payload 的 `source_layer` 会更准确。

2. 分流 429，避免健康污染。

- 在 raw buffered 非 2xx 分支 `internal/gatewayhttp/chat_completions_handler.go:783-794`，分类后先判断是否是“纯 429 限速”。如果是，写 per-model cooldown，跳过 `recordChannelHealthSignal`，仍保留 abort/retry/failure 语义。
- 在 canonical/HCSF error 分支 `internal/gatewayhttp/chat_completions_dispatch.go:698-727`，把 `forceCooldownFromUpstreamRateLimit` 改造成返回值 helper，例如 `applyUpstreamCooldownDecision(...) (modelScoped bool)`。当返回 `modelScoped=true` 时，不再调用 `recordChannelHealthSignal` 记录 `SignalRateLimit`。
- 在 streaming 非 2xx 分支 `internal/gatewayhttp/chat_completions_stream.go:222-245` 使用同一 helper，保证 stream 与 non-stream 一致。
- `recordChannelHealthSignal` 本身保持通用，不在函数内部特殊跳过 `SignalRateLimit`，避免破坏未来账号级 rate-limit 信号来源。特殊分流只在 chat upstream 429 入口做。
- `RecentReqRing` 由 `recordChannelHealthSignal` 触发：纯 429 不调用该函数，即不会把限速计入账号健康成功率/失败率。

3. 保持账号级冷却/禁用语义。

- 对 `ErrorClassCreditExhausted`、`ErrorClassKYCRequired`、`ErrorClassOrgDisabled`、`ErrorClassWorkspaceDeactivated` 等现有整号类，继续调用 `recordChannelHealthSignal`，让 `SignalAccountSuspended` 走 `channelhealth.evaluate` 的 ban signal 路径。
- 对 `SignalAuthChallenge` 保持 auth 车道短路，不能改成 per-model。
- 对 529 和 transient 5xx/408 仍按现有 `RateService` / channelhealth 路径处理，不下沉到 per-model；这些是容量/过载/传输语义，不是“单模型 429 限速”。
- 如果本切片新增 429 quota-exhausted 分类规则，必须先让它归 `ErrorClassCreditExhausted` 或等价整号 class，再由分流谓词排除 per-model 写入。

4. 分类补强的保守建议。

- 当前 HUAKAI 通用 429 只会落 R-013 `ErrorClassRateLimited`，没有从 429 body 区分 quota exhausted。为了满足“配额耗尽仍整号冷”，需要在 `internal/gateway/error_normalize.go` 的 R-013 之前增加 HUAKAI 自有的窄规则，只匹配明确 quota/billing exhausted 语义。
- 规则范围建议先 provider-specific 或 keyword 极窄，避免把普通 “rate limit quota” 误判成整号封禁。候选词表和 provider 范围需要 Owner/实现者确认；不能从 reference 源码复制 identifier 或原始匹配顺序。
- 如果 Owner 不批新增 429 quota 分类，本切片仍能完成“现有 rate-limited 429 下沉”，但要在风险里记录:带 quota-exhausted 语义却只命中 R-013 的 429 仍可能被 per-model 冷却。

## 关键配合测试

1. 账号不再被整号冷

- 更新 `internal/gatewayhttp/chat_completions_retry_failover_test.go:106-147` 的 429 测试:期望 handler 仍重试到第二账号并成功，但 `recordingChannelHealth.forceCooldowns` 长度为 0。
- 同一测试注入 `recordingModelRateLimiter`，断言写入 tenant/account/model/status/request_id/reset/reason。
- 断言第二次 selector request 仍带 `ExcludedAccounts[first]`，证明单次请求 failover 仍靠 per-attempt exclusion，不依赖整号冷却。

2. 账号健康分/FSM 不被限速污染

- 在 gatewayhttp 新增判别测试:纯 429 rate-limited 后，`recordingChannelHealth.signals` 不包含 `SignalRateLimit`；如果使用真实 `channelhealth.Service`，同一账号记录保持 `StateActive` 且 sample window 不增加 rate_limit hit。
- 保留/新增一条反向测试:quota exhausted / account suspended 信号仍进入 `ApplySignal`，防止实现者把所有 429/4xx 都跳过健康。

3. 配额耗尽/账号封仍整号冷

- `internal/gateway/error_normalize_test.go` 增加 429 quota-exhausted 规则测试（若本切片实现分类补强）:明确 quota exhausted body 不得归 `ErrorClassRateLimited`，应映射到整号类。
- `internal/channelhealth/service_test.go` 或 gatewayhttp 集成测试确认该类信号会使账号进入 `StateDisabled` / `SignalAccountSuspended`，并且不写 `ModelCooldowns`。
- 保留现有 `SignalAuthChallenge` 路径不进健康 FSM 的测试语义，避免把 token revoked 误下沉到模型格。

4. per-model reset 到期恢复

- 在 `internal/pool/router` 或 `internal/pool/dispatcher` 增加测试:同账号 `model_rate_limits["model-a"].reset_at = now+5m` 时，`model-a` 被跳过，`model-b` 仍可选同账号。
- 使用 `WithNow` 推进到 reset 后，`model-a` 重新可选，不需要清除 JSON key；现有 `modelRateLimitGate` 已按时间判断。
- 扩展 `internal/pool/router/no_capacity_recovery_test.go`，覆盖“只有 model cooldown 时 NoCapacityError.EarliestRecoveryAt 等于该模型 reset；同账号健康冷却+模型冷却时取较晚者”的既有语义不要回退。

5. 并发测试

- `internal/gatewayhttp` 增加多 goroutine 测试:同一账号同一模型同时收到多个 429，使用带 mutex 的 fake `ModelCooldowns` 和 `recordingChannelHealth`，断言每次都不 ForceCooldown，不写健康 rate-limit signal，且所有写入 scope 一致。
- `internal/rate` 增加 `ModelCooldownService` 并发单元测试，确保并发调用不会共享可变 input；若有 PostgreSQL integration 环境，再加一条同账号同模型并发 `jsonb_set` 测试，最终 JSON 只有一个 model key，audit 事件数与写入数一致或按实现约定可解释。
- 并发测试要避免“100 goroutines”注释但 N 不一致的弱测试；固定 N 并断言 N 次输入全部被观察到。

## 判别性测试清单

- 删除/绕过 per-model 429 写入应使 gatewayhttp 429 测试红。
- 仍调用 `ChannelHealth.ForceCooldown` 应使 429 测试红。
- 仍记录 `SignalRateLimit` 到 channelhealth 应使健康污染测试红。
- 把 `ModelCooldownKey` 错用为 requested model 时，应被 upstream-model key 断言抓住。
- 把 model gate 改成全账号阻断时，“model-b 仍可选同账号”测试红。
- 把 quota exhausted 429 误归 `ErrorClassRateLimited` 时，整号冷却反向测试红。
- reset 到期仍拒绝时，`WithNow` 恢复测试红。

## 风险与缓解

- 热路径风险:chat stream / non-stream / raw / HCSF 有多条失败路径，漏一条会导致行为不一致。缓解:三条路径共享同一个分流 helper，并分别有测试覆盖。
- 分类风险:429 body 的 quota exhausted 与普通限速容易混淆。缓解:只做窄规则；不确定 provider 不加入整号规则；把未覆盖词表列为 Owner 决策。
- 容量风险:纯 429 不再冷却整号后，同账号其它模型会继续被使用；这是 policy B 目标，但若上游实际是整号 RPM，可能增加 429 次数。缓解:保留 operator custom error rules / account-level 过载路径；必要时后续加 provider/account policy knob。
- 观测风险:跳过 channelhealth 后，rate-limit hit-rate 图可能下降。缓解:依赖 `rate_limit_audit_events` 的 `model_rate_limit_set` 与 routing reason；后续可补模型级 dashboard，不把观测缺口转成健康污染。
- audit 风险:并发 429 可能产生多条 model rate-limit audit。缓解:先接受“每次上游证据一条 audit”的现状；如噪声过大另开去重/合并策略，不在本切片隐式吞 audit。
- clean-room 风险:新增 quota 分类不能照搬 reference 函数名、词表顺序或注释。缓解:基于 HUAKAI 错误归一化表独立设计，review 时检查命名和注释。

## 决策点

1. 429 不带 Retry-After / body reset 时，是否沿用当前默认 5 分钟？
   - 我的建议:沿用现有 `DefaultRateLimitCooldown` / `ModelCooldownService` 5 分钟默认，不新增配置。
2. 429 quota-exhausted 的 provider 与词表范围。
   - 我的建议:本切片只接入最明确的 quota exhausted / billing exhausted 语义；不确定文案先作为 open question，不扩大整号禁用。
3. 纯 429 是否完全跳过 channelhealth，还是写一个不影响 FSM 的观测信号？
   - 我的建议:本切片完全跳过 channelhealth，避免健康分污染；模型级观测走 rate-limit audit。
4. `modelCooldownSourceLayer` 是否改名。
   - 我的建议:改成通用 `"gateway_upstream_error"` 或可传入 source layer。仅影响 audit payload 文本，不改 schema。
5. operator custom error rules 命中 429 时是否仍可整号冷。
   - 我的建议:RateService 决策如果不是 `StateRateLimited`，继续账号级处理；纯 `StateRateLimited` 才下沉 per-model。

## 执行顺序

1. 补 helper 层单元测试:先把 `recordModelCooldownOnUpstream404` 扩展测试写红，覆盖 404、429 rate-limited、非限速状态不写。
2. 改 helper 和 `ModelCooldownService` reason/source layer 支持，保持 404 旧行为。
3. 改 raw buffered、canonical/HCSF、streaming 三条 429 路径共用分流 helper。
4. 更新 gatewayhttp retry/failover 测试，锁定“不 ForceCooldown、不 SignalRateLimit、仍 failover”。
5. 增加 selector per-model 隔离和 reset 恢复测试。
6. 如 Owner/实现者决定纳入 quota-exhausted 429 分类，先补 error_normalize 判别测试，再加窄规则，再加整号禁用配合测试。
7. 跑检查:
   - `go test ./internal/gatewayhttp -count=1`
   - `go test ./internal/rate -count=1`
   - `go test ./internal/channelhealth -count=1`
   - `go test ./internal/pool/... -count=1`
   - 若改 error_normalize: `go test ./internal/gateway -count=1`
8. 按项目规则，代码实施后 staging diff 跑 `codex exec review --uncommitted --full-auto --sandbox read-only`，S0/S1 修完再提交。

## 成本估算

- 实现改动:约 80-160 行 Go，集中在 `internal/gatewayhttp/chat_completions_error.go`、`chat_completions_handler.go`、`chat_completions_dispatch.go`、`chat_completions_stream.go`，可选 `internal/gateway/error_normalize.go`。
- 测试改动:约 180-320 行 Go，主要是 gatewayhttp、pool/router、rate；若加 error_normalize quota 分类再增加约 40-80 行。
- 运行成本:本地 Go 单包测试预计数分钟；若加 PostgreSQL integration 并发测试，需 integration_pg 环境，时间另计。
- 运维成本:纯 429 从账号健康面板转移到模型级 audit，后续 admin UI 可能需要模型冷却可视化增强，但不是本切片阻塞项。

## Open Questions

- 哪些上游 429 body 文案在 HUAKAI 中应被视为“配额耗尽/账号封”而不是“模型限速”？
- 对没有 Retry-After 的 429，默认 5 分钟是否足以覆盖 Antigravity / Codex / Claude 等不同上游，还是需要 provider policy？
- 是否需要在 admin 读接口展示 model-level cooldown 的 `source_layer`、`status_code`、`request_id`，还是继续只依赖 audit event？

## 读过的源文件

- `/home/ubuntu/HUAKAI/.agents/skills/pm-orchestrator/SKILL.md`
- `/home/ubuntu/HUAKAI/.agents/skills/clean-room-license-guard/SKILL.md`
- `/home/ubuntu/HUAKAI/.agents/skills/acceptance-test-writer/SKILL.md`
- `../docs/01_PROJECT_BRIEF.md`
- `../docs/02_CAPABILITY_CONTRACT.md`
- `../docs/03_FEATURE_PARITY_MATRIX.md`
- `../docs/08_REAL_WORLD_SCENARIOS.md`
- `../docs/09_BUG_PATTERN_LIBRARY.md`
- `../docs/10_RISK_REGISTER.md`
- `../docs/11_ACCEPTANCE_TEST_MATRIX.md`
- `../docs/12_AGENT_WORKFLOW.md`
- `../docs/15_RELEASE_GATES.md`
- `cmd/gateway/routes.go`
- `cmd/gateway/wiring.go`
- `internal/gateway/error_normalize.go`
- `internal/gateway/attempt_error.go`
- `internal/gateway/error_apply.go`
- `internal/gateway/upstream_http_error.go`
- `internal/gatewayhttp/chat_completions_handler.go`
- `internal/gatewayhttp/chat_completions_dispatch.go`
- `internal/gatewayhttp/chat_completions_error.go`
- `internal/gatewayhttp/chat_completions_stream.go`
- `internal/gatewayhttp/chat_completions_error_test.go`
- `internal/gatewayhttp/chat_completions_retry_failover_test.go`
- `internal/gatewayhttp/chat_completions_handler_clientadapter_test.go`
- `internal/rate/rate.go`
- `internal/rate/upstream_service.go`
- `internal/rate/model_cooldown.go`
- `internal/rate/model_cooldown_test.go`
- `internal/channelhealth/types.go`
- `internal/channelhealth/service.go`
- `internal/channelhealth/failover.go`
- `internal/channelhealth/window.go`
- `internal/channelhealth/service_test.go`
- `internal/pool/router/types.go`
- `internal/pool/router/gates.go`
- `internal/pool/router/default_selector.go`
- `internal/pool/router/no_capacity_recovery_test.go`
- `internal/pool/dispatcher/account_source.go`
- `internal/pool/dispatcher/health_state_test.go`
- `sql/migrations/0004_rate_limiting.up.sql`
- `sql/queries/pool_accounts.sql`

## 给 Owner 的一句话总结

本计划建议复用 HUAKAI 已有 `provider_accounts.model_rate_limits` 与 selector model gate，把“纯 429 限速”从 channelhealth/ForceCooldown 分流到账号×模型格；配额耗尽、账号封、auth 失败和过载仍保留整号处理。观察结论来自 HUAKAI 当前代码 file:line；reference 只作为行为约束，未读取或复制非 MIT 源码。Open question 主要是 429 quota-exhausted 词表和无 Retry-After 默认冷却时长。
