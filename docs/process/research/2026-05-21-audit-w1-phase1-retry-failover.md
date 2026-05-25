# 2026-05-21 W1 Phase 1 retry/failover 回溯审计

## 0. 元数据与 clean-room 口径

| 项 | 内容 |
|---|---|
| 任务 | HUAKAI 方向 1 Phase 1 单请求重试 / 账号 failover / 跨池路由 / 错误 taxonomy 回溯对比 |
| lane | specifier |
| 参照项目 | sub2api 本地最新解压快照；CLIProxyAPI 本地最新解压快照 |
| 输出性质 | 行为级遗漏清单，不写 HUAKAI 代码 |
| clean-room 约束 | 只转述行为，不复制上游代码、注释、函数/字段命名或实现结构；`file:line` 只作为证据锚点 |
| Observed regions | 58 |
| Inferences | 9 |
| Open questions | 3 |

## 1. 版本口径与 Phase 1 范围

HUAKAI 当前工作树位于 `claude/phase-1`，HEAD 为 `2c2792b3369798f9ccdf37598fe42e24800bc51c`（`.git/HEAD:1`, `.git/refs/heads/claude/phase-1:1`）。本次按本地 reflog 识别 Phase 1 实现提交范围，而不是声明这是唯一官方范围：

- PR1 多候选 planner：`2554057` -> `c4d85f7`（`.git/logs/HEAD:353`）。
- PR2 错误 taxonomy + attempt 决策：`c4d85f7` -> `7de0aec`（`.git/logs/HEAD:354`）。
- PR3 handler attempt loop 骨架：`b3e3514` -> `d75a9a5`（`.git/logs/HEAD:358`）。
- PR4 billing claim 重开/重置路由与凭据：`7827532` -> `4c0430e`（`.git/logs/HEAD:360`）。
- PR5 打开 retry/failover：`b8939e2` -> `3fba163`（`.git/logs/HEAD:362`）。
- 当前 HEAD 还包含 Phase 1 文档记录：`3fba163` -> `2c2792b`（`.git/logs/HEAD:363`）。

参照目录 `/home/codex/refs/sub2api-latest/` 与 `/home/codex/refs/CLIProxyAPI-latest/` 是本地解压快照；目录内未发现 `.git` 元数据，本次不能给出 upstream commit SHA。目录 mtime 均为 `2026-05-21 14:34 UTC`，只能作为本地快照时间口径，不能等同 upstream 版本号。

## 2. 六个维度逐项对比

### 2.1 单请求重试

**sub2api 观察。** 参照 A 有两层请求内恢复：先做同账号短预算重试，默认同账号最多 3 次，固定间隔 500ms；同账号耗尽后再临时排除账号并换号，切换次数默认按平台分级，通用路径为 10 次，某些平台为 3 次（`backend/internal/handler/failover_loop.go:31`, `backend/internal/handler/failover_loop.go:63`, `backend/internal/handler/gateway_handler.go:75`）。上游请求发送层还有单账号内部重试，最多 5 次、总耗时 10s，延迟从 300ms 起并封顶 3s；401/403/429/529/5xx 可进入 failover，部分 400 会走兼容修复或可配置 failover（`backend/internal/service/gateway_service.go:3791`, `backend/internal/service/gateway_service.go:3815`, `backend/internal/service/gateway_service.go:3825`, `backend/internal/service/gateway_service.go:4575`, `backend/internal/service/gateway_service.go:4705`, `backend/internal/service/gateway_service.go:4751`, `backend/internal/service/gateway_service.go:4879`）。

**CLIProxyAPI 观察。** 参照 B 的请求级循环会在一次 mixed 执行失败后检查是否存在可等待的冷却窗口或 429 retry hint；等待时间受最大间隔约束，并受请求重试预算控制（`sdk/cliproxy/auth/conductor.go:1226`, `sdk/cliproxy/auth/conductor.go:1232`, `sdk/cliproxy/auth/conductor.go:1241`, `sdk/cliproxy/auth/conductor.go:1245`, `sdk/cliproxy/auth/conductor.go:2209`, `sdk/cliproxy/auth/conductor.go:2230`, `sdk/cliproxy/service.go:354`）。预算还能按凭据覆盖；测试覆盖显示覆盖值为 0 时不会等冷却重试，覆盖值为 1 时只允许第 0 次之后再等一次（`sdk/cliproxy/auth/conductor_overrides_test.go:18`, `sdk/cliproxy/auth/conductor_overrides_test.go:49`, `sdk/cliproxy/auth/conductor_overrides_test.go:62`）。

**HUAKAI Phase 1 确认。** HUAKAI 将 chat body 读成 bytes，Phase 1 chat path 可重放（`backend/internal/gatewayhttp/chat_completions_validate.go:75`）。attempt loop 的预算来自 route plan，并由 env 开关关闭；单池为 2，多池为 3（`backend/internal/router/default_router.go:100`, `backend/internal/gatewayhttp/chat_completions_attempt.go:150`）。重试门会检查“是否已向客户端交付”“是否最后一次”“body 是否可重放”“taxonomy 是否允许”和 stream end class（`backend/internal/gatewayhttp/chat_completions_attempt.go:187`）。Phase 1 指定链路中未看到重试前 sleep/backoff，也未看到内部等待 `Retry-After` 后再试；`Retry-After` 主要在最终错误响应写回客户端（`backend/internal/gatewayhttp/chat_completions_attempt.go:323`）。

**遗漏判断。**

- HIGH：缺少请求内退避 / 冷却等待，尤其 429/529/5xx 立即换候选会放大热账号和热池压力。参照 A/B 都有等待或 backoff 证据；HUAKAI Phase 1 只有最终 `Retry-After` 输出，没有内部等待。
- MED：缺少“先同账号短重试、再换号”的分层预算。HUAKAI 当前 retryable 失败基本直接切账号/池；这对连接复位、上游瞬时抖动、首包前失败不够细。
- MED：缺少参照 A 的 400 兼容修复类重试。HUAKAI taxonomy 对 400/422/413 多数终止是合理默认，但没有参照 A 中“请求形态可修复后再试”的窄路径（`backend/internal/service/gateway_service.go:4575`, `backend/internal/service/gateway_service.go:4705`）。
- 已有/更好：HUAKAI 的 body replay 判断、交付后禁止 retry、transport 分类比两个参照更规整（`backend/internal/gateway/attempt_error.go:19`, `backend/internal/gatewayhttp/chat_completions_attempt.go:350`）。

### 2.2 账号 failover

**sub2api 观察。** 参照 A 失败后会记录本请求已失败账号，后续选择排除这些账号；若候选耗尽但预算仍允许，针对单账号场景会短暂等待后清空本请求失败集合再试（`backend/internal/handler/failover_loop.go:42`, `backend/internal/handler/failover_loop.go:94`, `backend/internal/handler/failover_loop.go:127`）。401 有特殊路径：OAuth 类失败会先清 token 缓存、强制下次刷新，并临时不可调度；缺失刷新材料则转为错误状态（`backend/internal/service/ratelimit_service.go:181`, `backend/internal/service/ratelimit_service.go:203`, `backend/internal/service/ratelimit_service.go:223`, `backend/internal/service/ratelimit_service.go:238`）。429/529 会落账号限流或过载状态，含 reset header、响应体 reset、默认短冷却与过载冷却（`backend/internal/service/ratelimit_service.go:835`, `backend/internal/service/ratelimit_service.go:850`, `backend/internal/service/ratelimit_service.go:874`, `backend/internal/service/ratelimit_service.go:942`, `backend/internal/service/ratelimit_service.go:1264`）。

**CLIProxyAPI 观察。** 参照 B 的 mixed path 会维护本请求已尝试凭据集合，并在每次失败后标记结果；选择器会跳过不可用、模型冷却、已尝试或被禁用凭据（`sdk/cliproxy/auth/conductor.go:1360`, `sdk/cliproxy/auth/conductor.go:1364`, `sdk/cliproxy/auth/conductor.go:1393`, `sdk/cliproxy/auth/conductor.go:1406`, `sdk/cliproxy/auth/conductor.go:2995`, `sdk/cliproxy/auth/conductor.go:3027`）。状态更新层会按 401/402/403/404/429/408/5xx 设置不同冷却窗口；429 优先使用 retry hint，否则指数扩大冷却；成功会清理状态（`sdk/cliproxy/auth/conductor.go:2311`, `sdk/cliproxy/auth/conductor.go:2346`, `sdk/cliproxy/auth/conductor.go:2560`, `sdk/cliproxy/auth/conductor.go:2766`, `sdk/cliproxy/auth/conductor.go:2806`, `sdk/cliproxy/auth/conductor.go:2838`）。

**HUAKAI Phase 1 确认。** HUAKAI loop 有本请求失败账号集合，只有 taxonomy 决策要求切账号时才加入排除；选择请求会带排除集合（`backend/internal/gatewayhttp/chat_completions_handler.go:166`, `backend/internal/gatewayhttp/chat_completions_handler.go:194`, `backend/internal/gatewayhttp/chat_completions_dispatch.go:214`, `backend/internal/pool/router/gates.go:142`）。401/OAuth invalid grant 或 token revoked 有“一次刷新意图 + auth failover 预算”分支，且不会把所有 auth 失败都当作健康降级（`backend/internal/gateway/attempt_error.go:169`, `backend/internal/gateway/attempt_error.go:184`, `backend/internal/gatewayhttp/chat_completions_attempt.go:193`, `backend/internal/gatewayhttp/chat_completions_error.go:85`）。HUAKAI 也会把分类后的健康信号记录给 channel health（`backend/internal/gatewayhttp/chat_completions_stream.go:170`, `backend/internal/gatewayhttp/chat_completions_error.go:67`）。

**遗漏判断。**

- HIGH：Phase 1 指定链路只看到失败账号的“本请求排除”和健康信号写入，未看到参照 A/B 那种按 401/402/403/404/429/529/5xx 设置明确 TTL 的账号/模型冷却落地策略。若其他 channelhealth 模块已经消费这些信号，应补链路证据和测试；否则会导致热失败账号更快回流。
- HIGH：参照 A 在 sticky/prompt-cache 相关 failover 后设置缓存计费保护标记，避免换号后缓存命中语义影响用量归因；HUAKAI Phase 1 指定 gateway 链路未看到等价计费标记（`backend/internal/handler/failover_loop.go:157`, `backend/internal/handler/gateway_handler.go:900`）。这项触碰 money path，补救需 Owner 确认。
- MED：HUAKAI 有 401 特殊 failover，但没有参照 A 那种“先强制刷新 token 并临时不调度”的完整状态闭环。当前 refresh intent 出现在 attempt 决策里，但是否真正落到账号状态不在本次指定文件中可见。
- 已有/更好：HUAKAI 的 auth-failover 双通道门更明确，普通 retry 与 auth 刷新预算分开，降低无限 auth retry 风险（`backend/internal/gatewayhttp/chat_completions_attempt.go:187`）。

### 2.3 跨池路由 / 候选生成

**sub2api 观察。** 参照 A 在账号选择中分层处理模型路由、sticky、候选过滤、优先级、负载、最近使用时间、并发 slot、候选耗尽后的等待计划；模型路由候选会过滤排除账号、不可调度、平台/模型支持、窗口配额、RPM 等（`backend/internal/service/gateway_service.go:1552`, `backend/internal/service/gateway_service.go:1574`, `backend/internal/service/gateway_service.go:1633`, `backend/internal/service/gateway_service.go:1725`, `backend/internal/service/gateway_service.go:1930`, `backend/internal/service/gateway_service.go:1985`, `backend/internal/service/gateway_service.go:2044`）。OpenAI 兼容账号还有基于优先级、负载、队列、错误率、首 token 延迟的评分选择（`backend/internal/service/openai_account_scheduler.go:589`, `backend/internal/service/openai_account_scheduler.go:648`, `backend/internal/service/openai_account_scheduler.go:682`, `backend/internal/service/openai_account_scheduler.go:743`）。

**CLIProxyAPI 观察。** 参照 B 支持多 provider mixed selection：先标准化 provider 集合，再按模型能力、禁用状态、已尝试集合和 scheduler 选择；模型冷却或不可用时会同步 scheduler 后再试（`sdk/cliproxy/auth/conductor.go:1226`, `sdk/cliproxy/auth/conductor.go:3116`, `sdk/cliproxy/auth/conductor.go:3147`, `sdk/cliproxy/auth/conductor.go:3179`, `sdk/cliproxy/auth/conductor.go:3212`, `sdk/cliproxy/auth/conductor.go:3266`）。对某类额度耗尽还有跨凭据/跨模型候选的补偿路径（`sdk/cliproxy/auth/conductor.go:3719`, `sdk/cliproxy/auth/conductor.go:3751`）。

**HUAKAI Phase 1 确认。** HUAKAI router 会为一个模型生成多个 pool 候选，单池预算 2、多池预算 3；候选理由区分主候选、同池 failover、跨池 fallback（`backend/internal/router/default_router.go:68`, `backend/internal/router/default_router.go:84`, `backend/internal/router/default_router.go:100`, `backend/internal/router/route_plan.go:84`）。池内 selector 有 gate chain、sticky、模型路由、优先级/负载/最近使用排序、slot admission、claim writeback、WaitPlan（`backend/internal/pool/router/default_selector.go:73`, `backend/internal/pool/router/default_selector.go:81`, `backend/internal/pool/router/default_selector.go:95`, `backend/internal/pool/router/default_selector.go:167`, `backend/internal/pool/router/default_selector.go:197`, `backend/internal/pool/router/default_selector.go:252`）。chat dispatcher 会传 prompt/session hash、capability flags、排除集合与 claim id（`backend/internal/gatewayhttp/chat_completions_dispatch.go:214`, `backend/internal/pool/router/types.go:24`）。

**遗漏判断。**

- MED：跨池候选生成仍偏静态，Phase 1 router 只按注册候选顺序和简单预算产出跨池候选，没有参照 A/B 中基于健康、冷却、容量、错误率、首 token 延迟或模型冷却状态调整跨池顺序的证据。
- MED：chat path 当前只从请求中提取 `stream` 作为 feature，未把 tool use、vision、JSON/schema 等请求形态转为 router capability；router 本身有能力 flags 映射，但 dispatcher 没喂完整（`backend/internal/gatewayhttp/chat_completions_dispatch.go:64`, `backend/internal/router/default_router.go:147`）。
- LOW/MED：HUAKAI selector 能返回 WaitPlan，但 gatewayhttp 当前把 WaitPlan 映射为 429/Retry-After 后退出；参照 A 会在请求内等并对流式连接发保活 ping，体验更平滑（`backend/internal/gatewayhttp/chat_completions_dispatch.go:260`, `backend/internal/handler/gateway_helper.go:244`, `backend/internal/handler/gateway_helper.go:290`）。
- 已有/更好：HUAKAI 的 claim writeback 与 request-level exclusion 比两个参照更贴近本项目 billing/admission 原子性目标（`backend/internal/pool/router/default_selector.go:177`, `backend/internal/gatewayhttp/chat_completions_dispatch.go:150`）。

### 2.4 错误分类 taxonomy

**sub2api 观察。** 参照 A 对 401/402/403/429/529/5xx 有状态化处理；429 会从平台头、响应体或默认配置推 reset；529 有单独过载冷却；上游错误响应会抽取多种 body 形态并做安全输出，且可配置错误透传规则（`backend/internal/service/ratelimit_service.go:162`, `backend/internal/service/ratelimit_service.go:181`, `backend/internal/service/ratelimit_service.go:271`, `backend/internal/service/ratelimit_service.go:835`, `backend/internal/service/ratelimit_service.go:1264`, `backend/internal/service/gateway_service.go:6869`, `backend/internal/service/gateway_service.go:6897`, `backend/internal/service/gateway_service.go:6934`, `backend/internal/handler/gateway_handler.go:1352`, `backend/internal/handler/gateway_handler.go:1361`）。

**CLIProxyAPI 观察。** 参照 B 区分请求形态错误、模型不支持、401、quota/cooldown、408/5xx；请求形态错误不会通过换凭据解决，模型不支持可以让路由换候选（`sdk/cliproxy/auth/conductor.go:2669`, `sdk/cliproxy/auth/conductor.go:2694`, `sdk/cliproxy/auth/conductor.go:2733`, `sdk/cliproxy/auth/conductor.go:2766`）。某些 CLI 运行时 429 体里的重置字段会转成 retry hint，模型容量类 400 会转成可重试的限流类状态（`internal/runtime/executor/codex_executor_retry_test.go:11`, `internal/runtime/executor/codex_executor_retry_test.go:64`）。

**HUAKAI Phase 1 确认。** HUAKAI taxonomy 明确区分连接超时、网络超时、TLS、header/body idle、本地 dispatch、上游 4xx/5xx、rate limit、overloaded、OAuth invalid grant/token revoked、KYC、org/workspace、credit、policy、request too large 等；分类会输出 retry action、FSM transition、client status 和 Retry-After ms（`backend/internal/gateway/attempt_error.go:19`, `backend/internal/gateway/error_normalize.go:27`, `backend/internal/gateway/error_normalize.go:162`, `backend/internal/gateway/error_normalize.go:282`, `backend/internal/gateway/error_normalize.go:436`）。HTTP 分类会决定是否切账号/池、是否刷新凭据、是否终止（`backend/internal/gateway/attempt_error.go:143`）。最终错误响应有本地标准形态和 Retry-After header（`backend/internal/gatewayhttp/chat_completions_error.go:16`, `backend/internal/gatewayhttp/chat_completions_attempt.go:323`）。

**遗漏判断。**

- MED：缺少参照 A 的可配置错误透传/兼容错误形态策略。HUAKAI 会输出本地 normalized error；这更利于统一治理，但对依赖 vendor-compatible error body 的客户端可能退化。
- MED：缺少“模型容量类 400 视作可重试限流”的窄规则。HUAKAI 当前有 vendor-specific taxonomy，但本次读取未看到这类 400->retryable capacity 的规则。
- LOW：参照 A 对平台限流头和响应体 reset 解析更细，尤其 5h/7d window 与 body reset fallback；HUAKAI 解析通用 Retry-After，但平台 window 型 reset 需要补场景测试确认。
- 已有/更好：HUAKAI 的 transport taxonomy、StreamEndClass、FSM transition 比两个参照更可审计，且 auth failure 不盲目降健康（`backend/internal/gateway/forwarder_types.go:18`, `backend/internal/gatewayhttp/chat_completions_error.go:85`）。

### 2.5 流式 vs 非流式重试差异

**sub2api 观察。** 参照 A 在流式路径中以 writer 是否已有输出作为 failover 分界：如果已经向客户端写出内容，后续上游错误不再换号，而是以 SSE 错误结束；如果尚未写出内容，则允许 failover。测试覆盖了这两个边界（`backend/internal/handler/gateway_handler.go:750`, `backend/internal/handler/gateway_handler.go:813`, `backend/internal/handler/gateway_handler_stream_failover_test.go:19`, `backend/internal/handler/gateway_handler_stream_failover_test.go:103`）。底层流读取也区分“没有终止事件且未输出”与“已输出后错误”（`backend/internal/service/gateway_service.go:7465`）。

**CLIProxyAPI 观察。** 参照 B 的 stream 执行与 non-stream 共用 retry/cooldown 逻辑，但 handler 层还有“首 payload 前错误可 bootstrap 重试”的保护；一旦已经有 payload，测试证明不会再次换凭据重试（`sdk/cliproxy/auth/conductor.go:1292`, `sdk/api/handlers/handlers.go:789`, `sdk/api/handlers/handlers.go:793`, `sdk/api/handlers/handlers_stream_bootstrap_test.go:399`, `sdk/api/handlers/handlers_stream_bootstrap_test.go:455`, `sdk/api/handlers/handlers_stream_bootstrap_test.go:464`）。

**HUAKAI Phase 1 确认。** HUAKAI 使用 delivery tracker 标记 `WriteHeader` 或实际写 body；attempt loop 在 delivered 后不重试，stream forwarder 在交付前失败才返回 retryable failure（`backend/internal/gatewayhttp/chat_completions_attempt.go:187`, `backend/internal/gatewayhttp/chat_completions_attempt.go:350`, `backend/internal/gatewayhttp/chat_completions_stream.go:99`, `backend/internal/gatewayhttp/chat_completions_stream.go:178`, `backend/internal/gatewayhttp/chat_completions_stream.go:249`）。

**遗漏判断。**

- LOW：CLIProxyAPI 在 handler 层还有额外 bootstrap retry 预算，能覆盖 stream result 建立后、首 payload 前的 chunk error；HUAKAI forwarder 若已经把控制权进入 stream forwarder，会依赖 forwarder 返回未交付失败。当前看设计等价，但建议补“首 payload 前 chunk error 换号”的显式验收测试。
- 已有/更好：HUAKAI 的 delivered gate 是统一 retry 门的一部分，非流式和流式共享同一防线，优于只在 handler 层散落判断。

### 2.6 易漏的小功能

**header 处理。** HUAKAI 在 retry 前会清理 attempt-scoped response headers，避免前一次尝试污染下一次响应；这是参照对比中 HUAKAI 明显更细的点（`backend/internal/gatewayhttp/chat_completions_attempt.go:202`）。参照 A 的上游请求白名单处理很细，会限制传给 upstream 的 header 集合；HUAKAI 本次未审 forwarder header allowlist，因此不判定缺失（`backend/internal/service/gateway_service.go:332`）。

**错误响应兼容。** 参照 A 有可配置错误透传和流式/非流式统一错误形态（`backend/internal/handler/gateway_handler.go:1361`, `backend/internal/handler/gateway_handler.go:1418`, `backend/internal/handler/gateway_handler.go:1491`）。HUAKAI 有统一 local error shape，但兼容透传不是 Phase 1 可见能力（`backend/internal/gatewayhttp/chat_completions_error.go:16`）。

**限流细分。** 参照 A/B 都把 429 分成可从 header/body 得到 reset 的限流、无 reset 的短冷却、模型/账号级 quota 冷却；HUAKAI 有通用 Retry-After 和 health signal，但本次指定链路没看到平台 window 级 reset 解析和模型级冷却（`backend/internal/service/ratelimit_service.go:835`, `sdk/cliproxy/auth/conductor.go:2346`, `backend/internal/gateway/error_normalize.go:436`）。

**并发竞争。** HUAKAI 有 slot acquisition、claim race、WaitPlan 和 idempotency reserve/replay，优于参照项目中的一般“尝试下一个账号”逻辑，更符合 billing/admission 原子性（`backend/internal/pool/router/default_selector.go:167`, `backend/internal/pool/router/default_selector.go:177`, `backend/internal/gatewayhttp/chat_completions_dispatch.go:150`）。参照 A 有请求内等待 slot 的体验细节，HUAKAI 当前返回 Retry-After 让客户端重试（`backend/internal/handler/gateway_helper.go:290`, `backend/internal/gatewayhttp/chat_completions_dispatch.go:260`）。

**观测与日志。** 参照 A 会在 retry 耗尽和上游错误处理时记录 upstream status/body 摘要、request id、错误事件；参照 B 对 auth selection 和每次 result 标记较集中（`backend/internal/service/gateway_service.go:6934`, `backend/internal/service/gateway_service.go:7083`, `sdk/cliproxy/auth/conductor.go:1360`, `sdk/cliproxy/auth/conductor.go:1406`）。HUAKAI 有 route id、audit/cache headers 清理、health signal，但 Phase 1 指定链路未看到“已恢复但中间失败 attempt”的 ops-facing 汇总。

**幂等。** HUAKAI 明显更强：先 reserve claim，冲突返回 409，命中可 replay，retry 时会重置 execution state 并重新 prepare selection（`backend/internal/gatewayhttp/chat_completions_dispatch.go:150`, `backend/internal/gatewayhttp/chat_completions_handler.go:199`）。

## 3. 遗漏清单总表

| 维度 | 细节项 | sub2api | CLIProxyAPI | HUAKAI Phase 1 有无 | 严重度 | 补救工作量 |
|---|---|---|---|---|---|---|
| 单请求重试 | 内部 backoff / cooldown 后再试 | 有：同账号固定等待 + 上游指数退避（`failover_loop.go:31`, `gateway_service.go:3825`） | 有：按冷却窗口或 429 hint 等待，受最大间隔限制（`conductor.go:2209`, `conductor.go:2243`） | 无：最终响应带 Retry-After，但 retry 前未等待（`chat_completions_attempt.go:323`） | HIGH | M：attempt loop 加等待策略、测试 429/5xx/ctx cancel |
| 单请求重试 | 同账号短重试再换号 | 有（`failover_loop.go:63`） | 部分有：同一凭据多模型/冷却重试，凭据失败后换候选（`conductor.go:1389`, `conductor.go:1416`） | 无明显分层；retryable 默认切账号/池（`attempt_error.go:143`） | MED | M：taxonomy 增加 same-account retry action 或 attempt sub-budget |
| 单请求重试 | 可修复 400 的窄重试 | 有（`gateway_service.go:4575`, `gateway_service.go:4705`） | 有容量/模型支持类特判（`codex_executor_retry_test.go:64`, `conductor.go:2669`） | 未见 | MED | M：先以 feature flag + body classifier + tests 接入 |
| 单请求重试 | 请求体可重放 | 有：读取 body 后重建请求（`gateway_handler.go:138`, `gateway_service.go:4535`） | 有：请求对象在 loop 中复用（`conductor.go:1391`） | 有：body bytes + ReplayableBody gate（`chat_completions_validate.go:75`, `chat_completions_attempt.go:18`） | 已有 | S：补验收测试即可 |
| 账号 failover | 本请求失败账号排除 | 有（`failover_loop.go:94`, `gateway_handler.go:325`） | 有（`conductor.go:1364`, `conductor.go:3015`） | 有（`chat_completions_handler.go:166`, `gates.go:142`） | 已有 | S |
| 账号 failover | 账号/模型冷却 TTL | 有：401/429/529 等状态落冷却（`ratelimit_service.go:203`, `ratelimit_service.go:835`, `ratelimit_service.go:1264`） | 有：账号/模型级下一次可试时间（`conductor.go:2311`, `conductor.go:2766`） | 部分：有 health signal；Phase 1 链路未见状态 TTL 落地/消费闭环（`chat_completions_error.go:67`） | HIGH | M/L：若 channelhealth 已有则补接线与测试；否则建状态消费闭环 |
| 账号 failover | 401/auth 特殊路径 | 有：刷新窗口 + 临时不可调度（`ratelimit_service.go:203`, `ratelimit_service.go:223`） | 有：401 标不可重试并进入冷却（`conductor.go:2609`, `conductor.go:2766`） | 部分：auth failover refresh intent 一次预算（`attempt_error.go:169`, `chat_completions_attempt.go:193`） | MED | M：补账号状态落地和恢复测试 |
| 账号 failover | sticky/prompt-cache failover 计费保护 | 有：切换时设置缓存计费保护标记（`failover_loop.go:157`, `gateway_handler.go:900`） | 未见等价专门逻辑 | 未在 Phase 1 指定链路看到 | HIGH | M/L：money path，需要 Owner 确认；加 usage/billing 场景测试 |
| 跨池路由 | 跨池动态排序 | 有：优先级/负载/最近使用/slot/配额（`gateway_service.go:1725`, `gateway_service.go:1985`） | 有：scheduler + mixed provider + 模型冷却（`conductor.go:3212`, `conductor.go:3266`） | 部分：池内 selector 有排序，跨池 planner 静态预算（`default_router.go:68`, `default_selector.go:197`） | MED | M：把 pool health/capacity/cooldown summary 输入 planner |
| 跨池路由 | 请求能力完整提取 | 有：模型/平台/配额过滤（`gateway_service.go:1574`） | 有：模型支持和 route-aware selection（`conductor.go:3018`, `conductor.go:3256`） | 部分：当前 chat 只显式传 stream feature（`chat_completions_dispatch.go:64`） | MED | S/M：补 tools/vision/json/schema flags + tests |
| 跨池路由 | 请求内等待 slot | 有：等待循环、ping、backoff（`gateway_helper.go:244`, `gateway_helper.go:290`） | 未见同等级 admission wait | 部分：生成 WaitPlan，但 handler 直接返回 429（`chat_completions_dispatch.go:260`） | MED | M：先非流式短等，流式要 ping/ctx cancel |
| 错误 taxonomy | 平台 window/reset 解析 | 有：多平台 header/body reset（`ratelimit_service.go:835`, `ratelimit_service.go:874`） | 有：部分 CLI 429 body reset（`codex_executor_retry_test.go:11`） | 部分：通用 Retry-After 解析（`error_normalize.go:436`） | LOW/MED | S/M：补平台 reset parser 或明确 Safe Equivalent |
| 错误 taxonomy | 可配置上游错误透传 | 有（`gateway_handler.go:1361`） | 未重点观察 | 未见；HUAKAI 输出 normalized local shape（`chat_completions_error.go:16`） | MED | M：feature flag + allowlist + redaction tests |
| 错误 taxonomy | transport/TLS/connect/header/body idle 分类 | 未见同等级统一 taxonomy | 未见同等级统一 taxonomy | 有且更细（`attempt_error.go:19`, `forwarder_types.go:18`） | 已有更好 | S：保持回归测试 |
| 流式差异 | 首字节/首 payload 前可 retry，之后禁止 | 有（`gateway_handler_stream_failover_test.go:19`, `gateway_handler_stream_failover_test.go:103`） | 有（`handlers.go:789`, `handlers_stream_bootstrap_test.go:399`） | 有（`chat_completions_attempt.go:350`, `chat_completions_stream.go:249`） | 已有 | S：补 chunk-error 边界测试 |
| 易漏小功能 | retry 前 response header 清理 | 未作为重点观察 | 未作为重点观察 | 有且更好（`chat_completions_attempt.go:202`） | 已有更好 | S |
| 易漏小功能 | 中间失败 attempt 的 ops 汇总 | 有较多上游错误记录（`gateway_service.go:6934`, `gateway_service.go:7083`） | 有 result 标记（`conductor.go:1406`） | 部分：health signal 有，ops-facing attempt summary 未见 | LOW/MED | S/M：追加 attempt audit events |
| 易漏小功能 | 幂等 reserve/replay 与 retry 结合 | 未见同等级 money-path claim | 未重点观察 | 有且更好（`chat_completions_dispatch.go:150`, `chat_completions_handler.go:199`） | 已有更好 | S |

## 4. 按严重度排序的补救建议

### HIGH

1. **补请求内退避 / 冷却等待。** 在 attempt loop 中引入策略层：429 优先遵守 parsed retry hint，529/5xx 用短指数退避，等待受 request deadline、route budget、env flag 限制。先只覆盖非流式和首字节前流式失败；已交付后继续禁止 retry。

2. **补账号/模型冷却闭环证据或实现。** 如果 channelhealth 已经能按 `rate_limit_reset_at`、overload、timeout 等信号影响 selector，需要补到 Phase 1 测试和文档；如果没有，需要把 401/429/529/5xx 的冷却 TTL 写入可被 gate 消费的状态，并确保同请求 failedAccounts 与跨请求 cooldown 不冲突。

3. **补 prompt-cache/sticky failover 计费保护。** 参照 A 暗示 sticky/prompt-cache 失败切换会影响缓存用量归因。HUAKAI 已有 SessionHash/PASR 与 billing claim，这项必须做 money-path 风险评审：定义“换号后缓存读/写如何计费”，再加 usage settlement 测试。此项触碰 billing ledger，应先拿 Owner 确认。

### MED

4. **补同账号短重试预算。** 对连接复位、header/body idle、部分 5xx/529/429，可先在同账号做 1 次短 backoff 重试，再切账号/池。这样不会过早消耗跨池候选，也减少多账号同时打热 upstream。

5. **补请求能力提取。** chat dispatcher 不能只传 stream；至少补 tools、vision、JSON/schema、possibly reasoning/model family capability flags。router 已有 flags 映射，主要工作在 request parser 和 acceptance tests。

6. **补可配置错误透传/兼容形态。** 默认保持 HUAKAI normalized error；为确有兼容需求的租户/endpoint 提供 allowlist + redaction 的 Safe Equivalent，避免把 provider raw body 无限制透给客户端。

7. **补请求内 admission wait。** 先实现短等待上限和 ctx cancel；流式等待可加 ping/heartbeat。不要直接照搬参照实现，按 HUAKAI 的 slot/claim/WaitPlan 契约设计。

8. **补 400 capacity/model-support 窄分类。** 用严格 body classifier + vendor/endpoint allowlist，把“模型容量/模型不支持可换候选”从普通 invalid request 中拆出来。

### LOW / LOW-MED

9. **补平台 window 型 reset parser 和验收。** 通用 Retry-After 已有，但 5h/7d、body reset、capacity fallback 等更细场景应形成 Safe Equivalent 或明确 Mandatory Roadmap。

10. **补 recovered attempts 的可观测性。** 最终成功时也记录中间 attempt 的 upstream status、end class、account/pool、retry delay、是否切换账号，方便解释“用户成功但上游其实抖动”的事件。

## 5. Open questions

1. 本次没有审全 HUAKAI channelhealth / provider account persistence；“冷却 TTL 未见闭环”只限定于 Owner 指定的 Phase 1 gateway/router 文件。若其他模块已实现，应把证据接入 Phase 1 文档与测试。
2. 本次没有打开 billing ledger 细节；prompt-cache/sticky failover 计费风险是从参照 A 行为和 HUAKAI Phase 1 gateway 链路缺口推断，不是断言 HUAKAI billing core 一定没有补偿。
3. 参照项目目录没有 `.git`，不能证明它们正好是 upstream HEAD；后续若要进入正式 parity gate，建议补可追踪 SHA。

## 6. Source files read

HUAKAI:

- `.git/HEAD`
- `.git/refs/heads/claude/phase-1`
- `.git/logs/HEAD`
- `backend/internal/router/route_plan.go`
- `backend/internal/router/default_router.go`
- `backend/internal/gateway/attempt_error.go`
- `backend/internal/gateway/forwarder_types.go`
- `backend/internal/gateway/error_normalize.go`
- `backend/internal/gateway/error_apply.go`
- `backend/internal/gatewayhttp/chat_completions_validate.go`
- `backend/internal/gatewayhttp/chat_completions_attempt.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_error.go`
- `backend/internal/pool/router/types.go`
- `backend/internal/pool/router/gates.go`
- `backend/internal/pool/router/default_selector.go`

sub2api local snapshot:

- `/home/codex/refs/sub2api-latest/backend/go.mod`
- `/home/codex/refs/sub2api-latest/backend/internal/handler/failover_loop.go`
- `/home/codex/refs/sub2api-latest/backend/internal/handler/gateway_handler.go`
- `/home/codex/refs/sub2api-latest/backend/internal/handler/gateway_helper.go`
- `/home/codex/refs/sub2api-latest/backend/internal/handler/gateway_handler_stream_failover_test.go`
- `/home/codex/refs/sub2api-latest/backend/internal/service/gateway_service.go`
- `/home/codex/refs/sub2api-latest/backend/internal/service/ratelimit_service.go`
- `/home/codex/refs/sub2api-latest/backend/internal/service/account.go`
- `/home/codex/refs/sub2api-latest/backend/internal/service/openai_account_scheduler.go`

CLIProxyAPI local snapshot:

- `/home/codex/refs/CLIProxyAPI-latest/go.mod`
- `/home/codex/refs/CLIProxyAPI-latest/sdk/cliproxy/auth/conductor.go`
- `/home/codex/refs/CLIProxyAPI-latest/sdk/cliproxy/auth/conductor_overrides_test.go`
- `/home/codex/refs/CLIProxyAPI-latest/sdk/cliproxy/service.go`
- `/home/codex/refs/CLIProxyAPI-latest/sdk/api/handlers/handlers.go`
- `/home/codex/refs/CLIProxyAPI-latest/sdk/api/handlers/handlers_stream_bootstrap_test.go`
- `/home/codex/refs/CLIProxyAPI-latest/internal/runtime/executor/codex_executor_retry_test.go`

Lane: specifier  
Agent: GPT-5 Codex  
UTC timestamp: 2026-05-21T14:53:21Z

中文摘要：本次真实观察到的内容是 sub2api/CLIProxyAPI 都有比 HUAKAI Phase 1 更细的请求内等待、账号/模型冷却和部分 vendor 兼容错误处理；合理推断是 HUAKAI 需要把 channelhealth、billing claim、PASR/sticky 的闭环证据补齐，尤其 prompt-cache failover 计费保护可能影响 money path；Open questions 共有 3 个，主要是本地参照快照缺少 SHA、HUAKAI channelhealth/billing core 未在本次指定文件中完整展开。
