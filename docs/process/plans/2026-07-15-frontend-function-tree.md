# HUAKAI 前端功能树 · 逐页设计交付蓝图

交第三方 AI 逐页重新设计。全部页面、功能点、数据字段、后端端点均取自当前分支真码(feature 目录 Page.tsx + api.ts + types.ts),非臆测。金额约定:`*_cents` 为整数分、`*_usd`/decimal 为字符串(防浮点丢精)、`*_micro_usd` 为微美元。

---

# 第一部分 · 代码与技术选型

## 技术栈
- **框架**:React 18.3(`useSyncExternalStore` 做状态、`StrictMode`、新版 JSX 转换无需 import React)。
- **构建**:Vite 5.4(dev server + API proxy;`manualChunks` 把 react/react-dom/react-router-dom 拆 vendor chunk 长期缓存)。
- **语言**:TypeScript 5.6,`strict` 全开 + `noUnusedLocals/Parameters`;`tsc -b && vite build`——**类型不过则构建失败**。
- **路由**:react-router-dom 6.28,`createBrowserRouter` 数据路由 + `RouterProvider`。
- **零重依赖(刻意路线)**:无 Redux/Zustand、无 React Query/SWR/axios、无 Recharts/ECharts/D3、无 Antd/MUI、无 Tailwind。状态/请求/图表/组件/样式**全部自研**。
- **测试**:vitest(单测,157 文件=135 纯逻辑 + 22 组件,组件用 `renderToStaticMarkup` + `MemoryRouter` 无 jsdom)+ Playwright(e2e/ 独立)。

## 项目结构(feature 垂直切片)
```
src/main.tsx            入口 createRoot
src/app/  App.tsx / router.tsx(路由表)/ nav.ts(导航单一真相源)
src/auth/ store.ts(token)/ tokenForPath.ts / me.ts(切壳)/ RequireAuth.tsx / 登录页
src/lib/  api.ts(fetch 唯一基座)/ hermesStream.ts(SSE)
src/ui/   跨域复用组件(设计系统)
src/shell/ AppShell / TopBar / PipelineNav
src/styles/ tokens.css / global.css / components.css
src/features/<域>/  80+ 业务域,每域五件套
```
**feature 五件套(约定优于配置)**:`XxxPage.tsx`(路由入口)+ `api.ts`(只调 lib/api 封装本域端点)+ `types.ts`(镜像后端 JSON)+ `<域>.ts`(纯逻辑:映射/校验/分页,可单测)+ `*.test.ts`。**纯逻辑与 IO/UI 严格分离**(便于变异测试)。模态由各域自实现(`CreateKeyModal` 等),用 `useState` 管开合——**无通用 Modal 组件(重做时可抽公共)**。

## 数据层(lib/api.ts,全站唯一 fetch 基座)
- `apiGet<T>(path,opts)` / `apiSend<T>(method,path,payload,opts)`——覆盖全部读写;`opts`:bearer / query / signal(AbortSignal 供卸载取消)。
- **同源相对路径**(`API_BASE=''`,dev 靠 vite proxy);**错误归一** `ApiError{status,code,message}`(后端 `{error:{code,message}}`);**鉴权头** `authHeaders()` 按 `tokenForPath` 选 token 注入 `Authorization: Bearer`,默认带 cookie。
- **session 主动刷新**:请求前若走 session token 且距到期 <2 分钟,先 single-flight 换新(失败静默不阻断)。

## 三态鉴权(auth/tokenForPath.ts)
| 路径 | token |
|---|---|
| `/v1/auth/login,/register,/reset-password,/verify-email,/oauth,/passkey` 等公开认证 | 不带 |
| 其余 `/v1/auth/*`(/me、/2fa、/logout) | session |
| `/admin/*`、`/v1/admin/*` | admin 优先,回落 session |
| 其余用户态(`/v1/me/*`、`/v1/api-keys`) | session |

## 切壳(auth/me.ts + shell/AppShell.tsx)
- 权威来源=后端 `/v1/auth/me` 的 `panel` 字段(来自 users.role,**绝不信前端声明**)。四态 idle/loading/ready/degraded。
- **deny-by-default**:唯 `ready && panel==='admin'` 才启用运营台(可见 user+operator 两壳);加载/降级/user 一律仅用户壳(降级绝不默认 admin 防提权,也绝不空壳防白屏)。
- 布局:`TopBar`(品牌 + Cmd-K + 用户/登出)+ `PipelineNav`(单侧栏,`nav.ts` 数据驱动)+ `<Outlet/>`;**运营台壳叠加 Hermes 面板**(Cmd/Ctrl-K 唤起)。
- `nav.ts`:`NavItem{path,label,icon,built}` + `NavSection{key,shell:'user'|'operator',label,items}`;`PIPELINE_NAV` 是路由与侧栏的**单一真相源**;图标用 Unicode 符号(无图标库,重做可换专业图标集)。

## UI 组件层(src/ui/,只引 CSS 变量 token 禁魔法色值)
`DataListTable`(泛型表:列定义/行操作 link|button 带 danger·disabled·visible/多选全选半选)、`StatusBadge`(ok/warn/danger/muted/info + healthTone 账号健康映射)、`EmptyState`(空态主次操作)、`Donut`(自研 SVG 环形图)、`Sparkline`(自研 SVG 迷你折线)、`StatCard`(指标卡+内嵌 sparkline)、`ResourceCard`、`Skeleton`(骨架含 reduced-motion 降级)、`ErrorFallback`(路由级错误边界)、`confirmDanger`(不可逆二次确认)、`metricFormat`(BigInt 金额格式化防丢精)。

## 构建与部署
`npm run build`(tsc 前置门 → vite → dist/)→ 后端 `webui/embed_on.go`(`//go:build embed` + `//go:embed all:dist`)打进网关单二进制;`webui.go` 挂 NotFound handler,`IsAPIPath` 守卫 API 前缀返 404、其余非 API 回退 index.html(SPA 深链接存活)。`base:'/'` 硬约束不可改相对路径。

## 设计系统现状(重做基线)
`styles/tokens.css` 变量前缀 `--hk-*`(如 `--hk-primary-500`、`--hk-ink-900`、`--hk-line`、`--hk-space-*`、`--hk-radius-*`);组件内联 `style` 引用。**重做 = 重建这套 token(反克隆,自有设计语言),保留组件层结构与数据层。**

---

# 第二部分 · 用户门户功能树(session token,19 页)

导航 4 组:概览 / 我的账户 / 用量与计费 / 更多。身份后端从会话派生,前端从不传 user_id。

## 概览 `/overview`
- **功能**:顶部公告横幅(可按 id 关闭存 localStorage);四张独立加载独立降级卡——配额窗口(进度条+度量口径+已用/上限+状态徽章正常/接近/超额)、我的密钥(总数+活跃,跳 /keys)、近段用量简图(按活跃 key 花费归一化条形 SVG)、快捷入口(4 链接)。
- **字段**:`QuotaWindow{metric,window_kind,cap,consumed,remaining,overage,request_count,window_start,window_end}`、`ApiKeyView{api_key_id,name,key_prefix,status}`、`KeyUsageSummary{total_cost,total_tokens_input/output,total_cache_read/creation_tokens,request_count}`、`UserAnnouncement{id,title,body,severity,published_at,expires_at}`。
- **端点**:`GET /v1/me/quota`、`/v1/api-keys`、`/v1/me/keys/{id}/usage-summary`、`/v1/announcements`。

## 我的密钥 `/keys`
- **功能**:顶部 3 StatCard;**＋新建 Key**(名称≤128 / 环境 live·test / 过期预设+自定义日期 → 创建 → 一次性明文视图:明文+前缀+复制+一键接入面板 KeyIntegrations+「已保存关闭」);列表行「编辑/管理」「撤销(确认)」;**批量选择→批量撤销**(结果汇总 revoked/not_found);分页(每页下拉+上下页)。**编辑弹窗**:启停切换(确认)/名称/到期三态(保持·永不·设定日期)/高级折叠 5 项各独立 GET 回填+PUT 保存——①用量上限配额(计费敏感:上限/度量金额或次数/窗口日周月固定/超限模式,确认)②分组绑定③IP 白名单④IP 黑名单⑤模型白名单。
- **字段**:`ApiKeyView{...,expires_at,last_used_at,revoked_at,revoked_reason,created_at}`、`CreateKeyResponse{plaintext,key_prefix,status,notice}`、`KeyQuotaView{limit_usd,metric,window_kind,window_seconds,mode,used_usd,remaining_usd,window_end}`、`KeyGroupView`、`KeyIPListView`、`KeyModelAllowlistView`。
- **端点**:`GET/POST /v1/api-keys`、`DELETE|PATCH /v1/api-keys/{id}`、`/batch-revoke`、`/{id}/{quota|group|ip-allowlist|ip-blacklist|model-allowlist}`。

## 个人资料与安全 `/profile`（8 卡）
- **① 账号资料**:面板徽章/账号 ID/显示名,内联「修改」(`PUT /v1/auth/me/profile`)。
- **② 登录密码**:当前/新(≥8)/确认 → 修改(登出其它会话,`POST /v1/auth/me/password`)。
- **③ 两步验证 TOTP**:状态+剩余备用码;开启→secret+URI+一次性备用码→输 6 位确认;已开→输码关闭/重生备用码。`/v1/auth/2fa/{status|setup|enable|disable|backup-codes/regenerate}`。
- **④ 登录会话与设备**:设备族列表(状态/摘要/最近活跃/撤销原因),撤销(确认,撤当前会登出自己)/刷新。`POST /v1/sessions/{list|revoke}`;`SessionFamily{id,status,generation,created_at,last_active_at,device_info,ip_baseline,revoked_at/reason}`。
- **⑤ 通行密钥 Passkey**:名称+二次验证(密码或 2FA 码)→添加(WebAuthn create,2FA 分支再输动态码);列表(名称/时间/克隆风险),删除(step_up)。`/v1/me/passkeys/*`;`PasskeyItem{id,name,transports,attestation_type,clone_warning,sign_count,last_used_at}`。
- **⑥ 社交账号绑定**:已绑 provider(subject 脱敏+时间),解绑(末位登录方式后端 409);未绑 Telegram 显 Login Widget。`GET/DELETE /v1/users/me/oauth-bindings`、`POST .../telegram`。
- **⑦ 通知偏好**:渠道下拉(none/email/webhook/bark/gotify)按渠道显字段 + 低余额阈值 USD + 抄送邮箱(≤10);secret 只回「已配置」不回显。`GET/PUT /v1/users/me/notifications`;`NotifyPrefsResponse{notify_type,webhook_url,webhook_secret_configured,notification_email,bark_url,gotify_url,gotify_token_configured,gotify_priority,balance_threshold,extra_emails}`。
- **⑧ 注销账号(危险区)**:二次确认→软删+撤全 session(末位管理员后端 409)。`DELETE /v1/auth/me`。

## 站内信 `/notifications`
- **功能**:未读角标(>99 显 99+);全部/未读分段;卡片(未读高亮+加粗)含「标记已读」(乐观更新失败回滚)。
- **字段**:`UserNotification{id,title,body,severity,read_at,created_by_admin,created_at}`+`count`。**端点**:`GET /v1/notifications`、`/unread-count`、`POST /{id}/read`。

## 安全日志 `/activity`
- **功能**:当前用户敏感操作审计列表(签发/撤销 Key、登录、2FA、Passkey);刷新;offset/limit「加载更多」。
- **字段**:`UserAuditEvent{occurred_at,action,outcome,key_prefix,api_key_id,reason,request_id}`。**端点**:`GET /v1/me/audit-events`。

## 使用记录 `/usage`
- **功能**:3 统计卡;配额窗口分段方格进度条+重置倒计时;各密钥用量汇总表;**Key 级分析区**(粘贴 API Key,仅内存)——工具条(Key/起止日期 UTC≤31 天/粒度日周月/查询/清除)+缓存命中方格条+用量热力图+缓存热力图+费用与 Token 时间序列条形+按 request_id 查单笔+逐笔请求表(游标加载更多)。
- **字段**:`KeyUsageTimeSeriesPoint{day,requested_model,total_cost,tokens{input,output,cache_read,cache_creation},request_count}`、`KeyUsageRecord{requested_model,upstream_model,actual_cost,tokens,provider,provider_account_id,ledger_id,created_at,status,request_id,stream,latency_ms}`。**端点**:`GET /v1/me/quota`、`/v1/me/usage`、`/v1/me/analytics/time-series`、`/v1/generation?id=`(后三显式 API Key Bearer)。

## 用量明细 `/usage-records`
- **功能**:统计卡+刷新;跨全部 Key 逐请求列表(游标);**CSV 导出**(起止日期≤366 天,`GET /v1/me/usage/export.csv` blob);行「成本详情」展开→下钻(成本明细+**签名成本收据**拉 `/v1/receipts/{request_id}`含验签按钮+**发起账单争议**填原因≤4000 二次确认建 pending);页尾**我的争议列表**。
- **字段**:`UserCostReceipt{schema_version,receipt_sequence,occurred_at,cost{model,input/output/cached_tokens,cost_total_micro_usd,rate_table_snapshot_id},validation_state,verdict,canonical_hash,signature,pubkey_fingerprint}`、`Dispute{dispute_id,request_id,reason,status,operator_note,created_at,resolved_at}`。**端点**:`GET /v1/me/usage-records`、`/export.csv`、`/v1/receipts/{id}`、`POST /{id}/verify`、`/{id}/disputes`、`GET /v1/me/disputes`。

## 钱包与充值 `/wallet`
- **功能**:余额卡+累计已充值卡;**充值卡**(金额区间校验+预设快捷+支付方式→创建订单二次确认→展示人工支付指引);最近订单卡。
- **字段**:`UserBalance{amount_cents}`、`PortalTopupConfig{min/max_topup_cents,preset_amount_cents[],currency_code,providers[{provider,instruction}]}`、`CreateTopupResponse{order,payment_instruction}`。**端点**:`GET /v1/users/me/payments/{balance|config|orders}`、`POST /orders`。

## 我的订单 `/orders`
- **功能**:最近条数下拉+刷新;状态筛选 chip 带计数;列表行「撤单(pending)/申请退款(已完成充值)/详情」;**详情抽屉**(字段+状态流转时间线+撤单/退款+收据查看下载 .txt)。
- **字段**:`UserOrder{out_trade_no,amount_cents,currency_code,status,provider_kind,order_kind,subscription_plan_id,created_at,expires_at,paid_at,completed_at}`、`RefundRequestView{id,order_id,status,reason}`。**端点**:`GET /v1/users/me/payments/orders[/{id}]`、`POST /{id}/{cancel|refund-request}`、`GET /v1/me/orders/{id}/receipt`。

## 订阅套餐 `/subscriptions`
- **功能**:我的订阅卡(状态/有效期/自动续订/权益组+日周月配额进度条+重置倒计时);自助（关闭续订确认/换套餐仅升级确认）；订阅历史表；**在售套餐网格 PlanCard**(名称/简介/价/有效期/日周月上限/权益组+购买二次确认建单显支付指引)。
- **字段**:`PlanView{id,name,description,price_cents,validity_days,granted_group,daily/weekly/monthly_cap_usd,for_sale,enabled,sort_order}`、`SubscriptionView{...,status,starts_at,expires_at}`、`SubscriptionProgressView{window_kind,cap,consumed,remaining,overage,usage_percent,resets_in_seconds,over_limit}`。**端点**(`/v1/users/me/subscriptions`):`GET /plans,/me,/me/progress,/`、`POST /purchase,/cancel-renew,/change-plan`。

## 兑换码 `/redeem`
- **功能**:兑换码输入(Enter 提交)+兑换(idempotency_key,成功轮换 key);成功/失败/限流提示;`promoEnabled=false` 禁用;兑换历史表。
- **字段**:`RedeemResult{voucher{amount_cents,status},redemption{voucher_id,amount_cents,redeemed_at},balance_cents,subscription?}`。**端点**:`POST /v1/users/me/vouchers/redeem`、`GET /v1/me/voucher-redemptions`。

## 分组与倍率 `/my-groups`
- **功能**:刷新；当前等级徽章(user_group)；可调度模型分组表(名称/ID/倍率仅运营公开时显示否则「未公开」)。
- **字段**:`MeGroupItem{pool_group_id,name,ratio?,has_public_ratio}`+`user_group`。**端点**:`GET /v1/me/groups`。

## 接入指引 `/integration`
- **功能**:网关地址(自动取 site_api_base_url)+API Key(可选内存)→生成 3 代码卡(Claude Code/OpenAI SDK Python/curl)各「复制」。**端点**:`GET /v1/site/config`。

## 在线调试台 `/playground`
- **功能**:真实计费警告条;9 协议 tab(Chat/Completions/Claude Messages/Responses/Embeddings/Rerank/Images/Speech/Gemini v1beta);认证与模型卡(API Key 内存+模型 datalist+加载可用模型);请求卡按协议动态字段+流式开关;发送/取消,流式显示 SSE 文本+原始事件、非流式显示解析文本+usage+原始 JSON、embeddings/rerank/images/speech 专门渲染。
- **端点(API Key Bearer)**:`GET /v1/models`、`POST /v1/{chat/completions,completions,messages,responses,embeddings,rerank,images/generations,audio/speech}`、Gemini 路径。

## 媒体任务 `/media-tasks`
- **功能**:4 tab(总览/Midjourney/Suno/视频);总览刷新+新建任务(类型/模型/Prompt/高级 JSON)+进行中每 5 秒轮询+表格(任务#/类型/提供方/状态/进度/计费/时间);MJ 控制台(11 动作白名单+Base64 图+InsightFace 换脸+查询);Suno 控制台(歌词/风格/器乐/自定义+动作+查询);视频控制台(模型/时长/Prompt/参考图+查询+最近列表)。
- **字段**:`MediaTask{task_type,status,provider,provider_task_id,request_id,input_params,result,estimated_cents,actual_cents,error_class,progress,finished_at}`。**端点**:`GET /v1/media-tasks[/{id}]`、`POST /v1/media-tasks`、`/mj/*`、`/suno/*`、`/video/*`。

## 可用渠道 `/available-channels`
- **功能**:搜索(模型/canonical/厂商)+价格单位切换($/1M↔$/token)+按厂商聚合可展开卡(渠道名/模型数/输出价区间/能力徽章)展开表(模型/模式/输入价/输出价/上下文/能力)。
- **字段**:`PricingItem{model,canonical_id,owned_by,mode,input/output_price_per_token,context_length,max_output_tokens,capabilities{}}`。**端点**:`GET /v1/pricing/page`(公开)。

## 信任验证 `/trust`（3 区）
- **① 平台公钥**:当前签名公钥(算法/指纹/状态/时间/Base64)+公开发现信息+轮换历史表+按指纹核对。
- **② 证明验证**:按 request_id / 粘贴证明 JSON 两模式→校验→通过徽章+Ledger ID/Merkle 根/签名指纹。
- **③ Merkle 锚点**:条目数+最新 Merkle 根+刷新。
- **端点**:`GET /.well-known/huakai-pubkey.json`、`/v1/audit/{pubkey|pubkeys|pubkey/{fp}|merkle-tree.json}`、`POST /v1/audit/verify`、`/v1/trust/verify`。

## 每日签到 `/checkin`
- **功能**:今日签到卡(状态徽章+奖励区间说明+立即签到)；本月日历卡(上下月导航+签到天数+累计返还+7 列日历已签格显金额)；成功 flash「获得 X 当前余额 Y」。
- **字段**:`CheckinStatus{enabled,min_cents,max_cents,month,checked_in_today,records[]}`、`CheckinRecord{checkin_date,reward_cents,billing_event_id}`、`CheckinResult{reward_cents,new_balance}`。**端点**:`GET /v1/me/checkin?month=`、`POST /v1/me/checkin`。

## 推广返利 `/affiliate`（5 块）
- 专属邀请码卡(码+链接各复制)；生成活动邀请码(次数 1-100+有效 1-90 天→一次性展示)；返利汇总卡(被邀总数/合格/已返/累计返利)；被邀请人表；返利流水表。
- **字段**:`MyInvitationCode{code}`、`InvitationSummary{qualified_count,rewarded_count,rewards_earned_cents}`、`ReferralItem{referee_user_id,status,rewarded_at}`、`RewardLedgerItem{reward_type,amount_usd}`+`total_reward_usd`。**端点**:`GET /v1/me/{invitation-code|invitations|referrals|referrals/rewards}`、`POST /v1/invitations`。

## 跨页通用约定
一次性 secret 展示(新建 Key 明文/2FA secret/活动邀请码,仅内存不持久);money 敏感动作统一二次确认;分页两范式(offset 上下页 / 游标加载更多);列表多复用 DataListTable+StatusBadge+EmptyState+StatCard。

---

# 第三部分 · 运营台功能树(admin token,43 页,七域)

通用:走 admin token;平台管理员多数读写显式带 `?tenant_id`,租户运营者可省走自身作用域;破坏性/动钱动作前端二次确认。

## 一、上游资源与调度
- **账号中心 `/accounts`**:＋新建账号(含凭证获取向导)/健康聚合卡点选过滤/筛选(状态·池组·标签·名称·健康态)/按标签批量调参(启停·优先级·静态权重)/行内 启停·测试 dry-run·清限流·删除;分页游标。字段 `ProviderAccount{name,tags,account_type,enabled,health_state,credential_state,in_flight_count,priority,static_weight,cap_concurrency,last_dispatch_at}`+聚合 total/enabled/disabled/needs_attention。端点 `GET/POST/PATCH/DELETE /admin/v1/provider-accounts`、`/{id}/{enabled|clear-rate-limit|test}`、`/bulk-by-tag`、`/health-summary`。
- **账号详情 `/accounts/:id`**:审计原因+启停/清限流/编辑参数;诊断卡;TLS 指纹绑定/解绑;凭证面板;凭证获取向导;危险区删除。字段(调度/健康/凭据/限流过载全字段:probe_model/token_version/last_refresh_outcome/rate_limit_reason/overload_until 等)。端点 `GET /{id}[/health|/recent-requests|/upstream-models]`、`PATCH /{id}/fingerprint-profile`。
- **分组管理·池组 `/admin/groups`**:新建/编辑(含启停 PATCH enabled,禁用即删)/查看成员账号。字段 `PoolGroup{name,routing_policy_version,top_k_default,capability_default,allow_last_resort,sticky_wait_*,fallback_wait_*,forced_route_rate_limit_per_hour,enabled}`。端点 `GET/POST/PATCH /admin/v1/pools`。
- **账号池 `/routing`**(Tab 绑定/强制 pin):绑定筛选(model_id·pool_group·fallback_class)+新建 BindingModal+行内编辑删除+缺 normal 主类告警;强制 pin(池组内模型→账号子集增删改启停)。字段 `PoolBinding{model_id,pool_group_id,priority,selection_mode,provider_model_id_override,rpm_limit,tpm_limit,max_parallel_requests,fallback_class,enabled}`、`ModelRoutingOverride{pool_group_id,model,provider_account_ids[],enabled}`。端点 `/admin/v1/model-pool-bindings`、`/admin/v1/model-routing-overrides`。
- **请求路由规则 `/admin/route-rules`**:新建/编辑(全替换含 match_priority)/启停/软删。字段 `Route{name,user_group_match,model_pattern_match,pool_group_id,match_priority,enabled}`。端点 `/v1/admin/routes[/{id}][/{id}/enabled]`。
- **上游目录 `/admin/catalogs`**:provider 目录+channel 目录各 CRUD(删带 reason)。字段 `ProviderCatalogItem{code,display_name,upstream_protocol,enabled}`、`ChannelCatalogItem{pool_group_id,name,body_param_strips,param_override,sensitive_words,enabled}`。端点 `/admin/v1/{providers|channels}`。
- **厂商模型同步 `/admin/model-sync`**:填 reason→触发全局同步→按 vendor 结果表(新增/更新/未变/停用/快照/重启用)。字段 `ModelSyncResult{completed_at,total_added/updated/disabled,results[]}`。端点 `POST /admin/v1/model-sync`。
- **模型注册 `/admin/model-registry`**:scope 切换 tenant/global;模型主体列表+准备新建/新建/保存/软删;能力矩阵替换;能力绑定 upsert;别名批量导入(逐行结果);租户目录继承策略开关。字段 `AdminModel{canonical_id,protocol_family,default_provider_model_id,default_context_window,default_request_timeout_ms,pricing_class,model_owner,capabilities,max_output_tokens,model_mode,status}`。端点 `/v1/admin/models[/{id}][/{id}/capabilities|capability-bindings]`、`/models/aliases/bulk-import`、`/model-registry-policy`。
- **模型与定价·公开 `/models`**:只读定价表+速率版本面板。字段同 PricingItem。端点 `GET /v1/pricing/page`。
- **渠道健康台 `/admin/channel-health`**:状态聚合卡+明细列表+单渠道 pause/resume/force-active(确认)。字段 `ChannelHealthItem{channel_id,vendor,state,score,reason_class,confidence_tier,policy_version,ramp_stage_pct,ramp_failure_count,cooldown_until}`+audit_events。端点 `GET /v1/admin/channel-health[/summary|/{id}]`、`POST /v1/admin/provider-accounts/{id}/channel-health/{pause|resume|force-active}`。
- **出口代理池 `/admin/proxies`**:新建(auth_secret 仅创建下发)/编辑(留空不改)/质检 test/删除软删/切状态;设置清除租户默认出口。字段 `Proxy{name,protocol,host,port,auth_username,group_id,status,last_check_at}`+探测 ok/latency_ms/error_class。端点 `/admin/v1/proxies[/{id}][/{id}/{test|status}]`、`/admin/v1/tenants/{tenant}/default-proxy`。
- **TLS 指纹 Profile `/admin/tls-fingerprints`**:新建/编辑(GREASE·cipher_suites·curves·signature_algorithms·alpn·tls_versions·extensions_order·expected_ja3_hash)/改状态/软删。字段 `TLSFingerprintProfile{...,status(drift_detected 只读),last_validated_at}`。端点 `/v1/admin/tls-fingerprint-profiles[/{id}][/{id}/status]`。
- **渠道测试模板 `/admin/channel-test-templates`**:HTTP 模板 CRUD(method/path/body_template/headers)。端点 `/admin/v1/channel-test-templates`。
- **配额策略 `/admin/quota-policies`**:筛选+新建/编辑(enforce 模式二次确认)/删除。字段 `QuotaPolicy{scope_kind(global/user/api_key/channel/pool_group/provider_account),scope_id,metric(requests/tokens_estimated/cost_usd/concurrency),window_kind,window_seconds,limit_value,burst_value,mode(enforce/observe/manual_first/disabled),priority,enabled,valid_from/until}`。端点 `/admin/v1/quota-policies`。

## 二、用户与商业
- **用户管理 `/users`**:统计卡(含 2FA 普及率)+搜索+新建用户(邮箱/密码/显示名/角色)+行内 停用启用·解锁·跳余额+分页。列 邮箱/角色/状态/用户组/备注/余额/创建。端点 `/admin/v1/users[/{id}/{status|unlock}]`、`/2fa-adoption-stats`。
- **用户详情 `/users/:id`**:概览+用量卡+**手动调额**(money,幂等键,仅加款)+运维动作(设组/备注·强制关 2FA·清 passkey·解绑社交·软删)+通知偏好代管+余额历史台账。字段 `BalanceHistoryEntry{event_type,amount,source_type,source_id,occurred_at}`。端点 `/admin/v1/users/{id}[/balance-history|/usage|/2fa/force-disable|/passkeys|/group|/remark|/account-bindings/{provider}]`、`POST /admin/v1/balances/adjustments`。
- **订单管理台 `/admin/orders`**:仪表盘卡+多维筛选+详情/审计+**确认/取消/重试履约/退款**(动钱不可撤销幂等)+退款工单(通过/驳回)+支付商配置+**代客建单**+CSV 导出(订单/支付/退款/用量)。字段 `AdminOrder{out_trade_no,user_id,amount_cents,status,provider_kind,order_kind,paid_at,created_by_admin_id,confirm_reason,failure_code}`、DashboardStats、RefundView。端点 `/v1/admin/payments/*`、CSV `/v1/admin/{payments,orders,refunds,usage}/export.csv`。
- **套餐管理·订阅 `/admin/subscriptions`**:套餐 CRUD+分配/批量分配+订阅动作(延长/改套餐/撤销/取消/重置配额)+发订阅兑换券(明文一次)+详情审计。字段 Plan、AdminSubscription、AuditEvent。端点 `/v1/admin/subscriptions/{plans|assignments|assignments/{id}/*|vouchers}`。
- **模型定价设置 `/admin/pricing`**:分组倍率 upsert/删除(public_ratio 开关+审计哈希链验证)+缓存价覆盖(global/model/tenant scope)+计费策略(stream_input_only_interrupted_policy)+工具附加费默认(只读)。字段 `PricingRatio{pool_group_id,ratio,public_ratio}`、`CacheOverride{scope,model,tenant_id,multiplier}`、`BillingSettingsResponse{key,value,source,allowed_values}`。端点 `/admin/v1/pricing/ratios[/audit/verify]`、`/v1/admin/cache-price-overrides/{scope}`、`/admin/v1/billing/settings`。
- **兑换码管理 `/admin/vouchers`**:列表+状态筛选+单张/批量生成(明文一次)+吊销(reason)+批次详情。字段 `Voucher{code_fingerprint,amount_cents,valid_from/until,max_redemptions,redeemed_count,grant_kind,status}`+Batch+CreatedCode.code。端点 `/v1/admin/vouchers[/batch|/{id}/revoke|/batches/{id}]`。
- **用量与计费台账 `/admin/billing-claims`**(Tab 用量/claim):只读游标+多维筛选+**按当前价表重算**(dry_run 预览→确认写入幂等)。字段 UsageRecord(tokens/actual_cost/end_class/pending_reconciliation/trust_status/requested vs upstream_model/settlement_source)、BillingClaim(predicted/actual_cost/status/settled_at)、RepriceItem(original/authoritative_cost/cost_delta)。端点 `GET /admin/v1/{usage|billing/claims}`、`POST /admin/v1/billing/reprice`。
- **退款/扣费争议 `/admin/disputes`**:列表+状态筛选+**裁决**(resolved/rejected+备注,money 确认)。字段 `DisputeView{dispute_id,user_id,request_id,reason,status,operator_note,refunded_micro_usd}`。端点 `GET /v1/admin/disputes`、`POST /{id}/resolve`。
- **分销管理 `/admin/affiliates`**:只读(money-gated)概览卡+分销记录+返利账本。字段 AdminReferralItem、AdminReferralRewardItem、AdminReferralOverview{counts_by_status,total_reward_usd,reward_count}。端点 `GET /v1/admin/referrals[/rewards|/overview]`。

## 三、安全与风控
- **安全与审计 `/security`**:审计事件游标+筛选(class/type/severity/actor/时间)+导出 JSON+单条签名证明下载。字段 `AuditEvent{event_class,event_type,severity,ledger_id,claim_id,provider_account_id,request_id,actor_id,actor_role,reason,payload}`。端点 `GET /admin/v1/audit-events`、`/v1/audit/{export|proof/{id}.json}`。
- **风控总览 `/admin/risk`**:只读计数卡(count>0 标红+去处理跳转)。字段 `RiskOverview{disabled_keys,firing_alerts,disabled_users,ip_blacklisted_keys}`。端点 `GET /admin/v1/risk/overview`。
- **内容审核 `/admin/moderation`**:审核配置(enabled/fail_closed/采样率/封禁阈值窗口)+命中日志+关键词/哈希黑名单 CRUD+批量导入(≤1000)+被封 Key 解封。字段 ModerationConfig、ModerationLog(decision/reason_code/payload_hash)、KeywordRule、HashRule、BannedAPIKey(key_prefix/violation_count/last_violation_at)。端点 `/admin/v1/moderation/{config|logs|keywords|hashes|banned|api-keys/{id}/unban}`。
- **凭证续期监控 `/admin/credential-renew`**:只读游标。字段 `RenewStatusRow{tenant_name,account_name,vendor,auth_mode,state,credential_version,access_expires_at,refresh_before_at,last_refresh_outcome,failure_class,failure_count}`。端点 `GET /admin/v1/credentials/renew-status`。

## 四、可靠性与运维
- **运维大屏 `/ops`**:窗口 24h/7d/30d+维度切换;总览(请求/成本/token/活跃用户 Key/成功率)+请求趋势+性能(p50/p95/p99/TTFT/TPS/错误率)+模型成本排行+性能排行(按模型/账号)+分桶+健康分(业务面+基础设施面 overall/business/infra_score)+账号用量分布。端点 `GET /v1/admin/usage/overview`、`/perf-metrics/{summary|by-bucket}`、`/leaderboard`、`/performance`、`/health-score`、`/provider-account-counts`。
- **系统健康 `/health`**:子系统状态(database/channel_health/dlq/alerting)+网关运行时(go_version/goroutine/heap/gc/uptime)。字段 `HealthResponse{status,components[]{name,status,detail},runtime}`。端点 `GET /v1/admin/system/health`。
- **告警控制台 `/admin/alerting`**(Tab 规则/事件/静默):规则 CRUD(指标目录下拉)+事件(筛选+手动恢复 firing)+静默 CRUD。字段 AlertRule{metric,comparator,threshold,severity,window_seconds,sustained,cooldown,notify_email,filters}、AlertEvent{state,observed_value,threshold,fired_at,email_sent}、AlertSilence{rule_id,reason,starts/ends_at,platform,group_id}。端点 `/v1/admin/{alert-rules[/metric-catalog]|alert-events[/{id}/manual-resolve]|alert-silences}`。
- **日志与诊断 `/admin/logs`**:读/热调进程日志级别+运行日志面板(warn+ 轮询,按 level/component/request_id 筛+游标+清理)+sink 健康(queue_len/inserted/dropped)。端点 `GET/PUT /v1/admin/loglevel`、`GET /v1/admin/ops/runtime-logs[/health]`、`POST /runtime-logs/cleanup`。
- **死信队列 `/admin/dlq`**(Tab 核心 DLQ/观测死信):列表+状态筛选+**重放**(money 幂等确认)。字段 `DlqRecord{event_kind,lane(HIGH/MED/LOW),status,failure_reason,replay_attempts,next_retry_at,source_table/id,idempotency_key}`、ObsDlqRecord{priority,dead_reason,attempt_count}。端点 `GET /admin/v1/dlq/{handler}`、`POST /{id}/replay`、`/usage-record-dlq/{id}/replay`、`GET /admin/v1/obs-dlq`+`/{id}/replay`。
- **媒体任务孤儿对账 `/admin/orphan-reconcile`**:pending 孤儿列表(tenant 过滤)+**处置**(reconciled/cancelled/ignored+可选 back_charge 追扣=money 确认)。字段 `OrphanItem{task_id,provider,provider_task_id,estimated_cents,reconcile_status,observed_at}`。端点 `GET /admin/v1/media-task-orphans`、`POST /{id}/reconcile`。
- **L2 响应缓存监控 `/admin/cache`**:只读统计(命中/容量/TTL/条目)+按 key 驱逐(破坏性确认)。字段 `L2StatsResponse{enabled,size_bytes,max_size_bytes,ttl_seconds,entries[]{key,tenant_id,vendor,model,status,size_bytes,stored_at,expires_at},metrics{hit_total,miss_total}}`。端点 `GET /admin/v1/cache/l2/stats`、`DELETE /l2/{key}`。
- **备份与恢复 `/admin/backup`**:只读 manifest(表清单+行数估算+脱敏策略+schema 版本/dirty)。端点 `GET /v1/admin/backup/manifest`。（重做扩:一键备份/恢复/导出——见 sidebar-review 决定。）
- **版本与维护 `/admin/version`**:构建版本(version/commit/build_time/go_version)+SMTP 配置(host/port/user/password 留空不改/from/tls/email_verify+保存确认)+SMTP 测试+邮件模板编辑预览。端点 `GET /v1/admin/version`、`/v1/admin/email/{settings|test|templates/preview}`。

## 五、平台配置与凭证
- **设置中心 `/system`**(9 分签:general/users/security/gateway/features/payment/email/agreement/backup):每分签渲染设置项卡,控件按类型(bool/number/string/json/secret/multiline),单项编辑带 reason 写审计,env 来源只读,密钥类只显 value_configured。字段 `PlatformSetting{key,value,value_configured,source(env/db/default),updated_at/by,health}`。端点 `GET /v1/admin/platform-settings`、`PUT /{key}`。
- **模块知识脊柱 `/admin/modules`**:只读按 category 分组模块(身份+能力+静态覆盖+实时探针)。字段 `ModuleView{id,category,title,capabilities[],catalog{section,feature_id,status,parity,pkgs},live_probe{status,detail}}`。端点 `GET /admin/v1/modules`。
- **平台凭证 `/admin/platform-credentials`**(Tab 运维令牌/平台 API Key):签发运维令牌(role platform_admin/tenant_operator+tenant/过期/名称,明文一次 OneTimeSecretBox)+吊销;签发平台 API Key(代签 tenant/user/environment live·test/过期,明文一次)+吊销。字段 AdminTokenListItem{key_prefix,role,scope_tenant_id,bootstrap,status,expires_at,last_used_at}、PlatformApiKeyListItem。端点 `/admin/v1/{admin-tokens|api-keys}[/{id}/revoke]`。
- **Hermes 配置与工具执行 `/admin/hermes`**(Tab chat/tools/context/history):配置(启停当前用户 Hermes,api_source managed/dedicated_group+api-profile 增删,dedicated 绑 pool_group,删被引用 409);工具执行(列已注册工具 read_only/mutating 标记,只读直接执行,**改动型 dry-run→preview→二次确认** correlation_id 5 分钟一次性);历史删会话。字段 HermesSettings{enabled,api_source,profile_id}、HermesProfile{kind,api_key_id,pool_group_id}、HermesToolDescriptor{read_only,mutating,requires_confirmation,required_role,input_schema}、MutationPreview{correlation_id,expires_in_seconds,preview}。端点 `/v1/hermes/{settings[/enable|/disable]|api-profiles|tools|tool-execute}`。

## 六、通知
- **公告管理 `/admin/announcements`**:新建/编辑/删除+启停。字段 `Announcement{title,body,severity(info/warning/critical),active,published_at,expires_at,created_by_admin}`。端点 `/v1/admin/announcements[/{id}]`。
- **站内信广播 `/admin/broadcast`**:群发(tenant_id/title/body/severity 确认,返 inserted 收信数)+Worker 统计。字段 BroadcastResult{inserted}、WorkerStats{reminder{tick_count,sent_total,failed_ticks},expiry{...}}。端点 `POST /v1/admin/notifications/broadcast`、`GET /worker-stats`。

## 七、调试
- **Playground `/playground`**:同用户门户调试台(admin 也可用),多协议真发。

---

# 第四部分 · 二级管理员(分销商)UI（规划,分销 arc）

独立第三套壳(分销 token)。固定自助功能集,全 scope 锁自己子树:概览(批发余额/下级数/用量)、下级用户管理、下级发 Key/分子额度、零售定价(自设零售倍率≥批发地板)、透明数据(自己那摊真号脱敏「上游#N」/真模型/真并发,看不到别家/平台/对话)、公告(平台子域随平台 / 白牌自有)。功能树待分销 arc Phase 落地后按真码补全。

---

# 第五部分 · 侧栏重构映射(设计以此为准)

现有页面按 Owner 评审(2026-07-15-sidebar-review-owner-decisions.md)重组,设计时按重组后结构布局:
- **上游与模型融合页** = 账号中心 + 厂商同步 + 上游目录 + 渠道健康 + 渠道测试模板 + 模型注册(模型字典),tab 分区。
- **分组管理** 吃进 请求路由规则 + 按组限速(配额策略按组部分)。
- **IP 代理** = 出口代理池(TLS 指纹后端默认开、无独立页)。
- **运维监控面板** = 运维大屏 + 系统健康 + 检测台(安全监测,新)+ 告警,只显紧急(三层降噪)。
- **日志模块面板** = 日志诊断 + 审计 + 结算重试队列(死信队列)+ 资金对账(孤儿对账),全量明细。
- **充值中心** = 订单管理(充值/退款/支付商配置)+ 兑换码生成,归财务;订单管理不单列,退款争议后端留前端收起。
- **系统设置** 5 分区(登录注册/第三方登录/法律合规/邮件含 SMTP/全局参数);模块开关删(后端留)、版本页 SMTP 挪入、缓存监控并定价。
- **定价** = 官方价×倍率(输入输出倍率 + 缓存价独立倍率)。
- 用户门户新增**活动中心**(签到/邀请返利/推广)。

---

# 第六部分 · 设计交付规范

- 每页交付:布局线框 + 组件清单 + 交互流 + 空/错/加载/无权限各态 + 响应式(移动可用)。
- 三套壳共用一套**自有设计 token**(反克隆,不照搬参考项目的色/圆角/阴影/字体/图标/间距)。
- **必带 Hermes 内联解释按钮**组件:报错/账号方块/日志条目旁,点击调 Hermes 只读诊断用大白话解释错误出处/含义/怎么办。
- 复杂页用 tab/折叠聚合,别散页跳。
- **数据字段以现有 API 层(features/*/api.ts + types.ts)为准,设计不改后端契约。**
- 复用现有 ui/ 组件语义(表格/徽章/空态/卡片/骨架/图表),视觉重设计但保留数据结构与交互约定。
