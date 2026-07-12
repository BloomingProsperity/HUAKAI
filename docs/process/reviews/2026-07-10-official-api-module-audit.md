# 官方 API 模块全链审计(采集→key 物化→网关转发→计费)— 2026-07-10

**审计者**:codex(auditor lane),对照三镜(sub2api / new-api / CLIProxyAPI)+ 官方契约,基于 HUAKAI@7936a71c。
**范围**:用官方 API key 直通上游的族(anthropic_messages / openai_chat / openai_responses / gemini_messages / bedrock_invoke / vertex_* / openrouter_chat / deepseek_chat / 国内厂 *_chat 等)。
**总评**:只有"正确 vendor/auth + 已有可解析价格 + 无结算故障"的 happy path 接近闭合;作为完整官方 API 模块,**当前不满足全链闭合**。

## 缺口清单(按 severity)

### ✅ S0 — F0 Azure API key 外发 OpenAI【已修 0e706cfe】
Azure 账号 azure_api_key 物化成普通 APIKey → OpenAI adapter 发往 api.openai.com=密钥外发。已 fail-closed(PM 亲验+变异证)。Azure 完整支持(专属 adapter)属 follow-up。

### S1(未修,module ② 闭合前须处理;部分 Owner-gated 动钱/发布态)
- **G1 family/vendor/auth 不校验过河**:创建期只对 anthropic_claude_session 校验兼容;发网前同;选号 SQL 不按 credential vendor/auth 过滤;vault 直接拿 credential vendor 作运行时平台。→ 特权误配可把 A 厂 key 绑 B 厂 family,错投密钥/错误 transport-health 标签/计价归因分裂。`accountcreate.ValidateProtocolCompatibility` 已存在,扩到全族即可。
- **B0 结算失败四终局缺口(动钱,Owner-gated)**:①非流式 settle 失败在写客户端响应前入恢复队列→用户没拿到内容仍被扣;②流式 ledger+DLQ 双失败→已交付内容但 hold 释放=白吃;③settle+recovery DLQ 同失败只告警无第二补偿环;④Replicate 图片 settle 失败不 abort 不进 recovery,hold/槽冻到 sweep。
- **B1 OpenAI 流式不强制权威 usage**:不注入 stream_options.include_usage→缺权威 usage 时按估算终局计费(可能高/低估)。三镜主动要求 usage。openai_chat/kimi_chat 受影响。
- **B2 vertex_anthropic 缓存少计费**:input-不含-cache 集合漏了 vertex_anthropic→对缓存请求二次减 cache,少收 input token。
- **F2 Bedrock 错误补偿用 anthropic 标签**:v2 vault 运行时平台取 credential vendor=anthropic,而 Bedrock 专用 429/503 规则只绑 bedrock→限流/过载补偿不命中,影响 cooldown/health/failover。
- **M1 Vertex SA 能采集不能 serving**:legacy service_account / v2 raw SA / metadata-only 均物化 fail-closed(仅预置 access_token 短期路径可用)。→ 我的 vertexsa 包(未接线)补此;接线是 R1C-Vertex 切片。
- **F1 Kimi API-key 与 coding endpoint 不区分**:同 family 允许 api_key+kimi_oauth 但恒打 coding endpoint;普通 Moonshot API key 无可选默认 endpoint,APIKey 类型不能用 base_url 覆盖。三镜有独立 base URL。
- **A1 released 族不可采集(发布态矛盾,Owner-gated)**:openrouter/cohere/ollama/ollama_native/dify/replicate 标 Released 但无 handler + DB CHECK 拒绝(openrouter 测试明确锁"不注册")。mistral/groqcloud/together/perplexity/fireworks 是 Scaffold 且双重 fail-closed。→ 补 handler+DB 组合,或降级 release 状态。

### S2
- **G2 账号与 credential 非同事务**:账号先提交,credential 后建;双失败留 enabled/credential_state=valid/credentials={} 脏账号→选号选中后 vault resolve 失败回滚,5xx+池污染(不直接漏钱)。
- **P1 定价发布期不 gate**:运行时查不到价 reserve 前 503(正确 fail-closed),但绑定创建不 probe 定价→可发布 R0 绿、账号在、每次 reserve 503 的模型。
- **T1 无逐族全链 E2E**:真 v2 API-key 全链 E2E 只有豆包(仅非流式);混元是 legacy upstream_static;Grok 是 OAuth。缺逐族真 endpoint/usage/结算证据。

## 所有 fail-closed 官方族/模式
Released 但采集 fail-closed:openrouter/cohere/ollama/ollama_native/dify/replicate。Scaffold 双 fail-closed:mistral/groqcloud/together/perplexity/fireworks。Vertex 物化 fail-closed:vertex_gemini/vertex_anthropic 的 legacy/raw SA/metadata-only。Ollama 无鉴权模式 v2 无法表达。Dify workflow/completion 拒、仅 chatflow/agent。未定价模型 reserve 前 503(正确)。

## 推荐优先级(codex)
1. ✅S0 Azure 隔离(已修)→ 2. 全族写入期+发网前兼容校验(G1)→ 3. 结算三类终局(B0)→ 4. Vertex SA 铸 token 接线(M1/R1C-Vertex)→ 5. released 族补 handler/DB 或降级(A1)→ 6. vertex_anthropic 缓存计费(B2)→ 7. 流式权威 usage(B1)→ 8. 逐族 v2 E2E(T1)。

## Owner 决策点
B0(动钱)、A1(发布态翻转)、P1(定价发布 gate)、Kimi/Azure 新 adapter 均触 money/schema/新依赖,属 Owner-gated。是否将全部 S0/S1 作为 module ② 闭合前硬阻断项,请 Owner 定优先级与排期。
