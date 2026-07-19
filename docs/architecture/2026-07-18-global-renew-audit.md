# HUAKAI 全局 Renew 审查报告 — 问题清单(2026-07-18)

> **基线** HEAD `68ae7aa7` · 生成 `2026-07-18T13:05Z` · 分支 `fix/backend-closure-mvp`
> **方法** 两轮共约 20 个独立 finder 角度并行真读源码,关键结论逐条 Claude 亲读亲验或多 finder 交叉印证。**只读未改任何生产码。**
> **范围** 后端 218 包 / 1257 非测试 Go 文件 + docs 1282 篇 + 核心 relay/调度/计费/auth 全链 + 对标 sub2api@`bc2244c8`。
> **本报告只记缺点 / 问题 / 差距,不列优点。** 已达成熟、做得对的部分一律不写(Owner 指令:只要缺点)。

---

## 0. 一句话

**架构根因 = "账号可调度真相散在 5+ 处无桥接",派生一批调度核心漏洞(含两处"功能建了但生产恒不生效")。另有 3 个动潜洞(DLQ poison 无界冻结娱额=最高危 / Replicate 双抠 / opt-in 退宽超额)+ 1 个 auth 活体洞(2FA 被非密码登录绕过)+ 多实例上线硬阻塞(限流全进程内内存、探测无 leader 锁、无就绪探针)。** 计非/对账、凭据轮换、单实例限流等本身已成熟(本报告按 Owner 指令不展开优点)。

---

## 1. 🔴 S1 — 必修(动潜 / 安全活体)

### S1-0 DLQ poison 无界冻结用户娱额(无解冻原语)〔动潜,最高危,新发现 Claude 亲验〕
`dlq/service.go:176-198` + `billing/settler.go:365`/`balance_holds.sql.go:206`。
`post_delivery_settlement` 事件在 DLQ 里走 `NextFailureContinuous` **永续重试**:即便到 MaxAttempts/超 DLQAfter/`ErrUnretryable`,也只打一条告警、照样 `return StatusPending`,**永不进 quarantine 终态**。而冻结守卫全用 `status <> 'delivered'`(pending 命中)→ 真 poison 场景(如 `settler.go:132` claim.provider_account_id 为 NULL 每次 settle 硬失败)下:balance hold 永久停 `held`、`user_balances.held` 永不回退、LeaseSweeper 永久排除该 claim、abort 被永久拒绝。**用户预扣的娱额被无界冻结**,且全仓无任何 admin force-release/force-abort 原语,唯一出路是运营手工改 DB 根因 + replay。瞬时故障能自愈(设计对),但真 poison 无收敛。
**修向**:post_delivery_settlement 也要有终态(超阈值转 quarantine/operator_review)+ 补一个 admin 强制解冻/中止 hold 原语。

### S1-1 Replicate 图像换号双抠费〔动潜,合并提交 5bad429c〕
`imageshttp/handler.go:261` + `bindingfallback/sequence.go:118`(Claude 亲验)。
守卫 `familyRetrySafe`(已建付费 prediction 且取消未确认→不安全)只把 `RetryPermitted` 置 false,但 Coordinator 继续条件是 `RetryPermitted || (degradable && TargetConfigured)`——**第二条路径不看 RetryPermitted**;`ActionContinuePrimary` 分支只查 RetryBudget、不复检 familyRetrySafe → 直接换号发网建第二个付费 prediction = **同一请求重复扣费**。仅当该 binding 给对应 class 配了 fallback 相位时可达(生产常见)。⚠ 对应变异测试若在无 FallbackPhase 下跑会假绿。
**修向**:ContinuePrimary 分支对 image lane 复检 familyRetrySafe;或 familyRetrySafe 不安全时把信号一并降为 terminal。

### S1-2 2FA 被所有非密码登录路径绕过〔auth-core 活体,根因C〕
`session_handler.go:352`(社交 OAuth)/ `auth_handler.go:686`(Telegram)/ `passkeyhttp/handler.go:221`(Passkey)。
TOTP 闸(`authTwoFactorRequired`)只挂密码登录,社交/OAuth/Telegram 登录成功后直接 `Sessions.Create`、不跑 2FA。**活体触发**:平台开 2FA + 用户已注册 TOTP + 已绑社交身份 → 攻击者控制其社交账号即可绕过 TOTP 拿完整 session。
**修向**:把"签发 session 前的 2FA 闸"提炼成所有 `Sessions.Create` 共用的统一门(与 `EnsureLoginEligible` 同构)。auth-core,Owner-gated。

---

## 2. 🟠 调度核心逻辑漏洞(这轮最硬,多为"建了但生产恒不生效")

### S2-1 priority_weighted 加权选号生产恒失效
`pool/router/default_selector.go:330`(+ `routing_policy_source.go:86`)。
权重分支只在 `k>1` 触发,而生产从不设 BroadTopK → `topK()` 走 exact-tie 分支,要求 **Priority + LoadRate + LastUsedAt(微秒时间戳)三者全等**才同带 → 活体池几乎不可能 → k 恒=1 → 权重被静默忽略。运营配 weight 10:1 引流完全不生效。单测用人造三元组全等 fixture 掩盖(§14 非判别性)。

### S2-2 ramp 低流量永久卡 1%〔根因B⑤"半修"实为不可达〕
`channelhealth/service.go:225`(+ RampStageMinSamples=3 / MinObservation=1min)。
爬出 ramping 1% 需 60s 滚动窗内 ≥3 个已放行样本,但 1% 放行下 offered load 低于 ~300 req/min 的账号**永远攒不够** → 冷却一次即近乎报废;整池限流后大量账号集体卡 1%、总容量塌方无告警。合成探测每分钟至多补 1 样本仍 <3。**驱动补了也没用,样本门本身在低流量下不可达。**

### S2-3 冷却到期瞬间 thundering-herd〔可用性 + DB〕
`channelhealth/failover.go:113`(选号热路径内 Serializable 写事务)。
冷却到期瞬间高并发请求各自 `Allow()` 触发 ramp-start 写事务 → 40001 冲突 → 除首个外全部拿 err → filter 把带 err 的候选**误判不合格剔除**,甚至 eligible 为空返回 503,同时打 DB 事务风暴。
**修向**:ramp-start 从选号 Allow(读路径)挪到后台 worker,Allow 只读不写。

### S2-4 degraded 软熔断在默认选号器形同虚设
`default_selector.go:269` + `DegradedPenalty=2.0` 只在 PASRSelector 用。生产默认 DefaultSelector 的 rankFresh **不感知 degraded**,PoolGate 对 StateDegraded 照常放行 → degraded 账号与健康账号同权、LoadRate 低还可能优先 → 持续灌流量加速其跌入 cooling_down,失去"先减载再冷却"缓冲。

### S2-5 class 转移后目标 attempt 不带 ExcludedAccounts
`chat_completions_queue_wait.go:258`。normal 阶段已失败并入 failedAccounts 的账号,class 转移进目标池后 exclusion 被清空 → 可被再次选中,重复打刚失败的坏号、浪费 attempt 预算。

### S2-6 RampAdmissionKey 含 AttemptSeq → ramp 准入随重试漂移〔较小〕
`failover.go:158`。同 session 对同一 ramping 账号的准入在不同 attempt 间成独立抽签(放行↔拒绝翻转),削弱按 session 稳定放量 + 缓存亲和。

---

## 3. 🟠 账号"可调度真相"散列 — 架构级根因(sub2 1:1 对照)

**这是全部调度问题的总根,也是"比 sub2 不够 1:1"的核心。** 五次独立 finder 交叉印证。

sub2:单一账号实体 + 一个域级"可调度"判定(`sub2api@bc2244c8:internal/service/account.go:155-176`),选号/admin/上游错误写回/调度快照全收敛到唯一真相列,永不发散。

HUAKAI:无单一真相源、无物化快照、无 outbox 重建;真相散在 **5+ 处互不同步**:

| 真相 | 存储 | 谁写 | 谁读 |
|---|---|---|---|
| 账号级健康/冷却 | `provider_accounts.health_state` | credentialworker / 选号 SQL 惰性转正 | 选号门链 + 所有诊断视图 |
| 渠道健康 FSM | `channel_health_state` 表 | 运行时 429/5xx | **生产选号 PoolGate** |
| 凭据有效性 | `account_credentials.state` | 凭据刷新失败 | 选号 EXISTS 门 |
| 冻死镜像列 | `provider_accounts.credential_state` | **无人写(恒 valid)** | 选号旧过滤 + admin error 桶 |
| auth 降级 | 进程内 authcooldown | auth 车道 | 独立车道 |

**最大结构风险**:`channel_health_state`(成熟 FSM,运行时冷却写这里)与 `provider_accounts.health_state`(选号器唯一账号级读源)**没有回写桥接**——运行时账号级冷却写进了选号器根本不看的表。派生缺陷:

- **S3-1 健康诊断视图与选号发散**(`accounthealthview/view.go:100/126`):诊断读冻死列 `credential_state` 且把 `channel_health_state` 当 blocking,而选号器两者都不读 → **同一账号选号侧与诊断侧结论相反**,运营排障被误导;视图还自列 9 门未评估。
- **S3-2 EXISTS 凭据门漏 legacy 内联账号**(`pool_accounts.sql`,3df869b2,Claude 亲验):vault 有双服务路径(无 v2 行时回落读 `provider_accounts.credentials` 内联发号),而门只认 v2 行 → admin 不填 vendor/auth_mode 建的内联号被静默剔除、永不派发、无错误日志。
- **S3-3 死列 + 恢复原语分半**:`overload_until`/`temp_unschedulable_until` **无生产写者**(`MarkAccountTempUnschedulable` 零调用者,Claude 亲验)→ admin 过滤桶恒空、"active" 桶把已冷却号也算 active、容量误判偏乐观;恢复散在 **7 处写点各清一半**,**无 admin 口把 health_state='revoked' 清回 healthy** → 被 disable 的号点"清限流"无效。
- **S3-4 裸 401 靠文案关键词漏判成软冷却**(`channelhealth/signal_classifier.go:48`,Claude 亲验):只有命中 `token_revoked/account_suspended` 关键词才禁号;裸 `401 Unauthorized` 判 SignalChannelError → cooling_down → ramping 放回 → 再 401 → **无限 churn** 浪费额度。

> **根治 = 根因B切片5**:单一派生可调度视图 + 单一恢复原语 + `channel_health_state`↔`provider_accounts.health_state` 桥接对账。独立五次印证方向正确。

---

## 4. 🟠 其他 S2

- **S2-7 DLQ 兜底桶被静默丢弃**(`cmd/gateway/lifecycle.go:456`,db2727dc,Claude 亲验):`EventKindMetrics` 在 `eventbus/bus.go:294/369-370` **兼作所有未分类 handler 失败的兜底桶**,被注册成"确认即丢弃"后,未来漏登记 DLQKind 的 handler 失败会静默丢弃而非隔离留痕。修向:丢弃 handler 只绑专用时效性 kind,兜底桶仍走隔离。
- **S2-8 音频 TTS 部分交付全额退款**(`audiohttp/attempt.go:185`,待 Owner 定):speech 流式中途断连全额退款,但上游按字符已计费、音频已生成 → 与 completions 流式"部分交付照计费"不一致 → 平台单边吃成本。
- **S2-9 配额 reserve fail-open**(`chat_completions_dispatch.go:355`):配额存储抖动/宕机时超硬额度租户仍被服务。建议硬额度租户改 fail-closed。
- **S2-10 记账崩溃窗口 under-计非**(计非链):进程在"响应已发"与"结算提交"之间硬崩溃则无 DLQ 行,靠 30min lease 兜底成零成本 → 已交付 token 不计非(掉运营收入、不掉用户潜)。缓解:reserve 时预写可恢复结算意图(settlement-intent 骨架已有但默认关)。
- **S2-11 opt-in 退宽超额入账**(`audit/refund_worker.go:340` + `settler.go:729`,新发现 Claude 亲验):退宽全链无 `balance_holds.captured` 门,`RefundInTx` 只校验 `status=='committed'` + 按 `actual_cost` 封顶,**从不验证该 claim 曾真扣过用户娱额**。窄面触发:opt-in 租户 + 用户 reserve 时无 `user_balances` 行(Capture 空转未抠费)→ 用户后充值建行 → 旧签名 receipt 因定价快照变动重派生为 over_charge → 退宽把从未支付的额度 credit 进新娱额 = 净发潜。修向:RefundInTx 增 captured 门,按 captured 额封顶。

---

## 5. 🟡 S3 — 卫生 / 健壮性

- aux 裸 streamErr 分不清上游失败 vs 客户端断连(`completionshttp/attempt.go:194`)→ 客户端断连被当渠道故障喂 channelhealth、误冷却健康号(可能与 chat 主路共病)。
- `buildGatewayRuntime` ~970 行巨函数、27 个 worker 启动内联散落(违反 §13);停机两路径(`close()` vs `shutdownGateway()`)worker 集合不对称,靠共享 workerCtx cancel 保底。
- 恢复 SQL 五轴重复(recoverProviderAccountState vs ClearRateLimit)、worker 生命周期复制(SchedulerOutboxJanitor vs ReplayJanitor)、两恢复 handler 复制粘贴。
- healthscore(0-100 分)算了却只喂性能面板展示,**未接入调度/告警**。
- OAuth 通用 provider(OIDC/LinuxDo/NodeSeek/Discord)`email_verified` 完全信任上游 → 被攻陷/误配的 provider 可对不拥有的邮箱返 verified=true 触发按邮箱自动 link 到既有账号(潜伏,依赖运营配置的信任边界)。

---

## 6. 对标 sub2 成熟度 — 只列差的维度

**账号真相/调度(§3 根因):**
| 维度 | 差距 |
|---|---|
| 账号可调度**单一真相源** | ✗ 散 5+ 处无桥接(核心治本工程) |
| 派生物化可调度快照 + outbox 重建 | ✗ 每请求 live query,无快照 |
| 单一恢复原语 | ✗ 7 处各清一半 |
| 上游错误→禁号→换号 一体 | ✗ 写进选号器不看的表(桥接缺失) |
| 健康评分驱动调度 | ✗ 只展示不驱动 |

**多实例/上线运维(新发现,多实例横向扩展的硬阻塞):**
| 维度 | 差距 |
|---|---|
| **入站限流/登录节流跨实例一致** | ✗ 全进程内内存桶(`rate_limit.go:83`/`loginthrottle/limiter.go:91`,源码注释自认"多副本需集中式 Redis")→ **N 个副本 = N 倍有效放行量**,公网多实例上线即防滥用被稀释 N 倍。sub2 以 Redis 为协调基座。 |
| **探测型周期任务 leader 锁** | ✗ 全仓仅 alerting 一处有 leader 锁;proxyhealth/quotaprobe/tlsfphealth/windowcost 每副本各起 ticker → 多实例 **N 倍探测上游/代理**(quotaprobe 直打上游配额端点,N 倍极易被判异常封号)。修向:把 alerting 的 PG advisory leader lock 抽成通用组件包住所有探测/聚合 worker。 |
| **readiness/readyz 就绪探针** | ✗ 只有进程级 `/healthz`(刻意不碰 DB),带依赖的 system/health 被 admin 鉴权锁住 → 滚动发布/LB 无法探"是否就绪可收流量",DB 未连通或优雅停机时仍被打流量→发布窗口 5xx。修向:加无鉴权 `/readyz` 聚合 DBPing,停机前先翻 readiness=false 再 drain。 |
| **主动模型级巡检** | ⚠ channelprobe 的 ramp 推进已接线(21530932),但**主动模型可用性预探测(合成探测)未接线**(即根因B⑥,我已知延后)→ 冷门/新账号在首个真实请求前不知已失效、首个用户成探雷者。 |
| 账号签到/保活自动化 | ✗ checkin 只有 HTTP 手动入口、无调度 worker → 需保活的号靠手点/外部 cron,漏点静默掉池无告警。 |
| 日志查询面/指标切片自助面 | ✗ 无(logsink 已入库但无查询 HTTP 面)→ 线上排障只能 SSH 捞 JSON,无法交付非工程运营。 |
| 定时运营报表 digest | ✗ 无 |
| 上游用量回填校正记账 | ✗ 有探针但只喂限流观测,不纠账 |
| 迁移回滚 runbook | ⚠ 机制有(185 个 down)、文档缺 |

---

## 7. 三镜功能缺失(专项深审已完成 · 只列真缺口)

三镜(sub2api@`bc2244c8` / new-api@`a63364d1` / CLIProxyAPI@`106270be`)真码交叉审;HUAKAI 覆盖极全(近 300 包),已排除 Owner 缓做与形态不同等价物,**真缺口(≥2 家镜有)只三项**:

- **F-1 客户端每模型限速/限额〔最关键,自评矩阵 F-SEC-004 已标 Open〕**:`quota/types.go` 作用域只 global/user/apikey/poolgroup,**无 model 维度**;现有 per-model 限速只在上游账号侧(反应式响应上游 429),非面向买家。→ 运营无法对单个用户/Key 在某高价模型(opus/gpt-4)单独限速限额,买家一把 key 猛刷最贵模型成本瞬间打爆而其他维度未触发,也无法按套餐差异化。对照 new-api `middleware/model-rate-limit.go:78`(总数+成功数双阈值)、sub2api `service/model_rate_limit.go:21`(按模型 5h/1d/7d 窗口)。**≥2 家都有,对"卖 API 额度"的成本与滥用防护最关键。**
- **F-2 可配上游错误透传/状态码映射规则**:HUAKAI 只有硬编码固定错误目录(`clienterr/catalog.go`),不可后台配置"按上游错误码+关键词→透传原始提示/自定义状态码消息/是否计入渠道健康"。→ 上游特殊业务错误(区域限制/内容策略)时运营无法把有用提示透传给终端用户或改写,买家只见笼统固定消息、无法自助排障。对照 sub2api `error_passthrough_rule` + new-api `channel.status_code_mapping`。≥2 家都有。
- **F-3 上游新模型/缺失模型发现**:`modelsync` 只同步既有模型的定价/别名,无"渠道引用了但本地未登记的模型"清单、无上游新增模型检测提示。→ 上游上新或渠道配了未登记模型时运营无从发现,新模型无法及时上架售卖漏收入。对照 new-api `controller/missing_models.go`、CLIProxyAPI `registry/model_updater.go:23`。≥2 家都有。

低优参考(仅 1 家有,不计强缺口):在线调试台 Playground(仅 new-api `controller/playground.go`)。

**已知故意缓做(Owner 拍板,非缺口)**:真支付 canonical webhook、多级分销树、违规罚款扣费、反封禁 L1-L6 指纹栈、系统自更新、Rust 出口四家上生产。

---

## 8. 清理项(死代码 / 死开关 / 过期文档 / 文档矛盾)

> 纪律:带 legacy/deprecated 字样的**绝大多数是活兼容 shim,误删打穿钱路/鉴权**;"schema 已存在"≠"功能已生效"。

- **可自主删的死代码**(约 5-8 处 ~80 行 + 6 处测试 helper):`audit/refund_worker.go` 旧 sequence 版收据查找簇(被 idempotency 版取代)、`proto/passthrough.go:173 attachPassthroughToEvents`(零调用)、`credentialworker/health_state.go:88` 未用 type/方法、`credentialworker/options.go:38 withRotationClassifier`、若干测试 helper。
- **死开关**(均 Owner-gated 待激活、非删除对象,清单见 `deprecated-schema.md`):`routes` 表 26 列 / `mimicry_policy` / `oidc_provider_configs` / `protocol_capability_matrix` / `provider_accounts.cap_quota_*` / `moderation_config.violation_fee_usd` / `tier_progress`。⚠ 纠错:P2-c 计划称 `quota_policies.burst_value` 死开关,真读证伪(`quota/service_assess.go:96` 已消费),该计划结论需更正。
- **现行合同已收敛**:出口领域只维护 `egress-tls-mimicry-SSOT.md` 与当前唯一执行计划；旧计划只作历史证据，不再参与运行判断或复制出新的状态文档。
- **文档矛盾已修**:`egress-tls-mimicry-SSOT.md`、README、功能树与生成模块目录均以 Rust/BoringSSL sidecar 为唯一 mimicry 出口；Go uTLS/native fallback 不再被描述为活路径。
- 英文注释:§7 已彻底落实,真散文违规 0 处(此项无缺,仅备注免得再查)。

---

## 9. 修复路线图(按性价比)

1. **立即修 S1 动潜/安全**:DLQ poison 冻结娱额 + 补 admin 解冻原语(S1-0,最高危)、Replicate 双抠(S1-1)、2FA 统一门(S1-2,Owner-gated)、opt-in 退宽 captured 门(S2-11)。
2. **修调度硬伤**(生产恒不生效那两条最优先):priority_weighted 同带划分(S2-1)、ramp 样本门解耦低流量(S2-2)、ramp-start 挪出读路径(S2-3)、degraded 降权接入默认选号器(S2-4)。
3. **多实例上线阻塞**(公网横向扩展前必做):限流/登录节流下沉集中式(Redis/DB 分布式桶)、探测型 worker 统一 leader 锁、加 `/readyz` 就绪探针。
4. **修本 session 引入的连带回归**:健康视图读真相列(S3-1)、EXISTS 门补 legacy 内联(S3-2)、DLQ 兜底不丢弃(S2-7)。
5. **治本工程(核心)= 根因B切片5**:单一派生可调度视图 + 单一恢复原语 + 两套健康真相桥接对账。单独出详细切片计划。
6. **三镜真缺口**:客户端每模型限速限额(F-1,最关键)、可配错误透传(F-2)、缺失模型发现(F-3)。
7. **Owner 拍板项**:音频退宽口径、配额 fail-open、结算意图默认开。
8. **卫生批**:死代码清理、现行 SSOT 与模块目录同步、迁移回滚 runbook；不再新增平行状态文档。

---

## 附:验证纪律
全部结论真读源码得出,grep 只用于定位;矛盾结论由 Claude 亲读裁定,对抗验证多次奏效(证伪 revoked 复活、纠正"设计如此"误判、纠正 burst_value 死开关旧结论)。本报告发现均只读得出、未改任何源码。
