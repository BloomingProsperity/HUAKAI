# 账号模块 · sub2api ↔ HUAKAI 全面差距分析

日期:2026-07-15。基准:sub2api `d515c304`(v0.1.156,已 checkout 最新)/ HUAKAI `feat/ui-density-overview`。
方法:六路并行 Explore,全部结论带 file:line(真码,非臆测)。覆盖账号模块六大面:①导入后 UI 显示+实时抓取 ②重置券+配额颗粒度 ③全生命周期(导入/CRUD/凭证)④池调度选号 ⑤状态机+429分类+冷却 ⑥用量统计+安全审计。

---

## 0. 一句话结论

**账号的"骨架"(选号调度、凭证安全、错误分类、审计、多租户)HUAKAI 是 sub2 的严格超集甚至碾压;差距集中在"上游配额的精细感知"与"运营操作便利性"两块**——前者影响调度正确性和省钱(该补),后者影响运营效率(按需补)。

---

## 1. HUAKAI 已经更强 / 独有(不用动,心里有底)

| 能力 | HUAKAI | sub2 |
|---|---|---|
| 凭证存储 | AES-256-GCM 信封加密 + KEK 轮换 + AAD 绑 vendor/auth_mode(credentialstore/crypto.go:100-131);secret 结构上不下发前端 | **明文 JSONB 裸存**(ent/schema/account.go:74-81);加密器只护 TOTP/备份 |
| 凭证轮换 | 一等 rotate 端点 + 版本化凭证 + 预轮换扫描(admin_credentials_handler.go:77;scheduler.go:194) | 无独立轮换,靠刷新滚动 |
| 凭证指纹 | refresh_token SHA 指纹永不明文(0141) | 无 |
| 操作审计 | Merkle 链+签名+DLQ 防篡改账本 + UI 可导签名证明(auditledger/;AuditPage.tsx) | **基本无**(仅设置项 slog) |
| 多租户隔离 | 后端强制 tenant + CanIssueForTenant,越权 403(provider_account_tenant_resolve.go:23) | **无租户**,仅分组 |
| 选号调度 | 加权水塘 + PASR 缓存感知 5 模式 + pin 强制 + 协议/能力/订阅多闸(default_selector.go) | 仅 Priority→LoadRate→LRU 分层 |
| 健康体系 | per-account 健康 FSM(degraded/ramping)+ 主动探针 + ban 24-72h + operator_ack(channelhealth/) | **无 per-account 健康分/探针**,纯布尔冷却门 |
| auth 冷却车道 | 独立车道 + iron-clad/ambiguous 分级防误封(authcooldown/store.go) | 无,401 无 refresh 直接永久禁 |
| 错误分类 | canonical 规则表 R-001~R-029 单表(error_normalize.go:242) | switch 散落各文件 |
| 并发一致性 | DB Serializable + 90s lease,与计费结算原子绑定(slot_manager.go) | Redis 有序集合,靠过期清理 |
| 跨账号用量排行 | leaderboard by provider_account(leaderboard_handler.go) | 无 |
| 导入方式广度 | CSV/JSON/paste 一等批量结构化;vendor 覆盖含 qwen/国内大厂;PKCE verifier 落 PG 加密 | session 仅内存明文;vendor 仅 5 家 |

---

## 2. 真实缺口 —— 按影响分级(建议补)

### P0 · 影响调度正确性与省钱(强烈建议补)

**G1. 已探测的配额窗口"存了却不用于选号"(我们自己的隐性缺陷)**
- 现状:HUAKAI quotaprobe 探到 Anthropic 5h/7d utilization,写进 provider account 列,但**只被 admin 健康视图读**;选号 headroom 只用并发 LoadRate(pool/router/blend.go:11)。
- 后果:一个 7d 窗口已 95% 的账号,只要并发低照样被优先选中,**直到撞 429 才冷却**。sub2 用 `openAIQuotaHeadroomFactor` 提前按剩余量降权(openai_account_scheduler.go:2147)。
- 补法:把 session window utilization 接进 PASR/headroom 打分。**这条是修我们自己的正确性漏洞,不是抄 sub2。**

**G2. OpenAI/Codex 5h/7d 配额窗口未纳入 HUAKAI 主动探测**
- 现状:quotaprobe/worker.go:160 只探 `anthropic + claude_ai_oauth`;OpenAI 账号无主动窗口探测,只能撞 429 被动回避。
- sub2:openai_quota_service.go 完整探 codex 5h/7d + 落 Extra + 进 headroom。

**G3. Grok 上游配额窗口头未解析**
- 现状:HUAKAI 全仓无 `x-ai` requests/tokens 配额头解析(QuotaSnapshot 零命中)。
- sub2:openai_gateway_grok.go:876 读 x-ai 配额窗口 Remaining==0 → 持久化限流。
- 后果:Grok 账号对 HUAKAI 无配额可见性,选号/冷却全靠 429 反应式。

**G4. Anthropic Fable 专属 7d_oi 模型级窗口未解析**
- 现状:window_reset_parser.go:11 只认 5h/7d 前缀,不认 `7d_oi`。
- sub2:selectAnthropicFableWindowLimit(ratelimit_service.go:1269)做模型级限流(账号对其它模型仍可用)。

**G5. 429 兜底冷却粒度:我们 5min,sub2 5s**
- channelhealth/types.go:156 DefaultRateLimitCooldown=5min;sub2 默认 5s(可配 1-7200s)。
- 后果:短时 429(尤其无 reset 头)下,5min 冷却容易把账号池"饿死",sub2 秒级回避更不易饿死。**建议把无 reset 头的临时 429 兜底改到秒级可配。**

### P1 · 运营手段/效率(按需补)

**G6. OpenAI 重置券(reset credits)全缺**
- sub2:专门 endpoint `/wham/rate-limit-reset-credits` 查可用张数+过期,`/consume` 手动消券立即重置账号 rate-limit 窗口(openai_quota_service.go:29,246);UI OpenAIQuotaResetCell.vue 显张数+过期+「Reset Quota」按钮。近两周新加(commit d4da138c/dfb36e45)。
- HUAKAI:检测/存储/消费/UI **全无**(grep reset_credit/wham 零命中)。
- 价值:运营能在账号 429 后手动消一张券**立即恢复调度**,我们现在只能干等冷却到期。**注:sub2 的券也不进自动调度,是纯人工恢复手段。**

**G7. 账号批量操作 + 立即刷新 全缺**
- sub2:BatchCreate/BulkUpdate/BatchRefresh/BatchClearError/BatchUpdateCredentials/BatchRefreshTier + 分组批量指派 + 单/批「立即刷新 token」端点(admin.go:327-334)。
- HUAKAI:单账号 CRUD+启停+清限流齐,但**无任何批量操作、无立即刷新按钮、无批量改组**。规模上量后运营很痛。

**G8. 账号克隆 / 数据导出导入 缺**
- sub2:Duplicate 克隆(暂停态,丢轮换凭证)+ ExportData/ImportData(近似迁移/备份)。
- HUAKAI:均无。

### P2 · 观测/展示(锦上添花)

**G9. 单账号 30 天富统计弹窗缺**
- sub2:AccountStatsModal.vue 趋势折线+模型分布+端点分布+四卡+双口径计费(账号计费 vs 用户计费)。
- HUAKAI:账号详情偏诊断+近期请求,无富图表 Modal(但有 sub2 没有的跨账号排行)。

**G10. per-account 计费倍率缺**
- sub2:account.rate_multiplier 每账号计费倍率 + 账号统计 4 级自定义定价归因(account_stats_pricing.go:19)。
- HUAKAI:无 per-account 倍率。**注:与主线定价改造(官方价×倍率,组/模型倍率)部分重叠,补前先核是否被覆盖。**

**G11. 导入旁路细项缺**
- sub2 独有:Claude Cookie/sessionKey 自动换授权码、Grok SSO→OAuth、OpenAI codex_pat & mobile_refresh_token、CRS 整库同步、codex 导入硬去重(update/skip 合并)。
- HUAKAI:anthropic 仅 PKCE + CLI 导入;去重是"软"(非唯一索引+名字唯一+采集幂等),无同上游账号硬合并。

---

## 3. 账号导入后 UI 显示什么 / 实时抓什么(回答 Owner 原问)

**sub2 列表页显示字段**(AccountsView.vue:1265):名称+**邮箱(明文全显不脱敏)**、ID、平台+类型徽章(含 Pro/Plus/Free 计划、隐私模式、订阅到期、Antigravity tier)、容量(并发)、状态、可调度开关、今日统计(请求/token/费用)、分组、**用量窗口(核心额度进度条)**、代理(名+国家+到期)、优先级、调度分、计费倍率、最近使用、创建/过期时间、备注。

**实时抓取**:
- 导入时:各平台 OAuth 换 token 后解析 id_token + 调上游 profile 接口抓 email/plan_type/订阅到期/org/project_id(逐平台函数见完整报告)。
- 定时:token 刷新服务每 5min 检查、到期前 30min 刷新,顺带更新 email/plan。
- 用量/配额:Anthropic 走**被动采样**(每次网关请求后从响应头 `anthropic-ratelimit-unified-*` 抽 utilization/reset 写 Extra),OpenAI 走 `/responses` 探针读头(TTL 10min),Grok/Gemini/Antigravity 各有 fetcher+probe。UI 用量列缓存 5min,列表自动刷新可选 5/10/15/30s。

**HUAKAI 对照**:我们有 quotaprobe 主动探(仅 Anthropic)+ 上游头写会话窗口,但**探到的窗口没进选号打分**(见 G1),且**只探 Anthropic**(见 G2)。UI 侧我们账号详情偏诊断态,没有 sub2 那种一屏配额进度条矩阵。

---

## 4. 建议动作(供拍板)

- **P0(G1-G5)**:直接影响调度正确性和账号池利用率,尤其 G1 是我们自己的漏洞。建议派 codex 逐条补(配额窗口进选号打分 + OpenAI/Grok 配额探测 + 7d_oi + 秒级兜底冷却)。触调度核心,判别变异测试必须严。
- **P1(G6-G8)**:上量运营刚需。重置券(G6)是独立能力可单独做;批量操作(G7)工作量集中在 handler+前端。
- **P2(G9-G11)**:UI 重做阶段一并做(单账号统计弹窗归前端功能树);G10 计费倍率与主线定价改造合并评估。

> 全部为事实对照 + 影响判断,未擅自开工。P0 因属正确性修复,建议优先。
