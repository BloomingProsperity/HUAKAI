# HUAKAI 商业功能三镜核实 + 权威缺口账本

| 字段 | 值 |
| --- | --- |
| 日期 | 2026-06-02 |
| Lane | RESEARCH/VERIFY, 只读分析 |
| 产物 | `docs/process/commercial-gap-ledger-2026-06-02.md` |
| sub2api 参考点 | `Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca` |
| new-api 参考点 | `QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c` |
| HUAKAI landing | 初始取证工作树 `39ea71df875f41f57778f5cae984718ddc1e3b1c`；最新观测 `origin/fix/hermes-phase-1-e33d940@74844b2f466c3b3dd4d54100a066e6efba613595` |
| HUAKAI quota-subsystem | `origin/work/quota-subsystem` |
| 治理状态 | Owner 直接授权的单 Codex 研究核实账本；后续实现/发布前仍需 Owner 补齐或确认 parallel plan + synthesized plan gate。 |
| Observed regions | 62 |
| Inferences | 7 |
| Open questions | 4 |

## 0. Clean-room 约束

本账本只记录行为与产品能力，不复制 reference project 的源代码、文件组织、函数名、结构体字段、注释或实现细节。`sub2api` 为 LGPL-3.0 源，`new-api` 为 AGPL-3.0 源；两者在本任务中只作为商业功能证据，不作为 HUAKAI 实现来源。

判断口径：

- `landing 有` = 当前 `origin/fix/hermes-phase-1-e33d940` 能从源码行号看到可运行能力。
- `quota-subsystem 有` = 未并入分支 `origin/work/quota-subsystem` 能从源码行号看到可运行能力。
- `并入 quota 后仍缺` = landing + quota-subsystem 合并后仍不能等价覆盖 reference 行为。
- `建议借鉴源` = 推荐优先研究的 reference 行为来源；不是复制实现来源。

## 1. 主功能账本

| 功能 | sub2api 有(引证) | new-api 有(引证) | HUAKAI-landing 有? | HUAKAI-quota-subsystem 有? | 并入 quota 后仍缺? | 建议借鉴源 |
| --- | --- | --- | --- | --- | --- | --- |
| 支付门户: 统一返回配置、结账信息、套餐、支付渠道、限额 | 有。用户支付路由覆盖配置、结账信息、套餐、渠道、限额。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:28` | 有充值信息页，返回多支付开关、合规状态、产品与最低金额等。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/topup.go:24` | 部分。用户充值 route 已挂载，能创建充值单，但没有统一 portal 聚合配置。`backend/cmd/gateway/routes.go:113`、`backend/internal/paymenthttp/routes.go:78` | 部分。用户支付只提供订单列表与余额，不是完整 portal。`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:201` | 是。需要一个用户侧商业入口把可购套餐、支付渠道、限额、合规提示和余额状态合并。 | sub2api 主，new-api 补充值入口配置 |
| 钱包充值/余额充值下单 | 有。用户订单创建与 verify/list/detail/cancel/refund 入口同组挂载。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:34` | 有。用户 topup、自助 pay、各支付渠道 pay 入口挂载。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:94` | 有。`OpenRecharge` 创建 pending 充值单，HTTP 层提供用户充值入口。`backend/internal/payment/order.go:128`、`backend/internal/paymenthttp/routes.go:78` | 有。`CreateOrder` 可建充值订单，admin 可建单，用户可读订单/余额。`origin/work/quota-subsystem:backend/internal/payment/service.go:58`、`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:193` | 否，核心充值建单已覆盖；但真实支付渠道仍另列缺口。 | sub2api/new-api |
| 订单状态机与幂等履约 | 有。订单创建、核验、取消、退款请求、公开核验和后台处理分开暴露。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:34` | 有充值订单记录、通知锁单、状态更新和入账行为。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/topup.go:373` | 部分。landing 有 pending/paid/credited/completed/failed/expired/cancelled 状态常量和充值入账，但订单管理面较薄。`backend/internal/payment/order.go:16`、`backend/internal/payment/fulfillment.go:25` | 有。订单创建、手动确认、两段式履约、列表、详情、审计事件、余额均存在。`origin/work/quota-subsystem:backend/internal/payment/service.go:133`、`origin/work/quota-subsystem:backend/internal/payment/service.go:158`、`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:282` | 部分仍缺。缺用户/管理员取消、后台重试、退款生命周期。 | sub2api |
| 用户订单列表、详情、余额 | 有。用户订单列表、详情、取消、退款请求等入口。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/handler/payment_handler.go:316` | 有用户充值记录列表和搜索。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/topup.go:440` | landing 只有创建充值单和回调结果，不足完整用户订单中心。`backend/internal/paymenthttp/routes.go:49` | 有。用户订单列表与余额 route。`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:331`、`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:351` | 订单详情用户侧仍偏弱，管理员详情已有。 | sub2api |
| 支付公开核验 / resume-token 状态恢复 | 有。公开核验和恢复 token 解析。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:50` | 未在核实范围看到等价 resume-token 行为；主要是用户会话内 topup 与 webhook。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:94` | 无公开核验/resume-token。landing 回调是 provider 入口，不是用户公开查询。`backend/internal/paymenthttp/routes.go:82` | 无。quota 分支公开端点是 webhook，不是用户公开核验。`origin/work/quota-subsystem:backend/internal/paymenthttp/webhook.go:31` | 是。支付跳转后查询/恢复体验仍缺。 | sub2api |
| 支付回调/webhook | 有。EasyPay、Alipay、WeChat、Stripe、Airwallex 回调入口。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:57` | 有 Epay、Stripe、Creem、Waffo、Waffo Pancake 回调入口。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:56`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:75` | 有 HMAC 安全占位回调。`backend/internal/paymenthttp/provider_hmac.go:93`、`backend/internal/paymenthttp/routes.go:147` | 有 test provider 回调确认链，但真实渠道未注册。`origin/work/quota-subsystem:backend/internal/payment/webhook.go:19`、`origin/work/quota-subsystem:backend/internal/payment/provider.go:38` | 是。真实渠道的验签、幂等事件 ID、异常返回格式仍缺。 | sub2api + new-api |
| 多真实支付渠道适配器 | 有。支付 provider 工厂覆盖多类真实渠道。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/payment/provider/factory.go:9` | 有 Epay、Stripe、Creem、Waffo、Waffo Pancake 支付入口。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:97` | 部分。支持 HMAC provider 配置和禁止生产 mock。`backend/internal/paymenthttp/provider_hmac.go:139`、`backend/cmd/gateway/payment_routes_test.go:5` | 部分。明确只有 manual/test，真实渠道留后续 Owner-gated 切片。`origin/work/quota-subsystem:backend/internal/payment/provider.go:20` | 是，赚钱闭环高优先级缺口。 | new-api 覆盖渠道广，sub2api 覆盖退款更深 |
| 退款、退款申请、可退款 provider | 有。用户退款请求和可退款 provider；后台退款处理。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:41`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/handler/admin/payment_handler.go:138` | 未核实到等价退款管理闭环；充值侧重点是支付与入账。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/topup.go:490` | 无。管理员加款不支持 debit，提示借记被 gated。`backend/internal/payment/admin_credit.go:101` | 无。quota 分支订单履约未提供 refund/cancel 端点。`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:193` | 是。退款/撤销/冲正必须补，不能以手动加款替代。 | sub2api |
| 管理员支付面板: 统计、订单列表、详情、取消、重试履约、退款、计划与 provider 实例 | 有。后台 payment dashboard、orders、cancel、retry、refund、plan CRUD、provider instance CRUD。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:69` | 有 admin topup list/complete，但不等价完整支付面板。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:129` | 部分。landing 有管理员手动加款和 P0 catalog。`backend/cmd/gateway/routes.go:457` | 部分。quota 有 admin payment create/list/detail/confirm，但无 dashboard、cancel、retry、refund、provider instance CRUD。`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:193` | 是。后台支付运维面板仍缺多个关键操作。 | sub2api |
| 管理员手动补款/补单 | 未作为重点单独暴露；后台订单处理更完整。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/handler/admin/payment_handler.go:89` | 有 admin topup complete。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/topup.go:490` | 有。平台管理员可对用户余额调整，并生成充值订单/审计。`backend/internal/adminhttp/balance_credit_handler.go:33`、`backend/internal/payment/admin_credit.go:46` | 有。管理员可建单并确认履约。`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:207`、`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:241` | 否，基础补款覆盖；但退款/冲正另缺。 | new-api |
| 订阅套餐展示 | 有。用户订阅列表、活跃订阅、进度、汇总。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/handler/subscription_handler.go:45` | 有用户订阅套餐/self/balance pay 等入口。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:149` | 无完整订阅系统。 | 有。用户可看自己的订阅和在售套餐。`origin/work/quota-subsystem:backend/internal/subscriptionhttp/handler.go:246`、`origin/work/quota-subsystem:backend/internal/subscriptionhttp/handler.go:516` | 否，展示和基础状态覆盖。 | sub2api |
| 订阅购买: 余额支付与真实在线支付 | 有。订阅服务和支付订单结合。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/subscription_service.go:154` | 有 balance、Epay、Stripe、Creem、Waffo Pancake 订阅支付。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:155` | 无。 | 部分。订单可履约成订阅，兑换码可激活订阅；但真实在线支付渠道仍 manual/test。`origin/work/quota-subsystem:backend/internal/subscription/order_fulfillment.go:27`、`origin/work/quota-subsystem:backend/internal/payment/provider.go:20` | 是。缺余额自助购买 API 与真实支付渠道购买。 | new-api |
| 订阅管理: plan CRUD、分配、取消、用户订阅列表/详情 | 有。后台订阅 list/detail/progress/assign/bulk/extend/reset/revoke。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:516` | 有 admin plans 和 admin user subscriptions。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:162` | 无。 | 有。plan create/list/get/disable、assignment create/list/get/cancel。`origin/work/quota-subsystem:backend/internal/subscriptionhttp/handler.go:233`、`origin/work/quota-subsystem:backend/internal/subscriptionhttp/handler.go:363` | 部分仍缺 bulk assign、extend、manual quota reset 这类高级运维。 | sub2api |
| 订阅配额窗口、重置、进度、校验 | 有。窗口 reset、手动 reset、自动 reset、limit check、progress。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/subscription_service.go:753`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/subscription_service.go:840` | 有套餐字段包含 reset period / custom seconds。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/subscription.go:279` | 无订阅配额。 | 有配额 reserve、policy resolve、calendar month 窗口、订阅 caps 安装。`origin/work/quota-subsystem:backend/internal/quota/service.go:65`、`origin/work/quota-subsystem:backend/internal/quota/rate_window.go:35`、`origin/work/quota-subsystem:backend/internal/subscription/activation.go:94` | 部分。缺用户可见 progress API、管理员手动 reset API。 | sub2api |
| 订阅到期、降级、提醒、只升不降 | 有过期续期和 reset。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/subscription_service.go:253` | 有用户/后台订阅管理，但本次只读未核实到提醒。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/subscription.go:360` | 无。 | 有。到期 worker、提醒 worker、只升不降、防降级。`origin/work/quota-subsystem:backend/internal/subscription/service.go:161`、`origin/work/quota-subsystem:backend/internal/subscription/reminder.go:142`、`origin/work/quota-subsystem:backend/internal/subscription/activation.go:76` | 否，核心策略覆盖；多副本提醒去重是已知后续优化，不是功能缺失。 | HUAKAI quota 已覆盖 |
| 联盟/邀请返佣: 邀请码、绑定、返佣入账、冻结、转余额、后台记录、账本快照 | 有完整行为。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/affiliate_service.go:97`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/repository/affiliate_repo.go:117` | 有自助佣金余额转移能力。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:94`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/user.go:346` | 部分。有 invitation create，但不是商业返佣账本。`backend/cmd/gateway/routes.go:123` | 未覆盖。quota 分支未见 affiliate/rebate 子系统。 | 是。赚钱裂变能力缺口。 | sub2api 主，new-api 补自助佣金余额转移 |
| 兑换码/券码: 创建、批量、列表、吊销、兑换 | 未作为用户指定重点；本行主要看 new-api。 | 有兑换码 CRUD、搜索、清理、兑换入账。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:294`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/redemption.go:16` | 有余额券 create/batch/list/revoke/redeem。`backend/internal/gatewayhttp/voucher_handler.go:76`、`backend/internal/voucher/service.go:45`、`backend/internal/voucher/service.go:142` | 有，并扩展订阅券。`origin/work/quota-subsystem:backend/internal/voucher/types.go:17`、`origin/work/quota-subsystem:backend/internal/subscriptionhttp/handler.go:470` | 部分。缺 new-api 的搜索与清理无效码管理面。 | new-api |
| 订阅兑换码/订阅券 | 未作为单独可见行。 | 可通过订阅支付渠道购买，兑换码主要充值。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:149` | 无。 | 有。订阅券 grant kind、兑换履约激活/续期。`origin/work/quota-subsystem:backend/internal/voucher/types.go:17`、`origin/work/quota-subsystem:backend/internal/subscription/voucher_fulfillment.go:34` | 否，订阅券核心覆盖。 | HUAKAI quota 已覆盖 |
| 渠道 CRUD/search/detail | sub2api 有 channel admin 基础路由，但更偏账号/代理运维。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:595` | 有 channel list/search/detail/create/update/delete。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:227`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/channel.go:260` | 部分。P0 provider/channel catalog 只读。`backend/cmd/gateway/routes.go:375`、`backend/internal/adminhttp/channel_catalog_handler.go:38` | 未覆盖 channel CRUD；quota 的 routeadmin 是分组路由，不是 channel CRUD。`origin/work/quota-subsystem:backend/internal/routeadminhttp/handler.go:83` | 是。渠道 CRUD 是商业运维缺口。 | new-api |
| 渠道测试、余额更新、模型拉取、多 key、上游模型更新检测/应用、渠道计费 | sub2api 有账号测试、统计、上游模型同步、监控。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:295`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:305` | 有 channel test、balance update、fetch models、multi-key manage、upstream update detect/apply、channel billing。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:245`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:260`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/channel-billing.go:424` | 部分。provider account health/test、model sync、channel health admin。`backend/internal/adminhttp/provider_account_test_handler.go:57`、`backend/internal/adminhttp/provider_account_health_handler.go:47`、`backend/internal/adminhttp/model_sync_handler.go:54` | 部分。group routing gate 与 subscription route admin，不等价 multi-key/channel billing。`origin/work/quota-subsystem:backend/internal/subscriptionenforce/gate.go:35` | 是。特别是多 key 与上游模型检测/应用。 | new-api |
| provider/channel P0 catalog、健康、测试 | 未作为商业支付重点，但账号运营很完整。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:277` | channel admin 覆盖更广。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:227` | 有。provider list、channel list、account health/test 均有。`backend/internal/adminhttp/provider_catalog_handler.go:53`、`backend/internal/adminhttp/provider_account_health_handler.go:47`、`backend/internal/adminhttp/provider_account_test_handler.go:57` | 继承 landing，并增加分组路由。`origin/work/quota-subsystem:backend/cmd/gateway/routes.go:377` | 只读 catalog 已覆盖；CRUD 另缺。 | HUAKAI landing 已覆盖 P0 |
| 账号导入/导出、混 channel 确认、proxy 管理、账号统计 | 有。admin account import/export、check mixed channel、stats、proxy routes。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:277`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:371` | channel 层有 multi-key 和 key 管理，但账号导入/proxy 不是本次核实重点。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/channel.go:1231` | 部分。provider account admin、credential acquisition、health/test 存在；混 channel 风险门与 proxy 只看到基础/规划，未见 sub2api 等价导入/代理管理面。`backend/cmd/gateway/routes.go:384`、`backend/internal/provider/proxy_resolver.go:1` | 部分继承。quota 未新增等价账号导入/proxy 管理。 | 是。账号资产批量运营仍缺。 | sub2api |
| 调度监控、账号可用性、请求/上游错误、系统日志 | 有。ops monitoring、traffic、alerts、request/upstream errors、system logs。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:124` | 有 channel 自动测试/余额更新等运维任务。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/channel-test.go:986`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/channel-billing.go:498` | 部分。observability、channel health、audit/DLQ/reconciliation。`backend/cmd/gateway/routes.go:420`、`backend/cmd/gateway/routes.go:475` | 部分。quota worker 增加 subscription expiry/reminder。`origin/work/quota-subsystem:backend/cmd/gateway/wiring.go:496` | 部分。商业运营看板仍需整合账号调度、支付、订阅、渠道健康。 | sub2api |
| 分组路由 / 套餐档位路由 | sub2api 有 group stats、rate/group 管理。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:254` | new-api 有 group/channel routing 背景，但本次证据更强在 channel。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/channel.go:92` | landing 以 pool/router 为主，未见套餐档位 routeadmin。 | 有。routeadmin CRUD 与运行时 group policy gate。`origin/work/quota-subsystem:backend/internal/routeadminhttp/handler.go:83`、`origin/work/quota-subsystem:backend/internal/subscriptionenforce/gate.go:35` | 否，核心覆盖。 | HUAKAI quota 已覆盖 |
| 登录后角色面板 | 不主张 reference 商业功能；本行只记录 HUAKAI quota 自有角色面板能力。 | 不主张 reference 商业功能；本行只记录 HUAKAI quota 自有角色面板能力。 | landing 无 users.role 面板归属。 | 有。users.role 迁移、后端 whoami、deny-by-default 映射。`origin/work/quota-subsystem:backend/sql/migrations/0076_user_role.up.sql:7`、`origin/work/quota-subsystem:backend/internal/panelauthhttp/handler.go:37`、`origin/work/quota-subsystem:backend/internal/panelauth/resolve.go:5` | 否。 | HUAKAI quota 已覆盖 |
| mimicry sidecar / 反检测传输基础 | sub2api 有 proxy 管理，不等价 TLS sidecar。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:371` | 未在商业功能清单中作为付款功能。 | 有。Go transport 连接本地 sidecar，失败 fail-closed。`backend/internal/transport/mimicry/sidecar_client.go:21`、`backend/internal/transport/mimicry/sidecar_client.go:42` | 继承 landing。 | 不属于支付/订阅赚钱闭环缺口；proxy 运营面仍另缺。 | HUAKAI 自有差异化 |

### 1.1 Parity Disposition Map（功能 disposition 映射）

为保持 Owner 指定的主表列不变，本节补足项目强制 disposition；证据见主表同名行。

| 功能 | Disposition | 说明 |
| --- | --- | --- |
| 支付门户 | Mandatory Roadmap | 缺统一 checkout、渠道/限额/套餐聚合和公开恢复查询。 |
| 钱包充值/余额充值下单 | Implemented | landing 与 quota 均有建单/补款基础；真实支付另列。 |
| 订单状态机与幂等履约 | Mandatory Roadmap | 核心履约已在 quota 覆盖，但取消、退款、后台 retry 未闭环。 |
| 用户订单列表、详情、余额 | Mandatory Roadmap | quota 有列表/余额，用户侧详情与恢复体验仍缺。 |
| 支付公开核验 / resume-token 状态恢复 | Mandatory Roadmap | sub2api 类公开核验仍缺。 |
| 支付回调/webhook | Safe Equivalent | HMAC/test 回调是安全占位；真实渠道回调跟随支付插件路线。 |
| 多真实支付渠道适配器 | Plugin | 支付 provider 作为插件/Owner-gated money path 接入。 |
| 退款、退款申请、可退款 provider | Mandatory Roadmap | 退款、撤销、冲正不可用手动加款替代。 |
| 管理员支付面板 | Mandatory Roadmap | quota 有后台订单基础，dashboard/cancel/retry/refund/provider 实例仍缺。 |
| 管理员手动补款/补单 | Implemented | landing 与 quota 已覆盖基础补款/确认。 |
| 订阅套餐展示 | Implemented | quota-subsystem 已覆盖用户侧展示与状态。 |
| 订阅购买 | Mandatory Roadmap | 余额自助购买和真实支付购买仍缺。 |
| 订阅管理 | Mandatory Roadmap | 基础管理已覆盖，高级 bulk/extend/reset 运维仍缺。 |
| 订阅配额窗口、重置、进度、校验 | Mandatory Roadmap | quota 核心覆盖，用户 progress 与管理员 reset 仍缺。 |
| 订阅到期、降级、提醒、只升不降 | Implemented Better | quota 已有到期、提醒和防降级策略。 |
| 联盟/邀请返佣 | Mandatory Roadmap | 返佣账本、冻结、转余额、后台记录仍缺。 |
| 兑换码/券码 | Mandatory Roadmap | 基础券码与订阅券已覆盖，搜索/清理增强仍缺。 |
| 订阅兑换码/订阅券 | Implemented Better | quota 已扩展订阅券履约。 |
| 渠道 CRUD/search/detail | Mandatory Roadmap | landing 只有只读 catalog。 |
| 渠道测试、余额更新、模型拉取、多 key、上游模型更新、渠道计费 | Mandatory Roadmap | new-api 类渠道运维仍缺。 |
| provider/channel P0 catalog、健康、测试 | Implemented | landing 已覆盖 P0 catalog/health/test。 |
| 账号导入/导出、混 channel 确认、proxy 管理、账号统计 | Mandatory Roadmap | 账号资产批量运营仍缺。 |
| 调度监控、账号可用性、请求/上游错误、系统日志 | Merged Equivalent | HUAKAI 有分散 observability；商业运营总看板仍在结论列为路线图。 |
| 分组路由 / 套餐档位路由 | Implemented | quota routeadmin 与 gate 覆盖核心能力。 |
| 登录后角色面板 | Implemented | HUAKAI quota 自有能力，不作为 reference 缺口。 |
| mimicry sidecar / 反检测传输基础 | Safe Equivalent | HUAKAI 自有差异化；reference proxy 管理缺口另列。 |

## 2. 核实第三方 renew 审计

| 第三方声明 | 判定 | 证据与说明 |
| --- | --- | --- |
| 缺支付门户 | 成立 | landing 只有用户充值下单和 webhook 挂载，未见统一 checkout portal。`backend/cmd/gateway/routes.go:113`、`backend/internal/paymenthttp/routes.go:78`。quota 分支也只是订单/余额和后台订单。`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:201`。sub2api 明确有 config/checkout/plans/channels/limits。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:28` |
| 缺订单生命周期 | 夸大 | landing 是部分成立；quota-subsystem 已覆盖核心订单创建、确认、履约、列表、详情、审计和余额。`origin/work/quota-subsystem:backend/internal/payment/service.go:58`、`origin/work/quota-subsystem:backend/internal/payment/service.go:133`、`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:282`。但取消、退款、后台重试仍缺，所以不能判定完全覆盖。 |
| 缺多渠道适配器 | 成立 | landing 是 HMAC/mock 安全占位，quota 明确仍仅 manual/test，真实 Stripe/支付宝/微信/Epay 等留后续。`backend/internal/paymenthttp/provider_hmac.go:139`、`origin/work/quota-subsystem:backend/internal/payment/provider.go:20`。reference 两边均有多真实渠道。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/payment/provider/factory.go:9`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:97` |
| 缺退款 | 成立 | HUAKAI landing/admin credit 拒绝借记，quota 也无 refund 端点。`backend/internal/payment/admin_credit.go:101`、`origin/work/quota-subsystem:backend/internal/paymenthttp/handler.go:193`。sub2api 用户退款请求和后台退款均存在。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:41` |
| 缺重试 | 成立 | sub2api 后台订单 retry fulfillment 存在。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/handler/admin/payment_handler.go:102`。HUAKAI quota 有幂等重放和履约重试机制，但没有管理员对失败订单显式 retry endpoint。`origin/work/quota-subsystem:backend/internal/payment/service.go:158` |
| 缺订阅 | 已被 quota-subsystem 覆盖 | quota 分支有计划、分配/取消、用户列表、订单/兑换码激活、到期 worker 与提醒。`origin/work/quota-subsystem:backend/internal/subscriptionhttp/handler.go:233`、`origin/work/quota-subsystem:backend/internal/subscription/order_fulfillment.go:27`、`origin/work/quota-subsystem:backend/internal/subscription/reminder.go:142`。仍缺真实在线支付购买渠道，但“订阅系统不存在”的说法已不成立。 |
| 缺联盟返佣 | 成立 | landing 和 quota 未见 affiliate/rebate 账本。sub2api 有邀请码、绑定、返佣、冻结、转余额、后台记录。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/affiliate_service.go:269`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/affiliate_service.go:318`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/affiliate_service.go:411` |
| 缺兑换码 | 夸大 | landing 已有余额券创建、批量、列表、批次详情、吊销、兑换。`backend/internal/gatewayhttp/voucher_handler.go:76`、`backend/internal/voucher/service.go:45`、`backend/internal/voucher/service.go:142`。quota 还加订阅券。`origin/work/quota-subsystem:backend/internal/voucher/types.go:17`。但 new-api 的 search/cleanup 管理仍缺。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/redemption.go:29`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/redemption.go:174` |
| 缺管理面板 | 夸大 | landing 已有 provider/channel catalog、provider account health/test、admin credit、voucher、usage/billing/audit/DLQ 等。`backend/cmd/gateway/routes.go:375`、`backend/cmd/gateway/routes.go:391`、`backend/cmd/gateway/routes.go:457`、`backend/cmd/gateway/routes.go:469`。quota 又增加 payment/subscription/routeadmin/panelauth。`origin/work/quota-subsystem:backend/cmd/gateway/routes.go:416`、`origin/work/quota-subsystem:backend/cmd/gateway/routes.go:422`、`origin/work/quota-subsystem:backend/cmd/gateway/routes.go:428`。但完整支付 dashboard 和 channel CRUD 仍缺。 |
| 缺渠道 CRUD | 成立 | landing provider/channel catalog 是只读。`backend/internal/adminhttp/provider_catalog_handler.go:53`、`backend/internal/adminhttp/channel_catalog_handler.go:38`。quota routeadmin 是套餐档位到 pool_group 的路由 CRUD，不是渠道 CRUD。`origin/work/quota-subsystem:backend/internal/routeadminhttp/handler.go:83`。new-api channel CRUD/测试/余额/多 key/上游更新更完整。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:227` |
| P2-01 平台 admin 租户选择不一致 | 成立 | provider/channel catalog 对 platform_admin 未带 tenant_id 会要求显式 tenant。`backend/internal/adminhttp/provider_catalog_handler.go:137`。provider account health/test 在 platform_admin 无 scope 时默认 tenant 1。`backend/internal/adminhttp/provider_account_health_handler.go:90`、`backend/internal/adminhttp/provider_account_test_handler.go:127`。这会让同一 admin UI 的租户选择语义不一致。 |
| P2-02 dry-run 审计动作误标 `list_account_credentials` | 成立 | provider credential dry-run 测试 payload 标明 operation/dry_run，但 admin audit action 仍写成 credential list 类动作。`backend/internal/adminhttp/provider_account_test_handler.go:163`、`backend/internal/adminhttp/provider_account_test_handler.go:181`。这是审计语义缺陷，不影响凭证测试本身。 |
| P2-03 已验签但 provider 不匹配的回调无持久审计 | 成立，quota 未完全覆盖 | landing HTTP 层已验签后发现路径 provider 与回调 provider 不一致时直接返回 audit_reason，不进入 service 持久化。`backend/internal/paymenthttp/routes.go:164`、`backend/internal/paymenthttp/routes.go:172`。quota 分支把此类不一致拒绝为业务校验失败，但注释明确不单建拒绝事件，订单停留原状态。`origin/work/quota-subsystem:backend/internal/payment/webhook.go:38`。因此“无持久拒绝审计”仍成立。 |

## 3. 并入 quota-subsystem 后真正还缺、不能少的功能

按赚钱闭环优先级排序：

1. 真实支付渠道适配器与 provider 实例管理
   - 缺口：Stripe/Epay/Alipay/WeChat/Airwallex/Creem/Waffo 等真实渠道的创建支付意图、验签、回调幂等、失败语义和生产密钥隔离。
   - 证据：quota 仍仅 manual/test。`origin/work/quota-subsystem:backend/internal/payment/provider.go:20`
   - 借鉴源：new-api 用于多渠道入口覆盖，sub2api 用于 provider instance 管理和退款语义。
   - 工作量：L，涉及真实密钥、webhook 安全、支付语义、依赖/SDK 审计，必须 Owner-gated。

2. 支付门户与用户 checkout 恢复体验
   - 缺口：统一返回套餐、支付渠道、限额、合规、余额、跳转后公开核验与 resume-token。
   - 证据：landing/quota 只有充值/订单 API；sub2api 有 portal 和 public verify。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:28`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/payment.go:50`
   - 借鉴源：sub2api 主，new-api 补 topup info。
   - 工作量：M，主要是 API 聚合、前端面板、状态查询，不必先接真实支付也能落 Safe Equivalent。

3. 退款、撤销、后台重试履约
   - 缺口：用户退款申请、可退款渠道、管理员退款处理、失败订单 retry fulfillment、取消订单。
   - 证据：HUAKAI admin credit 当前不支持借记/退款。`backend/internal/payment/admin_credit.go:101`
   - 借鉴源：sub2api。
   - 工作量：M/L，若写 billing ledger 或退款账本属于 money-path 高风险，需 Owner 确认。

4. 联盟返佣商业闭环
   - 缺口：邀请码绑定、支付后返佣、冻结期、返佣转余额、后台记录、账本快照。
   - 证据：sub2api 有完整链路，HUAKAI 未见等价子系统。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/service/affiliate_service.go:97`
   - 借鉴源：sub2api 主，new-api 补自助佣金余额转移行为。
   - 工作量：M/L，涉及钱/余额/反刷，建议先 Manual First + Feature Flag。

5. 渠道 CRUD + multi-key + 上游模型检测/应用 + channel billing
   - 缺口：从只读 catalog 走向可创建/更新/删除/测试/余额刷新/模型拉取/多 key 管理/上游变化检测应用。
   - 证据：new-api 覆盖这些运维入口。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:227`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:router/api-router.go:260`
   - 借鉴源：new-api。
   - 工作量：L，牵涉 provider_accounts/channels/schema/credential 安全，分 slice 落。

6. 账号批量导入/导出、混 channel 确认、proxy 管理、账号统计
   - 缺口：sub2api 式账号资产运营能力仍未完整覆盖。
   - 证据：sub2api admin accounts/proxy/stats/routes。`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:277`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:371`
   - 借鉴源：sub2api。
   - 工作量：M/L，先做只读 dry-run/import preview，再做写入。

7. 兑换码管理增强: 搜索与清理
   - 缺口：HUAKAI 已有券码和订阅券，但缺 search/cleanup 无效码这类后台效率工具。
   - 证据：new-api 有 search/cleanup。`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/redemption.go:29`、`QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/redemption.go:174`
   - 借鉴源：new-api。
   - 工作量：S/M，低风险 docs/API/UI 切片即可。

8. 商业运营总看板
   - 缺口：把支付订单、订阅、渠道健康、账号调度、返佣、兑换码、退款统一成 Admin Ops 视图。
   - 证据：HUAKAI 目前是多个分散 admin route；sub2api 有 payment dashboard 与 ops monitoring。`backend/cmd/gateway/routes.go:475`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/handler/admin/payment_handler.go:29`、`Wei-Shaw/sub2api@aa69e3947dac0282c5973bc3a51fadf058bbc9ca:backend/internal/server/routes/admin.go:124`
   - 借鉴源：sub2api 主，new-api 补 channel 运维。
   - 工作量：M，先聚合只读数据，写操作逐项接入。

## 4. Open Questions

1. `origin/work/quota-subsystem` 合并顺序会影响支付/订阅/券码/routeadmin 的 API 路径是否最终保持当前形态；本账本按分支源码现状记录。
2. 真实支付渠道是否允许引入 SDK 依赖、还是采用轻量 HTTP adapter，需要 Owner 在 P-RealMoney 前决策。
3. 退款和返佣是否写入现有 billing ledger、payment_credits，还是新增独立 money movement 表，属于高风险 money-path 决策。
4. channel CRUD 是否直接操作现有 provider/channel/pool schema，还是先做 Admin Ops wrapper + pending change queue，需在 schema 风险前置评审。

## 5. Source Coverage Proof

Observed:

- `~/refs/sub2api`: `backend/internal/server/routes/payment.go`, `backend/internal/handler/payment_handler.go`, `backend/internal/handler/payment_webhook_handler.go`, `backend/internal/handler/admin/payment_handler.go`, `backend/internal/payment/registry.go`, `backend/internal/payment/provider/factory.go`, `backend/internal/handler/subscription_handler.go`, `backend/internal/service/subscription_service.go`, `backend/internal/server/routes/admin.go`, `backend/internal/service/affiliate_service.go`, `backend/internal/repository/affiliate_repo.go`, `backend/internal/handler/admin/affiliate_handler.go`, `backend/internal/handler/admin/account_data.go`.
- `~/refs/new-api`: `router/api-router.go`, `router/dashboard.go`, `controller/topup.go`, `controller/topup_stripe.go`, `controller/topup_creem.go`, `controller/topup_waffo.go`, `controller/topup_waffo_pancake.go`, `controller/subscription.go`, `controller/subscription_payment_epay.go`, `controller/subscription_payment_stripe.go`, `controller/subscription_payment_creem.go`, `controller/subscription_payment_waffo_pancake.go`, `controller/redemption.go`, `model/redemption.go`, `controller/user.go`, `model/user.go`, `controller/channel.go`, `controller/channel-test.go`, `controller/channel-billing.go`, `controller/channel_upstream_update.go`, `controller/payment_webhook_availability.go`.
- HUAKAI landing: `backend/cmd/gateway/routes.go`, `backend/cmd/gateway/wiring.go`, `backend/cmd/gateway/payment_routes_test.go`, `backend/internal/payment/order.go`, `backend/internal/payment/callback.go`, `backend/internal/payment/fulfillment.go`, `backend/internal/payment/admin_credit.go`, `backend/internal/paymenthttp/routes.go`, `backend/internal/paymenthttp/provider_hmac.go`, `backend/internal/adminhttp/balance_credit_handler.go`, `backend/internal/adminhttp/provider_catalog_handler.go`, `backend/internal/adminhttp/channel_catalog_handler.go`, `backend/internal/adminhttp/provider_account_health_handler.go`, `backend/internal/adminhttp/provider_account_test_handler.go`, `backend/internal/adminhttp/model_sync_handler.go`, `backend/internal/gatewayhttp/voucher_handler.go`, `backend/internal/voucher/service.go`, `backend/internal/voucher/types.go`, `backend/internal/provider/proxy_resolver.go`, `backend/internal/transport/mimicry/sidecar_client.go`.
- HUAKAI quota-subsystem: `backend/cmd/gateway/routes.go`, `backend/cmd/gateway/wiring.go`, `backend/internal/payment/provider.go`, `backend/internal/payment/service.go`, `backend/internal/payment/webhook.go`, `backend/internal/paymenthttp/handler.go`, `backend/internal/paymenthttp/webhook.go`, `backend/internal/subscription/service.go`, `backend/internal/subscription/activation.go`, `backend/internal/subscription/order_fulfillment.go`, `backend/internal/subscription/voucher_fulfillment.go`, `backend/internal/subscription/reminder.go`, `backend/internal/subscriptionhttp/handler.go`, `backend/internal/quota/service.go`, `backend/internal/quota/policy.go`, `backend/internal/quota/rate_window.go`, `backend/internal/subscriptionenforce/gate.go`, `backend/internal/routeadmin/service.go`, `backend/internal/routeadminhttp/handler.go`, `backend/internal/gatewayhttp/voucher_handler.go`, `backend/internal/voucher/service.go`, `backend/internal/voucher/types.go`, `backend/internal/panelauth/resolve.go`, `backend/internal/panelauth/types.go`, `backend/internal/panelauthhttp/handler.go`, `backend/sql/migrations/0076_user_role.up.sql`.

Inferred:

- `payment portal` 判定为缺口，是因为 HUAKAI 仅见分散充值/订单/余额 API，未见把配置、套餐、渠道、限额和支付恢复聚合成统一 checkout 的 endpoint。
- `真实支付渠道缺口` 判定基于 quota 分支注释与 provider registry 只含 manual/test 的源码证据。
- `商业运营总看板缺口` 判定基于现有 admin routes 分散存在、没有单一 dashboard 聚合支付/订阅/返佣/渠道运营。
- `affiliate 缺口` 判定基于 HUAKAI 只见 invitation，不见 rebate ledger/freeze/transfer 语义。
- `渠道 CRUD 缺口` 判定基于 landing catalog 只读与 quota routeadmin 不等于 channel CRUD。
- `P2-03 未完全覆盖` 判定基于 quota 分支拒绝 mismatched callback 但仍未写持久拒绝事件。
- `账号导入/proxy 管理缺口` 判定基于 HUAKAI 未见 sub2api 等价 import/export/proxy admin route。

Source files read: see `Observed` list above.
Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-06-02T10:22:35Z

中文摘要: 本次真实观察覆盖 sub2api 的支付、订阅、联盟、账号/代理/监控区域，new-api 的充值、订阅支付、兑换码、渠道运维区域，以及 HUAKAI landing/quota 两条线的支付、券码、订阅、配额、分组路由和面板代码。合理推断主要集中在“统一 portal 是否缺失”“真实渠道是否缺失”“运营总看板是否缺失”等由已读源码反向证明的缺口上；Open questions 共 4 个，主要是后续真实支付、退款/返佣账本和 channel CRUD 的 Owner 高风险决策。
