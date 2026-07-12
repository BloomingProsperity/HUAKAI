# 最终修复+建设路线图 —— Claude 平行草案(2026-07-10)

> 依 CLAUDE.md #10 独立起草(未看 codex 版)。综合本轮:6 账号验证 + Antigravity 多厂商破解 + 系统性普查双轨(codex 21 / workflow 28,26 双确认,S0=0,S1=3+)。
> 用影响图思维排序(别单点):先修上位根,再解阻,拟真共享层一次做,钱 Owner-gated。

## 一、总纲:这批问题是一个根的多处显形
两轨都命名了同一系统性模式 **「构件完工幻觉」——采集面建好、serving 面未接/占位/端点错,两半之间无一致性关卡**。本轮亲手又证一例:Antigravity adapter 端点写死 `api.antigravity.ai`(错,真端点 `daily-cloudcode-pa`)。Claude OAuth(adapter 未注册)、Vertex(铸造链缺失)、Copilot(exchanger 孤儿别名)都是同根。→ **修根 + 解阻高价值实例,不打地鼠。**

## 二、拓扑排序(动上游点先)

### Phase 0(上位根·先修)—— 跨层能力不变式 + 一致性门
- **S1-02**:控制面(catalog.go:115 / provider_catalog_mutation_handler)改为**同时查 serving StaticRegistry**,启动期校验链:`mode 可见 → exchanger 可完成 → 凭据可物化 → adapter 已注册且收该类型 → transport 可解析 → 有定价种子`。
- 加**判别测试**:`vendor 启用集 ⊆ 已注册 serving adapter ⊆ 定价种子集`,任一脱钩→红。
- **为什么先做**:它是 A 组 6 条 serving 断裂的共同上游;修了它,所有"采得到用不了"在**采集/配置期就暴露**,不再延后到请求期;且从此不再产新僵尸账号。**这是牵连图的根节点。**

### Phase 1(解阻·按账号价值排序,均依赖 Phase 0 的门)
1. **Antigravity 多厂商 adapter(最高 ROI,spec 已就绪)** — 一号顶三家(Gemini+Claude+GPT)。按 [2026-07-10-antigravity-multivendor-spec.md](2026-07-10-antigravity-multivendor-spec.md):daily-cloudcode-pa 端点 + OAuth(antigravity.google/oauth-callback+PKCE)+ body(enabledCreditTypes GOOGLE_ONE_AI)+ fetchAvailableModels 动态模型。**牵连**:新 protocol family + 多厂商模型路由(一个账号 fan-out 到 gemini/claude/gpt 三族 model)。
2. **Claude OAuth serving(头号 S1,核心 Claude 订阅)** — 注册 `OAuthSessionAdapter` + 六站对称接线(codex 已备计划 2026-07-10-claude-oauth-serving-mimicry-codex.md 的 S1 切片)。
3. **其余 serving 断裂**:Vertex SA 铸造链(private_key→JWT→token)、Copilot device-code 真 mode key、Kimi 设备码 form-urlencoded、refresh-only 首次换 token。

### Phase 2(拟真硬化·反检测·共享层一次做)
- shallow-mimicry 4 条(Codex 假 UA/无设备、Gemini UA 自曝 HUAKAI、Kimi 裸直通、Claude body 缺 system/billing)统一做一个**「账号→设备/UA/session profile」生成层**。**牵连(爆炸半径)**:该 profile 生成器/mimicry composer 跨 vendor 共享,改一处动全部反转 vendor 出站——**必须一次设计好,不能每 vendor 各补**(否则重复+不一致)。

### Phase 3(钱/配额一致性·Owner-gated)
- S1-14 quota reserve fail-open + 无 reservation 可修;S1-15 退款配额冲正被吞;S1-16 过期 token 仍 serving;S1-18 分组白名单报错放行;S2 租约无续租。**均触钱/entitlement → 逐条 Owner 签字**;S1-16↔S1-17↔credential 刷新是同一生命周期链,一起修。

### 横切(随手补,低风险快见效)
- 定价种子 ⊆ 启用 vendor(补 grok/kimi/step 定价,消 503);死开关清理(cooldown_429/529、max_parallel_requests、engine_fingerprint 接消费或下架);interplay 接缝跨模块判别测试 + 订正误导注释。

## 三、我建议的**立即第一刀**
**Phase 0 能力不变式**,理由:①它是根节点,先修则后续每个解阻都有启动门兜底、不会半接线;②它自带的一致性测试成为整类缺陷的**永久守门**(呼应死开关"删注入行→红"惯例);③工时小(改门+加测),风险低,不触钱。
**紧随 Phase 1.1 Antigravity**(spec 就绪、ROI 最高、纯新增不动存量核心)。

## 四、决策点(surface Owner)
1. 第一刀是 **Phase 0 能力门** 还是直接 **Phase 1.1 Antigravity**(ROI vs 根因先后)?
2. Antigravity 一个账号 fan-out 多厂商 → HUAKAI 计费怎么归属(按 Google 侧 quotaInfo 还是按我们自己 token 计)?**触钱,需拍板**。
3. Phase 3 钱一致性各条,逐条 Owner-gated 确认节奏。

## 五、工时粗估
Phase 0 ~1-2 人日;Antigravity adapter ~3-5 人日(多厂商路由+动态模型);Claude OAuth serving ~S1 切片 3-4 人日(六站);Vertex 铸造 ~3-4;拟真共享层 ~3-5;钱一致性逐条 Owner-gated。**写码全交本机 codex,我只规划/验收**(记忆硬规则)。
