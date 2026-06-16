<!-- Ask Hermes — 嵌入式诊断助手设计方案 v1 · 2026-06-16 · PM: Claude -->
<!-- 证据基: ask-hermes-design 工作流(4 并行 reader 摸真实契约 → 设计 → 对抗校验);全部 file:line 引自 HUAKAI 后端/前端真源 -->
<!-- Clean-room: 三家参考为行为/范式 paraphrase + repo@sha:path 引用,未抄任何源码标识符;Ask-AI-on-error 范式取自浏览器侧通例(documented no-equivalent) -->

# Ask Hermes —— 嵌入式诊断助手设计方案 v1

> **一句话**:把 Chrome 每页的「Ask Gemini」做成 HUAKAI 的「**Ask Hermes**」——管理员在任意 admin 页(尤其冒出 429/4xx/5xx 报错时)一键唤起,内置**诊断 skill** 快速**定位成因**并用人话**解释给管理员**,按需调用 Hermes 的只读诊断工具取证。
>
> **本文档定位**:Owner 决定「先深化方案文档,暂不写代码」。这是可直接照建的设计 + 内置 skill(runbook)规格 + 诚实的缺口标注。落地节奏待 Owner 拍板。
>
> **可行性定论(经对抗校验)**:✅ 可建,且 **Phase-1 几乎零后端改动**。它复用现有 `/admin/hermes` 的 SSE 流式聊天 + 管理员鉴权;**严格只读、结构上走不到任何改动路径**——所以"接它反而炸系统"的担心不成立(见 §4)。两处诚实缺陷必须先修(§7.3 的 429 取证边界 + §6 的前端 `ApiError` 字段保留),均非致命。

---

## 1. 愿景与定位

| 维度 | 取舍 |
|---|---|
| **受众** | **管理员/操作员**(admin-only)。Hermes 后端默认 admin-only 且 fail-closed,正好匹配"解释给管理员"。终端用户版列入 Phase 4 慎重决策(§10),v1 不做。 |
| **形态** | 全局可唤起的**右侧抽屉(drawer)** + **每个错误旁的内联「问 Hermes」按钮**。不是独立页,是贴着错误现场的助手。 |
| **能力边界** | **只读诊断 + 解释**。能查证据、能"建议"修复动作,但**绝不替人执行任何改动**。 |
| **核心差异** | 不是套壳 chatbot——价值在**内置诊断 skill(runbook)**:固定输出「成因→证据→修复→何时升级」,并把 HUAKAI 的错误分类(尤其 429 的 4 种成因)写死成决策表(§7)。 |

参考三家(clean-room):`sub2api@e34ad2b`(Vue)、`new-api@1ac0f58`(React `web/`)均以 toast/通知呈现 API 错误,**无"用 AI 解释这个错误"的等价能力**;`CLIProxyAPI@2a050dc` 无前端。故本范式取自浏览器侧 Ask-AI(Chrome Ask Gemini / DevTools Console insights)通例 —— documented no-equivalent,属 HUAKAI 在 Hermes 只读诊断工具之上的自有面。栈沿用融合定论(见 [[sub2api-frontend-reuse-verdict]]):Next.js 15 + React 18 + Tailwind v4 + shadcn,不 fork。

---

## 2. 端到端流程

```
[admin 页渲染] ──► 发生错误(如任一 admin/user API 调用返回 429)
       │                         │
       │                         ▼
       │            client.ts/userClient 抛出 ApiError(status, payload)
       │            页面捕获 → 渲染内联 Banner(沿用 admin/hermes/page.tsx:146 范式)
       │                         │
       ▼                         ▼
 (A) Header 全局           (B) 错误 Banner 上的「问 Hermes」按钮
 「Ask Hermes」按钮          携带捕获的错误信封(envelope)
       │                         │
       └─────────────┬───────────┘
                     ▼
       AskHermesProvider.open({ envelope?, route?, userQuestion? })
                     │  从 ApiError + 响应头 + error.details 构建上下文信封(§6)
                     ▼
       AskHermesDrawer(右侧滑出)—— 复用 ChatBubble + SSE 管线
                     │
                     │  ── PHASE 1(零后端改动)──
                     │  前端组装 1 条 messages[0]:
                     │    content = [诊断 skill runbook 文本] + [错误信封 fenced JSON] + [管理员追问]
                     │  (chat 契约今天只认 messages[].content,无 context/system_preamble 字段:
                     │   bridge_request.go:159 validateChatPayload 仅校验 messages → 把 skill+信封内联进 content,这是诚实路径)
                     ▼
       startHermesChat(scope, { messages }, signal)   [lib/api/hermes.ts:214]
         POST /v1/hermes/chat?tenant_id=&as_user_id=
         Authorization: Bearer <huakai_admin_token>   ← admin 轨,localStorage 已有,无新鉴权
                     ▼
       网关 chat_handler.startChat [chat_handler.go:15]
         → GetSettings(启用?否则 hermes_disabled)
         → requireRunner(默认 OFF,除非 3 个 runner 环境变量已设)
         → PrepareRequest:建会话、铸 internal_token(HMAC 5min)、
           绑定 SessionOperator(role+admin_token_id)到 request_id、
           注入只读 tool_catalog(仅因 admin 绑定)[bridge_request.go:104-131]
                     ▼
       runner(外部 LLM)流式返回;过程中回调
         POST /internal/hermes/tool-execute  Bearer <internal_token>  [routes.go:380]
         → 多道顺序鉴权门 [internal_tool_handler.go:118-194]
         → 只读过滤门(gate 6)拒绝任何改动类工具:结构性护栏,本功能永不触改动路径
         → 跑 6 个只读诊断工具之一,返回脱敏摘要
                     ▼
       SSE 回抽屉: event: conversation{id} → token{delta}* → done{total_tokens}
         由 lib/sse.ts parseSSEStream + hermes.ts parseTokenDelta 解析,逐字渲染
                     ▼
       答案结构由 skill 强制: 成因 → 证据(工具输出)→ 修复建议 → 何时升级
       同一抽屉内追问复用同一 conversation_id
```

**两条已被真源坐实的事实(校验纠正了旧情报)**:
1. `routes.go:320-369` 把整个 `/v1/hermes/*` 子树(含 `POST /chat`)挂在**单一** `hermesAuth` 中间件后;当 `HUAKAI_HERMES_ADMIN_ONLY` 为真(默认,且 fail-closed,见 `hermes_admin_gate_test.go:108-148`)它就是 `AdminAuthMiddleware`。**所以 Ask Hermes 用 localStorage 里的 admin token + `?tenant_id&?as_user_id` 鉴权,和现有 `/admin/hermes` 页一模一样,零新增鉴权。**
2. 工具回路**仅在 admin 绑定会话**下武装(`bridge_request.go:110-131`:`Operator.Role != "" && AdminActorTokenID > 0` 才注入 tool_catalog)。我们的调用者正是 admin,所以诊断工具可用——这恰是终端用户路径拿不到的。

---

## 3. 入口(entry points)

1. **全局 Header 按钮**:在 `components/layout/Header.tsx:121-129` 右侧加一个图标按钮(lucide `Bot`/`Wand2`)。每个 `/admin/*` 页常驻,无错误信封时开"自由问"模式。这是字面意义上的"每页 Ask Gemini"。
2. **每错误内联按钮(centerpiece)**:扩展现有错误 Banner(`admin/hermes/page.tsx:146-161` 范式,各 admin 页通用),尾部加「问 Hermes」。点击把活的 `ApiError`(status/code/message/request_id/Retry-After/details)装进信封并预置开抽屉。**这就是"尤其 429 冒出来时"的主 UX。**
3. **页面上下文按钮**:各 admin 页把 `route` + 主作用域实体(如 `/admin/accounts` 的 accountId、`/admin/channels` 的 channelId)传给 Provider,让全局按钮即便无错误也知道"我在哪页",能给页面相关诊断建议。
4. **(预留)Toast 动作区**:将来引入 toast 库后,每个错误 toast 配一个动作按钮 → 同一 `provider.open({envelope})`。现在设计预留,后接。

---

## 4. 鉴权与安全 —— 为什么"接它不会多出炸点"

- **Admin-only,复用现有 token**:`/chat` 在默认部署下走 `AdminAuthMiddleware`;Ask Hermes 沿用 admin 轨,无新鉴权面。
- **结构性只读**:工具回路只挂 6 个只读诊断工具;`internal_tool_handler.go:164` 拒绝 `Mutating || !ReadOnly`,`Registry.Run` 再以角色下限 + `ErrNotMutating` 二次拦截。**改动类工具结构上不可达**。Ask Hermes 只会用文字**建议**改动动作(让人去执行),自己绝不执行。
- **默认 OFF**:Hermes 整体默认关,需 3 个 runner 环境变量 + 一个真部署的 runner 才挂载(见 §11 风险)。未挂时 Ask Hermes 优雅降级,不报错刷屏。
- **不新增任何 mutation 路径**:校验确认设计无一处开改动口。先前对 Hermes "会不会炸系统"的担心,在只读诊断这个用法下不成立。

---

## 5. 内置诊断 skill(runbook)—— 本方案的中枢

这是让 Ask Hermes **从 chatbot 变成诊断师**的东西。Phase 1 它内联进 `messages[0].content`;Phase 2 迁为**服务端 preamble**(§8 缺口②),防被用户消息篡改、且每次省 token。

### 5.1 身份 + 硬规则(preamble 原文意向)
> "你是 Hermes,HUAKAI 的**只读**网关诊断师。你用人话向管理员解释运维错误。你**只能**调用只读诊断工具(`request_diagnose`/`account_health_diagnose`/`credential_diagnose`/`dlq_inspect`/`audit_lookup`/`log_analyze`)。你**绝不**亲自执行任何改动、配置写入、密钥轮换或重启——这类动作你只能**建议**人去做。永远按四段式回答:**成因 → 证据 → 修复建议 → 何时升级**。证据每条都要绑到具体信号(信封字段或某工具结果字段)。工具不可用/被拒时,如实说明,并基于错误信封给出**临时**判断。用管理员的语言作答(默认中文)。"

### 5.2 输出契约(每次作答)
1. **成因** —— 一句话点名最可能的单一根因 + 置信词(确定 / 很可能 / 待确认)。
2. **证据** —— 逐条绑到具体信号(信封 status/code/retry_after,或某工具结果字段)。不许空话。
3. **修复建议** —— 有序,blast-radius 最小者优先;**明确标注哪些步骤是需人执行的改动**。
4. **何时升级** —— 阈值 + 升级给谁(operator vs platform_admin vs 上游 provider 工单)。

### 5.3 ⭐ 429 的四种成因(最难的部分)+ **诚实取证边界**
四者都呈现为 HTTP 429,但 `error_code` 不同、修复不同。skill 先认 `error_code`,再按下表确认。
**关键诚实修正(校验抓出):四种里只有两种能被只读工具"坐实"。**

| # | 成因 | 主信号 `error_code` | **能否工具取证** | 确认/判断方式 | 修复建议 | 升级阈值 |
|---|---|---|---|---|---|---|
| ① | **入站 IP/单 operator-token 限流**(网关自身限流器) | `rate_limited`,常带 `details.tier`=global\|auth_strict\|media_strict | ❌ **仅凭错误体**(`rate_limit.go:341-358` 只写 429 JSON,**不落任何 usage/audit 行**;无工具能查到"按 IP/端点的请求速率") | 据 `error_code` + `tier` + `Retry-After` 判断;**skill 必须如实说"无工具可证,仅凭错误体"**,不得编造工具证据 | 退避 Retry-After 秒;脚本狂打则限流它;合法 operator 撞桶则调高配置速率(人改配置) | 退避后仍持续 429,或限流对合法负载过低 → platform_admin 调 `HUAKAI_HERMES_MUTATE_RATE_PER_TOKEN`/入站 tier |
| ② | **用户配额耗尽**(余额或按窗口 token 上限) | `insufficient_quota`(429 窗口)/ `insufficient_balance`(402 余额) | ✅ **工具可证** | `account_health_diagnose`(余额+配额窗口态)、`audit_lookup`(近期花费) | 窗口型:等到 `window_resets_at`,或调高用户配额(人);余额型:充值/拨信用(人,计费路径,**非** Hermes 执行) | 余额确实耗尽且用户合法 → 计费 operator;配额策略疑似配错 → platform_admin |
| ③ | **上游 provider 账号冷却**(provider 返 429 透传) | `upstream_rate_limited` | ✅ **工具可证** | `account_health_diagnose` + `credential_diagnose`(绑定账号/凭证是否冷却/被限)、`request_diagnose`(该次尝试的上游状态) | 等账号冷却结束;确保池内有其他健康账号可改路;某账号长期被限则降权(人)。注:Bedrock ThrottlingException 归一化为 `rate_limited` 非 `overloaded`(R-018) | 池内全部账号同时被限 → 容量问题,platform_admin 加账号/联系 provider |
| ④ | **Hermes 改动护栏拒绝**(改动类操作的并发上限/单 token 滑窗) | `hermes_busy` / `ErrBusy` 以 429 呈现 | ❌ **仅凭错误体**(`mutateguard.go` 纯内存信号量+滑窗,**啥都不落库**;无工具能查到护栏拒绝) | 仅出现在 Hermes 改动端点;`ErrBusy` 于 acquire 超时(2s)或超预算(默认 30/min/token);据此判断,不得编造工具证据 | 降低并发改动调用;窗口后重试;确属合法突发则调高 `MUTATE_MAX_CONCURRENCY`/`RATE_PER_TOKEN`(人改配置) | 护栏拒绝正常单操作员用法 → 上限配错,platform_admin |

**决策助记(skill 内置)**:`upstream_provider` 存在 → ③;`window_resets_at`/`insufficient_quota`/`insufficient_balance` → ②;`tier∈{global,auth_strict,media_strict}` 或 `rate_limited` → ①;源自 Hermes 改动端点/`hermes_busy` → ④。`error_code` 模糊时,先用 `request_diagnose(request_id)` 定位哪层发的 429(注意 §7.3 的 best-effort 边界)。

### 5.4 其余分类条目(非 429,同四段式)
- **401/403**:`unauthorized`/`forbidden` → `audit_lookup`(token 角色+近期拒绝);若是 provider 凭证失败用 `credential_diagnose`。修:重新鉴权/授权(人)。
- **402 余额不足**:走 ② 余额分支。
- **5xx / upstream_error / no_capacity**:`request_diagnose`(尝试链)+ `account_health_diagnose`(池健康)+ `dlq_inspect`(是否落死信队列)。修:failover/重试;池空则升级。
- **死信/卡住的异步任务**:`dlq_inspect` 列出排队/失败项及原因。
- **凭证过期/轮换**:`credential_diagnose` 暴露续期态(active/refreshing/expired/needs_rotation/revoked,见 `types.ts AuthCredentialRenewState`)。修:轮换(人)。

### 5.5 工具调用纪律(写进 skill)
- 从信封起步,调**最少**工具确认假设(通常 1–2 个),别六个全 fan-out。
- 严格按 §5.3/5.4 的"症状→工具"映射;类别不匹配的工具不调。
- 工具返回 `denied`/`error_class` 时,在"证据"里**如实**标为缺口("因工具返回 role_forbidden 未能确认 X"),不得据此编造成因。网关工具失败只回脱敏 `error_class`(§8 缺口⑥),skill 把它当线索非堆栈。
- 租户作用域由会话绑定钉死(runner 不能覆盖),skill 永不向用户要 tenant_id。

---

## 6. 上下文信封(context envelope)+ **必须的前端前置改动**

抽屉把以下 JSON 在 Phase 1 序列化进 `messages[0].content` 的 fenced ```json 块(Phase 2 改走顶层 `context` 字段)。全是只读元数据,**不需新后端**——但**需要一处小而必须的前端改动**(校验纠正了"零成本"的说法)。

### 6.1 前端前置改动(REQUIRED,非零成本)
现状:`ApiError`(`lib/api/client.ts:22-36`)只存 `{status, code}`(+ `Error.message`),**丢弃** `payload.error.request_id`、`payload.error.retry_after_seconds`、`payload.error.details`,以及**所有响应头**(`parseResponse`/`userClient.ts:77-89` 构造 `ApiError(status, payload)` 时把 Response 对象扔了)。
信封依赖 `request_id`、`retry_after_seconds`、`rate_limit_reason`(=`details.tier`)、`upstream_provider`/`upstream_status`(=`details.*`)、`window_resets_at`(=`details.*`)、以及 `Retry-After`/`X-Request-ID` 头。
**所以必须**:扩展 `ApiError` 保留 `payload.error.{request_id,retry_after_seconds,details}`,并在 `client.ts` **和** `userClient.ts` 两处解析点捕获 `Retry-After`/`X-Request-ID`/`X-Correlation-ID` 头。**这是最高价值的单点前端改动**——它直接支撑 §5.3 里**真正能工具取证的** ② ③。不改,信封只有 status+code+message,②③ 失去区分字段。约 1 个小文件改动,**非后端改动**。

### 6.2 信封字段
```
schema_version: "1"
kind: "ask_hermes_error_context"
http_status:          number     // ApiError.status (429/402/403…)
error_code:           string     // body error.code (rate_limited|insufficient_quota|upstream_rate_limited|hermes_busy…)
error_message:        string     // 后端原始 message(非 friendlyMessage 翻译,让 Hermes 见 ground truth)
request_id:           string|null// 响应头 X-Request-ID/X-Correlation-ID 或 body error.request_id
endpoint:             string     // 失败请求的 path
method:               string     // 失败请求的 HTTP method
retry_after_seconds:  number|null// Retry-After 头 或 body error.retry_after_seconds(前端新增解析)
rate_limit_reason:    string|null// error.details.tier(global|auth_strict|media_strict)/reason
upstream_provider:    string|null// error.details.upstream_provider
upstream_status:      number|null// error.details.upstream_status(透传 429 背后的真上游码)
window_resets_at:     string|null// RFC3339,配额窗口 429/402
tenant_id:            number     // 当前 HermesScope.tenantId
as_user_id:           number     // 当前 HermesScope.asUserId
page_route:           string     // window.location.pathname
occurred_at:          string     // 客户端 ISO 时间戳
client_version:       string|null
raw_error_body:       object|null// 完整错误 JSON(截断到远低于 4MB 体上限),让 Hermes 读未提升的字段
user_question:        string|null// 管理员在抽屉里的自由追问,附在信封后
```

---

## 7. 已知边界与缺口(诚实)

### 7.1 `request_diagnose` 是租户窗口范围、内存按 request_id 过滤
`tools_observability.go:54-92`:它 `ListUsage/ListClaims` 拉整租户近窗(`PageLimit obsReadLimit`)再在内存里按 request_id 过滤。**若失败请求早于近窗、或租户高量,会查不到那条 request_id**。故"自动用 request_id 跑 request_diagnose"(Phase 3)定位为 **best-effort**;查不到时 skill 须如实说"近窗内未找到该 request_id"。且管理员 429 携带的上游 X-Request-ID 只在与 usage 记录所存一致时才对得上,跨层关联不保证。

### 7.2 内联 runbook 的 token 成本(Phase 1)
Phase 1 的 runbook 作为 user-message content **每次都发**,既耗 token 又可能被用户追问视觉盖过。修复路由到 §8 缺口②(服务端 preamble)。Phase 1 期间把 runbook **激进精简**(短版),与服务端全版分离。

### 7.3 429 取证边界(已写进 §5.3,此处再强调)
①入站限流 与 ④改动护栏 **后端不落任何库**(`rate_limit.go`/`mutateguard.go` 已核实),**无只读工具可证**。skill 对这两类**只能凭错误体判断**。**Owner 已决定:暂不补后端落库,先如实标注。** 若日后要全覆盖,补一条后端:把这两类拒绝事件落到可观测/审计库,只读工具即可查证(走分支,不进主线)。

---

## 8. 后端缺口(Phase 2 打磨,均非 Phase-1 阻塞)

| # | 缺口 | 为何需要 | 提议改动 | 量级 |
|---|---|---|---|---|
| ① | `/v1/hermes/chat` 无结构化 `context` 字段 | 今天只能把信封塞进 `messages[].content`,膨胀且易被用户消息盖过 | 加可选、允许列表的顶层 `context`(有界 JSON,≤64KB),在 `validateChatPayload` 放行,经 `setJSONField` 转发给 runner(同 `internal_token` 注入缝) | M |
| ② | 诊断 skill 应为**服务端** preamble 而非客户端文本 | 客户端文本可被改、每次耗 token;skill 是产品策略(错误分类+只读约束+输出格式),应服务端版本化、防篡改 | 把 preamble 作为后端常量(挨着 `hermesops` 工具注册);admin 绑定且 `context.kind==ask_hermes_*` 时,bridge 以 system/developer 消息前置注入(同 `tool_catalog` 注入缝 `bridge_request.go:122-130`);带 `preamble_version` | M |
| ③ | 无一次性(ephemeral)会话模式 | 每次 chat 略 `conversation_id` 即 `createConversation` 永久持久(仅软删,`bridge_request.go:88`);抽屉对瞬时 429 触发会刷爆 `/admin/hermes` 历史 | 加可选 `ephemeral: true`:跑全流(绑定/工具回路/SSE)但跳过持久化 insert / `persistDone`,或带 TTL reaper;默认行为不变 | M |
| ④ | 新会话无 title/metadata | Ask-Hermes 线程全无标题,管理员翻历史认不出哪条解释哪个 429 | 新会话可选 `title`(及 `source: ask_hermes`),自动起名如 "429 rate_limited on /v1/chat/completions @ 14:03";`HermesConversation` 已有 title 字段(`hermes.ts:78`) | S |
| ⑤ | 无 request_id 透传作 runner 默认 arg | 关联 Hermes 诊断到那次确切请求,可自动跑 `request_diagnose` | Phase-1 已放在信封里(无需新契约);Phase-2 可让 runner 以 `envelope.request_id` 为 `request_diagnose` 默认 arg。纯加性 | S |
| ⑥ | (Owner 暂缓)入站限流 + 改动护栏事件落库 | 让 §7.3 的 ① ④ 也能工具取证,使 Hermes 对全部 4 种 429 都可坐实 | 把这两类拒绝事件落到可观测/审计库;新增/复用只读工具读取 | M(已决定暂不做) |

> 引用纠错:架构图里曾写 `/internal/v1/openai/tool-execute`(那是 `DefaultInternalBaseURL`,`internal_token.go:17`);实际挂载路由是 `/internal/hermes/tool-execute`(`routes.go:380`)。不影响前端设计,仅订正引用。

---

## 9. 前端构建计划(待 Owner 拍板后执行,本文档不含代码)

复用优先:`/admin/hermes` 已实现 SSE 聊天、admin 鉴权、气泡 UI——**抽取封装,不重写**。

**新增文件(规划)**
- `components/ask-hermes/AskHermesProvider.tsx` —— 在 `AppLayout.tsx:20-37` 高处挂一次的 React context;`useAskHermes()` 暴露 `open(opts)/close()`,持有开合态、当前信封、当前 scope(HermesScope)、页面 route、追问 conversation_id。
- `components/ask-hermes/AskHermesDrawer.tsx` —— 右侧滑出面板(`max-h` 滚动、点遮罩/Esc 关、焦点陷阱)。kit 无 Dialog 原语;用 Tailwind + `tailwindcss-animate`(已是 dep)手搓,或引入 `@radix-ui/react-dialog`(轻、Radix Slot 已在用,a11y 更好——小依赖决策)。内含:信封摘要卡(status Badge/code/endpoint/Retry-After,复用 Badge+Card)、流式转录(复用 ChatBubble)、追问 textarea、错误 Banner。
- `components/ask-hermes/AskHermesTrigger.tsx` —— 两个薄展示触发器:`<HeaderAskHermesButton/>`、`<InlineAskHermesButton error={ApiError}/>`,都调 `useAskHermes().open()`。
- `lib/ask-hermes/buildEnvelope.ts` —— 纯函数 `buildEnvelope(err, ctx)`(读 ApiError + 新增 Retry-After/details 解析)、`composePreamblePayload(envelope, q)`(Phase 1 返回内联 skill+信封+追问的 messages[];Phase 2 后端 `context` 落地后切成只发裸信封——单一切换点)。
- `lib/ask-hermes/runbook.ts` —— Phase-1 的 skill 文本家;服务端 preamble(缺口②)落地后弃用/精简,版本化以防 FE/BE 漂移。

**直接复用(import,不抄)**:`lib/api/hermes.ts`(`startHermesChat`/`HermesScope`/`parse*`)、`lib/api/client.ts` 的 admin token 管线(`huakai_admin_token` Bearer 已内建)、`lib/api/errors.ts friendlyMessage`(人面文案;信封发原始 message)、kit 的 Button/Card/Badge、lucide 图标。

**markdown**:bundle 暂无 markdown 渲染。Phase 1 用 `whitespace-pre-wrap`(同现有页 `hermes/page.tsx:399`),四段式纯文本可读;需要时再加 `react-markdown`(独立小决策,非阻塞)。

---

## 10. 分阶段路线

- **Phase 0 — 闸门+开关(不出 UI)**:前端 feature flag `askHermes.enabled`;确认后端 Hermes 已挂(默认 OFF,需 3 runner 环境变量;默认 admin-only)。Hermes 禁用时 Ask Hermes 不渲染——复用现有 `hermes_disabled` 优雅降级。
- **Phase 1 — 只读解释,零后端改动**(+§6.1 小前端改动):抽屉 + 每错误内联按钮 + Header 全局按钮;skill 内联进 `messages[0].content`;工具回路因 admin 绑定自动武装,改动类结构性排除。改动动作仅文字建议、绝不执行。**今天契约即可上**。
- **Phase 2 — 后端打磨**(缺口 ①②③④):落 `context` + 服务端 `system_preamble` + `ephemeral` 一次性 + 会话自动标题;前端切 `composePreamblePayload` 发裸信封(更小、防篡改),线程 ephemeral 化不刷历史。
- **Phase 3 — 更富上下文**:据 request_id 自动跑 `request_diagnose`(best-effort,§7.1);页面上下文感知(account/channel/credential id);可选 react-markdown;追问动作 chips。
- **Phase 4 — 触达(慎重,可选)**:是否给终端用户一个**仅解释**的弱版(不武装工具,`bridge_request.go:111` 终端用户无 tool_catalog)——纯 LLM 凭信封解释,可能误导且更贵。v1 倾向不做,需 Owner 明确。

---

## 11. 最小可行路径 + Owner 决策项

**今天就能上**(零后端改动 + §6.1 一处小前端修):挂 Provider + Drawer 复用 `startHermesChat`/`parseSSEStream`;admin token 鉴权同现有页;组装 1 条 messages[0]=runbook+信封+追问(契约合法:bridge 宽松解码只校验 messages);工具回路自动武装、改动结构性排除;runner 未挂时优雅降级。**唯一被低估的必须前端前置**:§6.1 的 `ApiError` 字段保留。

**悬而未决(Owner 拍板)**:
1. **Runner 成本/依赖**:每次点击都打外部 LLM runner(默认 OFF,需 3 环境变量 + 真部署)。需确认 runner 为 admin 诊断流量备好,接受每调 token 成本(Phase 1 内联 runbook 加 token 直到服务端 preamble 落地)。是否给 Ask Hermes 自身加每-operator 限速防失控开销?
2. **服务端 preamble vs 客户端 runbook**:先纯客户端上 Phase 1、后迁移?(推荐:先上 Phase 1,后端字段作紧接的下一刀)
3. **ephemeral vs 持久线程**:首版接受默认持久(会刷历史),还是先等缺口③?
4. **Phase 4 终端用户版**:是否做(倾向否)。
5. **模型选择**:runner 背后用哪个模型(延迟 vs 工具推理质量)——runner 环境决策,影响四段式答案质量。
6. **全局按钮的 scope 来源**:无 scope 的页默认用什么?(tenant_operator 默认自身;platform_admin 可能需抽屉内快速 scope 选择器)
7. **抽屉依赖**:引 `@radix-ui/react-dialog`(a11y 更好,Radix Slot 已是 dep)还是手搓?

---

## 12. Clean-room 与引用

- 全文为行为/IA/范式 paraphrase + `file:line`(HUAKAI 自有源)/ `repo@sha:path`(三家参考)引用,未抄三家任何源码标识符或代码块。
- 三家无"AI 解释错误"等价能力(documented no-equivalent);范式取自浏览器侧 Ask-AI 通例。
- 关键真源:`routes.go:320-380`(Hermes 挂载+只读 tool-execute 路由)、`bridge_request.go:104-183`(请求构建/校验/工具注入)、`internal_tool_handler.go:118-194`(只读护栏)、`chat_handler.go:15-85`、`hermes_admin_gate_test.go:108-148`(admin-only fail-closed)、`rate_limit.go:341-358` 与 `mutateguard.go`(①④ 不落库,§7.3 取证边界)、前端 `lib/api/{hermes,client,errors,userClient}.ts`。
- 相关记忆:[[huakai-frontend-wiring-test-setup]]、[[sub2api-frontend-reuse-verdict]]。覆盖矩阵见 `WIRING-COVERAGE-MATRIX.md`,布局总纲见 `FUSION-LAYOUT-PLAN-v3.md`。

> **状态**:设计已定稿待 Owner 拍板落地节奏。Owner 决定先深化文档、暂不写代码;§7.3 的 ①④ 取证缺口按 Owner 决定**先如实标注、暂不补后端落库**。
