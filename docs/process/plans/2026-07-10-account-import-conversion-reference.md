# 账号→API:导入格式 × 转换方式 跨系统参考(sub2api + CLIProxyAPI)

> 2026-07-10 双 agent clean-room specifier-lane 调研。ref:`sub2api@12d811b` + `CLIProxyAPI@26d45fd`。
> **范围说明**:本轮 Owner 明确限定"只看 sub2 与 cli 两个系统",故 new-api(§16 默认三镜之一)**本轮未纳入,属 Owner 定范围、非遗漏**;涉 new-api 的账号导入对照留待后续补。**逐条 `<repo>@<sha>:<file>:<line>` 证据**在两 agent 原始报告(`tasks/a267ea1384dcdc0fe`=CLIProxyAPI、`tasks/adba6393eaed95bb9`=sub2api),本文为其行为级综合摘要;此处未能溯源的结论一律以 agent 报告为准。
> 目的:给 HUAKAI 凭据导入(credentialacq)与 Antigravity adapter 重写(R3)对齐参考。外部契约值(OAuth 端点/scope/client_id/凭据 JSON 键/HTTP header 名)按上游 wire 事实原样列;两系统内部 Go 标识符按角色改述。

## 一、导入格式的六大类(跨所有厂商)

| 导入格式 | 说明 | 用它的厂商/类型 |
| --- | --- | --- |
| **OAuth 授权码流** | 浏览器授权→回贴 code 换 token。变体:有无 PKCE、有无 client_secret、localhost 回调 vs 托管页 | Claude、Codex/OpenAI、Gemini、Antigravity、Grok/xAI |
| **设备码流(RFC 8628)** | 无回调 server,轮询 token 端点 | Kimi(CLIProxyAPI) |
| **粘贴 session/token JSON** | 直接贴客户端的凭据文件/裸 token/cookie | Codex session JSON、Antigravity refresh_token、Claude sessionKey cookie(均 sub2api) |
| **API Key 手工填(+base_url)** | 运营者填 key,可带中转 base_url | 每厂的 apikey 子型 |
| **上传 service-account JSON** | 上传云凭据,运行时本地签 JWT 换短期 token | Vertex(两系统)、Bedrock AWS 凭据 |
| **Upstream 中转** | base_url+api_key 纯透传,不做协议转换 | Claude upstream、Antigravity upstream(sub2api) |

## 二、转换方式的五种手法(存的凭据 → 上游请求)

1. **Header 注入**:`Authorization: Bearer`(OAuth)/ `x-api-key`(Claude apikey)/ `x-goog-api-key`(Gemini·Vertex apikey)/ `chatgpt-account-id`(Codex,从 id_token JWT 解)/ `anthropic-beta: oauth-...`(Claude OAuth 订阅号必需)。
2. **Refresh 兑换**:refresh_token→access_token,带提前量 skew / singleflight 去重 / 请求路径硬超时→failover、后台续刷。
3. **端点选择**:按 oauth_type(Gemini code_assist→cloudcode-pa vs ai_studio→generativelanguage)、daily-vs-prod(Antigravity)、base_url 覆盖(apikey/upstream)、token_uri 强制官方防 SSRF(Vertex)。
4. **Body 包裹/转换**:Antigravity CloudCode `v1internal` 外层信封 + `enabledCreditTypes`;Claude→Gemini body 转换(Antigravity Claude 形);schema 清洗;**身份补丁系统提示(硬需求,缺则 429)**。
5. **指纹/UA stamping**:uTLS 指纹(Claude 过 Cloudflare)、强制 HTTP/1.1(Antigravity)、Codex UA/originator/session_id、Antigravity 刷新请求**故意用 `Go-http-client` UA**。

## 三、跨系统全类型对照总表

| 厂商/类型 | sub2api 导入 | CLIProxyAPI 导入 | 转换(→上游) |
| --- | --- | --- | --- |
| **Claude OAuth** | 授权码+PKCE(client_id `9d1c250a…`,回调贴 code);**或 sessionKey cookie 自动换**;setup-token(scope 仅 inference) | 授权码+PKCE(回调 `localhost:54545`) | Bearer + `anthropic-beta: oauth-2025-04-20,claude-code-…` + 身份系统提示/metadata。**这些 beta 头+身份是订阅号必需** |
| **Claude API Key** | api_key(+base_url 中转) | config.yaml 填 key | 默认 `x-api-key`;可切 Bearer |
| **Claude Upstream / Bedrock / Vertex-SA** | base_url+key 透传 / AWS 凭据 SigV4 / SA JSON 签 JWT 换 token | vertex:上传 SA JSON | Bedrock SigV4 或 Bearer;Vertex SA→Bearer,token_uri 强制官方防 SSRF |
| **OpenAI/Codex** | **Codex session JSON 粘贴导入**(解 id_token JWT 抽 chatgpt_account_id)/OAuth/PAT/apikey | 授权码+PKCE(回调 `localhost:1455`)+设备码/apikey | Bearer + `chatgpt-account-id`(JWT 解)+ `Originator` + Codex UA + `OpenAI-Beta: responses=experimental` + session 隔离 |
| **Gemini** | OAuth 三态 `code_assist/ai_studio/google_one`(内建 CLI client_id `681255809395…`)/apikey | 仅 apikey(`x-goog-api-key`)+ vertex;**无 gemini-cli OAuth 目录**(与退役一致) | code_assist→cloudcode-pa+Bearer;ai_studio/apikey→generativelanguage+`x-goog-api-key` |
| **Kimi** | (未展开) | **设备码流**(client_id `17e5f671…`,轮询 auth.kimi.com) | Bearer + `X-Msh-*` 设备头 |
| **xAI/Grok** | PKCE OAuth(client_id `b1a00492…`,回调 `127.0.0.1:56121`)/apikey | OIDC discovery+PKCE+nonce(校验 host 属 x.ai) | OpenAI 兼容 Bearer,base_url `api.x.ai/v1` |
| **Antigravity** | OAuth(见下)/refresh_token 粘贴/upstream 透传 | OAuth(见下) | 见 Antigravity 深度 |

## 四、Antigravity 深度(三系统 + 亲抓 agy 交叉)——喂 R3 重写

### OAuth 流:同一 client_id,三种登录姿势
`client_id = 1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com`(三处一致),scope 五段含专属 `cclog` + `experimentsandconfigs`:

| 来源 | redirect | client_secret | PKCE | 备注 |
| --- | --- | --- | --- | --- |
| **亲抓 agy CLI(真客户端·权威)** | 托管页 `https://antigravity.google/oauth-callback` | — | ✅ S256 | `auth_method=consumer`,登录后网页显 code 手动粘 |
| sub2api | `http://localhost:8085/callback` | ✅ `GOCSPX-…z6qDAf`(可 env 覆盖) | ✅ S256 | 授权码+双持 secret+PKCE |
| CLIProxyAPI | `http://localhost:51121/oauth-callback` | ✅ 同上 | ❌ 无 PKCE | 桌面应用流 |

→ **同一 OAuth app 支持多种流**。localhost+secret 流对 HUAKAI **无头自动化更友好**(不用粘托管页 code);但真客户端(agy)用托管页+PKCE。HUAKAI 可择一,以**亲抓真流量为准、localhost 流为便利选项**。

### 上游端点(⚠️ 校正 spec)
- prod:`cloudcode-pa.googleapis.com`(loadCodeAssist/额度/隐私 API 走这)
- daily:**亲抓 agy = `daily-cloudcode-pa.googleapis.com`(权威)**;sub2api 写的是 `daily-cloudcode-pa.sandbox.googleapis.com`(带 `.sandbox.`,可能旧/不同);CLIProxyAPI 无 `.sandbox.`。**以亲抓为准**。
- 生成/流式 daily 优先、prod 兜底;动作 `/v1internal:{generateContent|streamGenerateContent?alt=sse|countTokens|fetchAvailableModels|loadCodeAssist|onboardUser}`。

### v1internal 外层信封
`{project, requestId:"agent-<uuid>", userAgent:"antigravity", requestType:"agent"(image→image_gen), model, request:<内层 Gemini body>}`;响应取 `response` 字段解包。

### 一账号 fan-out 两/三形(核心)
**一份 Google OAuth 凭据 → 两种入向协议,出向端点/信封/鉴权完全一致**:
- **Gemini 形**:原生 Gemini→注身份补丁→清 schema→包信封→streamGenerateContent→SSE 透传。
- **Claude 形**:Claude Messages→模型映射→**Claude 请求转 Gemini 格式**→包信封→streamGenerateContent→**Gemini SSE 逐块转回 Claude SSE**;含 thought_signature 400 两级整流(剥 thinking→降级 tool 为 text 重试)。
- CLIProxyAPI 侧:claude 模型强制 `toolConfig.mode=VALIDATED`、用一段由首条 user 文本 sha256 派生稳定 session id 的逻辑、按模型族分 schema 清洗。

### ⚠️ 三个硬约束(不是可选伪装)
1. **身份补丁必需**:Gemini 形在 `systemInstruction.parts` 头插 "You are Antigravity";Claude 形强制打开身份补丁开关。**缺失即 429**——这是 serving 前提,不是 mimicry 加分项。
2. **只支持流式上游**:非流式请求由网关**收集流后回转**。adapter 必须内建 SSE 收集器。
3. **信用类型**:上游用一段信用类型注入逻辑,在「上下文要 credits + 开关开」时注入 `enabledCreditTypes:["GOOGLE_ONE_AI"]`;耗尽判据响应 `INSUFFICIENT_G1_CREDITS_BALANCE`(CLIProxyAPI)/ `paidTier.availableCredits`(sub2api)。

### 存储三元组(全链枢纽)
`{access_token(Bearer), refresh_token(后台续期), project_id(信封 project 字段+配额 Project 参数+token 缓存 key)}`;email→文件名/展示;plan_type/tier→仅展示与调度,不进 wire。

### 配额抓取
`fetchAvailableModels`(需 project_id)→每模型 `quotaInfo.remainingFraction`+resetTime;`loadCodeAssist`→tier(free/g1-pro/g1-ultra)+AI Credits 余额;403 分三态(validation 抽 validation_url / violation 封号 / forbidden)。

## 五、凭据脱敏(两系统共性,HUAKAI 应对齐)
敏感键集合:access_token/refresh_token/id_token/api_key/session_key/cookie/aws_secret_access_key/aws_session_token/service_account_json/private_key。
- **读脱敏**:DTO 剥敏感键,回吐 `{键:是否存在}` 状态位。
- **写合并**:全对象 PUT 时敏感键「incoming 没带就保留 existing」,避免脱敏回写清空 token。

## 六、对 HUAKAI 的直接影响(R3 + credentialacq)
1. **Antigravity 占位端点 `api.antigravity.ai` 错**——真后端 `cloudcode-pa`/`daily-cloudcode-pa` 的 `v1internal:streamGenerateContent`,两系统 + 亲抓三方实锤。
2. **身份补丁 + SSE 收集器 + 信用类型**是 Antigravity serving 三前提,adapter 重写必带。
3. **导入格式要覆盖多形态**:同一厂商往往有 OAuth / 粘贴 session / apikey / upstream 多条导入路;HUAKAI credentialacq 若只做一条=构件完工幻觉的又一处(R0 闸应能抓到「导入路缺失」)。
4. **sub2api 的 Platform×Type 正交模型**值得借鉴:凭据存原始 map、脱敏统一收敛、apply-credentials 原子 key 级合并——比按厂硬编码更省维护。

---
Source: 双 agent 报告(tasks/a267ea1384dcdc0fe + adba6393eaed95bb9)。Lane: specifier 合成。Agent: Claude / 06b5fe50。UTC 2026-07-10。
