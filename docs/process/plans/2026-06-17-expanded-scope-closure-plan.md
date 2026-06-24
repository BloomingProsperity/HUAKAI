# HUAKAI 扩展范围闭环计划 — 2026-06-17

**PM:** Claude (Opus 4.8)。**方法:** 6-agent 真码现状审计(workflow wd4e4f6ry)+ origin 校正,再合成分阶段计划。
**状态:** 计划待 Owner 审批;审批通过才逐波建造(rule #9 plan-before-execute)。每个功能动手前再给该功能小计划。

## 0. Owner 锁定的范围
- **做**:登录方式(OAuth/passkey/2FA)、订阅/推荐/兑换码(走 **admin 发放 + 兑换码** 到账,不接真支付)、Trust/Hermes/审计护城河、完整 admin 设置页、多 provider 家族、MVP 主线前端接线。
- **不做**:真实支付网关写入(#2,PSP webhook 保持隔离 + fail-closed)。
- **前端**:只接线测试、不做精美设计/设计系统统一(功能骨架优先,沿用已建的 proxies/pricing/moderation 页范式)。

## A. 现状总判(真码审计 + origin 校正)
**核心结论:6 块的后端几乎全部 done-active(真做好且已挂路由);要补的绝大多数是前端接线 + 极少数后端小项。**

| 块 | 后端 | 前端 | 真实剩余 |
|---|---|---|---|
| 登录方式(OAuth/passkey/2FA) | ✅ done-active(go-webauthn 真库 + RFC6238 TOTP + OAuth flow + 7 social + telegram;2FA 默认开,passkey/OAuth 运维开关默认关) | 🟡 mixed:安全页 2FA 全齐、passkey 仅看删、OAuth 仅看解绑;**登录入口侧全是"即将上线"桩** | 前端为主:2FA 验证码[S]、社交登录+回调页[M]、passkey 注册[M]、passkey 免密登录[M]、安全页发起绑定[M];待核:OAuth provider 是否支持 DB/UI 配置(现仅 env) |
| 订阅/推荐/兑换码(admin 发放+兑换码) | ✅ done-active(订阅生命周期 + 兑换码 balance/subscription 两路 + 推荐自动入账 + admin 手动发放 Serializable+幂等;PSP webhook 已隔离 fail-closed) | 🟡 mixed:用户页(兑换/订阅/充值/推荐)齐;admin 有发余额/建兑换码/分配订阅 | 前端为主:admin 订阅生命周期操作(改/退/续/换/批量)[M]、订阅券创建[S]、批量码导出[S];可选后端:人工补发推荐返利[S,Owner 定] |
| Trust / Hermes / 审计护城河 | ✅ done-active(Merkle hash-chain + ed25519 + append-only 表 + DB 触发器强制不可改,接入实时转发,production 强制 postgres;Hermes 引擎 + 每日巡检 worker,默认 dormant 需 runner env) | ✅ **done**(audit 验证页 372 行 + 4 组件;hermes admin 页 688 行 SSE) | **基本完工**,仅 S 级可选项(Hermes 默认开关属运维装配、dev 持久化、可选独立 verify 页) |
| 完整 admin 设置页 | ✅ done-active(provider/channel 目录、quota 策略、渠道测试模板、定价比例、路由 admin、model-sync、DLQ、L2 缓存、平台设置 per-key 全真码) | 🟡 mixed:origin 已有 11 页(含本会话 pricing/moderation/model-pool-binding) | 前端最大块:provider 目录[M]、channel 目录[M]、配额策略[M]、渠道测试模板[M]、model-sync+能力/别名[M]、audit-events/DLQ/cache 运维面[M]、provider-account/pool-group 纳入 admin 外壳[M]、平台设置补键[S]、**路由可视化向导[L]** |
| 多 provider 家族 | ✅ done-active(**37 个 protocol family 已可路由**:20+ OpenAI 兼容直通 + anthropic/gemini/bedrock/dify/ollama/vertex 专用;收敛 5 canonical 翻译形态;forwarder 真消费 + 对称守卫测试) | n/a(family 是后端概念) | 基本完工。加 OpenAI 兼容家族[S,4 处各 1 行];加全新 wire 形态[M,成熟流水线];session 反转家族[M,需 OCAW 真流量采集→roadmap];可选 family 选择器 UI + 列举端点[M] |
| MVP 主线前端(登录/Key/playground/用量) | ✅ done-active | ✅ **done**(2026-06-15 计划的"前端全缺"判断已过时:三页 + 客户端全建好接线,playground 真拉模型 + hk_key 输入 + 错误友好化) | model→pool binding 端点 **已补**(PR#2);剩:prod compose 加 migrate[M,部署层]、mvp-seed 单事务[S]、路由守卫/base URL env/verify-reset 落地页[S×3]、端到端冒烟[M] |

**已完成、无需再做(先让 Owner 安心):** ① 护城河 Trust/Hermes/审计(后端+前端都齐);② 用户主线 登录/建Key/playground/用量(前端已接);③ 37 个 provider 家族可路由;④ model→pool 绑定端点 + 定价/绑定/审核黑名单 admin 页(本会话已交付合并)。

## B. 分阶段建造计划(波次)
每波内每个功能动手前,按融合纪律(CLAUDE.md §16)先调研 sub2api/new-api/CLIProxyAPI 三家的同款管理面 → 各自结论 → 对照我们 → 融合出我们自己的 affordance(像审核黑名单页那样)。前端只做功能骨架,不追求设计。

### Wave 1 — 登录方式补全(用户最先感知,前端为主)
2FA 登录验证码[S] → 社交/OAuth 登录入口 + 回调页[M] → passkey 注册[M] → passkey 免密登录[M] → 安全页发起第三方绑定[M]。顺带:verify-email/reset-password 落地页[S]、layout 路由守卫[S]、base URL 改 NEXT_PUBLIC_API_BASE[S]。
- 先决待核:OAuth provider 是否仅 env 配置(若要纯 UI 配 provider 需后端评估,可能有真缺口)。
- 验收:邮箱密码 + 2FA + 社交 + passkey 四种方式都能真登录;已登录可绑/解绑第三方。

### Wave 2 — admin 管理后台补全(最大前端块)
provider 目录[M]、channel 目录[M]、配额策略[M]、渠道测试模板[M]、model-sync + 能力/别名[M]、订阅生命周期操作 + 订阅券[M+S]、provider-account/pool-group 纳入 admin 外壳[M]、audit-events/DLQ/cache 运维面[M]、平台设置补键[S];最后做 **路由可视化向导[L]**(交互最重,单独切片)。
- 验收:运维能从 admin UI 管全部资源,不再靠直写 DB / seed。

### Wave 3 — 多 provider + 收尾(小)
可选 family 选择器 UI + 后端 /protocol-families 列举端点[M];按需再接几家厂商[S/家];session 反转家族留 roadmap(需真流量);可选 referral 人工补发端点[S,Owner 定];部署收尾:prod compose 加 migrate[M] + 端到端冒烟[M]。

## C. 需要 Owner 拍板的决策
1. **先做哪一波?** 推荐 Wave 1(登录方式)——用户最先感知、几乎纯前端、风险低。
2. **OAuth provider 配置**:接受"仅环境变量配 provider"(快)还是要"admin UI 配 client_id/secret"(需后端评估是否已支持 DB 配置)?**推荐先 env,UI 配置留 Wave 2/3。**
3. **推荐返利人工补发**:要不要补后端端点(现仅自动入账 + 只读)?**推荐:暂不,保持自动入账,Wave 3 再议。**

## D. 不在本轮(沿用既有边界)
真实支付网关写入(#2);设计系统统一/精美 UI(前端只接线测试)。
