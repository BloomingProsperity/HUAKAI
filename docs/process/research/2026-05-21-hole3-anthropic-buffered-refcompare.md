# 2026-05-21 洞3 Anthropic Messages buffered 响应参照对比

| 项目 | 内容 |
|---|---|
| 任务 | HUAKAI 方向 1 Phase 2 洞3：非流式 / buffered 的 Anthropic Messages 响应翻译，开工前 specifier lane 调研 |
| 范围 | sub2api 与 CLIProxyAPI 的对应模块行为观察、细节对比、HUAKAI 吸收与升级建议 |
| 不做 | 不写 HUAKAI 代码；不做 git 操作；不复制参考项目代码、内部标识符、注释或实现结构 |
| Clean-room lane | specifier |
| Observed regions | 54 |
| Inferences | 12 |
| Open questions | 5 |

## 1. 两个 repo 最新 HEAD 信息

本次按 Owner 指定优先走 codeload tarball。沙箱内网络请求失败，无法确认远端最新 HEAD，也没有获得带 commit 的 tarball 顶层目录名。

| repo | codeload 结果 | 本次可用 fallback 证据 | 本报告版本口径 |
|---|---|---|---|
| sub2api | `main` 下载失败，`master` 兜底下载也失败，curl exit 7 | 本地只读 fallback 位于 `/home/codex/refs/sub2api/`；`.git/FETCH_HEAD` 记录 `91da815993732e6536be8c702168822e482cd850`；`.git/HEAD` 指向 `main`；`.git/shallow` 包含该提交 | `sub2api@91da815993732e6536be8c702168822e482cd850`，不是远端最新确认 |
| CLIProxyAPI | `main` 下载失败，curl exit 7 | 本地只读 fallback 位于 `/home/codex/refs/CLIProxyAPI/`；无 `.git` 目录；项目根的 `.huakai-head-sha` 记录 `21fad9dbb447a2ab70d51d0ac3e3d032525a6054` | `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054`，不是远端最新确认 |

结论：以下对比是“本地 fallback 版本的源文件行为观察”，不能替代远端最新 HEAD 复核。等网络可用后，应重新拉 tarball 并检查同一模块是否变动。

## 2. sub2api 该模块细节清单

### 2.1 对应路径与响应形态

sub2api 在本次读到的代码里有两类与洞3相关的路径。

第一类是“上游已经是 Anthropic Messages”的直接转发路径：服务层把请求发到上游消息接口；非流式响应成功时完整读取响应体，抽取 token 用量，然后把上游 body 和响应内容类型直接写回调用方；错误响应则读取有限长度 body，并把状态码和错误内容透传给调用方，其中 429 还会进入上游错误处理逻辑。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4199`、`:4235`、`:4242`、`:4274`、`:4287`、`:4314`。

第二类是“上游是 Gemini / Antigravity 内部形态，然后生成 Anthropic Messages 响应”的翻译路径：非流式客户端请求在该路径下可能由上游 SSE 聚合而来，服务层收集事件中的候选内容、思考内容、工具调用和内嵌媒体，构造最终上游响应对象，再交给响应转换器生成 Anthropic Messages JSON。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:3728`、`:3773`、`:3788`、`:3805`、`:3826`、`:3858`，以及 `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:13`、`:31`、`:42`。

另有 Anthropic Messages 响应到 OpenAI Responses 响应的兼容转换路径，适合洞3借鉴“非 Anthropic 输出协议”时的损耗点。该路径接收 Anthropic message 形态，输出另一种响应协议。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_to_responses_response.go:18`、`:32`、`:55`、`:78`、`:96`。

### 2.2 content block 覆盖

在 Gemini / Antigravity 到 Anthropic 的响应生成路径中，普通文本会转成 Anthropic 文本块；工具调用会转成工具使用块，并在上游缺少工具调用 ID 时生成兜底 ID；思考文本会转成思考块，并保留可用签名；带签名但无文本的片段会被当作签名延后处理；带文本又带签名的片段会先输出文本，再补一个只承载签名的思考块。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:103`、`:113`、`:123`、`:132`、`:142`、`:153`、`:166`。

该路径对内嵌图片不是生成 Anthropic 原生图片块，而是转成 Markdown 风格的数据 URI 文本块。这能保住可见内容，但会改变协议语义：调用方看到的是文本块而不是图片块。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:180`、`:196`。

该路径还把上游 grounding / web search 信息追加为文本块，包含查询和来源标题 / URL / 摘要。这个属于容易被漏掉的小功能：它不是 Anthropic 核心字段，但能让客户端拿到搜索上下文。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:217`、`:310`、`:329`。

在 Anthropic 到 Responses 的兼容路径中，文本块会变成输出文本，思考块会变成 reasoning 摘要，工具使用块会变成函数调用；未识别块不会被显式转出。若转换后完全没有输出项，会兜底生成一个空文本输出。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_to_responses_response.go:34`、`:38`、`:47`、`:59`。

结构定义层面，本次读到的响应内容结构覆盖文本、思考、工具使用；没有观察到对 `redacted_thinking` 响应块的专门结构字段。另一个兼容结构包含图片、工具结果等字段，但响应转换逻辑没有看到图片响应块的非流式输出分支。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/claude_types.go:93`、`:100`、`:117`，以及 `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/types.go:52`、`:61`、`:66`。

### 2.3 usage 字段

Gemini / Antigravity 翻译路径会读取 prompt、候选、缓存命中、思考 token、总 token 和分模态 token；Anthropic 输出里的 input tokens 会用 prompt token 减去缓存命中 token，output tokens 会把候选 token 与思考 token 相加，cache read 会来自缓存命中 token；另有图片输出 token 的单独提取。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:257`、`:263`、`:269`、`:277`、`:284`、`:296`，以及 `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/gemini_types.go:158`、`:169`。

直接 Anthropic 上游路径会从响应体中抽取 input、output、cache read、cache creation，以及更细的短期 / 长期 cache creation 子项；如果 JSON 无法解析或缺少 usage，会返回零值用量而不是阻断响应。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4500`、`:4507`、`:4516`、`:4521`、`:4526`。

直接转发路径的结算结果只把 input、output、cache read、cache creation 汇总字段带回调用链；上一步读到的短期 / 长期 cache creation 细分没有在该结果对象中观察到继续传播。该点是“观察到的潜在损耗”，不是确认缺陷。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4314`、`:4319`。

Anthropic 到 Responses 的兼容路径会保留 input 与 output，并计算 total；cache read 被转成 cached token 细项；cache creation 没有看到等价输出字段。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_to_responses_response.go:84`、`:90`。

### 2.4 stop reason 与 model

Gemini / Antigravity 翻译路径的停止原因以工具调用优先；如果存在工具调用，输出 stop reason 会是工具使用；上游最大 token 结束会映射为 max tokens；其他常规结束默认回到 end turn。 malformed tool call 会记录日志，但本次没有看到它被映射为特殊响应状态。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:237`、`:242`、`:246`、`:250`。

该路径的响应 model 使用的是请求侧原始模型名，而不是上游响应里的实际模型版本。这对“客户端可见模型别名”友好，但如果 HUAKAI 要做审计，应另存上游实际模型。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:232`、`:257`。

Anthropic 到 Responses 的兼容路径把 max tokens 转成 incomplete 状态和对应原因；end turn、tool use、stop sequence 和默认情况都转为 completed。该路径没有观察到把 stop sequence 字符串作为 Responses 输出字段继续保留。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_to_responses_response.go:96`、`:100`、`:103`。

### 2.5 错误、边角与 body 限制

Gemini / Antigravity 翻译路径会先尝试 wrapper 形态，再在 wrapper 缺少候选时尝试直接响应形态；两种都无法解析时返回解析失败错误。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:13`、`:24`、`:31`、`:39`。

上游 SSE 聚合路径对“没有有效响应”会返回可重试的 502，并允许同账号重试；上游流超时也会走错误路径。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:3742`、`:3816`、`:3826`。

项目内存在通用的上游响应体大小限制工具：超过限制会关闭连接，并按目标协议输出错误形态；默认配置代码里读到的是 128MB，README_CN 中读到的说明是 8MB。该差异需要后续复核；同时，直接 Anthropic 成功响应路径本次观察到的是完整读取 body，没有看到调用该限制工具。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/upstream_response_limit.go:13`、`:31`、`:47`，`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/config/config.go:55`、`:660`、`:1780`、`:2333`，`sub2api@91da815993732e6536be8c702168822e482cd850:README_CN.md:509`，以及直接读取位置 `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4287`。

直接 Anthropic 路径没有观察到对顶层 `type` 是否为 `message` 的强校验；它主要抽取 usage 后透传 body。这个结论只限本次读到的路径。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4287`、`:4296`、`:4314`。

## 3. CLIProxyAPI 该模块细节清单

### 3.1 对应路径与响应形态

CLIProxyAPI 的 Claude messages handler 会从请求体判断是否流式；非流式请求进入 buffered handler。该 handler 设置 JSON 响应类型，启动非流式 keepalive，经过认证 / 执行器拿到完整响应，再写回调用方。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/claude/code_handlers.go:67`、`:75`、`:163`、`:171`、`:178`、`:200`。

执行层在“源协议与目标协议相同”的情况下会优先保留上游响应形态；如果没有注册对应的非流式转换器，注册表会直接返回原始 JSON。这意味着 Claude 到 Claude 直连路径可以是 pass-through，同时其他源协议会进入对应转换器。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/translator/registry.go:94`、`:100`，以及 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:130`、`:147`、`:281`。

项目注册了多个“转换为 Claude 非流式响应”的来源：Gemini、OpenAI、Antigravity、Codex 等。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/init.go:4`、`:10`、`:17`、`:24`、`:31`。

### 3.2 content block 覆盖

Gemini 到 Claude 的非流式转换会处理文本、思考文本和工具调用。工具调用参数如果不是对象会落到空对象；如果上游结束原因缺失而存在工具调用，会把 stop reason 视为工具使用。没有在该路径观察到图片 / 内嵌媒体输出处理。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/gemini/claude/gemini_claude_response.go:261`、`:289`、`:304`、`:318`、`:334`、`:355`。

OpenAI 到 Claude 的非流式转换覆盖普通文本、数组形式文本、reasoning 内容和工具调用；工具调用参数会尝试修复 JSON 字符串，不是对象时退回空对象；usage 会把 cached token 从 prompt token 中扣出并放到 cache read。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/openai/claude/openai_claude_response.go:613`、`:656`、`:681`、`:708`、`:734`、`:775`。

Antigravity 到 Claude 的非流式转换会从 parts 中生成文本、思考和工具使用块；思考签名支持两种来源字段并进入 Anthropic 形态；工具名会尝试恢复为请求侧原始工具名；工具调用参数必须是对象，否则落到空对象。没有在该路径观察到图片 / 内嵌媒体输出处理。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go:390`、`:429`、`:443`、`:459`、`:485`、`:506`。

Codex / Responses 到 Claude 的非流式转换会处理 reasoning 项、message 输出文本和函数调用。reasoning 的摘要内容会并入思考文本，存在加密内容时会作为签名保留；函数调用名会按请求侧工具名映射恢复，调用 ID 会被转换到 Anthropic 可接受的形态，参数不是 JSON 对象时落到空对象。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:248`、`:281`、`:298`、`:327`、`:349`、`:363`。

AMP 模块另有一层非流式响应改写：它会在 buffered JSON 响应中把模型字段改回客户端请求模型，把常见工具名规范化，并为工具使用 / 思考块补空签名；在配置要求下，含工具调用的非流式响应会移除思考块。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:124`、`:146`、`:172`、`:204`。

### 3.3 usage 字段

Gemini 路径输出 input 与 output；output 会把候选 token 与思考 token 相加。该路径本次没有观察到 cache read / cache creation 的提取。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/gemini/claude/gemini_claude_response.go:276`、`:282`。

OpenAI 路径会读 input、output、total 及 prompt details 里的 cached token；最终 Anthropic usage 中 input 会扣除 cached，cache read 会记录 cached。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/openai/claude/openai_claude_response.go:775`、`:785`。

Antigravity 路径会读 prompt、candidate、thought、total、cached；output 会候选加思考，如果 output 缺失但 total 与 prompt 可用，会用 total 减 prompt 兜底。该路径设置 cache read，但 input token 没有像 OpenAI 路径那样扣除 cached。这个差异需要 HUAKAI 设计时主动定口径。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go:394`、`:402`、`:411`、`:416`。

Codex / Responses 路径会读 input/output 与 cached input 细项，并将 input 扣除 cached 后输出，同时设置 cache read。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:265`、`:271`。

执行器层在成功响应后还会解析 Claude-shaped 用量并做记录；如果是“流式翻译模式”则跳过该非流式用量解析。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:253`、`:265`。

### 3.4 stop reason 与 model

Gemini / Antigravity 路径都采用“存在工具调用则工具使用优先”的策略；max tokens 映射为 max tokens，正常停止或未知停止默认 end turn。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/gemini/claude/gemini_claude_response.go:337`、`:342`、`:352`，以及 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go:509`、`:514`、`:526`。

OpenAI 路径把 stop 映射为 end turn，把 length 映射为 max tokens，把工具调用相关结束映射为工具使用；content filter 没有映射到特殊 Anthropic reason，而是回到 end turn。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/openai/claude/openai_claude_response.go:490`、`:496`、`:501`。

Codex / Responses 路径的停止原因更细：能保留 stop sequence 字符串；能把 pause、refusal、context window exceeded 等直接或近似映射到 Anthropic 输出。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:375`、`:384`、`:392`、`:416`。

模型字段方面，通用执行链会记录请求模型和推理 effort 元数据；AMP 层会把响应中的多个模型字段改回客户端请求模型。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/handlers.go:557`、`:568`、`:582`，以及 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:124`、`:136`。

### 3.5 错误、边角与 body 处理

CLIProxyAPI 的非流式路径有较强的压缩兜底：执行器会根据 Content-Encoding 解 gzip、deflate、brotli、zstd；如果没有 header，也会尝试识别 gzip 与 zstd 魔数。Claude handler 写回前还会对 gzip 魔数做一次兜底解压。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:817`、`:838`、`:853`、`:868`、`:883`、`:898`，以及 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/claude/code_handlers.go:185`。

错误响应会被包装成 Claude error envelope，并按 HTTP 状态映射为 authentication、billing、permission、not found、request too large、rate limit、timeout、overloaded、api error 等类别；如果上游错误体中有嵌套错误对象，会抽取上游类型和消息。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/claude/code_handlers.go:340`、`:372`、`:389`、`:404`、`:432`。

AMP 非流式改写器最多缓存 2MB；如果响应像 SSE 或超过缓冲上限，会切到流式写出。非流式 flush 时会重写 JSON 并更新 Content-Length。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:36`、`:62`、`:84`、`:106`。

AMP 代理层专门测试了“gzip JSON 但没有 Content-Encoding”的场景，保证非 SSE 响应会被解压，而普通 JSON 保持不变；它只把明确的 event-stream 视为真正流式。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/proxy.go:108`、`:134`、`:205`，以及 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/proxy_test.go:520`、`:556`、`:590`。

请求侧 sanitizer 会移除签名非法的 assistant 思考块，并去掉代理补上的工具签名；注释中明确它保留 raw JSON 以避免大整数精度变化。响应侧测试覆盖了思考块保留、签名注入、工具名规范化等行为。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:343`、`:359`、`:379`，以及 `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter_test.go:104`、`:130`、`:178`。

fallback 包装层会先读请求体并做 sanitizer，再根据模型路由决定是否包一层响应改写；模型映射路径会开启思考抑制，本地 provider 路径会按 provider 是否为 Claude 决定是否抑制思考。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/fallback_handlers.go:118`、`:126`、`:255`、`:270`。

本次没有观察到 Claude 执行器成功响应路径存在整体 body 大小上限；它会完整读取已解码 body。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:244`。

## 4. 对比表

| 细节项 | sub2api | CLIProxyAPI | HUAKAI 应吸收 / 升级 |
|---|---|---|---|
| Anthropic 到 Anthropic pass-through | 直接上游路径抽 usage 后透传成功 body；错误 body 有限读取并透传状态。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4287`、`:4314` | 同协议无转换器时返回原始 JSON；执行层仍会做解压、记录、工具名恢复等前后处理。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/translator/registry.go:94`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:253` | HUAKAI 至少要支持 pass-through 与 translated 两种模式，并在 pass-through 模式也做 usage、body limit、压缩、审计记录。 |
| 文本块 | Gemini / Antigravity 路径正常输出文本块；兼容路径可把文本块转为另一协议输出文本。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:132`、`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_to_responses_response.go:34` | 多个来源转换器均覆盖普通文本和部分数组文本。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/openai/claude/openai_claude_response.go:656`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:327` | 必须覆盖普通文本、数组文本、空文本；空块要有明确策略，不得悄悄丢账单与 stop 信息。 |
| 工具使用块 | 生成工具使用块；缺 ID 时生成兜底 ID；工具调用存在时 stop reason 优先工具使用。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:113`、`:237` | 各转换器生成工具使用块；参数必须是对象，不合格时空对象；AMP 还做工具名规范化。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go:459`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:146` | HUAKAI 至少要验证工具参数对象形态、兜底 ID、原始工具名恢复、工具 stop 优先级，并记录参数修复事件。 |
| thinking | 支持思考文本与签名；特殊处理“签名在空文本片段上”以及“文本片段带签名”的尾签名场景。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:123`、`:142`、`:153` | 多来源支持 reasoning / thought 到 thinking；Antigravity 与 Codex 路径会保留签名或加密内容；AMP 可补空签名或抑制 thinking。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:281`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:172` | HUAKAI 要把 thinking 当成一等块处理，支持签名、空签名兼容开关、抑制策略、审计标注。 |
| redacted_thinking | 未观察到专门响应输出处理。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/claude_types.go:93` | 未观察到专门响应输出处理。多处只处理 reasoning/thinking。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:281` | 这是 HUAKAI 明确升级点：支持 `redacted_thinking` 原样保留或安全等价透传，并写测试。 |
| 图片 / image block | Antigravity 路径把内嵌图片转成 Markdown 数据 URI 文本，不是 Anthropic 图片块；兼容结构有图片字段但响应转换未见输出分支。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:180`、`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/types.go:61` | Gemini / Antigravity 非流式转换未观察到图片输出处理。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/gemini/claude/gemini_claude_response.go:289`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go:429` | HUAKAI 应升级为协议级图片块策略：能原样保留、降级为文本时显式标注、并记录损耗。 |
| usage cache read | Antigravity 路径从缓存命中 token 提取 cache read，并从 input 中扣除；直接 Claude 路径也抽 cache read。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:263`、`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4516` | OpenAI 与 Codex 路径扣除 cached 并设置 cache read；Antigravity 路径设置 cache read 但未见扣除 input。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/openai/claude/openai_claude_response.go:785`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go:411` | HUAKAI 必须定义统一口径：Anthropic output 的 input 是否排除 cached，按协议语义固定，避免来源之间双计或漏计。 |
| usage cache creation | 直接 Claude 路径抽 cache creation 与短 / 长期细分，但返回结果只见汇总传播。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4521`、`:4319` | 本次未观察到 cache creation 细分输出。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/openai/claude/openai_claude_response.go:775`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:265` | HUAKAI 升级点：保留 cache creation 汇总与 TTL 细分，账单层和响应层分开建模。 |
| output tokens with thinking | Antigravity 路径把候选 token 与思考 token 相加。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:269` | Gemini / Antigravity 路径也把思考 token 纳入 output；Antigravity 还有 total-prompt 兜底。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/gemini/claude/gemini_claude_response.go:282`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go:402` | HUAKAI 应把 visible output、thinking output、billed output 区分建模，再映射到 Anthropic usage。 |
| image output tokens | Antigravity 路径提取图片输出 token。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:296` | 未观察到同等输出。 | HUAKAI 可升级为 multimodal usage 子结构，避免图片 token 丢到普通 output 后不可审计。 |
| stop sequence | Anthropic 到 Responses 路径对 stop sequence 只表现为 completed，未见保留字符串。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_to_responses_response.go:100` | Codex 路径会保留 stop sequence 字符串。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go:416` | HUAKAI 至少应 lossless 保留 `stop_reason` 与 `stop_sequence`，跨协议无法表达时记录转换损耗。 |
| model echo | Antigravity 路径输出请求侧原模型。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:232` | 执行链记录请求模型；AMP 会把多个模型字段改回客户端请求模型。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/handlers.go:568`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:124` | HUAKAI 应采用“响应给客户端用请求别名，内部审计保留上游实际模型”的双轨策略。 |
| 压缩解码 | 直接 Claude 成功路径未观察到专门解压；错误体有限读取。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4274`、`:4287` | Header 与 magic-byte 双路径解压，覆盖 gzip/deflate/brotli/zstd；handler 还有 gzip 兜底。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:817`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/claude/code_handlers.go:185` | HUAKAI 必须吸收 CLIProxyAPI 的压缩容错，否则 buffered JSON 解析会在真实代理链路中失败。 |
| 超大 body | 项目内有通用限制工具，但直接 Claude 成功路径未见使用；配置默认与文档说明存在差异。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/upstream_response_limit.go:13`、`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:4287` | AMP 改写 buffer 超 2MB 会切流式；Claude 执行器成功路径未见整体 body 上限。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go:84`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:244` | HUAKAI 应做每路由可配置 buffered max，超过时返回 Anthropic-shaped typed error，不应无界读。 |
| bad JSON / empty body | 转换路径 bad JSON 会报解析失败；聚合路径无有效响应会 502 可重试；直接 pass-through 对 usage bad JSON 只返回零用量。证据：`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go:39`、`sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go:3826`、`:4507` | 执行器 body decode 错误会失败；部分 gjson 转换路径对缺字段可能生成默认响应；错误 envelope 分类较细。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go:244`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/claude/code_handlers.go:340` | HUAKAI 应把 empty body、bad JSON、top-level type mismatch、usage missing 分成不同错误 / 警告，避免“默认响应”掩盖协议坏包。 |
| 顶层 message 校验 | 直接 Anthropic pass-through 未观察到强校验。 | 同协议 pass-through 注册表可直接返回 raw JSON；未观察到强校验。 | HUAKAI 升级点：可配置的 Anthropic response validator；生产默认至少校验顶层类型、role、content 数组和 usage 形态。 |
| keepalive | 未观察到非流式 keepalive。 | 非流式 handler 启动 keepalive 防 idle timeout。证据：`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/claude/code_handlers.go:171`、`CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/handlers.go:166` | HUAKAI 可吸收为长耗时 buffered 请求的连接保活策略，但要确保不会污染 JSON body。 |
| 协议字段透传与损耗记录 | pass-through 保留 body；跨协议转换存在 image、cache creation、stop sequence 等损耗点。 | pass-through 保留 raw；多转换器对 stop、tool、thinking 做兼容增强，但也有 redacted/image 缺口。 | HUAKAI 应内置“转换损耗事件”观测，不只是生成目标 JSON。 |

## 5. 给洞3的吸收与升级建议

### 5.1 他们有，HUAKAI 至少要有

1. **双模式响应处理**：支持 upstream Anthropic Messages 直接 pass-through，也支持其他协议到 Anthropic Messages 的 buffered 翻译。pass-through 不能等于“不处理”：仍需 usage 抽取、压缩解码、body 限制、审计和错误分类。

2. **content block 基线覆盖**：文本、工具使用、思考必须完整覆盖；工具调用缺 ID、工具参数非对象、思考签名、空思考签名、工具 stop 优先级都要有显式处理。

3. **usage 基线覆盖**：至少保留 input、output、cache read、cache creation。对支持思考 token、图片 token、短 / 长 cache creation 的来源，要进入 HUAKAI 的内部 usage 扩展结构，不能只压平成两个数字。

4. **模型回显策略**：响应给客户端的 `model` 应按客户端请求别名回显；上游实际模型应进入内部审计字段，用于排障、账单和路由解释。

5. **停止原因 lossless 策略**：`end_turn`、`max_tokens`、`stop_sequence`、`tool_use` 至少要完整覆盖；`stop_sequence` 字符串要保留。来源协议有更细原因时，不能悄悄压成 end turn，应记录降级原因。

6. **压缩和代理容错**：吸收 CLIProxyAPI 的 header + magic-byte 解压思路，尤其是“JSON 被 gzip 但缺 Content-Encoding”的真实代理场景。

7. **非流式长请求保活**：吸收 CLIProxyAPI 的非流式 keepalive 思路，但 HUAKAI 必须证明它不会往 JSON 响应体里写入非 JSON 内容。

8. **buffer 上限**：吸收 sub2api 的“协议形态错误响应”思路，并补齐直接成功路径；吸收 CLIProxyAPI 的小 buffer 改写上限思路，但 HUAKAI 不应切到假流式掩盖非流式语义。

9. **错误 envelope 细分**：吸收 CLIProxyAPI 的 HTTP 状态到 Anthropic error type 的细分，增加 empty body、bad JSON、type mismatch、usage missing、oversize body 等 HUAKAI 自有错误码。

10. **兼容性小功能**：签名补齐、工具名恢复 / 规范化、上游 grounding 追加、图片 token 提取、cache TTL 细分、token 估算兜底，都应进入洞3 checklist，而不是以后“发现 bug 再补”。

### 5.2 他们没有或不完整，HUAKAI 的升级 delta

1. **全 content block 策略**：HUAKAI 应覆盖 `text`、`tool_use`、`thinking`、`redacted_thinking`、`image`、空块和未知块。已知块按协议输出；未知块按可配置策略选择保守透传、降级文本或 typed error，并记录损耗。

2. **Anthropic response validator**：在 buffered 响应入口先验证顶层 shape：message 类型、assistant role、content 数组、stop reason 合法、usage 类型合法。pass-through 模式也可以“验证但不改写”，并把失败作为上游协议风险事件。

3. **Usage normalizer with provenance**：内部 usage 应带来源字段与转换口径，明确 cached token 是否已从 input 扣除，避免 sub2api 与 CLIProxyAPI 中观察到的来源间差异在 HUAKAI 账单层扩散。

4. **Thinking 与签名 feature flag**：把签名补空、thinking 抑制、redacted thinking 保留分成独立 feature flag。默认保护协议正确性，兼容模式只对指定客户端开启。

5. **多模态 response 正式化**：不要把图片响应简单塞进文本；如果必须降级，响应中要有清晰标注，审计事件中要记录原始媒体类型、大小、token 和降级原因。

6. **Stop reason 扩展字典**：在 Anthropic 标准原因之外保留内部扩展原因，例如 refusal、content filter、context window exceeded、pause 等。客户端只看标准字段，运维和测试看内部原因。

7. **Body pipeline 顺序固定**：读取限制、压缩识别、解压、JSON parse、schema validate、translate、usage normalize、write response 应形成固定管线，并为每一步打观测点。这样能避免“先无界读再解析”的风险。

8. **转换损耗报告**：每次跨协议 buffered 翻译都应生成内部 loss report：丢失字段、降级字段、估算 token、修复工具参数、修正模型名、签名处理、未知 content block。它不一定暴露给普通客户端，但必须可查。

9. **测试矩阵升级**：洞3 acceptance tests 应覆盖至少：文本-only、工具-only、文本+工具、thinking+signature、redacted thinking、image、空 content、坏 JSON、空 body、缺 usage、cache read/cache creation、stop sequence、超大 body、gzip 无 header、model alias echo、type 非 message。

10. **HUAKAI 三维升级**：
   - 框架维：把 buffered Anthropic response 处理做成可插拔 translator pipeline，而不是散落在 handler。
   - 算法维：做 usage 归一化和 content block lossless policy，显式处理来源差异。
   - 生态维：兼容 Claude Code / AMP / OpenAI Responses / Gemini / Antigravity 等客户端和上游的字段差异，同时输出 HUAKAI 自有观测事件。

### 5.3 Open questions

1. sub2api 远端最新 HEAD 无法确认；本报告用的 `91da815993732e6536be8c702168822e482cd850` 是否仍代表最新行为，需要网络恢复后复核。
2. CLIProxyAPI 远端最新 HEAD 无法确认；本报告用的 `21fad9dbb447a2ab70d51d0ac3e3d032525a6054` 来自本地记录文件，需要网络恢复后复核。
3. sub2api 配置默认值与 README_CN 对 upstream response body limit 的说明不一致，需要确认哪一个是当前产品真实默认值。
4. 两个项目对 `redacted_thinking` 的响应处理都没有在本次源文件阅读中观察到，仍需用最新 HEAD 和测试覆盖再确认是否存在其他路径。
5. 两个项目对“顶层 `type` 非 `message` 的 Anthropic buffered 响应”都未在本次读取路径观察到强校验；是否有更外层网关校验，需要另做横向搜索。

## 6. 读过的源文件清单

Source files read:

- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/response_transformer.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/claude_types.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/antigravity/gemini_types.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/antigravity_gateway_service.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/service/upstream_response_limit.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/config/config.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_to_responses_response.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/types.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/pkg/apicompat/anthropic_responses_test.go`
- `sub2api@91da815993732e6536be8c702168822e482cd850:README_CN.md`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/claude/code_handlers.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/handlers.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/translator/registry.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/init.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/runtime/executor/claude_executor.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/gemini/claude/gemini_claude_response.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/openai/claude/openai_claude_response.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/antigravity/claude/antigravity_claude_response.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/translator/codex/claude/codex_claude_response.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/response_rewriter_test.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/proxy.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/proxy_test.go`
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/modules/amp/fallback_handlers.go`

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-21T14:26:32Z

中文摘要：本报告只基于实际读过的本地 fallback 源文件做行为摘要；真实观察包括两项目的 pass-through / translated buffered 响应路径、content block 覆盖、usage 抽取、stop/model 处理、压缩与边角兜底；合理推断集中在 HUAKAI 应如何统一 usage 口径、损耗记录和 validator 管线；仍有 5 个 open questions，最高优先级是网络恢复后重新拉取两个远端 HEAD，复核 redacted thinking、图片响应和 body limit 是否已有新实现。
