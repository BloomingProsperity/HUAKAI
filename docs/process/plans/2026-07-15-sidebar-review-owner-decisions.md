# 左侧栏逐项评审 · Owner 决定实录

日期:2026-07-15。方式:Owner 要求逐项大白话介绍,介绍一项定一项。本文只记**已拍板**项,全部过完后一次性出重构方案再实施。

## 运营台 · 网关资源组(原 13 项)

1. **模型服务(/models)→ 去掉**。Owner:「相当于模型广场,去掉。可以在 UI 首页显示:系统部署者设置了哪些 llm 厂商,首页 ui 就显示已支持/上架的厂商」。落法:运营台侧栏删该项;首页(用户门户概览/落地页)加"已支持厂商"展示,数据源=模型注册里启用模型的归属厂商,自动跟随后台配置;用户侧细节仍走既有"可用渠道"页。
2. **模型注册(/admin/model-registry)+ 上游账号(/accounts)+ 厂商同步(/admin/model-sync)→ 融合成一页**。Owner:「放一块!给他们融合一起」。落法:新「上游与模型」中心页,tab 分区=①上游账号(加账号:各厂商接入方式,OAuth账号转API/官方apikey 两种;既有 accounts 页整体迁入)②厂商同步(一键拉官方模型清单)③模型字典(能力/别名/主体CRUD,即原模型注册;C③ 的主体 CRUD 分区落进这个 tab)。对照:sub2 本就把"账号+每账号可用模型+上游同步"绑在账号编辑里,此融合与 sub2 心智一致且更集中。
3. **上游目录(/admin/catalogs)→ 也并入融合页**(作厂商同步 tab 内的原始清单列表)。**Owner 要求:该融合页的详细设计待逐项评审完后单独细讲**(页面级设计,细稿再过一遍才动工)。
4. **渠道健康(/admin/channel-health)→ 也并入融合页**。Owner:「这个也并入刚刚的账号一页。参考sub2」(sub2 在账号管理里直接展示每号状态/限流/异常)。融合页滚动终形=①上游账号(含健康状态列+手动体检)②厂商同步(含上游目录)③模型字典。渠道测试模板(第13项)大概率随健康并入,介绍到再定。
5. **账号池(/routing)→ 单独保留**。Owner:「账号池同意」。调度核心(模型→池绑定+权重/并发/兜底+强制pin),与"加账号"两种心智。
6. **路由规则(/admin/route-rules)→ 并入分组管理页,参考 sub2**。Owner:「路由规则融合一下 参考sub2」+确认总格局「参考sub2的格式 我们也这样放。用户单页,账号单页,经销商单页」。落法:分组详情里直接配"该组用户→模型模式→目标池"连线(保留我们的模型维度,入口挪进分组页,sub2 心智=组绑账号即路由)。
7. **总格局(Owner 拍板,对齐 sub2)**:账号单页(=融合页,对齐 sub2「账号管理」)/ 用户单页(用户管理独立)/ 经销商单页(分销商管理独立,分销 arc 落成后挂此)/ 分组页(含路由规则)。
8. **流量控制(/admin/quota-policies)→ 不单独保留,直接融合(sub2 式拆散)**。Owner:「不单独保留,直接融合」。落法:按组限速→分组页;按用户/按key限速→用户页/密钥管理;全局默认值→系统设置。后端 quota_policies 能力不变,只挪配置入口。
9. **按量付费与订阅 = 两个独立页面**(Owner 插入拍板)。用户与财务组:按量付费定价(模型单价/倍率)一页,订阅套餐管理一页,不合并。
## 运营台 · 用户与财务组

U1. **用户管理(/users)→ 独立保留(两层:列表页→详情页)**。详情页已有余额调整/用量/安全操作/改组。顺手:按用户/按key限速配置挪进详情页(流量控制拆散那条)。
U2. **订单管理(/admin/orders)→ 砍掉,充值/退款并进用户页,对齐 sub2**。Owner:「参考sub2 直接放到用户那一块。手动增加和减少用户余额也是可以的」。实证:sub2 无独立订单/支付台,admin 直接 POST /users/:id/balance 加减余额+余额历史;HUAKAI 已有对等(用户详情 UserBalanceAdjust + balance_credit_handler.go,金额非零/reason 非空/幂等键防重复扣)。落法:侧栏删订单管理;充值=用户详情页 admin 手动加/减余额(带原因+幂等);自动支付渠道/退款审批队列后端保留、前端收起(Owner 模式=预付+手动 admin 充值,分销商线下自收,真支付非上线 blocker)。

U3. **订阅管理(/admin/subscriptions)→ 独立保留,对齐 sub2**。Owner:「都要的。参考sub2」(按量+订阅并存)。sub2 实证:subscription_plans 套餐表 + user_subscriptions + 套餐绑组(groups.subscription_type)+ admin 套餐CRUD/订户/进度/手动+批量指派 + usage_logs 挂 subscription_id。落法:订阅管理页=套餐CRUD+订户管理+用量进度+指派;与"按量付费定价"两页并存(Owner 前定"按量付费和订阅分两页")。
U4. **定价设置(/admin/pricing)= 官方价×倍率模式,对齐 sub2 的 LiteLLM 定价链**。Owner:「价格固定,按照官方标准。我们只控制倍率,1倍就是官方原价」。sub2 实证:定价链 Channel→LiteLLM→Fallback,官方标准价来自 **LiteLLM 社区维护的全模型价表**(Claude/GPT/Gemini 全覆盖,官方调价跟随),分组 rate_multiplier 叠倍率;channel_model_pricing 可覆盖(models jsonb + input/output/cache/image price,NULL=用默认)。落法:
    - **内置 LiteLLM 官方价表作底价**(clean-room:LiteLLM MIT,model_prices json 可用);现有 billing_pricing_versions 改为"官方价 fallback + 可选覆盖",pricing_ratio 倍率叠加(1倍=官方原价)。
    - 定价页极简=**只调倍率**(全局/按组/按模型覆盖);不做逐模型手工录价页。
    - **✅ 直接解决 MVP S1 blocker #1**:Claude/GPT/Gemini 内置官方价→不再 fail-closed;无需手工 SQL 种价、无需 admin 录价写口。
    - ⚠ 动钱=Owner 上线切片时点头;方向已由本条锁定。

## 运营台 · 网关资源组(续)

10. **出口代理(/admin/proxies)→「IP 代理」页;TLS 指纹(/admin/tls-fingerprints)→ 侧栏删项、后端默认开、无页无开关。对齐 sub2**。Owner:「启动rust模块...命名为IP代理」+「就参考sub2:代理线路清单+账号各自挑线,不挑就直出,TLS 指纹独立管;TLS 指纹不需要单独页面也不需要开关,默认开启就行」。落法:
    - 侧栏一项「IP 代理」= 纯代理线路清单(对齐 sub2 proxies 表:name/protocol http-https-socks5/host/port/账密/status)+ 每账号各自挑线(C① 已有)+ **不挑=原始 IP 直出(默认)**。
    - **TLS 指纹:后端独立管(按号绑,像 sub2 独立 profile),前端不给页、不给开关、默认启用**;侧栏删原「TLS 指纹配置」项。Rust tls-sidecar 数据面默认跑。⚠ 默认行为翻转+启用 Rust 运行时=Owner-gated,已由本条明确授权。
    - sub2 实证:proxies 表 + accounts.proxy_id 每号选线 + backup_proxy_id 兜底;TLS 指纹 sub2 独立 profile 按号绑、不暴露给用户。

## 用户门户 · 组重构(Owner 拍板)

U5. **兑换码管理(/admin/vouchers)→ 运营台侧保留(admin 生成卡密:单张/批量/吊销/批次,明文码仅生成时返回一次),归用户与财务组**。真码:voucher_handler.go MountVoucherAdminRoutes,platform_admin。Owner 模式契合:预付+发卡卖码、不碰支付通道。
UP1. **用户门户新增「活动中心」组**:每日签到 + 邀请返利 + 推广返利。
    - **签到**:后端现成(checkin 包),得**余额**(reward_cents 走 billing_event),金额**部署者可设**——platform_settings `KeyCheckinMinCents`/`KeyCheckinMaxCents`(默认 1~20 分随机,**固定金额=min设成等于max**),开关 `KeyCheckinEnabled` 默认关。
    - **邀请返利**:后端现成(referral/invitation 包),返**余额**(reward_type+amount_usd),开关 `KeyReferralRewardEnabled`。
UP2. **「充值中心」归运营台财务组(admin 配置聚合),非用户门户**。Owner 修正:「充值中心应该放到财务这块」「充值本质是财务」+确认。归属:
    - **运营台·用户与财务组 →「充值中心」**:admin 配收款方式(**实时收款/自动支付渠道配置**,paymenthttp providers config 后端已存)+ 发卡(兑换码生成)+ 看账。上线初期实时收款前端收起,配了才启用。
    - **用户门户侧**:不叫"充值中心",就是钱包里的充值/兑换码兑换/我的订单入口(用户自助)。
    - 签到金额/返利开关→系统设置。
U7. **退款与扣费争议(/admin/disputes)→ 后端留着,前端不显示(收起)**。Owner:「后端留着 前端不显示」。争议裁决台(客户发起申诉→admin 裁决支持退款/驳回,operator_note≤4000,状态流转防乱改)偏客服/合规,上线初期客户量小用手动加减余额即可解决;后端 dispute_handler.go 全保留,前端侧栏删项,需要正式申诉流程时再亮。
U6. **用量与计费台账(/admin/billing-claims)→ 保留,改名「使用记录」,补 sub2 报表能力**。Owner:「sub2是啥样?使用记录?」。sub2 实证(admin UsageView.vue):主打图表报表——趋势图(天/小时)+模型分布/分组分布/端点分布饼图+Excel 导出+**三份模型对照 requestedModelStats/upstreamModelStats/mappingModelStats**(也有防掺水对照)+可选"按最终上游模型计费"。落法:保留 HUAKAI 逐笔台账强项(每笔走哪个真号,sub2 只聚合不逐笔)+ 补 sub2 趋势图/分布图/导出;命名「使用记录」。战略价值:requested_model vs upstream_model 两列证不掺水(市场影子API 45%偷换模型),分销商只见自己那摊(透明隔离)。
    - **Owner 追加维度(2026-07-15)**:「不光看用户使用记录/模型调用记录/IP/客户端,也看部署者生成的 key 使用记录」。现状核实:usage_records 已记 user_id/api_key_id(逐key)/requested_model/upstream_model/provider_account_id/**user_agent(客户端,0112加)**;**唯一缺 IP**(早期 by-design 不记,accesslog 也故意不记 IP)。**待办(schema,Owner 已要求)**:usage_records 补 client_ip 列(≤512 CHECK,照 user_agent 范式)+ 使用记录页加 IP 列/筛选;与安全监测 security_events.source_ip 联动(同一 IP 用量 vs 是否爆破)。sub2 usage_logs 同级(user_agent+request_id 关联),IP 两家早期均不记全量;补 IP 后 HUAKAI 更全。
U8. **分销管理(/admin/affiliates)→ 经销商单页,对接分销 arc**。现为扁平 affiliate 推广返利管理,将来=分销商列表/建站/停用/设批发价/划账号(上帝视角管);分销商自己登录=独立第三套壳(二级管理员 UI),不在本运营台。分销 arc 后端建好后填充,现占位保留。

**用户与财务组小结(6 项)**:用户管理(含限速)/ 使用记录(原计费台账+报表)/ 订阅管理 / 按量付费定价(官方价×倍率)/ 充值中心(admin 配收款+发卡+看账)/ 经销商。砍:订单管理(并入用户余额)、退款争议(前端收起)。

## 运营台 · 安全与审计组

S1. **凭证续期(/admin/credential-renew)→ 并入账号(融合页),对齐 sub2**。Owner:「凭证续期不应该放到账号里面吗?像sub2一样」。sub2 实证:凭证刷新全挂账号操作(accounts/:id/refresh、batch-refresh、refresh-tier、apply-oauth-credentials),无独立续期页。落法:融合页账号行加"刷新凭证"单个+批量按钮,侧栏删独立项。
S2. **审计日志(/security)→ 独立保留,HUAKAI 比 sub2 更集中**。Owner 追问「审计日志是啥」。是啥:操作留痕本(admin 敏感操作 append-only,查责追溯)。sub2 实证:无统一审计大页,按业务拆散(payment_audit_logs/deleted_api_key_audits/系统日志清理审计等分散各模块)。HUAKAI:统一 admin_audit_events 总表+总页,更集中好查,保留。是安全监测台数据源之一。
S3. **新增「安全监测台」放本组第一项**(Owner 主线需求,方案 2026-07-15-security-monitoring-module-claude.md):攻击态势(爆破/封IP/429洪峰)+系统资源(CPU/内存/磁盘)+安全事件流,一屏。
S4. **风控总览(/admin/risk)→ 并进安全监测台**(Owner:「你的建议通过」)。核实:HUAKAI 风控总览=只读态势仪表盘(封禁key/触发告警/封号用户/IP黑名单key 计数);**sub2 无此态势面**(sub2 的"风控中心"实为内容审核);态势信号与安全监测台同类,合并不单列。
S5. **内容审核(/admin/moderation)→ 独立保留 ≈ sub2「风控中心」**。核实:HUAKAI 审核配置+关键词黑名单(单条/批量)+违规哈希库+命中日志+违规自动封key(ban_counter)+解封;sub2 risk-control 一一对应(配置/测key/日志/封解封/哈希),HUAKAI 多关键词批量导入。命名保留"内容审核"(比"风控"准)。

**安全与审计组小结(3 项)**:安全监测台(新,吃进风控总览)/ 审计日志(保留)/ 内容审核(保留)。移出:凭证续期→账号融合页。

## 运营台 · 设置组

SET1. **系统设置(/system,SettingsCenterPage)→ 保留(聚合配置中心)**。60+ platform_settings 键一页分区:登录注册/验证码/邮箱域名/邀请码/签到金额(KeyCheckinMin-MaxCents)/返利开关/限速默认(RPM并发)/媒体任务/内容审核外部API/OAuth/Passkey/支付配置/配额探测等。前定的签到金额/返利开关/限速默认都归此。对齐 Owner「复杂设置聚合一页」;sub2 设置散在 ops/高级设置多处,HUAKAI 更集中。
    - **sub2 系统设置全貌(穷尽抓)**:①第三方登录(GitHub/Google/钉钉/微信公众号+移动+PC扫码 OAuth 全套)②**登录条款文档**(服务条款/隐私/使用政策 Markdown+登录强制同意+更新重新同意)③邮件(SMTP+模板编辑器)④管理密钥⑤relay 运行调优(过载/429冷却/流超时/整流器/beta/联网搜索模拟)⑥告警/日志/指标阈值⑦支持的国家地区。
    - **Owner 拍板补齐缺项(全交 codex,逐项核 HUAKAI 现状)**:「登录条款文档管理必须加!缺的补上」。必补:**①登录条款文档管理(服务条款/隐私政策 Markdown+登录强制勾选同意+条款更新重新同意)=合规刚需** ②邮件模板编辑器(可视化改通知邮件)③relay 运行调优补齐(整流器/联网搜索模拟等)④支持的国家地区 ⑤SMTP 从版本页挪入。
    - **建议分区(Owner 修正:系统设置=配置参数,非操作界面;操作界面都在独立页/组,不重复)**:登录与注册(注册开关/验证码/密码策略) / 第三方登录(OAuth 配置) / **法律与合规(条款文档,必加)** / 邮件(SMTP+模板) / **全局参数**(签到金额+返利开关+限速默认+预算+配额探测+媒体参数+外部审核API开关,纯配置) / Relay 运行调优(冷却/超时/整流)。
    - **关键区别(Owner 点明)**:系统设置放"配"(填数值/开关),前面已定的独立页/组放"用/管"——签到界面在活动中心组、内容审核规则管理在安全分组独立页、媒体任务在用户门户;系统设置里只有它们的**参数/开关**,不重复列操作页。删掉上一版误导的"活动与营销""内容审核·媒体"分区名。
    - **Relay 运行调优 → 隐藏内置,不给 UI(Owner 拍板)**:「能不能直接隐藏内置?直接编码写好固定那几个」。冷却/429/流超时/整流这些技术参数**代码内置合理默认,前台不显示配置**;保留 env 环境变量 override 作后门(运维万一需调可改,不暴露前台)。系统设置分区去掉此项。**最终分区**:登录与注册 / 第三方登录 / 法律与合规 / 邮件 / 全局参数。
SET-版本. **版本与维护(/admin/version)→ 菜单拿掉,拆分**。真相:该页塞了版本号(只读构建信息)+ SMTP 邮件设置。落法:**SMTP 挪入系统设置的「邮件」分区**(Owner 认可「smtp肯定归系统设置」);版本号收成页脚/关于一行;菜单项删除。sub2=系统里有版本+可回滚版本列表。
SET2. **模块开关(/admin/modules,ModuleRegistryPage)→ 侧栏拿掉,后端保留(喂 Hermes)**。Owner:「拿掉吧,后端代码有就行,都是配置hermes的」。真相:非功能开关,是**只读"模块体检花名册"**——20+ 模块按类别(money-path billing/routing 选号+路由规划/credentials/channel-health/reliability DLQ/registry 模型+同步/payments/subscription/promo voucher/auth 密码社交+passkey+2fa)登记身份+能力+实时探针,主消费者=Hermes AI 运维助手自省诊断。真正功能开关在系统设置的 xxxEnabled 键。sub2 无此自省视图。

SET-缓存. **缓存管理(/admin/cache)→ 并入定价页,两套独立倍率(Owner 拍板)**。缓存管理真相=缓存计费价格覆盖(非清缓存):提示缓存的写入价/读取价(Anthropic 缓存读约比输入便宜10x)。Owner 定:「要分开。缓存和输入输出都默认1倍,都是官方价。两个价格各有能调倍率的窗口」。落法:并入按量付费定价页;**缓存价倍率窗口 + 输入输出价倍率窗口分开**,各默认 1.0(官方价),各自可调、**不同步**。真码现状已符合(pricingeval/cache_override.go 的缓存倍率独立于分组倍率,只缩放 cache 两段;TestApplyCacheCostOverride_ScalesCacheAndAdjustsTotal 证 1.5x 只动缓存),**无需改计费逻辑**,只需 UI 给两个倍率窗口。sub2=cache_write/read_price 绑渠道定价内。

SET-备份. **备份与恢复(/admin/backup)→ 应用内真备份(主力)+ 云快照(文档),两个都做**。Owner:「那两个都做吧。很多人不会买aws/微软/谷歌云的服务器,也不会买这个快照」。现状缺口:HUAKAI 现只有只读 manifest(backuphttp),**无真备份/恢复/导出**;sub2 有 CreateBackupJob/ListBackupJobs + accounts/proxies ExportData;new-api 无(只 2FA 备用码)。落法:
    - **应用内(刚需,对齐 sub2)**:一键备份(导出全库关键数据:用户/账号/配置/账单)+ 恢复(导入)+ 分项导出(账号/代理/配置,搬家迁移用)。**理由:开源自部署者多用普通 VPS 无云快照**。
    - **云快照(部署文档)**:AWS RDS 自动备份 / EBS 快照,几乎不占服务器资源+便宜,用云者额外保险。
    - **安全硬门**:备份含敏感数据(凭证)→ 导出必加密或脱敏(照 secret-mask,不导明文凭证)、恢复入口鉴权防越权、导入校验防注入。=schema/数据导出+安全敏感,codex 实施带判别测试。

SET-Hermes. **Hermes(AI 运维助手)→ 配置页展示真实运行合同 + 两大产品化扩展(Owner 拍板)**。后端当前权威执行计划见 `2026-07-22-hermes-ops-and-codebase-convergence-codex.md`。
    - **配置页展示真实合同**:展示启停与模型身份、工具循环和模型提议总开关、按当前身份动态返回的工具目录、安全边界和巡检配置。不得继续展示已删除的 `ADMIN_ONLY` 开关，也不得在前端硬编码工具矩阵；当前完整注册表为 15 个只读工具和 8 个改动型工具，实际可见项由角色、租户授权、`Proposable` 和运行时开关共同决定。
    - **扩展① 每日巡检+主动邮件预警(Owner 新需求)**:确定性巡检已归入 `opsinspection`，默认关闭，使用 `HUAKAI_DAILY_OPS_INSPECTION_ENABLED`、`HUAKAI_DAILY_OPS_INSPECTION_INTERVAL` 和 `HUAKAI_ADMIN_NOTIFICATION_EMAIL`。它独立聚合账号池、凭据、死信、错误趋势和模块状态，不由模型自治执行；后续前端只配置和展示这份真实状态。
    - **扩展② 内联 Hermes 解释按钮(Owner 新需求,核心小白友好亮点)**:报错/账号方块/日志条目旁放**通用小按钮组件**,点击→按上下文调 Hermes 诊断工具→模型用**大白话翻译**错误出处、含义和处理办法。目录按角色和租户过滤；可逆改动只允许生成预案，真正执行仍需运营者独立确认，普通用户无 Hermes 入口。
    - **定位升华**:Hermes 从"运营专家工具"→"小白友好的系统解释器 + 主动预警助手",差异化护城河。
    - 公告管理(/admin/announcements)保留(平台通告墙);站内信广播(/admin/broadcast)保留(群发私信,与公告不重复)。

SET3. **平台凭证(/admin/platform-credentials)→ 保留但易懂化**。Owner:「保留吧,但是有点复杂,不是很好懂」。管平台管理侧钥匙:admin token(程序化调管理 API,带 scope,可吊销)+ 平台 API key。区别于上游账号凭证(账号页)、客户 API key(用户/密钥)。是分销 arc token-only 写口+自动化基础。落地要求:**加大白话用途说明+高级细节折叠+术语通俗化**,非技术 Owner 一看就懂。sub2=单一远程管理密钥;HUAKAI 多 token+scope 更细。

## 运营台 · 观测与运维组(重组)

O1. **运维监控面板**(参考 sub2 DashboardView)= 系统监控 + 检测台并入 + 告警中心并入(tab)。只显紧急态势(三层降噪规则见 security-monitoring 方案)。
O2. **日志模块面板** = 全量明细(运行/错误/上游错误/攻击行为 + 审计操作日志),审计级可搜可导。
O3. **死信队列(/admin/dlq)+ 孤儿对账(/admin/orphan-reconcile)→ 归进日志组,改名**。Owner:「刚刚那个建议很好,直接放到日志组里,名字不好听」。是啥:钱账兜底——DLQ=扣费结算失败自动重试队列(正常应空,不空=有钱卡结算);孤儿对账=扫"预扣了但请求崩了、既没结算也没释放"的挂空孤儿钱,释放/补偿回去。sub2 无此(HUAKAI 钱账更扎实)。**Owner 定名(2026-07-15)**:死信队列→**「结算重试队列」**、孤儿对账→**「资金对账」**,两项分开,归日志组。
