# codex 账号全量翻译层接通 — Claude 实现计划(2026-07-06,Owner 已拍板全量翻译层)

## Owner 决策
AskUserQuestion 拍板:**全量翻译层**(任意客户端形态 chat/responses/messages → codex 账号自动翻译为 codex/responses 上游),对齐 sub2api 默认参考,codex 账号可当通用 OpenAI API 卖。

## 🔴 实现地基问题(亲核发现,必须先解)
HUAKAI 内部对 codex 上游形态的记载**自相矛盾且与三镜实证不符**:
- endpoint = `/backend-api/codex/completions`(codex_session.go:25 defaultCodexEndpoint);
- 响应解析 = **chat-chunk**(protocol_selector.go:97 注释「响应 SSE 与 Chat Completions 兼容 data:{choices}」,复用 openai.Adapter);
- 请求侧却标 **Responses 形**(upstream_dispatcher_hcsf.go:342 native-raw Responses 形);
- 注释自承「仓内两处记载互斥,形态未定,待 OCAW 真实流量采集确认后再接」→ 这是 codex 一直 fail-closed 的根因。

**三镜实证(2026-07-06 调研,SHA sub2api@87dfc66/CLIProxyAPI@9e9c244/new-api@8874d19)**:三家 codex OAuth 上游一致是 `chatgpt.com/backend-api/codex/**responses**`,请求响应**都是 Responses 形**(不是 /completions,不是 chat-chunk)。证据:sub2api chatgptCodexURL=/backend-api/codex/responses(openai_gateway_service.go:43);new-api GetRequestURL 拼 /backend-api/codex/responses 只认 Responses(adaptor.go:137);CLIProxyAPI codex executor to=codex responses(codex_executor.go:752)。

**结论**:HUAKAI 的 codex endpoint(/completions)与响应解析器(chat-chunk)很可能是过时/错误记载,真实应是 /responses + responses SSE(三镜共识)。**但改 relay 核心转发形态必须真实确认,不能凭三镜就改**——需用真实 codex 账号抓一次真实上游响应,坐实形态。

## 实现路径(形态确认后)
1. **确认形态(前置,需真实账号)**:真实 codex/chatgpt session 账号发一次请求,抓真实上游 endpoint(/completions vs /responses)+ 响应形态(chat-chunk vs responses SSE)。解开「两处记载互斥」。
2. **修正上游形态**(若三镜对):defaultCodexEndpoint → /backend-api/codex/responses;protocol_selector codex 响应解析器 → responses SSE(而非 chat-chunk)。
3. **接通翻译层**:canonical ↔ codex-responses 双向翻译(请求侧复用 OpenAIResponsesClient.RequestToCanonical→codex responses body;响应侧 codex responses SSE→canonical→客户端形态)。HUAKAI 已有 openai_responses 的 canonical 双向翻译(openai_responses_request/response/stream.go),codex 请求侧=responses 形,大概率复用+小调整。
4. **解除 fail-closed**:hcsfProviderRequestModelFamily 把 openai_codex 纳入翻译表(移出 native-raw fail-closed);validateNativeRawBodyIngress 相应调整(codex 改走翻译而非 raw 直通)。
5. **响应侧 chat/messages 回译**:codex responses SSE → canonical → openai_chat chunk / anthropic messages(让 chat/messages 客户端也能用)。
6. **live e2e**:真实账号真实上游,验证 chat completions 客户端 → codex 账号 → 真实 chatgpt.com → 响应回译 → 计费。

## 分片建议(渐进,每片可验)
- 片1:确认形态 + 修正 endpoint/响应解析器(真实账号,最小,解地基)。
- 片2:responses 客户端 → codex 直通翻译接通(canonical 复用)。
- 片3:chat completions → codex 翻译 + 响应回译(sub2api 式)。
- 片4:anthropic messages → codex(可选)。
- 每片 §14 变异 + §17 配合测试 + 对抗审查 + live e2e(有账号后)。

## 关键前置 = 真实 codex 账号
所有实现的地基是「确认 codex 真实上游形态」,必须真实账号。Owner「服务器有 gpt 账号」正是钥匙——请提供账号形态/凭据(env 注入,codex 不见明文),先抓真实形态。

## Owner-gated 标注
改 codex 上游 endpoint/响应解析器 + 解除 dispatch fail-closed = 默认行为改动,Owner 已拍板全量翻译层方向;具体形态修正待真实流量确认后落。

---

## ✅ 地基已解:真实账号 live 探测确认(2026-07-06,Owner 授权,~/.codex/auth.json chatgpt OAuth)
用真实 ChatGPT OAuth access_token(有效到 7/13)发最小 Responses 形请求探测,token 全程掩码:
- **`/backend-api/codex/responses` → HTTP 200**,响应 = **Responses API SSE**(`event: response.created` / `data:{"type":"response.created","response":{id,model:"gpt-5.5",reasoning,...}}`),即标准 OpenAI Responses 流式事件,**非 chat-chunk**。
- **`/backend-api/codex/completions` → HTTP 404 `{"detail":"Not Found"}`**——HUAKAI 现硬编码端点是死的。
- 请求头 originator=codex_cli_rs + chatgpt-account-id + version + Bearer + OAI-Language 被真实 backend 接受(200)。

**裁定(live 坐实,可动手)**:①defaultCodexEndpoint `/backend-api/codex/completions`→`/backend-api/codex/responses`;②protocol_selector 对 openai_codex 的响应解析从 chat-chunk 改 **Responses SSE**(复用 openai_responses adapter);③CodexSessionAdapter 补 originator + chatgpt-account-id(从 Extra/account_id)头;④validateNativeRawBodyIngress 放行 openai_responses→openai_codex(同为 Responses 形,live 兼容),chat/messages→codex 翻译留片2。片1 = 让 Responses 形客户端(Codex CLI)真实打通到 codex 账号,live e2e 用本机 ~/.codex 账号验。

## ✅ codex 账号能力面 live 探测(2026-07-06,多方位,Owner 要求"所有功能都测含图片")
真实账号逐能力探测(token 掩码,min 额度):
- **流式文本** ✅ Responses SSE:response.created→in_progress→output_item.added→content_part.added→output_text.delta→output_text.done→completed。
- **工具调用/function_call** ✅ 200(带 reasoning item)。
- **reasoning(effort)** ✅ 200(response.output_item.added reasoning)。
- **图片输入/vision** ✅ 200,有效 PNG(input_image)正确识别红图输出"Red",完整事件序列含 output_text.delta。
- **非流式** ❌ `{"detail":"Stream must be set to true"}`——codex /responses **只支持流式**。
- **硬约束(否则 400)**:`store` 必须 false;`max_output_tokens` **不支持**(需删);`stream` 必须 true。

**实现含义(codex OAuth 请求变换,对齐 sub2api applyCodexOAuthTransform)**:转发 codex 账号前必对 body 施加:强制 stream=true、store=false、剥离 max_output_tokens。客户端发非流式 → 网关内部强制流式再聚合回非流式给客户端(全量翻译层片2/3 处理)。
**测试矩阵(§17 多方位,每切片覆盖)**:流式文本 / 工具调用 / reasoning / 图片输入(vision) / 非流式(客户端非流式→内部流式聚合) / 长文本 / 结构化输出——live e2e 逐项。

## 边界/限额(Owner 要求"按官方标准测边界+看借鉴项目",2026-07-06)
**官方标准(OpenAI 官方文档,clean-room 豁免)**:
- 图片输入:单张 ≤20MB;格式 png/jpg(jpeg)/gif/webp;单请求可多张(按 token 计费);detail high/low/auto/original;文件类各 ≤50MB、合计 ≤50MB。
- 工具/function:单请求最多 128 tools;parallel_tool_calls 可 false 禁并行;部分模型不支持 parallel。
- 图片生成:image_generation 内建工具;尺寸 1024²/1024×1536/1536×1024;n 张数;质量档。
**live 探测(真实 codex backend,与公开 API 可能不同)**:
- image_generation 工具 → **200 接受**(codex backend 不拒图片生成工具;完整生成行为待读全流确认)。
- 130 个 function tools → **200 接受**(codex backend 未在 128 处拒——限额可能更高或不同层校验)。
- 注:200=请求被接受开始流,非完整成功;边界真实行为需读完整 SSE 流确认。
**测试矩阵补边界维度(§17,每项 HUAKAI 层校验单测 + 少量 live 边界探测)**:
1. 图片:>20MB 拒 / 非法格式拒 / 多张(2-3 张)/ 各 detail 档;
2. 工具:128 上限 / 超限处置 / parallel_tool_calls 开关;
3. 图片生成:codex 是否真出图(读全流)/ 尺寸档 / n 张并发;
4. **HUAKAI 应在自己层校验官方限额**(拒超大图/超量工具),而非把垃圾抛给上游——两部分:①HUAKAI 层校验单测(不打上游);②live 边界探测(节制,别打 20MB 真图轰炸账号)。
**待三镜对照**:sub2api/CLIProxyAPI/new-api 怎么处理图片输入/生成/工具限额与转发前校验(调研中)。

## 三镜边界调研结论(2026-07-06,a163910d,逐行取证+8点复核)
**codex 请求变换三镜共识(HUAKAI 必对齐,漏则上游 400)**:
1. store→强制 false;2. 剥离 max_output_tokens **+ temperature**;3. **instructions 字段必须存在(至少 "")**;4. header 注入 account_id/originator。
- 差异:stream 强制(sub2api/CLIProxyAPI 强制 true,new-api 跟随客户端)——HUAKAI 学 sub2api 强制 true(codex live 证只支持流式);reasoning 处理(new-api codex 渠道不碰=空白点,HUAKAI 避免);sub2api 注入真实 Codex CLI instructions 模板,另两家兜底空串。
**⚠️ 片1 验收补验点(我 prompt 只写了 stream/store/max_output_tokens,漏了)**:确认 codex 变换也**剥 temperature** + **instructions 必存**;若片1 没做,补。
**边界闸门三镜一致「不设」**:图片大小/张数、工具 128 上限、图生成 n/并发——三家全不校验,交上游报错。HUAKAI 决策=学"不设闸门"(对齐三镜低风险)vs 自建防滥用(三镜无参照)→ Owner-gated,倾向先对齐三镜不设闸门,防滥用作 follow-up。
**唯一模型能力级拒绝**:sub2api spark 模型+图片→400(openai_codex_transform.go:672)。
**图片生成三档**:CLIProxyAPI 默认注入 image_generation 工具(最激进)/ sub2api 双路由+组权限门 / new-api codex 渠道不支持(最保守)。HUAKAI 定位待 Owner。

## 🎉 片1 live 全能力验证通过(2026-07-06,真实 ChatGPT 账号)
TestCodexLiveResponsesMatrix 5 项全 PASS 打真实 chatgpt.com:流式文本✅/工具调用✅/reasoning✅/图片vision✅/请求变换验证✅(客户端带 temperature/top_p/max_output_tokens/store=true/stream=false→网关剥离变换→上游200)。真实 ChatGPT 账号→HUAKAI relay→真实 codex 后端→响应→计费全链打通。
**排障挖出的 live 事实(测试harness+生产)**:①codex input 必须是 list(纯字符串→400 "Input must be a list");②**version 头:显式发旧版(0.99.0)→400 "requires a newer version";不发 version 头→200**(故生产缺 codex_version 时 omit 正确,operator 别配旧版);③codex 不需 TLS 伪装(明文200,ChatGPT mimicry 模板缺失是已知gap,e2e 关伪装);④account_type='session'(非 auth_mode);⑤api_key key_prefix 取 bearer 前16字符,前缀须短到让 unique 进前16(否则 LIMIT 5 漏新行)。
