# 保号 Slice 1：Gemini 真实 UA 与全 Provider 出站身份头普查报告

日期：2026-07-15  
分支：`feat/provider-real-ua`  
起始 HEAD：`0f7d6b69`  
执行者：Codex  
状态：Gemini 修复与普查已完成；禁止 commit / push；`make quality-gate` 被基底既存 deadcode 差异阻塞，详见“门禁结果”。

## 1. 结论

Gemini Code Assist 的两项默认身份头都不是抓包真值：

- `User-Agent` 从 `HUAKAI-GeminiCLI/1.0` 改为 `GeminiCLI/0.41.2/gemini-3.1-pro-preview (linux; x64; terminal) google-api-nodejs-client/9.15.1`。
- `X-Goog-Api-Client` 从 `google-genai-sdk/1.0 gl-go/1.0` 改为 `gl-node/22.22.2`。

两个真值都来自 `tools/fingerprint-collector/templates/gemini-advanced.json:263-284`，其 `_field_sources.http_layer` 明确记录为 Owner 自有账号的 mitmproxy HTTP 解密抓包，目标也是同一个 `cloudcode-pa.googleapis.com` 模型端点。生产改动位于 `backend/internal/provider/gemini/code_assist.go:39-46`，实际注入点仍是同文件 `:194-204`。

Gemini Advanced 网页会话路径没有套用这份值。`backend/internal/provider/gemini/gemini_advanced_session.go` 的目标是 `gemini.google.com` 浏览器会话，而名为 `gemini-advanced.json` 的模板实际抓的是 Gemini CLI 的 `cloudcode-pa` 请求；两者 endpoint、鉴权和客户端形态都不同。现有网页 UA 仍是自编 Chrome 124 值，归入待采集清单，不以错形态抓包冒充修复。

## 2. 判定口径

- **真（真抓）**：HUAKAI 自有 HTTP 抓包，且 endpoint、鉴权与客户端形态和代码路径一致。
- **真（诚实直通）**：标准 API/server-to-server 路径不宣称自己是某个第一方桌面/CLI 客户端，也没有自编身份头；这不是“已抓到第一方指纹”，而是没有假冒声明。
- **假编**：代码本身写明“模拟 / 占位 / 推测 / 伪造”，或同形态真抓已经证明当前固定值错误。
- **缺抓**：存在看似合理的固定值、源码分析值或不同形态抓包，但没有同形态 HTTP 抓包；Truth-First 下不能标为真。

审计不是 grep 计数：先由 `backend/internal/provider/registrydefault/default.go` 确认实际注册/环境门控关系，再逐个阅读请求构造、委托关系和刷新/引导路径，最后逐字段对照四份模板的 `_field_sources` 与 `http_layer`。模板只有 Anthropic、Codex、Gemini、Kiro 四份；其中只有 Gemini 和 Kiro 的 HTTP 层是自有解密抓包，Anthropic HTTP 层是 manual placeholder，Codex HTTP 层是 source-analysis。

## 3. 单一真相源处理

仓库确有 `internal/transport/mimicry.TemplateRegistry`，gateway 启动时也会加载模板目录；但该 registry 只注入 transport factory，没有传给 `provider.CodeAssistAdapter`。当前 `HTTPLayer` Go 结构还能读取 `user_agent`，却没有 `x_goog_api_client` 字段，且 provider registry 构造也没有模板依赖。强行为两个静态头增加运行时文件读取，会让二进制依赖工作目录和外部 JSON，扩大启动失败面。

因此本片采用 Owner brief 允许的回退方式：在现有常量处落真抓固定值，中文注释写明模板路径与 Owner 自有抓包出处，并用不引用生产常量的精确测试防止两份值一起漂移。没有新增依赖、没有新加载路径、没有翻任何默认 flag。

## 4. Gemini 改动与核对

| 路径 | 身份头 | 修改前 | 修改后 / 判定 | 处置 |
| --- | --- | --- | --- | --- |
| Gemini Code Assist 模型请求 | `User-Agent` | `HUAKAI-GeminiCLI/1.0` | 抓包精确串；真（真抓） | 已修改 |
| Gemini Code Assist 模型请求 | `X-Goog-Api-Client` | `google-genai-sdk/1.0 gl-go/1.0` | `gl-node/22.22.2`；真（真抓） | 已修改 |
| Gemini Code Assist 模型请求 | `Accept` | 流式 `text/event-stream`，非流 `application/json` | 抓包为 `*/*`；这不是本 Slice 的客户端身份头，但属于 wire 分歧 | 未改，列入新分歧 |
| Gemini Code Assist 模型请求 | `Accept-Encoding` / `Connection` / header 顺序 | adapter 未显式控制 | 抓包为 `gzip,deflate` / `close` / 固定顺序 | 未改；需要 transport 级专项，不能靠 provider 常量伪造 |
| Gemini Advanced 网页会话 | `User-Agent` | 自编 Chrome 124 Windows UA | 假编；现有模板是 CLI 而非网页 | 不错套 CLI 真值，待网页抓包 |
| Gemini Advanced 网页会话 | `X-Goog-Authuser` / `X-Origin` | 默认 `0` / `https://gemini.google.com` | 缺抓；语义合理但模板没有网页证据 | 保持，待网页抓包 |

判别测试从“两个头非空”升级为精确值断言：`backend/internal/provider/gemini/code_assist_test.go:173-190`。测试期望串独立写出，没有引用 `codeAssistUserAgent` 或 `codeAssistAPIClient`，因此修改生产常量不能同步把测试答案改掉。

## 5. 全 Provider 出站身份头对照表

| 厂商 / 路径 | 身份头 | 现值 | 判定（真 / 假编 / 缺抓） | 处置 |
| --- | --- | --- | --- | --- |
| Anthropic API key + OAuth 模型请求 | `User-Agent`、`X-Stainless-*`、`X-App`、会话/请求 ID | `claude-cli/2.1.63 (external, cli)`；package `0.74.0`；runtime `v24.3.0`；OS/Arch 按账号派生；其余固定/随机 | 缺抓。Anthropic 模板 HTTP 层是 manual placeholder，且记录的是 token endpoint，不足以证明模型请求这组值 | 不改；先抓模型请求 HTTP 层，再整体校准 UA、Stainless、会话头及其组合 |
| Anthropic API key + OAuth 模型请求 | `Anthropic-Beta` | 默认不发；有凭据配置/入站 token 时合并，OAuth 路径套 allowlist | 缺抓。值不是无条件自编，但“真实 CLI 会发哪些组合”没有模板证据 | 保持现有协议语义；待同版本 CLI 抓包后核对默认组合与 allowlist |
| OpenAI 官方 API key | 无第一方客户端身份头；仅 Authorization、Content-Type、Accept 与可选 org/project/beta | adapter 不显式设 UA | 真（诚实直通） | 保持；无需把开发者 API 请求伪装成某个桌面客户端 |
| OpenAI Codex session | `User-Agent` | `codex/1.0.0 (linux; go)` | 假编；与模板的动态 `codex_cli_rs/...` 格式不符，且模板 HTTP 层只是源码分析、没有可落的完整实抓串 | 不猜 `<OS_VERSION>/<TERMINAL_TOKEN>`；列入待采集 |
| OpenAI Codex session | `originator` | 默认 `codex_cli_rs` | 缺抓；与模板源码分析一致，但无 HTTP wire 抓包 | 保持，待抓包确认 |
| OpenAI Codex session | `OAI-Device-Id`、`OAI-Language`、`version`、account/session 类头 | 部分固定、部分凭据 Extra、部分缺失 | 缺抓；现有代码头集合与模板列出的 session/thread/window/client-request 头并不完全一致 | 不在本片猜补；待模型请求完整抓包 |
| Gemini 标准 API key | 无客户端 UA；`X-Goog-User-Project` 仅按凭据动态发 | 无自编固定身份 | 真（诚实直通） | 保持 |
| Gemini Code Assist | `User-Agent`、`X-Goog-Api-Client` | 已换为同一请求抓包真值 | 真（真抓） | 已修并加精确测试 |
| Gemini Advanced 网页 session（默认 off） | 浏览器 UA、`X-Goog-Authuser`、`X-Origin` | Chrome 124 固定 UA、`0`、Gemini origin | 假编 / 缺抓 | 不套 CLI 模板；待网页实抓 |
| Antigravity session + project resolver（默认 off） | `User-Agent`、`X-Goog-Api-Client` | `antigravity/hub/2.2.1 darwin/arm64`；`google-genai-sdk/1.0 gl-go/1.0` | 缺抓；模板目录无 Antigravity 文件，代码中的“已确认”无法由本次指定证据集复核 | 保持，待模型请求与 load/onboard 两类抓包 |
| Kiro session（默认 off） | UA、AWS 客户端/目标/遥测头 | 当前 UA 为 `Kiro/1.0.0 (linux; x64; aws)`，另有推测性 Cognito/request-id 头；缺少模板中的 `x-amz-user-agent`、`x-amz-target` 等 | 假编。Kiro 模板有真抓，但抓的是 `q.us-east-1.amazonaws.com` + AWS JSON 形态；现 adapter 是 `api.kiro.aws/v1/chat/completions` 占位形态 | 不做局部换头，避免产生“真 UA + 假 endpoint/body”的混合指纹；需单独整体对齐 |
| Cursor session（默认 off） | UA、`x-cursor-client-version`、可选 checksum/cookie | `cursor-editor/0.43.6 (linux; x64)`、`0.43.6` | 假编 / 缺抓；代码明确写“模拟”，模板目录无 Cursor 文件 | 保持默认 off；待同版本模型请求抓包 |
| GitHub Copilot session、service-token refresh、device bootstrap（session 默认 off） | UA、Editor-Version、Plugin-Version、Intent、API-Version、Integration-Id | `GitHubCopilotChat/0.26.7`、`vscode/1.95.0`、`copilot-chat/0.26.7`、`conversation-panel`、`2025-04-01`、`vscode-chat` | 缺抓；三条路径复用同组固定值，模板目录无 Copilot 文件 | 不猜更新版本；需分别抓模型、service-token 与 device flow |
| Windsurf session（默认 off） | UA、extension version、可选 telemetry tags | `Windsurf/1.0.0 (linux; x64; codeium)`、`1.8.40` | 假编；代码明确写“模拟/推测”，模板目录无 Windsurf 文件 | 保持默认 off；待同版本模型请求抓包 |
| xAI Grok 官方 API | 无网页客户端身份头；仅标准 Bearer/JSON | generic OpenAI-compatible adapter | 真（诚实直通） | 保持 |
| Grok 网页 session（未注册） | 浏览器 UA、Sec-CH、Origin、Baggage、固定 Statsig | Chrome 133/macOS 静态组 + 固定 Base64/遥测串 | 假编；代码注释直接写“伪造”，无自有模板 | 不接线、不改成另一个猜值；列入待采集与 clean-room 整改 |
| Kimi、DeepSeek、Mistral、Groq Cloud、Together、Perplexity、Fireworks | 无第一方客户端身份头 | 共用 OpenAI-compatible adapter，只发 Bearer/JSON/Accept | 真（诚实直通） | 保持 |
| Qwen、GLM、Yi、Baichuan、Doubao、ERNIE、Step、Hunyuan、MiniMax | 无第一方客户端身份头 | 共用 OpenAI-compatible adapter，只发 Bearer/JSON/Accept | 真（诚实直通） | 保持；国内厂商没有被漏掉 |
| Cohere compatibility | 无第一方客户端身份头 | 共用 OpenAI-compatible adapter | 真（诚实直通） | 保持 |
| OpenRouter | `HTTP-Referer`、`X-Title` | 只在运营凭据显式提供时原样发出 | 真（不自编） | 保持 |
| Bedrock | SigV4 的 date/hash/security-token/Authorization | 由真实请求、凭据和时间动态计算，无固定客户端 UA | 真（协议派生） | 保持 |
| Vertex Gemini / Anthropic | `X-Goog-User-Project` | 真实 project_id 动态值；无固定客户端 UA | 真（协议派生） | 保持 |
| Ollama、Dify、Replicate | 无第一方客户端身份头；各自只发协议/鉴权头 | 无自编 UA | 真（诚实直通） | 保持 |
| Gemini / Codex / Antigravity / Cursor / Kiro / Windsurf OAuth refresh | 显式 UA/客户端身份头 | 均未显式设置，实际发送由 Go transport 使用自身默认 UA | 缺抓；这些是订阅客户端刷新形态，不能证明与真客户端一致 | 不编值；待每家 refresh 抓包 |
| Vertex service-account token mint | 无客户端身份头 | 标准 RFC 7523 form 请求 | 真（诚实 server-to-server） | 保持 |

## 6. 待采集真身份清单

按优先级排序：

1. **Gemini Advanced 网页会话**：模型请求完整 URL、真实浏览器 UA、Sec-CH-UA 族、Referer、Origin/X-Origin、X-Goog-Authuser、Visitor/扩展头、动态 `bl` 与 header 顺序。现有 Gemini 模板不能覆盖此路径。
2. **Kiro 真实目标形态裁决 + 抓包**：先确认产品要接的是模板中的 AWS `q.us-east-1` 形态还是当前 `api.kiro.aws` 形态；确认后一次性对齐 endpoint、Content-Type、UA、`x-amz-target`、`x-amz-user-agent`、opt-out、SDK request/invocation ID。禁止只拼头。
3. **OpenAI Codex 模型 API + refresh**：完整 UA 动态片段、`originator`、`version`、account/session/thread/window/client-request/turn metadata 头及实际条件；当前模板 HTTP 部分不是抓包。
4. **Anthropic 模型 API + OAuth refresh**：UA、全部 X-Stainless、X-App、Anthropic-Beta 默认组合、会话/请求 ID、header 顺序；当前模板仅 TLS 真抓，HTTP 是 placeholder。
5. **Antigravity**：生成请求、`loadCodeAssist`、`onboardUser` 分别采集 UA 与 X-Goog 头，确认版本/platform 组合。
6. **Cursor**：模型请求 UA、client version、checksum、request/timezone/trace 头与 cookie 条件。
7. **Copilot**：模型请求、service-token 获取、device OAuth 三条路径分别采集，不能假定一组头通吃。
8. **Windsurf**：模型请求 UA、extension/IDE/client-type/common-flags/telemetry/Origin/Referer。
9. **Grok 网页 session**：仅用 HUAKAI 自有账号抓包重建浏览器身份；现有固定 Statsig/遥测串不得继续当真值证据。
10. **各 session provider 的 OAuth refresh**：目前多数只显式发 Content-Type/Accept，需确认真客户端是否带 UA、SDK 标识或设备头。

标准开发者 API key/server-to-server 路径不在“待采集第一方客户端身份”清单，因为它们没有宣称自己是某个官方桌面客户端；若未来产品决定对这些路径也模拟特定 SDK，应另立 scope 并先抓包。

## 7. 判别性变异实测

### 7.1 测试先行红灯

先只把旧“非空”测试改成真值精确断言，尚未改生产常量时执行：

```text
go test ./internal/provider/gemini -run '^TestCodeAssistCapturedIdentityHeaders$' -count=1
User-Agent="HUAKAI-GeminiCLI/1.0"，期望抓包真值 "GeminiCLI/..."
X-Goog-Api-Client="google-genai-sdk/1.0 gl-go/1.0"，期望抓包真值 "gl-node/22.22.2"
FAIL（exit 1）
```

这证明新测试能识别两个旧假值，而不是事后只测新常量存在。

### 7.2 Owner 指定变异刀

生产真值转绿后，临时把 `codeAssistUserAgent` 精确改回 `HUAKAI-GeminiCLI/1.0`，执行同一命令，得到：

```text
--- FAIL: TestCodeAssistCapturedIdentityHeaders (0.00s)
    code_assist_test.go:185: User-Agent="HUAKAI-GeminiCLI/1.0"，期望抓包真值 "GeminiCLI/0.41.2/gemini-3.1-pro-preview (linux; x64; terminal) google-api-nodejs-client/9.15.1"
FAIL
exit 1
```

随后立即用补丁还原真 UA，再执行同一命令：

```text
ok github.com/BloomingProsperity/HUAKAI/internal/provider/gemini 0.004s
exit 0
```

最终 diff 中不含旧 UA；精确测试同时守卫 `X-Goog-Api-Client`。

## 8. 门禁结果

用户给定的 `/home/ubuntu/.gotmp` 在当前沙箱是只读，首次目标测试在编译前报：

```text
go: creating work dir: mkdir /home/ubuntu/.gotmp/go-build...: read-only file system
```

改用 `/tmp/huakai-*` 后可运行，但全量测试并发链接时 `/tmp` 的 7.8G tmpfs 一度报 `disk quota exceeded`。清理本轮 `/tmp` Go cache 后，改用仓库 `.gitignore` 已覆盖的 `backend/.gocache` 与 `backend/.gotmp`（根盘剩余约 145G）复跑相同测试命令。两次环境失败都不算代码通过，最终结果以下面的成功复跑为准。

| 门禁 | 结果 | 证据摘要 |
| --- | --- | --- |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test ./internal/provider/... -count=1` | PASS | 21 个 provider 包全部通过 |
| `go test ./... -count=1` | PASS（第二次，根盘 cache/tmp） | 全包通过；`internal/gatewayhttp` 等首次受 tmpfs 影响的包复跑均绿 |
| `make quality-gate` | **FAIL：基底既存 deadcode 差异** | staticcheck baseline `93/93` 且无新增；deadcode baseline `811/811`，但报 `internal/modeladminhttp/routes.go: NewRouter` 与 `internal/modelroutingadminhttp/routes.go: NewRouter` 未入 baseline |
| `go test ./internal/codebudget -count=1` | PASS | exit 0，0.046s |
| `git diff --check` | PASS | 无空白错误 |

quality-gate 两项归属说明：两个 `routes.go` 最近归属提交为 `8dcdaff154967b01cb5cea76b4a92f28ea2db639`；本 Slice 起始 HEAD 已包含该提交，本次代码 diff 仅触及 Gemini `code_assist.go` 与其测试。它们还只被各自 `_test.go` 调用，`deadcode ./...` 不看跨测试引用，形态与 baseline 中已有的测试 router 假阳一致。按 Owner 的“baseline 不新增”和当前“不扩 scope”要求，本片没有洗 baseline、没有删除/接线无关路由；需 Claude/Owner 决定由原功能片清债还是按既有 test-helper 规则显式补录。因此当前不能诚实声称“全门禁全过”。

## 9. 新发现的分歧与风险

1. **Gemini 模板命名误导**：`gemini-advanced.json` 的 `mode_name` 虽是 `gemini_advanced`，HTTP endpoint 实际是 Gemini CLI 的 `cloudcode-pa`，不能给网页 session 使用。
2. **Gemini wire 仍有非身份分歧**：抓包是 `Accept: */*`、`Accept-Encoding: gzip,deflate`、`Connection: close` 与固定 header 顺序；本片只修 UA/API client，没有扩大到 transport/wire 重写。
3. **模板 schema 文档陈旧**：`tools/fingerprint-collector/templates/SCHEMA.md:153` 仍写 Gemini 模板“仍是 stub”，但 JSON 已是真抓回填。未在本片改，避免夹带文档清理。
4. **Kiro 不能局部换真**：真抓模板与占位 adapter 是不同 endpoint/body/content-type；只换 UA 会更假。
5. **Anthropic/Codex 模板不是 HTTP 抓包**：不能把 manual/source-analysis 当作真 wire 证据。
6. **预存 clean-room 风险**：`backend/internal/provider/grok/session.go:1-38` 的生产注释直接点名外部实现并声明沿用伪造固定值，违反当前“代码注释不提借鉴项目”和自有抓包要求；该 adapter 未注册，且本片没有复制/扩散其标识符。建议独立 clean-room 整改，不应在本 Slice 静默洗掉证据。
7. **规则张力**：`docs/RULES.md` CB-001 旧条仍把第一方客户端冒充列为 park；本次 Owner 指令明确授权仅以自有抓包模板换真身份头并排除 R7。本片按更具体、更新的 Slice 授权执行，未新增请求体伪装、未翻默认 flag。

## 10. Owner 汇报

1. **做了什么**：把 Gemini Code Assist 的 UA 与 X-Goog-Api-Client 从自编值换成同一模型请求的 Owner 自有抓包真值；把弱“非空”测试升级为精确判别测试；亲做旧 UA 回切红灯和还原绿灯；逐条普查所有 provider 模型、session、引导和刷新出站身份头。
2. **改了哪些文件**：`backend/internal/provider/gemini/code_assist.go`、`backend/internal/provider/gemini/code_assist_test.go`、Codex 独立计划 `docs/process/plans/2026-07-15-mimicry-slice1-provider-identity-codex.md`、本报告 `mimicry_slice1_report.md`。
3. **为什么这样做**：只有 Gemini Code Assist 同时具备同 endpoint、同客户端形态、自有 HTTP 抓包和可直接落地的固定值；其它路径缺抓或形态不匹配，Truth-First 下不能猜。
4. **有没有功能缩水**：没有。未删 header、未关 provider、未翻 flag；只替换两个默认假值并加强测试。
5. **有没有 clean-room 风险**：本次真值只来自 HUAKAI 自有抓包模板，没有读取或复制三镜源码；发现一处预存 Grok 生产注释/固定值风险，已如实列出但未扩散。
6. **有没有安全风险**：未碰 auth、计费、quota、schema、密钥或 R7；身份头变更只影响默认关闭的 Gemini Code Assist 出站形态。主要剩余风险是版本漂移，已由精确测试暴露而非静默接受。
7. **哪些地方需要 Owner 确认**：quality-gate 的两个基底 deadcode 假阳由原片清债还是显式补录；Kiro 下一片选择哪套真实 wire；是否另开 transport slice 对齐 Gemini 的 Accept/压缩/连接/header 顺序；是否立即处理预存 Grok clean-room 风险。
8. **下一步建议**：Claude 先亲验 diff、复跑 Gemini 变异测试并确认 quality-gate 基底问题；随后按待采集清单从 Gemini 网页、Kiro、Codex、Anthropic 四项开始，不要再用“看起来像”的静态串填空。

本工作没有 commit、没有 push，停在工作树等待 Claude 亲验。

