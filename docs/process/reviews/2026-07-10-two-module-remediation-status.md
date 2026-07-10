# 两模块闭环 remediation 状态说明(账号转API relay + 官方API)— 2026-07-10

面向 Owner 的闭环状态汇总:哪些已闭合落地、哪些在 Owner 决策前沿、哪些需真账号。基线分支 feat/fe-wire-users-mod。

## 一、已闭合并落地(全部亲手 + 判别测试 + 变异证 + 独立提交推送)

### 模块① 账号转API relay
- **R0 薄能力闭合闸**(99dda494):servingcapability 跨层一致性关卡,治"采集面建好、serving 面未接"的构件完工幻觉。
- **R1A Claude OAuth/session 端到端 serving**(7936a71c):anthropic_claude_session 从 fail-closed 变可 serving(registry/六站/官方形态门/model remap/选号/计费/failover)。经 **codex 三轮对抗审**,累计 **12 个 S1 全修 + 变异证**:官方门重写为诚实启发式形态门(对照本机 Claude Code 2.1.199 反汇编 + 三镜)、model remap、FOR SHARE 协议锁、R0 语义诚实定界、锁序敏感确定性并发测试等。
- **G1 全族 vendor/auth 兼容校验**(a0ef533d):防特权误配把 A 厂 key 绑 B 厂 family。
- **credential 创建路径守卫**(289ef37c/2c7452ee):收敛 G1 account-first + R1A credential-写入绕过。

### 模块② 官方API(codex 全链审计 + 7 处修复)
- **S0 Azure 密钥外发**(0e706cfe):azure_api_key 原物化成 APIKey 被发往 api.openai.com=密钥外发第三方;已 fail-closed。
- **G1 跨厂错配**(a0ef533d,同上,跨两模块)。
- **B2 缓存少计费**(44c753c5):vertex_anthropic + anthropic_claude_session 补进缓存计费集合 + 防复发覆盖测试。
- **F2 Bedrock 错误标签**(df264642):错误分类用 bedrock 命中专用 429/503 限流/过载规则。
- **B1 流式权威 usage**(9bbbe2db):官方 OpenAI 兼容族流式注入 stream_options.include_usage。
- **M1 Vertex SA 接线**(4eddd814):raw SA 经刷新链铸 token 闭合 vertex_gemini/vertex_anthropic fail-closed 族(vertexsa 包端到端接线)。
- **credential 守卫**(同①)。

全量单测 + integration_pg + codebudget + gofmt 全绿。审计裁定/设计落盘:官方API审计(20cbc34d)、R1A对抗审(2026-07-10-R1A-*)、B0设计(e0f6c7e6)。

## 二、Owner 决策前沿(需 Owner 拍板,未擅自实现)

- **B0 结算失败四终局**(动钱,已落盘完整设计 2026-07-10-B0-*):非流式误扣未交付/流式已交付白吃/settle+DLQ双失败sweep非补偿/Replicate不释放。**缺口3正解=独立第二持久环,推翻 Owner D4(2026-05-24 只alert不disk spool)**;需 Owner 定:交付政策(完整业务体写成功才算交付)+ 第二环架构(外部队列 vs 本地WAL)+ 是否新增 delivery/settlement intent schema + 反转两个锁错终局测试。
- **A1 released 族无 handler**:openrouter/cohere/ollama/ollama_native/dify/replicate 标 Released 但无 credential handler(不可导入),mistral/groqcloud/together/perplexity/fireworks 是 Scaffold 双 fail-closed。需 Owner 定:补 handler+DB 组合 vs 降级 release 状态(发布态翻转)。
- **P1 定价发布期不 gate**:运行时查不到价 reserve 前 503(正确),但绑定创建不 probe 定价→可发布每次 reserve 503 的模型。需 Owner 定发布期定价预检。
- **F1 Kimi endpoint 分流**:kimi_chat 恒打 coding endpoint,但契约允许 api_key(普通 Moonshot);普通 Moonshot key 无可选 endpoint。修复需 §16 三镜确认 Moonshot vs coding 分流 + 确保 kimi_oauth 携带 coding endpoint(否则改默认会破坏现有 coding 账号)——需谨慎设计切片。
- **per-反转账号 ACL**:当前完整伪造 Claude Code 形态 + 同租户模型已授权的有效 key 会用池内反转账号(跨租户/禁用模型仍拦);是否需"哪个 key 用哪个具体反转账号"的细粒度 ACL 属产品/安全决策。
- **Azure 专属 adapter**:S0 后 azure api-key fail-closed;完整 Azure 支持(base_url/deployment/api-version + api-key 头)是新 adapter 切片。

## 三、需真账号(第③层真上游测验)
IDE 四账号(Cursor/Copilot/Windsurf/Kiro)+ ChatGPT 整链 E2E:真实反转账号打真上游拿真响应,需按安全约束全新 OAuth 采集,不可用假数据糊弄成"已测"。这是上线前最后一道真测。

## 四、上线就绪判断
核心 relay(账号池→key→网关转发→计费)+ 官方 API 直通的 **concrete/工程闭环已达成**(happy path + 已修的错配/密钥外发/计费correctness/失败释放)。**上线前应由 Owner 决策 B0(结算失败补偿,付费中转站必需)+ 完成第③层真上游 E2E。** A1/P1/F1/per-账号ACL/Azure-adapter 按 Owner 优先级排期,均不阻断核心闭环但影响完整族覆盖与运营健壮性。
