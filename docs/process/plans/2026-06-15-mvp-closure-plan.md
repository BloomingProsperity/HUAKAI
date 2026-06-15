# HUAKAI MVP 闭环计划（树状图）— 2026-06-15

**PM:** Claude (Opus 4.8). **方法:** 8-agent 真端点 + 前端骨架 inventory（不靠记忆,查真码),再合成。
**MVP 范围(主脊):** 可演示核心垂直切片 — 真人 **登录 → 建 API key → playground 真流式调网关 → 看用量/余额** + 最小 seed 让网关有路由目标。单 provider 家族。部署(merge+prod)与全产品页为**显式可选层**。
**状态:** 计划待 Owner 审批;审批通过才动手建造(rule #9 plan-before-execute)。

---

## A. MVP 树状图

```
MVP 闭环（一个真人：登录 → 建Key → playground真流式调网关 → 看用量/余额）
│
├── 0. 前置阻断项（不解决则后面全空转）
│   ├── 迁移未自动化 [❌missing][FE:n/a] — docker-compose.prod.yml 无 migrate 步骤(grep migrat=0)，
│   │     空库启动即在 MaybeBootstrap/EnsureDefaultTenant 上炸；迁移只在 Makefile `make db-migrate`
│   ├── 默认租户+admin 自举 [✅done-active][FE:n/a] — wiring.go:1243 MaybeBootstrap + :1249 EnsureDefaultTenant(tenant id=1)，
│   │     需 HUAKAI_ADMIN_BOOTSTRAP_TOKEN(hk_admin_+24位base32) 等5个env
│   ├── 路由目标缺写入口 [❌missing][FE:❌缺] — 无 POST /admin/v1/models、无 model_pool_bindings 创建端点
│   │     (openapi grep model_pool_binding=0)；现在只能靠 cmd/smoke-setup/main.go:310 一把seed
│   ├── 站点配置种子 [✅done-active][FE:❌缺] — GET /v1/site/config 须返回可用 tenant_id +
│   │     registration_enabled/password_login_enabled=true, two_factor/captcha=false
│   ├── quota 默认ON但空策略放行 [✅done-active][FE:n/a] — HUAKAI_QUOTA_ENFORCE 默认true(config.go:182)，
│   │     无策略 Reserve→DecisionAllow，不阻断未配置MVP；保持现状
│   └── 集成测试隔离 [⚠️partial][FE:n/a] — tenancy bootstrap_integration_test.go 全局软删+序列回绕，只在 -p 1 安全
│
├── 1. 前端基座（所有环的地基）
│   ├── App 壳已存在可复用 [✅][FE:✅有] — app/layout.tsx → AppLayout(Sidebar+Header+main)，next build 13路由 exit0
│   ├── api client 已存在(仅admin token) [✅][FE:🟡骨架] — lib/api/client.ts，getAdminToken() 读 localStorage 'huakai_admin_token'
│   ├── 会话感知 client/auth store [❌任务][FE:❌缺] — 持久化 SessionTokenBundle，注入 Bearer<session_token>，
│   │     暴露 login/register/logout/refresh/me  [M]
│   ├── Sidebar 重接真实路由 [任务][FE:🟡骨架] — Sidebar.tsx:38-44 硬编码陈旧(密钥/用量 disabled:true)  [M]
│   ├── API base 改 env 驱动 [任务][FE:🟡骨架] — next.config.mjs/lib/api/huakai.ts 硬编码 localhost:8080 → NEXT_PUBLIC_API_BASE  [S]
│   └── (可延) openapi-typescript 代码生成 / 设计系统统一 / mimicry mock
│
├── 2. 鉴权环（登录/注册/会话刷新）—— 后端全通，工作量≈纯前端
│   ├── GET /v1/site/config [✅done-active][FE:❌缺] — 取 tenant_id+开关(routes_siteconfig.go:19)
│   ├── POST /v1/auth/register [✅done-active][FE:❌缺] — auth_handler.go:173
│   ├── POST /v1/auth/login [✅done-active][FE:❌缺] — auth_handler.go:225；200存bundle，401通用，429显Retry-After，202(2FA)不崩
│   ├── GET /v1/auth/me [✅done-active][FE:❌缺] — whoami；AuthProvider+路由守卫调它
│   ├── POST /v1/auth/logout [✅done-active][FE:❌缺]
│   ├── POST /v1/sessions/refresh [✅done-active][FE:❌缺] — session_handler.go:39；token family+generation 轮转
│   ├── verify-email / reset-password 落地页 [✅done-active][FE:❌缺] — auth_handler.go:438/457，处理202枚举安全响应  [M]
│   ├── (可延-dormant) 2FA / passkey / OAuth / telegram [⚠️dormant][FE:❌缺] — 默认off，MVP仅占位
│   └── (后端微调) AuthLoginRequest schema 补 captcha_token [❌任务] — handler已收，spec漏  [S]
│
├── 3. Key 环（建/列/撤 API key + 一次性明文）—— 后端全就绪
│   ├── POST /v1/api-keys [✅done-active][FE:❌缺] — userkeyhttp/handlers.go:43，201返plaintext仅一次
│   ├── GET /v1/api-keys [✅done-active][FE:❌缺] — 含revoked，永不返明文/hash
│   ├── DELETE /v1/api-keys/{id} [✅done-active][FE:❌缺] — 幂等软撤
│   ├── PATCH /v1/api-keys/{id} [✅done-active][FE:❌缺] — 改名/status
│   ├── lib/api/apiKeys.ts 客户端 [❌任务][FE:❌缺]  [S]
│   ├── app/api-keys/page.tsx [❌任务][FE:❌缺] — 列表+撤销+撤销态区分  [M]
│   ├── 建Key弹窗(一次性明文复制) [❌任务][FE:❌缺] — 复制前禁关闭  [M]
│   ├── 打开 Sidebar '密钥' 入口 [任务][FE:🟡骨架] — disabled:true→false  [S]
│   └── 无 reveal 端点 [❌missing-by-design] — 明文仅 create 一次，UI必须当场抓取
│
├── 4. 网关调用环（playground 真流式）—— 页面已能跑，缺打磨
│   ├── app/chat/page.tsx 已存在可用 [✅][FE:✅有] — OpenAI+Anthropic tab、SSE渲染、用量面板、正确用 hk_ key
│   ├── POST /v1/chat/completions [✅done-active][FE:✅有] — routes.go:89，真分发+计费全链路
│   ├── POST /v1/messages [✅done-active][FE:✅有] — routes.go:104
│   ├── SSE 真流 [✅done-active][FE:✅有] — forwarder.go:103 flush
│   ├── GET /v1/models [✅done-active][FE:❌缺] — routes.go:107，鉴权门控但前端0调用者
│   ├── lib/api/models.ts fetchModels() [❌任务][FE:❌缺]  [S]
│   ├── chat 页 model<select> 接 fetchModels [任务][FE:🟡骨架] — 替换硬编码 page.tsx:17-27  [M]
│   ├── hk_ key 输入框 [任务][FE:❌缺] — 写 localStorage huakai_api_key，admin被拒  [S]
│   ├── /chat 加导航入口 [任务][FE:❌缺] — Header.tsx 无 nav link  [S]
│   └── 401/402/429 错误友好化 [任务][FE:🟡骨架]  [S]
│
├── 5. 用量环（自助看用量/余额）—— 后端6端点全活，前端全缺
│   ├── GET /v1/me/usage [✅done-active][FE:❌缺] — inbound-key bearer，含 next_cursor
│   ├── GET /v1/me/quota [✅done-active][FE:❌缺] — session auth，cost_usd 维度窗口
│   ├── GET /v1/me/analytics/time-series [✅done-active][FE:❌缺] — 须 from+to(≤31天)+granularity
│   ├── GET /v1/users/me/payments/balance [✅done-active][FE:❌缺] — paymenthttp/handler.go:213，amount_cents
│   ├── lib/api me-usage.ts + Me* 类型(成本保持 string numeric20,8) [❌任务][FE:❌缺]  [S]
│   ├── app/usage/page.tsx 自助仪表盘 [❌任务][FE:❌缺] — StatCard+TrendChart+Table(游标分页)，复用 components/dashboard/*  [L]
│   ├── 小数安全格式化(禁 Number() 丢精度) + 时间范围/粒度控件 [任务][FE:❌缺]  [S+M]
│   └── 打开 Sidebar '用量' 入口 [任务][FE:🟡骨架]  [S]
│
├── 6. 最小路由目标（网关有东西可路由）—— 隐藏前置，否则 no_eligible_pool
│   ├── provider/channel/pool/account/credential 创建端点 [✅done-active] — POST /admin/v1/providers,channels,pools,provider-accounts,credentials 全 route-mounted
│   ├── POST /admin/v1/model-sync [✅done-active] — routes.go:975，厂商目录填 models
│   ├── POST /admin/v1/models 直建 [❌missing] — 无端点，仅 sync+seed 写  [M]
│   ├── POST model→pool binding [❌missing] — ★关键缺口：model_pool_bindings 仅 smoke-setup 写；
│   │     空则 Registry→空 PoolCandidates→Router no_eligible_pool(default_router.go:61)  [M]
│   ├── 参数化 smoke-setup 作 canonical seed [任务] — 整链一把(tenant→...→binding→snapshot)，同事务 bump version  [M] ← MVP首选
│   └── (可延)前端路由目标向导 + providers/channels/credentials/models 客户端 [任务][FE:❌缺]  [L]
│
└── 7. 部署 + 闭环验证（显式可选层，非核心 MVP）
    ├── prod compose 加 migrate 一次性步骤 [❌任务] — gateway depends_on migrate completed_successfully  [M]
    ├── redis 决策(MVP Budget OFF 不强需) + .env.prod.example  [S]
    ├── compose 冒烟(空卷→migrate→gateway→/healthz绿+tenant id=1)  [M]
    ├── 端到端冒烟 register→verify→login→me→refresh→logout  [M]
    └── playground 全环冒烟 seed→hk_key→/v1/models→chat stream→落 usage  [M]
```

## B. 关键事实
1. **后端核心链路已通,MVP 主要是前端工程** — 鉴权(7端点)、Key CRUD、网关推理+真流式+计费全链路、用量读6端点全部 done-active 且 route-mounted;除两个路由目标缺口外几乎不需新后端。
2. **网关要能路由必须先有 provider账号+凭证+model_pool_binding(隐藏前置)** — model→pool 无任何 admin 写入口,缺它 Router 直接 `no_eligible_pool`。
3. **登录后端有但前端完全无,admin token 现是手贴 localStorage** — frontend 是联调控制台,零登录页;hk_ key 与 admin token 是两套。
4. **playground 已能跑真流式** — app/chat 已有 SSE+用量面板,缺口只是 models 硬编码/无 key 输入框/无导航入口。**最快能 demo 的一片。**
5. **MVP 取舍三连** — quota 默认ON无策略即放行(保持现状);CRED-288 旋转未接线(显式排除);集成测试隔离最便宜修法是加 `-p 1`。
6. **部署一条命令起不来** — prod compose 无迁移步骤,空库启动即炸。

## C. 垂直切片建造顺序（最小可demo优先）
1. **路由目标种子**(地基,无UI) [M] — 参数化 smoke-setup 或补 admin 端点。验收:chat 调用 Registry 返非空 PoolCandidates、不报 no_eligible_pool。
2. **playground 打磨**(最快可见) [SM] — models 真拉取+hk_ key 输入框+导航+错误友好化。验收:浏览器输 hk_ key→真 alias→流式渲染+落 usage。**此片即可对 Owner 端到端演示**(手贴 hk_ key,不依赖登录)。
3. **前端基座+鉴权环** [ML] — 会话 client、login/register/me/logout/refresh、守卫、verify/reset 落地页。验收:register→login→me→刷新→logout。
4. **Key 环** [M,依赖3] — apiKeys.ts、/api-keys 页、一次性明文弹窗。验收:create 明文一次可复制→list 明文消失→revoke 幂等。
5. **用量环** [L,依赖3] — Me 类型、me-usage.ts、/usage 仪表盘、小数安全。验收:余额+quota窗口+趋势图+游标分页表全渲染,成本不丢精度。
6. **核心闭环串联演示** [M,test] — 验收:**真人 login → 建 key → 复制 hk_ → playground 流式调用 → /usage 看到本次用量与余额变化**。MVP 主脊验收。
7. **(可选层)部署可发布** [ML] — prod compose 加 migrate、env 化、compose 冒烟。

## D. 明确不在 MVP 内
OAuth/passkey/telegram 登录 · 2FA 挑战流 · money/PSP/充值写入 · subscriptions/referrals/vouchers · Trust/Hermes/审计 Merkle/Pools 护城河/mimicry · 完整 admin 设置页/路由可视化向导 · 多 provider 家族 · openapi 代码生成/设计系统统一/CSV/高级 key 控制。

## E. 前置阻断项（决策点需 Owner 拍板）
- **路由目标最小记录链** — MVP 走 **seed 二进制**(快,M) 还是补**两个真 admin 端点** `POST /admin/v1/models` + model→pool binding(可复用,各M,需 OpenAPI+handler+route+同事务 bump snapshot version)?**建议走 seed。**
- **quota 演示策略** — 是否给默认租户配一条演示 quota 策略(否则 /v1/me/quota 窗口为空)?
- **集成测试隔离** — `-p 1`(牺牲并行速度,便宜) vs 重构 fixture 隔离?**建议先 -p 1。**
- **部署形态** — MVP demo 用**本地 next dev + 本地 gateway:8080**(不需 compose) 还是要 **compose 一键起**(则迁移步骤必做)?
- **CRED-288** — 确认显式排除出 MVP(无异议即排除)。

## F. 总览 + 粗估 effort + 最大风险
**总览:** MVP 交付一条可演示核心垂直切片(登录→建key→真流式调网关→看用量/余额)+ 最小 seed;部署与全功能 admin 为可选层。
**粗估:** 核心 6 片 ≈ 等效 **2L**(前端为主,约 18-24 个 S/M 任务);后端净新增极小(走 seed 仅 1 个 S;补 admin 端点则 +2M)。可选部署层另 +ML。
**最大单点风险:** **model_pool_binding 缺口** — 把"可解析 alias"变成"可路由 pool"的唯一记录,无任何 admin 写入口,缺它整个 playground/计费/用量演示链全失败于 `no_eligible_pool`。切片1必须先坐实,且任何写法都要**同事务 bump `model_registry_snapshots.version`**,否则缓存/审计回放漂移。
