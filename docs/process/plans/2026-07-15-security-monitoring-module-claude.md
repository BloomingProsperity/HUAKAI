# 运维安全监测 + 日志模块 · Claude 规划稿

日期:2026-07-15。Owner 主线需求:监测服务器系统 + 运维安全(被扫描/IP攻击/密码爆破探测)+ 安全事件日志归集面。摸底(ae19d787,真码 file:line)结论:**应用层骨架已相当完整,缺的是"归集+展示"与"系统资源采集";真主机层入侵探测(端口扫描/SSH爆破)网关进程无可见性,须主机层配合**。

## 0. 现有可复用(已建+已接线,不重建)
- 登录爆破防御:loginthrottle IP 滑窗失败计数+限时封 IP(internal/loginthrottle/limiter.go:1-97,接线 auth_handler.go:270-279)+ 账号锁定(userauth/store.go:211-212)+ 认证事件 zap sink(cmd/gateway/auth_event_sink.go:30-33)。**缺口:封 IP 决策纯内存不落库,运营台看不到"谁在攻击"**。
- 审计归集三表:admin_audit_events(0010)/user_audit_events(0107)/rate_limit_audit_events(0004)+ auditledger 签名链;均有端点,前两者有 UI。
- 风控总览 riskoverviewhttp(/admin/v1/risk/overview,4信号聚合,强制 tenant_id 收敛)——聚合面雏形。
- 告警 alerting(规则/事件/静默+UI)。⚠ cpu_usage_percent 是**死指标**(types.go:29,生产无采集源)。
- 系统健康 systemhealthhttp(DB/渠道/DLQ/告警+Go runtime)。⚠ 无系统级 CPU/磁盘/连接数。
- 运行日志 ops_runtime_logs(0180)异步 sink 只收 warn/error+UI(/admin/logs)。

## 1. 模块范围(增量四件套,应用层自足)
1. **新表 `security_events`**(append-only 归集:occurred_at/type/severity/source_ip/tenant_id/detail jsonb;**不存明文凭证/key**,照 user_audit_events 只存前缀先例)。**[schema,新表低风险,surface Owner]**
2. **信号落库钩子**:loginthrottle 封 IP/爆破超阈 + 认证失败事件 + per-key IP 黑名单命中 + 429 洪峰 → 写 security_events(异步,照 logsink 有界队列范式,绝不反压业务)。**只在安全事件里记攻击者 IP,不动 accesslog 的 by-design 不记 IP**。
3. **系统资源采集器**:gopsutil 采 CPU/内存/磁盘/连接数 → 喂 systemhealth + alerting 指标源(**激活死掉的 cpu_usage_percent**,顺带 mem/disk)。[新 runtime 依赖 gopsutil,surface Owner]
4. **「安全监测台」聚合端点 + 页面**:攻击态势(近24h 爆破/封IP Top/429洪峰)+ 系统态势(资源)+ 安全事件流(按 IP/类型过滤)。后端 securityoverviewhttp 新包;UI 落现有 nav「安全与审计」组,组件照 RiskOverview/Health/LogsDiag 范式。**[页面级设计 Owner-gated:先出后端+一句话页面描述,Owner 点头再建页]**

## 2. 主机层边界(如实告知 Owner)
真端口扫描/SSH 爆破/内核连接表——网关进程看不到 22 端口和 auth.log。**三镜(sub2api/new-api/CLIProxyAPI)全部无等价**(sub2api 的 channel monitor 是上游渠道可用性,非入侵监测;成熟网关都外置给 fail2ban/云WAF)。方案:
- **部署文档指引**:fail2ban + 云安全组收紧(必做,文档活)。
- **(可选,Owner-gated)旁路采集 agent**:独立进程读 fail2ban jail 状态/auth.log 摘要→喂 security_events 展示;权限敏感,与网关解耦。

## 3. 安全硬门
- 归集查询强制 tenant_id 收敛(防 riskoverview 注释里那次跨租户 IDOR 复发);security_events 读端点仅 platform_admin。
- 全参数化;detail jsonb 不含凭证/token;新路由补 openapi.yaml+一致性测试;判别变异(去落库钩子→事件流断言红;去 tenant 收敛→跨租户读 403 测试红)。

## 3.5 面板重组 + 运维监控降噪聚合规则(Owner 2026-07-15 拍板,参考 sub2 但更智能)

**面板分工**(Owner:「日志模块=很详细所有操作都记相当于审计;运维监控只显紧急不列那么多;安全单独分组=细节」):
- **运维监控面板**(观测与运维组,参考 sub2 DashboardView:实时流量QPS/TPS+并发+账号可用性+告警):**只显紧急态势**,检测台(攻击/爆破/渗透探测+系统资源)并入此盘。
- **日志模块面板**:**全量明细**(运行日志+错误/上游错误+攻击行为+审计操作日志),审计级、可搜可导,事后追查。
- **「安全」分组**(单独命名,只放细节):内容与拦截审核(违规词/渗透特征词/测试拦截,部署者可加;关键词拦截是应用层补充防线,核心防注入仍靠代码全参数化)。

**运维监控三层显示规则**(Owner 问的"门道":错误都一样/单账号429 vs 多账号429/被攻击 怎么区分显示):
1. **同类去重聚合**:相同错误码+原因不逐条列,聚成"近N分钟 X 次"一行(sub2 阈值告警之上加聚合,防刷屏)。
2. **按影响面分级**:单账号 429=个例(不上盘只进日志,除非唯一货源)/ 多账号同时 429=系统性(黄,上游收紧或并发过猛)/ 池全线429+触钱线(余额/凭证)+健康探针挂=红置顶。判定维度=受影响账号数+错误率斜率+是否触钱线。
3. **出错 vs 被攻击**:出错=出站(你→上游)业务错(429/500);被攻击=入站异常模式(同IP高频失败登录爆破/扫描路径/请求量突增/异常UA)→红色"安全事件"单列。区分关键=方向(出站/入站)+模式(单点偶发/同源高频)。
底座复用现有 alerting 阈值引擎(metric+threshold+window,与 sub2 ops_alert 同路子),上叠这三层。

## 3.6 日志留存清理 + 用户数据隐私(Owner 2026-07-15 问)

**日志定期清理**:HUAKAI 现状=有 CleanupRuntimeLogs(logsink/store_postgres.go:118,删 before 之前)但**保留策略靠运营者手动触发,无自动定时**;cleanup_runtime_logs 审计动作已在。**缺口(上线补,S2)**:加自动定时清理任务(可配保留天数,如日志/usage 留 N 天自动删,防磁盘堆爆),对齐 sub2 ops_cleanup_executor(cron 每日 02:00 批量 5000/30min 超时)。归日志模块。

**用户数据隐私(卖点,现状已优)**:HUAKAI usage_records **只存元数据**(模型/token/成本/end_class/号),**正常请求完全不落 prompt/completion 对话正文**——隐私合规干净。sub2 曾错误时存 request_body(为重试,ops_error_logs)后经 136 迁移移除;HUAKAI 更干净。**行动**:部署文档写明"不存储用户对话内容"作信任卖点;分销商透明只见用量元数据、看不到下级对话(隐私隔离天然成立)。

## 4. 执行
codex 实现(排在 C³ 之后,避免并发争抢),切片:①表+钩子+聚合端点(后端)→②资源采集器→③UI 页(Owner 点头后)→④部署文档 fail2ban 指引。每片判别变异+真 PG+审查零 S0/S1。
