# chatgpt/codex session 账号转 API 全链路 e2e + endpoint 可配 — Claude 切片计划(2026-07-05)

## 背景(亲核 file:line)
- gpt 转发链接线完整:registrydefault/default.go:231 `MustRegister(ProtocolOpenAICodex, &CodexSessionAdapter{})`,出站 chatgpt.com backend,凭据 session_token/upstream_passthrough。
- 组装层已有单测:internal/provider/openai/codex_session_test.go + ua_guard_test.go(Bearer 注入/OAI-Device-Id/OAI-Language/反封禁 UA guard)。
- **缺全链路 e2e**:cmd/gateway/upstream_e2e_test.go 只覆盖 api_key(豆包)+ upstream_passthrough(混元);chatgpt_oauth / codex_*_oauth 这条 OAuth 账号转 API 链无端到端。
- **endpoint 硬编码**:codex_session.go:25 defaultCodexEndpoint 固定 chatgpt.com;endpoint 只从 adapter.Endpoint(注册时空),运行时不可按账号覆盖(对比豆包/混元走 base_url 可配)。codex_session.go:注释自留「SUB2-02 接缝:未来换 admin 可调 platformsettings」。

## 方案
1. **endpoint 可配**:CodexSessionAdapter.BuildRequest 读 `in.Credential.Extra["base_url"]`(存在且非空则覆盖 defaultCodexEndpoint),与它已读的 Extra["user_agent"]/["oai_device_id"] 同模式。默认无 base_url = 走 chatgpt.com = **零行为变化**(非默认翻转,是新增 opt-in 覆盖点)。
   - **SSRF 守卫**:base_url 来自运营者账号配置(Credential.Extra,非客户请求可控),但仍加格式校验对齐今天 Bedrock F-1——必须 https、host 非空、拒绝含控制字符/内网元数据形态(loopback/169.254/metadata);校验失败 BuildRequest fail-closed 返错。**e2e 本地 httptest 是 127.0.0.1**,故校验要允许显式测试注入(用 dev-only 豁免或测试专用 base_url 前缀),生产拒内网——这个边界让 codex 亲核既有 SSRF 守卫(internal/gateway proxy host guard / bedrock validAWSRegion)后择一,报告说明。
2. **全链路本地 e2e**(cmd/gateway,build tag,复用 upstream_e2e 骨架):seed openai + chatgpt_oauth provider account(假 session_token + oai_device_id 入 account_credentials/Extra),Extra["base_url"] 指向 httptest 假 chatgpt backend → 起网关子进程 → 客户端用 HUAKAI key 发 /v1/chat/completions → 假上游**捕获并断言**收到 `Authorization: Bearer <session_token>` + `OAI-Device-Id` + Codex CLI 风格 UA(非浏览器)→ 返回 mock 响应 → 断言 PG:claim committed、actual_cost>0、quota settled>0/reserved=0、并发槽归零。

## 三重价值
- 补 OAuth 账号转 API 全链路测试(不依赖真账号,CI 可常跑);
- 解锁 endpoint 可配(运营者为账号配镜像/代理端点);
- 成为**真账号 live e2e 骨架**:endpoint 换真 chatgpt.com + token 换真 session_token 即 live 验证「真实 ChatGPT 订阅账号 → 转 API」闭环。

## 三镜对照(#15,codex 实现批亲读填充)
sub2api / CLIProxyAPI / new-api 各自怎么配 chatgpt/codex session 的上游 endpoint(是否 per-account 可覆盖 / 是否 base_url 化 / 反封禁头怎么带)——codex 亲读源码填 file:line。

## 爆炸半径 / 风险
- 触 relay 转发端点确定逻辑(codex adapter,热路径),但 opt-in override 默认零变化;
- SSRF:运营者配置非客户可控,但加格式校验 fail-closed 兜底误配;
- 决策点:Extra["base_url"] override = medium-risk 自主(对抗审查+变异兜底);真账号 live 测 = 等 Owner 提供账号形态。

## 测试(§14/§17)
- e2e:上述全链路,断言上游请求头精确(Bearer/OAI-Device-Id/非浏览器 UA);
- 单测:BuildRequest 读 Extra["base_url"] 覆盖 endpoint(有则覆盖/无则默认/非法 fail-closed);
- 变异:删 base_url 读取→e2e 打默认 chatgpt.com 连不上假上游→红;删 SSRF 校验→内网 base_url 未拒→红。

---

## 验收裁定 + 🔴 重大发现(2026-07-05,Claude PM 逐链亲核)

**交付验收**:endpoint override(Extra["base_url"])+ SSRF fail-closed 校验 + 组装单测 = 正确、质量高、门绿、变异有牙(放行所有 IP→metadata/私网/特殊用途 IP 单测全红)。SSRF 拒 metadata(169.254.169.254)/私网/link-local/特殊用途 IP/编码 host/数字混淆/非 ASCII,loopback 放行(本地 e2e 需要,且 metadata 才是高危)。三镜对照:CLIProxyAPI 是 per-account base_url(空回落默认)、new-api channel 有 base URL 字段,本改对齐它俩;sub2api OAuth 固定 backend 无 per-account。

**🔴 逐链追踪发现的核心 gap(比"缺 e2e"根本得多)**:codex/chatgpt session 账号(upstream family=openai_codex)在**生产默认配置下,没有任何客户端入口能路由到它**。证据链:
- `validateNativeRawBodyIngress`(upstream_dispatcher_hcsf.go:403)只放行 ingressFamily==endpointFamily(同族)或 anthropic_messages→bedrock_invoke,**其余跨协议 fail-closed**。
- `ingressFamily := string(env.RequestMeta.ClientProtocol)`(:124)。
- 所有客户端入口的 ClientProtocol(`ClientProtocolByIngressPath` client_adapter_default_registry.go:89)只产出 openai_chat / openai_responses / anthropic_messages / gemini——**没有 openai_codex**。`/backend-api/codex/responses` 也映射成 openai_responses(:93)。
- 故 openai_chat→openai_codex、openai_responses→openai_codex 全部 fail-closed。**codex 账号唯一走通方式 = dev-only `HUAKAI_DISPATCH_HCSF=0`** 关掉整个 HCSF 门强制 raw 直转(本 e2e 正是靠它)。

**结论**:codex/chatgpt session 账号的采集流、credential 物化、CodexSessionAdapter 组装(token+风控头)全部接线完整,但**请求分发层缺一个能路由到 openai_codex 的真实客户端入口**——这条核心链默认断在 dispatch 跨协议守卫。本 e2e 验证的是 adapter 组装+计费正确(靠 HCSF=0 强制路径),**不是生产默认可达路径**。

**接通方案(Owner-gated 产品决策,surface)**:①让 `/backend-api/codex/responses` 映射成 ClientProtocol=openai_codex(而非 openai_responses),使同族直通;或 ②在 validateNativeRawBodyIngress 白名单放行 openai_responses→openai_codex(二者请求侧都是 Responses 形,本兼容)。两者都改默认 dispatch 行为=Owner-gated。endpoint override 在接通前是半 inert(仅 HCSF=0/接通后被消费),但为接通前置件+对齐三镜,先落。
