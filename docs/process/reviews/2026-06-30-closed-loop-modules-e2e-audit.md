# 已闭环模块·端到端逻辑审计(2026-06-30)

触发:Owner「按当前(端到端逻辑)标准,回查之前已闭环的模块,是不是都有问题?」
方法:12 个并行 subagent 逐模块追「前端 api → tokenForPath 凭证 → 后端 handler → 真实 flow 是否走到可用终态」,
覆盖 ~60 个 feature 模块 + 全部 auth/*。所有发现均亲核 file:line;高危项我本人复核。

## 结论一句话
**不是「都有问题」**:钱核心(钱包/订单/订阅/兑换/签到)、凭证子系统、绝大多数 admin 读面、profile/通知/会话
全部端到端干净。但确有 **2 个系统性 S1 簇 + ~8 个 S2/S3 模块缺口**,且**本会话我自己刚建的 DeviceConfirm 页也中招**
(同一系统性根因)——印证 Owner 的担忧:之前「接线完成」只验了「UI↔端点连上」,没验「整条逻辑走到可用终态」。

---

## S1（关键:看着接好、实际端到端不可用）

### S1-A 邮件 token 投递系统性断裂(3 条流程)— 我已亲核
- 根因:后端三个邮件正文**只发裸 token、零链接**——`email/sender_factory.go:266`(验证)、`:271`(密码重置)、
  `:276`(设备确认),函数只接收 token、不含任何 URL。
- 而三个前端落地页是**纯 URL-query 消费、无手动粘贴框**:`EmailVerifyPage.tsx:28`、`ResetPasswordPage.tsx:26`、
  `DeviceConfirmPage.tsx:28`(后者是我本会话 Wave O 刚建的,同样假设了邮件带链接)。
- 后果:用户拿到邮件里的 token 却**无处可填**:
  - **密码重置(最严重,常开)**:任何忘记密码的用户都走不通 → 永久锁外。
  - **邮箱验证**:开 `VerifyEmailEnabled` 后未验证用户登录被 `userauth/service.go:301` 挡,而唯一解封路(验证)不可达 → 锁死。
  - **新设备确认**:开 `DevicePolicy=confirm` 后新设备登录 403 发确认邮件,token 无处填 → 新设备永久锁。
- 修法(二选一,均可安全自主):① 前端三页加「手动粘贴 token」输入框(纯前端,立刻可用,与邮件格式无关);
  ② 后端邮件改投带 `?token=&tenant_id=` 的完整前端链接(需引入前端 base URL 配置)。建议**两者都做**(①先解锁、②更好 UX)。

### S1-B 社交登录 pending-email 死路(telegram + QQ + 任何无验证邮箱的 OAuth)— 我已亲核
- `userauth/social_login.go:170` 的 `if !EmailVerified { return ErrOAuthPendingEmailRequired }` 在**查既有绑定之前**就拦截;
  QQ(`oauth_social_provider_flows.go:188` 恒 false)、Telegram(widget 恒 false)、DingTalk/generic 在上游不给已验证邮箱时也 false。
- 全仓**无** pending-email 补全端点、前端**无** `oauth_pending_email_required` 处理 → 这些 provider 登录永远卡 202、到不了 session;
  且门在查绑定前,连已绑定用户也被拦(顺序错)。WeChat 更只有常量、无 OAuth 交换实现。
- telegram 另有两处:前端把它当普通 OAuth 渲染按钮调 oauth-init(后端不认→报错)、siteConfig 未解析 bot_username(widget 渲染不出)。
- 这是 auth-core + 反转「所有账号须有邮箱」的刻意策略(有锁定测试),**§2/§15 需 Owner 选完成模型**(见末尾决策)。

---

## S2（功能缺口:看着能用、实则不达成目的）

| # | 模块 | 缺陷 | 证据 |
|---|---|---|---|
| S2-1 | disputesadmin | 运营点「支持退款」(status=resolved)**后端绝不退款/不动余额/不写 ledger**,用户余额纹丝不动 | `internal/db/audit/models.go:11` 自陈 "no refund or ledger mutation";`dispute_store.go:170` |
| S2-2 | hermes(运营台+只读面板) | `/v1/hermes` 整 mount 被 `routes.go:333 hermesRunner!=nil` 门控;**prod/direct compose 都没设 runner env、没 runner 服务** → 生产开箱即 404 | `routes.go:333`+`runner_client.go:45`+`docker-compose.prod.yml`/`.direct.yml` |
| S2-3 | pricingadmin 缓存价覆盖 | 纯内存 store、无 DB/迁移,**重启丢全部覆盖+审计链**;但被计费链真消费(活的、不持久) | `billing/cache_price_override.go:82`+`wiring.go:1304(nil loader)`;消费 `chat_completions_pricing.go:233` |
| S2-4 | ops 成功率 | `success_rate` 是 0~1 小数,前端当百分比拼 `%`→显示**100 倍偏小**("0.9950%"),且配色 `v>=99` 恒落 danger(永远红);测试夹具用百分比形态掩盖 | `OpsPage.tsx:154`+`ops.ts:34` vs `overview_handler.go:171` |
| S2-5 | alerting | 前端唯一内建指标 `cpu_usage_percent` **后端无任何生产者** → 用它建的规则永不触发(死配置);真实可告警指标未在下拉暴露 | `alerting.ts:57` vs `alerting/types.go:29`(仅常量);evaluator `service.go:318` |
| S2-6 | upstreammodels | 模型同步 service 仅当配了厂商 API key 才非 nil,否则恒 **503**;按钮开箱点不动且前端零前置提示 | `wiring.go:1558`+`model_sync_handler.go:60` |
| S2-7 | channeltesttemplates | 测试模板 CRUD 全绿,但渠道/账号测试执行器**零引用模板表**(用 account.ProbeModel);迁移 0115 自陈 inert | `provider_account_test_handler.go:87`;`0115_*.up.sql:22` |
| S2-8 | 社交注册返利 | 社交登录注册不绑定 referral(`issueSignupCredits(...,false)`)→ 社交渠道返利静默丢失 | `social_login.go:211` |

## S3（瑕疵）
- dlq:9 种事件类型里 `account_health`/`metrics` 后端无 replay handler→点重放恒 409(`dlq/types.ts:65` vs 注册点)。
- modelregistry:能力矩阵 PUT 留空 `max_output_tokens`/`model_mode` 被 SQL 无条件写 NULL→抹掉已有值(`model_capabilities.go:130`)。
- landing:`landing/api.ts` 死代码(无引用)。
- version:SMTP 设置 `tenant_id=0` 保存→可见 400(UX 容错不一致)。
- 设置 UI:新加的 `telegram_bot_username` key 在 allowlist 但未进任何 TAB_GROUPS → 运营在设置页改不到(本会话 telegram 后端缺口)。

---

## 验证为「干净」的(端到端真闭环,有 file:line 支撑)
钱核心:wallet/orders/ordersadmin/redeem/checkin/subscriptions/subscriptionsadmin(退款空值防住、订阅金额服务端派生、
pending 有真结算);凭证:accounts/credentialsacq/credentialrenew(secret write-only、OAuth 向导真落库);
proxies/routing/routeadmin(绑定真消费、SSRF 守卫、往返不丢字段);keys/usage/quota(配额往返修复已落地、user_id 过滤真绑 SQL);
catalogs/channelhealth/tlsfp(旧旁路已修真消费)/moduleregistry;health/audit(IDOR 双守);
overview/settings/logsdiag/availablechannels/rankings;passkey/profile/通知/会话;announcements/broadcast/vouchersadmin/legal;
mediatasks(读)/playground(真 relay)。

---

## telegram 完成模型决策(§2/§15,Owner 拍板)
new-api 对照:telegram=**先绑定后登录**(TelegramBind 要先登录),不靠 telegram 单独建号。三选一:
- **(A) 合成邮箱跳邮箱门 + 既有绑定优先**:telegram/QQ 一键登录即注册(最佳 UX,比 new-api 更宽;email-less 账号存在)。
- **(B) 先绑定后登录**:对齐 new-api(需建 bind 端点 + reorder;不产生 email-less 账号)。
- **(C) 建 pending-email 补全端点 + 前端收集邮箱**:守住「所有账号须有邮箱」既有策略。
A/B/C 同时也决定 S1-B 怎么收口。
