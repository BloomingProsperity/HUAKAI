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

## 片1 对抗审查(ff76e3f4,post-commit,24 agent):1 存活 S2,记入片2/3
**S2(存活)**:normalizeCodexResponsesBody 无条件 stream=true,非流式客户端走缓冲 SSE 重组受 1MiB 上限(maxRawBufferedUpstreamBodyBytes)约束——SSE 体积是等价 JSON 数倍,长非流式补全累计>1MiB→误判 CodeUpstreamResponseTooLarge 502。片1 明确 defer 的"非流式→内部流式聚合"未做的后果,短输出测试掩盖。**修=片2/3**:非流式客户端→内部流式→聚合成非流式 JSON 返回(不走 1MiB 缓冲原始 SSE)。非 S0/S1 不 hotfix。
其余驳回/降级:max_output_tokens 剥离(客户端上限失效,S3)、top_logprobs 可能误剥(S3 待 codex 支持性确认)、reasoning encrypted_content 流式丢弃(多轮续接,降级)、chatgpt-account-id 双缺省静默(多账号路由,S3)——记 follow-up。

## 其他 vendor 账号转 API 现状盘点(2026-07-06 亲核)
**采集+物化:全 wired**(Claude claude_ai_oauth/claude_code、Grok xai_oauth、Gemini code_assist/google_one、Antigravity oauth 均有物化 handler,types.go:274-294)。
**serving(账号转API)状态(与 codex 同类,需 verify+wire+live)**:
- codex/ChatGPT:✅ 片1 done,live 全能力验证。
- **Claude**(claude_ai_oauth/claude_code→anthropic_claude_session):**serving 未注册=fail-closed**(protocol_selector 只注册 anthropic_messages,无 anthropic_claude_session;oauth_session.go:22 有 Platform 名但 dispatch transport 无映射)。需 codex 同款处置。**Owner:不允许用本机 claude 凭据测**→需 Owner 提供 Claude 订阅账号 live 测。
- **Grok**(xai_oauth→grok_chat,openai.Adapter):family 注册、dispatch 走默认 providerCode=grok;openai-compat 可能可用但**未 live 验**真实 Grok OAuth 上游端点/形态。
- **Gemini 账号**(code_assist/google_one→gemini_advanced_session;dispatch:30-31 映射 ProviderGeminiAdvanced):dispatch transport 已映射但**未 live 验**真实端点/形态(codex 同款风险:端点/响应形可能过时错)。
- **Antigravity**(→antigravity_session/ProviderAntigravity,dispatch:32-33):同 gemini advanced,dispatch 映射在、未 live 验。
**结论**:除 codex 外这些账号 serving 均"部分接线未 live 验",每个需 codex 片1 同款方法论(真实账号探端点/形态→修 endpoint/transform/parser/ingress→live 全能力 e2e)。均需真实账号(Claude 必须 Owner 提供、不用本机)。

## 片2a 对抗审查裁定(506f3baa,wlfrjrse5,15 agent,§17 链路配合视角复核)
Owner 提醒「不能只看当前链路,要看链路上其他颗粒度模块 + 模块之间的配合」——已把两发现放回整条 reader 聚合链路亲核真码,而非就地补丁。

**链路全貌**:泛型 `ReconstructBufferedFromSSEReader` 有两个消费端——(a) raw/HCSF=0 路径 `chat_completions_dispatch.go:756`;(b) 生产默认 HCSF 路径 `upstream_dispatcher_hcsf.go:278`;对**所有 family**(anthropic/gemini/openai/dify/ollama)生效。另有字节版 `ReconstructBufferedFromSSE` 两入口(hcsf:246 / handler:809,输入已 bounded)。

**S2(losses/总事件无界→OOM)= 链路通病,fix(片2b)**:软上限只数 canonical 事件(reconstruct.go:127-135),而 adapter 遇坏事件返回「0 事件+1 loss」(responses_sse.go:49-52/210-214),`losses` 在 :144 无界增长、软上限永不触达。恶意上游狂发不可解析小事件→数 GB→拖垮共享网关。**adapter 契约↔聚合 DoS 守卫的配合缺口**,是刚 ship 的片2a 代码的安全洞→片2b hotfix:泛型层加总消费事件上限 `MaxUpstreamEvents`,一次覆盖全 family+两消费端。

**S3(重试分类分叉)= 真·模块配合缺陷,dev-only,记录不混修**:raw 路径(chat_completions_dispatch.go:757-769)对瞬时聚合错误硬 502 不 failover;HCSF 默认路径(upstream_dispatcher_hcsf.go:283)把同错误交 `ClassifyAttemptDispatchError`→idle/network timeout 判可重试→换号 failover。**尚未向客户端写字节时**(缓冲聚合恒真,failover 安全),raw 路径把本可恢复的瞬时错误变硬失败。但 raw 路径只在 `HUAKAI_DISPATCH_HCSF=0`(dev-only)可达,生产默认走 HCSF 路径 failover 正确。收敛=让 raw 路径瞬时聚合错误也走 `ClassifyAttemptDispatchError` 分类(未写客户端则返回可重试 failure 供换号),与 HCSF 路径一致。**因触 failover/重试语义(记忆两次栽在 codex 擅改 failover 语义),列为专门切片 + surface Owner,不在片2b hotfix 混改。** 生命周期本身正确(单次 abort、无 hold 泄漏、无重复 abort),仅可恢复性分叉。

**审查驳回的(未存活,记录以免重开)**:①response.failed/cancelled/传输截断当成功结算少计费——驳回(失败流不产 buffered_response→被 :285/:771 判 `!ok` 拒、不结算,且已记 loss+stop_reason);②聚合读取无 inter-event 超时占并发槽 S3——seedCtx 带请求级 deadline,记 follow-up;③16MiB 对 codex 不可达 S3——2MiB per-event 先绑,设计如此。

## 片2c 计划:chat/anthropic 客户端打通 openai_codex(方案A 接线,非新建翻译层)
让标准 OpenAI Chat SDK / Anthropic Messages 客户端也能用 codex 账号。经 7-agent Workflow(wf_1a475897-6d6)穷尽镜像点+双对抗审查验证。

**三镜对照(§16)**:sub2api=chat→Responses 双阶段(typed struct+map,Responses 形为 IR,`chatcompletions_to_responses.go`+`openai_codex_transform.go`,证实**支持** chat→codex 非直通);CLIProxyAPI=逐 `(from,to)` 对直译无统一 IR(`internal/translator/codex/openai/chat-completions/`);**HUAKAI upgrade delta(架构维)= canonical IR(HCSF)单渲染器**:实现一次 canonical→Responses(复用现成 `marshalOpenAIResponses`),chat/anthropic/gemini→codex 全客户端协议复用,无需逐对写。

**方案A=接线,复用现成资产**:请求侧 `marshalOpenAIResponses` 已投影 Responses 形;响应侧 codex 已注册 `ResponsesAdapter`(SSE→canonical)+ `OpenAIChatClient`(canonical→chat)零新码;字段裁剪 `normalizeCodexResponsesBody`(adapter 层)自动兜。分叉键=**ingressFamily**(禁 body 嗅探)。

**改动(P1-P5,生产码4处+可观测1处,3文件)**:P1 `hcsfProviderRequestModelFamily` 加 `case openai_codex→openai_responses`(一处驱动非流式marshal+流式marshal+流式翻译门);P2 注释同步;P3 `hcsfProviderRequestUsesNativeRawBody` 加 ingressFamily 形参+codex 白名单(""/同族/openai_responses→native-raw 保真直通,其余→marshal)——**保真红线:绝不无条件 false**;P4 放宽两处聚合门(门①`hcsfShouldAggregate`=HCSF-on 生产关键 / 门②`shouldAggregate`=HCSF=0 dev-only)删 `ClientProtocol==responses`;P5 D1 可观测性。

**对抗审查裁定的两项(surface Owner)**:
- **D1(S2,money-adjacent)**:chat 客户端 max_tokens→max_output_tokens/temperature/top_p 被 `normalizeCodexResponsesBody` 静默剥离(codex 上游确实拒收,剥离必需)**但无 ProtocolLossEntry**——客户端输出/花费上限在 codex-backed 模型上失效且不可观测。§17 配合缺陷(marshal 建模为生效↔adapter 无声丢)。片2c P5 补 loss 记录;若落点不干净则降级 follow-up。**codex-backed 模型不支持这三参数**须写进部署文档。
- **D2(S3 验证盲区)**:注入的 `stop`/`parallel_tool_calls`/`tools`/`text` 不在 codex 剥离集,若 chatgpt.com codex 端点比标准 Responses 更严拒收则 chat→codex 硬 400。**live e2e 必须带 stop+tools 的真实 body** 才能证伪,否则覆盖盲区。

**测试(§14/§17)**:判别测试打 **HCSF-on 路径(门①)** 非 dev-only 门②(否则假绿);例外表移除 openai_codex;变异点=P1去case→501/P3无条件false→保真破/门①改回带ClientProtocol→非流式chat→codex不聚合。派 codex 实现(dispatch 无 --sandbox),PM 亲检+跑门+live e2e。
