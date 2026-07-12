# 官 key 厂商扩容:Grok + 国内大厂 api_key 接入(存储约束放行)

- 日期:2026-07-02
- 作者:Claude(主线切片 A,Owner /loop 指派「国内大厂接入…要不要现在接 Grok」+ 已给真 xai-/deepseek key)
- 分支:`feat/officialkey-vendors`(基于 origin/feat/fe-wire-users-mod @ 8cd574ba)

## 背景与现状(真码核实)

- 三个出站协议族早已注册齐(`internal/provider/registrydefault/default.go`):grok_chat / deepseek_chat / kimi_chat / qwen_chat / glm_chat / yi_chat / baichuan_chat / doubao_chat / minimax_chat / ernie_chat / hunyuan_chat / step_chat,全部 OpenAI 兼容 Bearer 端点(文心=千帆 v2、混元=腾讯 OpenAI 兼容端点,均无需旧版签名)。
- 采集/录入侧的架构**早已为本切片预留**:`credentialacq.hiddenOpenAICompatiblePlan`(types.go:271)把 grok/deepseek 等 api_key 粘贴计划藏在 `account_modes.openai_compatible` 旗后,RiskReason 原话「account credential storage constraints are not released for this provider」;catalog 测试(adminhttp/catalog_test.go:207-210)断言「storage constraints unreleased 时默认目录不可见」。
- **存储约束 = 迁移 0143 的两个 vendor-mode CHECK 只放行 anthropic/openai/gemini**。潜伏后果:代码里已有 handlerSpec+ModePlan+exchanger 的 copilot/copilot_oauth、antigravity/oauth、windsurf/oauth、grok/xai_oauth、kimi/kimi_oauth 在真库下采集/落库必违反 CHECK(dead-on-arrival,S2 级潜伏)。

## 范围(一个切片)

1. `internal/credentialstore/types.go`:+9 个国内厂 vendor 常量(qwen/glm/yi/baichuan/doubao/minimax/ernie/hunyuan/step,命名与协议族前缀对齐)+12 条 api_key handlerSpec(grok/deepseek/kimi + 9 国内厂;RuntimeAPIKey,required=["api_key"])。
2. 迁移 `0169_officialkey_vendor_modes`:两个 CHECK 纯加性扩展(镜像 0143 形状)——新增 12 个 vendor×api_key;顺带把已有 handlerSpec 的 5 个 OAuth 组合补进白名单(治愈上述潜伏 S2)。down 恢复 0143 形状。
3. `internal/credentialacq/types.go` DefaultModePlans:grok/deepseek 从 hidden 提升为 `apiKeyPlan`(Owner 已给真 key 明令接入);+10 条 `apiKeyPlan`(kimi+9 国内厂)。**openrouter/mistral/groqcloud/together/perplexity/fireworks 保持 hidden 且不加 handlerSpec、不进 CHECK**(Owner:全球推理托管云不接;存储层继续拒绝=纵深防御)。
4. `internal/adminhttp/catalog.go` RequestedChannelDispositions:grok/deepseek + 新厂商改/增为 enabled 审定记录;catalog_test 同步(可见性断言=判别性测试)。
5. `docs/openapi/openapi.yaml`:CredentialAcquisitionStart / CredentialAcquisitionHelperImport 两处 vendor enum 扩展(model-sync 处不动,那是模型同步能力边界)。
6. `frontend/src/features/accounts/credentials.ts`:VENDOR_OPTIONS +9 国内厂(grok/deepseek/kimi 已在)。
7. 测试:credentialstore 单测(12 家 handler 行为+未接厂商负断言)+ integration_pg 约束定义测试(镜像 0143 的判别性范本)+ catalog 可见性断言更新。

## 成功标准

- `go build ./... && go vet ./...` 绿;全量 `go test ./... -count=1` 绿;quality-gate(含 codebudget)绿。
- §14 变异:①删任一新 handlerSpec → 单测红;②回退 0169(在 0143 库上跑集成测试)→ 集成测试红;③把 grok 计划改回 hidden → catalog 可见性断言红。
- E2E(合并后):真 xai- key 经 admin API 建 provider(grok_chat)+channel+账号(vendor=grok/auth_mode=api_key 加密 v2)→ relay 转发 grok 最便宜模型 → 计费正确。

## 影响面/风险

- 纯加性:存量 3 家 vendor 的 CHECK 分支逐字不变;未列组合仍被拒(白名单语义保持)。
- **迁移 gate(Owner-gated 项)**:本迁移是 Owner 指派功能的必要组成(不放行则官 key 建号必违反 CHECK),纯加性、无数据回填、可干净 down;随切片报告向 Owner surface。
- 不碰碰撞区(pool/registry/proxy/channel/gateway*/tlsfp*);credentialstore/credentialacq/adminhttp 均可编辑。

## 镜像对照(§16)

三镜此前已在凭证域做过 9-agent 两镜调研(见 docs/process/plans/2026-07-01-role-based-auth-migration-claude.md 凭证段):new-api 渠道=类型+base_url+key(明文列存);sub2api=账号池+静态密钥;CLIProxyAPI=配置文件式 provider key。HUAKAI delta(三维):架构=加密 v2 credential store(AES-256-GCM+KEK)替代明文列;生态=vendor×auth_mode DB CHECK 白名单 + 审定目录(catalog disposition)分级放行;算法=无(纯数据接入)。本切片不引入新对照面(接入形状=已有 anthropic/api_key 同构复制)。
