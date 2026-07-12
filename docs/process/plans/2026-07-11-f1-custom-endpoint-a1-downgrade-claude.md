# 2026-07-11 F1 允许 api_key 自定义上游地址 + A1 五厂降级 — 实施计划(Claude spec)

Owner 两项拍板:①「允许自定义上游地址」(F1)②「那 5 个厂商不做」(A1)。本计划把两项落成可派 codex 的精确 spec。基线分支 feat/fe-wire-users-mod @01d2d3af。

## 范围 / success criteria / blast radius / 决策点

| 项 | 内容 |
|---|---|
| Scope | F1:放开 `EndpointForCredential` + `UsesCustomPassthroughEndpoint`,让 CredentialTypeAPIKey 凭据也认 `Extra["base_url"]` 自定义上游 endpoint,过同一 SSRF 守卫。A1:6 条契约从 Released 降 Scaffold。 |
| 范围外 | 不动 SSRF 守卫本身(safePassthroughBaseURL / ValidatePassthroughEndpointTarget 逻辑不变);不加 DB schema(base_url 早在 Extra 白名单);不建 A1 五厂 handler(Owner 明确不做);不碰 auth-core/money-ledger。 |
| Success | F1:api_key + 合法 base_url → 打到自定义 endpoint;api_key + 危险 host(metadata/loopback/内网)→ 拒;api_key 不带 base_url → 仍走 adapterDefault(零回归);passthrough 原行为不变。A1:6 family HasContract=true 但 releaseCanServe=false + 非 startup-blocking + catalog 不标可卖。 |
| Blast radius | F1 影响所有 OpenAI 兼容 api_key 厂商(Kimi/Grok/DeepSeek/国内六厂等)的 endpoint 选择——都获得"可配自定义 endpoint"能力(与 Owner「敏感模块给能力非控制」一致);默认无 base_url 路径零变化。A1 只影响这 6 family 的 serving 评估与启动门。 |
| 决策点 | 两项均 Owner 已拍板。实施中若发现需动 schema / auth-core / money 立即停。预期不触发。 |

## F1 精确改动

### 现状(已核 file:line)
- [adapter.go:96-99](../../backend/internal/provider/adapter.go):`EndpointForCredential` 第 97 行 `if cred.Type != CredentialTypeUpstreamPassthrough { return adapterDefault }` → api_key 凭据的 base_url 被忽略。
- [passthrough_endpoint_guard.go:106-116](../../backend/internal/provider/passthrough_endpoint_guard.go):`UsesCustomPassthroughEndpoint` 第 107 行 `if cred.Type != CredentialTypeUpstreamPassthrough { return false }` → dispatcher 不对 api_key 跑出站前 SSRF 校验。
- [types.go:220](../../backend/internal/credentialstore/types.go):materialize 的 Extra 白名单**已含 base_url** → api_key 凭据本就能存 base_url,只是下游忽略。
- 凭据类型映射 [postgres_vault.go:294-306](../../backend/internal/provider/postgres_vault.go):RuntimeAPIKey→CredentialTypeAPIKey。

### 改法
1. `EndpointForCredential`:把门从「仅 passthrough」放宽到「api_key 或 passthrough,且 Extra["base_url"] 非空」。即:CredentialTypeAPIKey **且** base_url 非空时,走与 passthrough 相同的 `safePassthroughBaseURL` + path 拼接逻辑;api_key 且 base_url 空 → 仍返回 adapterDefault(零回归)。其余类型(OAuth/Session/SigV4)行为不变。
2. `UsesCustomPassthroughEndpoint`:api_key 凭据带 base_url 时返回 true(让 dispatcher 出站前跑 `ValidatePassthroughEndpointTarget` DNS-time SSRF 守卫)。函数名/注释相应更新(不再只指 passthrough)。
3. 复核所有 `EndpointForCredential` / `UsesCustomPassthroughEndpoint` 调用点(尤其 openai_compat_passthrough / gemini passthrough / dispatcher),确认 api_key + base_url 路径**必经** SSRF 静态校验(safePassthroughBaseURL)+ DNS-time 校验(ValidatePassthroughEndpointTarget),无绕过。
4. 注释诚实:说明这是「给 operator 自配上游 endpoint 的能力,SSRF 守卫仍 fail-closed 拦 metadata/内网/loopback」。不写任务号/日期/Owner 拍板等过程语句。

### 判别测试(每条变异证)
- `api_key + base_url=https://api.moonshot.cn/v1` → endpoint 含 moonshot host。**变异**:改回 `!= CredentialTypeUpstreamPassthrough` 旧门 → 断言 endpoint==moonshot 变红(实际返回 adapterDefault)。
- `api_key + base_url=http://169.254.169.254/...`(metadata)→ 拒(ValidatePassthroughEndpointTarget 报错)。**变异**:去掉 api_key 的 SSRF 校验分支 → 该拒却放行,测试红。
- `api_key + base_url=http://127.0.0.1...`(loopback)→ 拒。
- `api_key 不带 base_url` → endpoint==adapterDefault(零回归)。**变异**:若新逻辑误把空 base_url 也当自定义 → 返回空/错,测试红。
- `passthrough + base_url` → 原行为完全不变(回归保护)。
- Kimi 具体:kimi api_key 带 base_url=moonshot → moonshot;不带 → coding 默认口(不破坏编程订阅账号)。

## A1 精确改动

### 现状
[contracts.go:208-265](../../backend/internal/servingcapability/contracts.go):
- openrouter_chat(:209 releasedOpenAICompatible)
- cohere_chat(:254 releasedOpenAICompatible)
- ollama_chat(:255 releasedOpenAICompatible)
- ollama_native(:256 releasedContract)
- dify_chat(:259 releasedContract)
- replicate_image(:262 releasedImageContract)

全标 Released,但 [types.go handlerSpec](../../backend/internal/credentialstore/types.go) 无这 5 vendor 的 handler → 不可导入凭据 → 不可 serving。已核这 6 family 在生产码无账号/定价/其它依赖(grep 零命中)。

### 改法
把这 6 条从 released* 构造器改为 `contract(..., ReleaseStateScaffold, ..., wireVerified=true, reason="no_credential_handler")`(request/response/stream shape 保持各自原值:ollama_native 用 NDJSON、replicate_image 用 image/None、dify 用 SSE、其余 OpenAIChat/SSE)。
- 效果:releaseCanServe=false(不可卖)、StartupBlocking=false(Released 才阻塞,见 evaluate.go:154)、catalog readiness 标 scaffold。
- reason 用 "no_credential_handler"(诚实说明为何未发布:无凭据 handler)。

### 判别测试
- 断言这 6 family `HasContract==true` 且 `releaseCanServe==false`。**变异**:把任一改回 Released → 若无 handler 仍标可卖,断言 releaseCanServe==false 变红。
- 更新任何现存断言这些是 Released / 计入 released 计数的测试(codex 全跑 `go test ./...` 找出并修)。

## 门禁(codex 必跑,禁 commit)
`gofmt -l`、`go build ./...`、`go vet ./...`、`go test ./...`(含 codebudget、servingcapability、provider、credentialstore)。产出统一 diff + 变异红点记录 + 门禁输出,交 Claude 亲检后由 Claude 提交。

## clean-room / 语言
纯 HUAKAI 内部改动 + 官方公开契约(moonshot/官方 endpoint 是公开地址),无三镜借鉴代码。代码注释、报告全中文。
