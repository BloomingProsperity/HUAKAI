# 片3a:429 限速冷却下沉到 per-model 格(账号×模型二维粒度)— Claude 计划草案

日期 2026-07-07。借鉴来源 CLIProxyAPI #1(账号池支柱)。Owner 已拍板 policy B(分类 429:限速下沉/配额封整号)。

## 背景(亲核 file:line,HUAKAI 当前真实运行逻辑)
- **分类器已分好**(无需新建分类):`internal/gateway/error_normalize.go:59-88` `SignalFromClassification`——429/限速→`SignalRateLimit`(:62/:79);配额/组织封→`SignalAccountSuspended`(CreditExhausted/OrgDisabled/WorkspaceDeactivated/KYCRequired,:71-73)。
- **但 429 限速当前落整号**:dispatch/handler 出错(chat_completions_handler.go:793、chat_completions_dispatch.go:726)→ `recordChannelHealthSignal(SignalRateLimit, …, rateLimitResetFromClassification(带 Retry-After))` → 写凭证级 channelhealth。`ChannelKey`(chat_completions_error.go:137-150)**无 model 维** → 整号所有模型冷到 Retry-After。
- **per-model 格只 404 写**:`RecordModelRateLimit`(internal/rate/model_cooldown.go:72)是通用写入器,但唯一生产调用 `recordModelCooldownOnUpstream404`(chat_completions_error.go:248-261)守卫 `statusCode==404`;sourceLayer 常量写死 `gateway_upstream_404`(model_cooldown.go:17)。
- **选号侧两 gate 已就位**:账号级 `IsEligible`(channelhealth/failover.go:88/123)+ per-model `modelRateLimitGate`(pool/router/gates.go:253-278,只挡该模型)+ 聚合读 `earliestPoolRecovery`(pool/router/default_selector.go:382-409,逐账号 max(健康冷却,本模型 reset) 跨账号 min 回池级)。骨架完整。

## 目标
把 **429 限速**(`SignalRateLimit` 且带 Retry-After / 可判定为纯限速)从"整号 channelhealth 冷"改为"写 per-model 格(该账号×该模型,reset=Retry-After)";**配额耗尽/账号封**(`SignalAccountSuspended`)维持整号冷不变。单模型限速不再牵连整号其它模型,提升池容量利用率。

## 方案(最小改动,复用骨架)
1. **新增 429 限速→per-model 写分支**:在记录信号处(chat_completions_handler.go:793 / dispatch.go:726 附近),当 `SignalFromClassification`==`SignalRateLimit` 且有 model 上下文(ex.upstreamModelID 非空、accountID 非空)时,调 `RecordModelRateLimit`(reset 用 rateLimitResetFromClassification 的 Retry-After;Reason=ReasonModelLimitExceeded 或新增 ReasonModelRateLimited;StatusCode=429)。sourceLayer 常量从 `gateway_upstream_404` 泛化(如新增 `gateway_upstream_429` 或改成入参),别复用 404 语义。
2. **429 不再喂整号健康 FSM 冷却(关键配合点)**:改路由后,`SignalRateLimit` 不应再让 channelhealth 把整号打冷/降健康分——否则白改。需确认 channelhealth 对 SignalRateLimit 的处理:若它把账号打入不可选(IsEligible=false)或降 Score,要么①限速信号不再送 channelhealth(只送 per-model 格 + RPM ring),要么②channelhealth 对 SignalRateLimit 只记不冷账号。**必须亲读 channelhealth service.go 对 SignalRateLimit 的 FSM 反应**再定,不能猜。保留:RPM ring(RecentReqRing)仍记(限速是真实流量信号)。
3. **配额/封号路径零改动**:`SignalAccountSuspended` 继续走 channelhealth 整号冷(config/org 级封本就该整号)。
4. **格字段**:先只写 reset_at + reason + status(现有 model_rate_limits JSONB 已够);workflow 提的"配额耗尽标志/退避层级/末次错误"字段扩展**推迟**(本片不做,避免范围膨胀)。

## 模块配合验证(§17,重点)
- **配合点 1**:429→per-model 写 与 选号 modelRateLimitGate 读的闭环——写入后该账号该模型被 gate 挡、其它模型仍可选、其它账号该模型仍可选。
- **配合点 2**:429 不污染 channelhealth 账号态——限速后账号对其它模型仍 IsEligible=true、Score 不降。
- **配合点 3**:配额封(SignalAccountSuspended)仍整号冷,不误落 per-model。
- **配合点 4**:per-model reset 到期后自动恢复(earliestPoolRecovery 背压 + 到期清态)。
- **并发**:多请求并发打同账号同模型限速,per-model 写幂等/最新 reset 生效,不重复冷、不泄漏。

## 测试(§14 判别性 + §17 配合)
- 单测:SignalRateLimit+model 上下文→RecordModelRateLimit 被调(reset=Retry-After);SignalAccountSuspended→不调 per-model、走整号。变异:去 429 分支→限速仍整号冷→配合测试红。
- 配合测试:构造 429 打账号A模型X → 断言 A 的 X 被 modelRateLimitGate 挡、A 的模型Y 仍可选、账号B 的 X 仍可选、A 的 channelhealth Score 不降。变异:429 仍走整号 channelhealth→"A 的模型Y 仍可选"断言红。
- 集成:pool router 级 earliestPoolRecovery 对 per-model reset 的聚合正确。

## 爆炸半径 / 风险 / 决策点
- 触**账号池选号热路径 + 配额分类语义**(Owner-gated,已获 policy B 授权:限速下沉/配额封整号)。
- 风险:若 429 判定不准(把配额误判成限速)→ 该账号该模型被短冷但其它模型继续试 → 可能连环 429 加重封号。缓解=严格用分类器已有的 SignalRateLimit(它已排除 SignalAccountSuspended),只对"纯限速带 Retry-After"下沉。
- **决策点(surface Owner)**:纯限速但**不带 Retry-After** 的 429 怎么办?(a)用默认冷却时长(model_cooldown.go defaultCooldown=5min)下沉 per-model;(b)保守整号冷。倾向 a(与 CLIProxyAPI modelQuotaExceededWindow 默认窗口一致)。
- 不触 schema(model_rate_limits JSONB 已存在)、不触 money ledger。

## 成本
中(骨架 ~80% 在)。核心 = 新增 429→per-model 写分支 + channelhealth FSM 对 SignalRateLimit 不冷账号的处理 + 配合测试。估 3-4 人日。

---

## #10 交叉讨论综合(Claude+codex)+ Owner 决策(2026-07-07)

两份独立计划(claude + codex 各一)交叉对比:
- **核心一致**:429 纯限速下沉 per-model 格,配额/账号封维持整号冷。
- **codex 计划更完整,抓到 Claude 草案漏的**:①第二条整号冷路径 `forceCooldownFromUpstreamRateLimit`→`ChannelHealth.ForceCooldown`(chat_completions_dispatch.go:785-803,绕样本阈值)——两条整号冷机制都要分流;②三条代码路径(raw buffered / canonical HCSF / streaming)须共用一个分流 helper;③429-quota 分类 gap。
- **裁定:以 codex 计划为实现基**,合入 Claude 的 channelhealth 机制细节(service.go:564 RateLimitResetAt→StateCoolingDown、failover.go:123 IsEligible)。

**Owner 决策:B-full**——OpenAI/codex 的 `insufficient_quota` 用 429,现落 R-013 会被误下沉抖死号。故加 HUAKAI 自有窄规则:429 带明确配额耗尽语义(insufficient_quota / billing_hard_limit 等)→ 归整号类(account-level),其余纯限速 429→per-model。clean-room:HUAKAI 自定 keyword,不抄 sub2api classifyAntigravity429 的标识符/词表顺序。

**429 无 Retry-After**:沿用 ModelCooldownService 默认 5min(与 CLIProxyAPI modelQuotaExceededWindow 默认窗口同向)。

---

## 实现落地 + PM 验收(2026-07-07)

codex 实现(后台任务 b7lfuoz5v),PM 亲检 + 独立跑门,codex 自报一律不信。

### 落地内容
- **分流 helper**:`applyUpstreamErrorCooldown(upstreamErr, classification, applyAccountCooldown) bool` 统一三路径(raw/canonical/streaming);返回 `true`=已下沉模型格、调用方跳过 channelhealth 信号。404→写模型格(reason=model_limit_exceeded);429 且 class≠RateLimited(配额)→return false 交上层整号;429 纯限速→查 RateService 决策,非纯限速态→forceCooldownFromDecision 整号,纯限速→写模型格(reset 用 RateService 的 CooldownUntil,回退 Retry-After)。
- **两条整号冷都分流**:信号 FSM(recordChannelHealthSignal,靠 `!modelScopedRateLimit` 守卫跳过)+ ForceCooldown 路径(forceCooldownFromUpstreamRateLimit,429 走 helper 分支不再无条件冷)。
- **B-full 窄配额分类**:R-026~029(openai/codex × insufficient_quota/billing-hard-limit,priority 48 抢在 R-013 前)→ CreditExhausted→SignalAccountSuspended。HUAKAI 自定关键词,未抄镜像词表。normalizeProvider 补 openai_codex→codex。
- **sourceLayer** gateway_upstream_404→gateway_upstream_error(泛化)。
- **HandleUpstreamError 确认纯决策函数**(upstream_service.go:175-246,只读 now/规则/头返回 Decision,零 mutation),故 raw 路径调它取 reset 无副作用。

### PM 门禁结果(全自跑)
- gofmt 干净 / go build ./... 0 / go vet 0 / quality-gate PASS(staticcheck 94、deadcode 879、codebudget 绿)/ 全量 `go test ./...` **233 包全绿零失败**。
- **变异证红三发**(cp 备份法,非 git 还原):①R-026~029 Class 改 RateLimited→配额分类测试全红(含 PR5 端到端配额429 变异后返200=被当限速换号,反证 B-full 语义);②抽 429→per-model 写→per-model 测试全红(calls=0);③去 canonical 的 `!modelScopedRateLimit` 守卫→纯429污染账号健康(SignalRateLimit=1)红。还原后三文件逐字节一致、基线复绿。

### §17 配合测试覆盖(判别 + 并发)
gates_test:429打A模型X → A的X挡/A的Y放/B的X放/到期恢复/账号健康门不降;selector 级 fall-through 到账号B。error_test:纯429→per-model写+reset+零ForceCooldown;配额429→零per-model+account_suspended;insufficient_quota 端到端 channelhealth State=Disabled;24 goroutine 并发全 modelScoped 且零 health 污染。

### ⚠️ 待 surface Owner 的语义观察(非 blocker,S3)
**配额 429 现不换号**:B-full 后配额429 归 CreditExhausted,`decisionFromHTTPClassification`(attempt_error.go:191-239)对 CreditExhausted **不设 SwitchAccount** → 当前请求返 429 给客户端、不 failover 到健康账号(片3a 前落 R-013→RateLimited→换号救活当前请求)。
- **性质**:与既有 402 credit-exhausted 行为一致;是 Owner 已批 B-full「配额封整号」的直接后果;只影响触发那一个请求(后续请求跳过被封账号→路由到健康号)。等于用「1 个失败请求 + 干净封号」换掉「0 失败但抖死号(临时冷→复活→再429)」,正是 Owner 要治的抖号。
- **可选增强(需 Owner 拍板,money 邻域)**:若要「封号 + 当前请求仍 failover」两全,需给 CreditExhausted(或专设 quota-exhausted 决策)加 SwitchAccount——但会改所有 CreditExhausted(含 402)行为,属 money-adjacent 默认翻转,Owner-gated。默认**不改**,先按 B-full 一致语义落地。
- **usage_limit_reached 不误伤**:codex 5h/周窗口临时限速关键词是 usage_limit_reached,不含 insufficient_quota,故不被 R-027/029 捕获,仍走限速→per-model+换号+自愈(正确)。

### 注释条化说明
把 R-005/R-018/R-021/R-022/R-023 的注释条化(删抓取URL+日期化 provenance),纯注释无逻辑改动:①与 Owner 注释精简/干净硬规则(禁日期化 provenance)同向;②**§13 预算必需**——error_normalize.go 加 R-026~029 后逼近 600 行上限,条化腾出空间(现 597 行);provenance 留在 docs/reference_delta/2026-05-06/vendor-drift-audit.md 权威文档(R-005 注释保留该 doc 路径指针)。

### 独立 codex 审查(换 lane)+ PM 复核修复
独立 codex 会话审查(read-only,内嵌 diff),出 3×S1+1×S2+1×S3;报告不信,逐条对真码复核:
- **S1(采纳修)模型格写失败仍抑制账号健康**:纯429 分支无条件 return true,但 recordModelCooldownFromUpstreamError 在 ModelCooldowns 未接线/modelKey 空/DB 落库报错时静默 no-op → 跳 channelhealth → 该模型零冷却被立刻重选反复挨限速(片3a 前 429 必经 channelhealth 冷,是真回退)。**修**:record 返回 bool,写成功才 return true 抑制;失败 return false 回落账号健康。补判别测试(注入 DB 错→断言 modelScoped=false),变异证红。
- **S1(采纳修)billing hard limit 只匹配空格文案**:漏机器码形 billing_hard_limit_reached → 落 R-013 限速。**修**:关键词改下划线机器码形 billing_hard_limit(命中 code/type 字段更可靠),测试 fixture 同步改 code 形,变异证红。
- **S1(降级为 surfaced Owner-gated)配额429 不换号**:= 上文换号语义观察。codex 纯可用性视角判 S1;PM 复核=Owner 已批 B-full「封整号」直接后果 + 与既有402一致 + 改 failover 属 money/quota-enforcement Owner-gated,非片3a 新缺陷,不 block,留 Owner 拍板(见下「片3b 提案」)。
- **S2(defer)disableCooling 被 per-model 绕过**:核实为**非片3a 回退**——片3a 前 429 走 channelhealth 冷本就不认 RateService.disableCooling(它是另个服务),行为等价延续;记录不修。
- **S3(采纳)注释 provenance**:见「注释条化说明」,R-005 保留 doc 路径折中。

### 片3b 提案(surface Owner,非本片):relay 语境下配额耗尽应否换号
中转站语境强论据:上游账号的配额/欠费状态**不该泄漏给 relay 客户端**——客户端付的是 HUAKAI,上游某号欠费应对客户端透明(换到健康号)而非把 402/429 透传。当前 CreditExhausted(含402 Anthropic 与新 429-quota)都不换号=可能是既有 402 的潜在设计缺陷,片3a 只是延续。**片3b 提案**:给 CreditExhausted 设计「封当前账号 + 交付前换号到健康号」的 decision(影响 402 与 429-quota 两者,money/quota-enforcement Owner-gated)。默认不动,待 Owner 定。
