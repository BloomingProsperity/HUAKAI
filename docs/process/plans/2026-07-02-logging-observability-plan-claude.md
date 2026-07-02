# 日志可观测性 fusion-upgrade 计划(三镜+成熟项目调研)

> 状态(2026-07-02):片 A+B(billing/quota worker 处理量/失败可观测)已实现+对抗审查(零 S0/S1/S2,6 条 S3 已修)+合并 @d1cabb7f。
> 片 D(slog 门面统一)为下一优先,先出 design;片 C/E/F/G/H 按本文档排期。
> 调研方法:5 路并行(sub2api/new-api/CLIProxyAPI clean-room specifier + HUAKAI 现状亲核 + LiteLLM/Portkey/Helicone 成熟项目),全部 HUAKAI file:line 为 @48d9a7b6 亲核。

核实完毕,4 个疑似缺口全部在真码命中,并发现一个研究稿低估的更严重割裂点。以下是完整交付(全部基于 HUAKAI@48d9a7b6 亲核)。

---

# HUAKAI 日志体系:三镜 + 成熟项目对照 · 缺口核实 · fusion 升级计划

> 亲核基线:`/home/ubuntu/HUAKAI` @ `48d9a7b6`(feat/fe-wire-users-mod),与研究稿同 hash。所有 HUAKAI file:line 均本人 grep/sed 复核。

## 0. 一句话结论

HUAKAI 的**审计/计费侧已是三镜里最强的**(ed25519+Merkle 签名链 auditledger,三镜全无),但**运维应用日志侧最弱且被低估**:不是"zap 为主 + slog 只在 privacy",实测是 **zap 23 文件 vs slog 199 调用点的真·双栈**,而 `slog.SetDefault` 生产从不调用 → 大量 slog 落 Go 默认 TextHandler(纯文本、非 JSON、不受 `/loglevel` 管辖),这是比研究稿所述更严重的结构裂缝。补的次序:**先把今晚两个 worker worktree 的处理量/失败可观测补上(零热路径、可单测),再独立做 slog 门面统一 + auth failover 可见性 + 全链 request_id。**

---

## 1. 日志体系九维对照表

| 维度 | sub2api | new-api | CLIProxyAPI | 成熟项目共识 | HUAKAI 现状 | 差距 |
|---|---|---|---|---|---|---|
| **框架** | zap 单例 + slog/log/printf 三桥全收敛,console/json 可切,lumberjack 轮转 | 自研文本行 `[LEVEL] time \| reqid \| msg` + DB Log 表二分,MultiWriter | logrus 单例(白名单字段文本呈现)+ 自建 FileRequestLogger 全文取证 | slog 门面 + 生产 JSON,统一 logger 工厂,禁裸 fmt/log | **双栈**:zap `NewProductionConfig` JSON→stderr(main.go:26,23 文件)+ slog 199 调用点;`slog.SetDefault` 从不调用→多数 slog 落默认 TextHandler | 无统一门面;两种输出格式并存;第三方库日志无桥接兜底 |
| **级别** | debug/info/warn/error(+fatal),运行时热改+落库审计 | INFO/WARN/ERR/DEBUG,DEBUG 单 env 开关,无 per-module | debug/info/warn/error,单一 debug 开关驱动,gin 按响应码分档 | 四级,级别="谁该被叫醒"非信息量,atomic 调级 | zap 四级默认 Info,`loglevel.AtomicLevel`+/loglevel 热调;privacy 五档 severity | **Debug 几乎空置**(全后端仅 1 处 .Debug);**`/loglevel` 只管 zap,199 处 slog 不受其控** |
| **请求生命周期事件** | 双管道:zap access_log + ops_error_logs 取证入库(五段延时/error_phase/upstream_errors 数组) | gin access line + DB Log 表(Consume/Error 落库) | 访问层每请求一行 + request-log 全文文件(按 reqid 命名) | 入口/鉴权/选号/上游 attempt/响应/错误分类/重试/限流/计费 10 类事件 | **仅收尾一条 access_log**(method/path/status/latency/bytes,accesslog.go:24);选号/上游/重试/换号全静默,钱与配额走审计 DB | 请求内部事件在应用日志流"不可见";无"请求开始"日志;failover 链无日志 |
| **关联** | X-Request-ID + X-Client-Request-ID 双 ID,ctx 携带子 logger 全链继承 | request_id 每行强制打印,取不到兜底 "SYSTEM";抓上游 id 落 Log 表列 | crypto/rand 8 位 reqid,ctx+gin 双通道,深层 `logEntryWithRequestID` | 逻辑 reqid ↔ attempt_id 双层 + trace_id 透传 + 回写响应头 | request_id(chi)+ client_request_id + logical_request_id 三层已有(chat_completions_handler.go:184/341/440);userauditlog/ledger 均带 RequestID | **trace_id 声明未填充**(default_redactor.go:254);**worker 补偿动作与原请求无 reqid 关联**;slog 不自动注入 reqid,漏传即断链 |
| **审计/应用分离** | 三层:zap 应用 + ops_system_log_sink 索引入库 + PaymentAuditLog 强审计;sink 绕级别门控直写 | 彻底二分:文本→stdout,Log 表(枚举分类)+ 鉴权链兜底埋点 auditRouteActions | **无审计概念**(grep 零命中),只分"应用日志 vs 请求全文" | 三层:app log(可丢)/audit(append-only 不可丢)/billing(精确对账) | **最强**:auditledger(ed25519+Merkle,写失败进 DLQ 不静默)+ internal/audit(admin/pool/dispute)+ channelhealth 转换审计 + privacy security 独立 sink | 无差距,方向正确;唯一改进=确保调低 loglevel 绝不少写审计(已满足) |
| **脱敏** | 专包 logredact:白名单 key + 递归 map + 正则(GOCSPX/AIza)+ 深度上限;sink 入库统一脱敏 | LocalLogPreview 2048 截断 + MaskSensitiveInfo(URL/IP)+ DEBUG 才 dump body,无中央 redactor | MaskAuthorizationHeader/Query 掩码族,管理面路径不记日志 | 五层纵深:源头不采集/类型 String()/正则/递归/身份不取自 body | privacy.AllowlistRedactor **deny-by-default 三层**(字段白名单+key 黑名单+值前缀扫描,default_redactor.go:141/258/316);sanitizer.go 覆盖 sk-/JWT/org- | **redactor 只保护 slog 通道**;zap 侧(access_log 外的 lifecycle/warn)不过 redactor,靠调用点自律 |
| **失败日志** | failover warn + 单账号重试 LegacyPrintf + ops 错误分类流水线(phase/severity/owner/is_business_limited) | 重试每轮 ERR + 链>1 补一条 INFO 摘要;禁用渠道 SysLog+邮件;429 静默 | executor 逐跳 debug(换 base-url/退避/冷却);token 刷新 warn 带 attempt | 按"可否自愈"定级,每条带 reqid+account+attempt+上游 status | **重试/failover 全程静默**;上游错→渠道健康信号+billing Abort;限流每次 warn(rate_limit.go:291);ledger 失败分级 warn+入队(forwarder.go:905) | **failover/换号在日志里完全不可见**;auth 401 返回 "" 既不写信号也不写日志(见缺口④) |
| **worker 日志** | channel_monitor/usage_record 池:启动 info+扩缩 info+丢弃 warn+panic error,队列满 60s 节流一次 | for-sleep-SysLog 起止各一行,运行历史入 DB,无逐条 | 启动 info+周期活动 debug+失败 warn,"无事发生不打日志" | 生命周期 info + 每轮聚合计数 + 单 item 失败 warn + panic error;心跳走 metrics | **A 类零日志**(billing sweeper/reconciler、quota worker `_, _ =` 丢弃 count+err);**B 类只记失败边界**无处理量心跳 | **所有 worker 无"本轮处理 N 条/存活"日志**;多数失败只落 DB 不进 stdout(见缺口①②③) |
| **防刷屏** | access log 跳探针 + zap sampling(100/100 可配)+ 掉日志 60s 节流 + sink 分级降量 + 队满即丢 + 错误过滤开关 | DEBUG 门控 + 重试去重 + 2048 截断 + 429 不打 + 按行数轮转 | debug 门控 + 每请求一行 + GET 跳过 + 流式满即丢 + commercial-mode 总关 | 每请求恒 1 条 + debug 开关 + 运行时调级 + 采样 + worker 聚合 | zap `NewProductionConfig` 内建 Sampling(未覆盖)+ /loglevel 热调 + access_log 一行不记 body | **debug 分层无效**(空置);**slog 通道不享受 zap sampling**;无按请求/租户采样;限流 warn 纯靠 sampling 兜底 |

---

## 2. HUAKAI 日志缺口清单(全部亲核 · 按三价值排序)

价值标记:🔧运维排障 / 🛡️审计合规 / 💰计费对账。严重度 = 该缺口造成的"黑盒面积 × 触发频率"。

### 【S1】🔧 缺口⑤(研究稿低估):slog/zap 真双栈割裂 + slog 落默认 TextHandler + `/loglevel` 只管 zap

- **核实结论:比研究稿更严重。** 研究稿框为"slog 只在 privacy 子系统",实测 `slog.` 非测试调用点 **199 处**,分布 userkey(32)/exporthttp(18)/mediatask(17)/gatewayhttp(13)/privacy(11)/quotaenforce(8)…遍布全后端。
- `cmd/gateway/main.go:26` 用 `zap.NewProductionConfig()`(JSON→stderr,内建 Sampling)+ `loggerCfg.Level = loglevel.Level`,**显式向下传递**,仅 23 文件接收。
- `slog.SetDefault` **生产从不调用**(全仓仅 `internal/privacy/logger.go:78` 用 `slog.New` 建 privacy 自己的 JSON 实例);而服务如 `userkey.NewService` 在 logger 为 nil 时 `logger = slog.Default()`(userkey.go:177)、`privacy.LogSystem` 包级函数用 `slog.Default()`(logger.go:89)→ **落 Go 内建 TextHandler:纯文本、非 JSON、Info+、无 reqid 自动绑定、不被 zap sampling 去洪、不受 `/loglevel` 管辖**。
- **后果**:生产 stdout/stderr 同时存在 zap-JSON 与 slog-text 两种格式(采集器难统一);事故时运维翻 `/loglevel` 到 Debug **对 199 处 slog 完全无效**(它只调 zap 的 AtomicLevel)。这是运行时可观测控制面的真空洞。

### 【S1】💰🔧 缺口①(billing worker 零日志):动钱回收 worker 丢弃 count+error

- **核实=真。** `internal/billing/lease_sweep.go:66` `_, _ = s.sweepOnce(ctx)`、`internal/billing/reconciliation_worker.go:82` `_, _ = w.RunOnce(ctx, w.now())`,两 worker 整个包**无任何 logger**。LeaseSweeper 回收孤儿预扣/并发槽(动钱),PendingReconciliationWorker 兜底结算——处理量、失败、backlog 在 stdout 完全不可见,只有 DB 副作用。运维无法从日志判断"钱回收 worker 是否在跑/跑了多少/是否在报错"。

### 【S1】🔧🛡️ 缺口④(auth failover 静默):401 令牌失效既不写健康信号也不写日志

- **核实=真缺口。** `internal/gatewayhttp/chat_completions_error.go:170` `signalFromClassification` 是纯函数,`ErrorClassTokenRevoked/OAuthInvalidGrant` 分支 `return ""`(:181,注释明确"不把令牌问题写成账号健康降级信号");`recordChannelHealthSignal`(:149)在 `class == ""` 时 :153-154 早退。→ 401/oauth-invalid-grant 触发换号时:**既不写 channelhealth 审计,也不写任何日志**;上层 `SwitchAccount` 与 auth-failover slot 决策亦静默,仅热刷新失败才 `logInternalError`。运维看不到"哪个账号令牌挂了、换了几次号"。

### 【S2】🛡️🔧 缺口⑥(settlementrecovery DLQ 补偿低可见):补偿 handler 无日志,DLQ 失败只落表

- **核实=半真。** `internal/settlementrecovery/handler.go` 自身无日志;驱动它的 `internal/obs/dlq/worker.go` **仅 panic recover 时 Error(:169)**,重试/dead/completed 只 `MarkFailedRetry/MarkFailedDead` 写出站表。→ 结算补偿在日志流里基本不可见,失败状态+error 串仅持久化于 DLQ 表。与钱恢复相关的路径运维无法从 stdout 感知,只能主动查表。

### 【S2】💰🔧 缺口②(quota worker 半黑盒):worker 丢弃处理量,底层只记边界

- **核实=半真(如研究稿)。** `internal/quota/reconciliation_worker.go:70` `_, _ = w.RunOnce(...)` 丢弃 count+err;底层 `Reconciler` 有 slog 但**只记两种边界**:`reconciler.go:118` InfoContext "tenant sweep hit limit"(带 processed_jobs)、`:222` WarnContext "job reached max attempts"。正常每轮处理量与瞬时失败不记。

### 【S3】🔧 缺口⑦:worker 普遍无处理量心跳 / last_run 信号

- windowcost/mediatask/proxyhealth/modelsync/credentialworker 均无"本轮处理 N 条/存活"日志;存活性无 metrics gauge 兜底 → 长时间空转无法被发现。

### 【S3】🔧 缺口③(channelhealth 状态转换,研究稿判"假"确认):有审计无 slog,stdout 盲区

- **核实=假缺口(审计存在)但确有观测盲区。** `internal/channelhealth/` 全包 **0 处 slog**;但 `emitTransitionEvents`(service.go:656)在每次 degraded/cooling/disabled/ramp-started/rolled-back/recovered 转换(调用点 :119/:163/:203/:246/:308/:408)写 DB 审计事件 + `emitAlert` 告警。机制健全,只是**走 DB 不走 stdout**,运维实时观察冷却/熔断需查表或看告警,非日志。

### 【S3】🛡️ 缺口⑧:trace_id 声明未填充 + zap 侧不过 redactor

- trace_id 仅在脱敏 allowlist 声明(default_redactor.go:254)从不填充,无分布式 trace 接入点;redactor 只保护 slog,zap 侧非 access_log 的调用点靠自律(access_log 结构上不记敏感,风险低)。

---

## 3. fusion-upgrade 日志计划(切片 · 非碰撞优先 · 可测)

### 三维 delta(HUAKAI 该长成什么样)

- **架构 delta:双轨定型。** 运维轨=**统一 slog 门面 + 生产 JSON**(吸收 sub2api 的 encoder 固定 key/全局 service/env/version 字段/从 ctx 取子 logger),本地 console;审计轨=**HUAKAI 自有 auditledger hash 链**(ed25519+Merkle,三镜皆无,是 HUAKAI 的护城河 delta)+ billing/usage 落 DB 对账。关键动作:wire 时 `slog.SetDefault` 一个受 `loglevel.Level` 联动的 JSON handler(或桥接进 zap 多 core),让运行时调级同时覆盖 zap+slog,消除双格式割裂。
- **算法 delta:** ① 采样——zap 已内建,给 slog 通道补等价的"每秒前 N 条全记之后每 M 条"限频(吸收 sub2api zapcore sampler);② 递归 map 脱敏 + 深度上限 + 敏感 key 单一真相源(吸收 sub2api logredact,与现有 AllowlistRedactor 合并为一个函数供 app log/audit 共用);③ worker **每轮聚合计数**而非逐条(吸收三镜共识 + new-api for-sleep 起止模式);④ 失败日志分级判据固化为"谁被叫醒 vs 信息量";⑤ `logical_request_id ↔ attempt_id` 双层关联写进字段规范(HUAKAI 已有,三镜里 sub2api 也有,是中转站必需项)。
- **生态 delta:** ① CI 脱敏断言测试(构造带假 token 的错误串,断言日志输出不含该 token,mutation:删脱敏则转红);② admin `/loglevel` 运行时调级扩到 slog;③ metrics 承接心跳(worker `last_run_ts`/queue depth/DLQ size 走 gauge,日志只在状态跳变打点)——日志 vs metrics 分工;④ `huakai-verify` CLI 独立验签(已有,delta 保留)。

### 切片拆分(每片标注碰撞面 + 可测点 + 体量)

| 片 | 内容 | 碰撞面 | 可测点 | 体量 |
|---|---|---|---|---|
| **A** ⭐今晚 | billing `LeaseSweeper`+`PendingReconciliationWorker`+`PostgresPendingReconciliationFinalizer` 注入 slog:启动/停止 info、每轮汇总 info(processed/failed/backlog)、单项失败 warn、RunOnce err 不再丢弃 | 仅 `internal/billing/lease_sweep.go`、`reconciliation_worker.go`(worker loop,零热路径) | 注入 fake handler 断言每轮恰一条汇总 + 失败 warn;`_, _ =` 变异转红 | S(~1 worktree) |
| **B** ⭐今晚 | quota `ReconciliationWorker` loop 把 `_, _ =` 改为:err→warn、每轮 count→info 汇总;`Reconciler` 补每租户处理量 info | 仅 `internal/quota/reconciliation_worker.go`、`reconciler.go` | 同 A;断言 RunOnce 返回的 count 被记录 | S |
| **C** 后续 | auth failover 可见性:`signalFromClassification` 返回 "" 分支在 `recordChannelHealthSignal` 早退前补一条 warn(class/account/reqid);`SwitchAccount`+auth-failover slot 决策补 warn | **碰 gatewayhttp 热路径**(chat_completions_*),须与今晚三片错开 | 表驱动断言 token-revoked 换号打 warn;流式中途 failover 用例 | M |
| **D** 后续(基础设施) | slog 门面统一:wire `slog.SetDefault` 一个 JSON handler + 联动 `loglevel.Level`;`privacy.LogSystem` 指向配置实例;全局注入 service/env/version;从 ctx 取子 logger 门面铺开 | 影响面大(199 调用点的输出格式/级别),但多为非侵入(改 wiring+handler);先出 design 再落 | 断言 slog 输出为 JSON 且带 reqid;调 /loglevel 到 Debug 后 slog Debug 可见 | L |
| **E** 后续 | channelhealth 状态转换镜像到 slog:`emitTransitionEvents` 各转换点加一行 structured log(审计已在,补 stdout 可见) | 仅 `internal/channelhealth/service.go` | 断言 cooling/recovered 转换打一条 info/warn | S |
| **F** 后续 | settlementrecovery/`obs/dlq` worker:重试/dead/completed 补 warn/info(带 reqid+error),处理量汇总;handler 关键分支补日志 | `internal/settlementrecovery/`、`internal/obs/dlq/worker.go` | 断言 MarkFailedDead 打 warn、每轮汇总 | M |
| **G** 后续(跨模块,最后) | 全链 request_id:worker 继承被处理事件的 reqid;入口透传 W3C `traceparent`→填充 trace_id(现声明未填充);worker 补偿动作与原请求关联 | 跨 billing/quota/dlq/gatewayhttp 多模块 | 断言补偿日志带原请求 reqid;traceparent 透传用例 | L |
| **H** 后续(工程化) | 采样补 slog 通道 + 敏感 key 清单单一真相源(合并 AllowlistRedactor+吸收 logredact 递归)+ CI 脱敏断言测试 | 横切,低侵入 | 假 token 不出现在日志的 CI 测试 | M |

---

## 4. 今晚三片 vs 独立后续片(明确划线)

**✅ 今晚可立即搭车(落今晚的两个 worktree,零热路径、纯 worker loop、可单测):**

- **片 A → `wt-billing-retry` worktree**:billing retry 相关的 `LeaseSweeper` + `PendingReconciliationWorker` + `PostgresPendingReconciliationFinalizer` 补处理量/失败可观测(缺口①,S1 且动钱)。改动全在 `internal/billing/lease_sweep.go`、`reconciliation_worker.go`,与 retry 逻辑同文件簇,顺手补日志不新增碰撞。
- **片 B → `wt-quota-reconciler` worktree**:quota `ReconciliationWorker` loop 不再丢弃 count+err + `Reconciler` 补每轮/每租户处理量(缺口②)。全在 `internal/quota/reconciliation_worker.go`、`reconciler.go`。

> 这两片正好落在今晚已有的两个 worktree 内,是"给正在动的 worker 补处理量/失败可观测",与三片的功能改动**不新增文件碰撞**,且都能用"注入 fake logger 断言每轮汇总 + `_, _ =` 变异转红"单测闭环。建议今晚随三片一起交付。

**⏭️ 独立后续片(碰热路径 / 跨模块 / 属基础设施,须单独立项、勿今晚混入):**

- **片 C**:auth failover 可见性(缺口④,S1)——碰 gatewayhttp 热路径 `chat_completions_*`,与今晚三片错开时段做,单独一个 worktree。
- **片 D**:slog 门面统一 + `/loglevel` 联动 slog(缺口⑤,S1 基础设施)——影响 199 调用点输出格式,先出 design 再落,是"补运维日志短板"的**总开关片**,建议紧随今晚两片之后优先排。
- **片 E**:channelhealth 状态转换镜像 slog(缺口③,S3)。
- **片 F**:settlementrecovery/DLQ 补偿可见性(缺口⑥,S2)。
- **片 G**:全链 request_id 关联 + trace_id 填充(缺口⑧,跨模块,最后做)。
- **片 H**:slog 采样 + 脱敏单一真相源 + CI 脱敏断言(工程化横切)。

**排期建议**:今晚 A+B(随三片)→ 下一批 D(门面总开关,解 S1 割裂)→ C(auth 可见,解 S1 静默)→ F/E → G/H。

---

### 关键真码锚点(本人复核)

- 双栈证据:`cmd/gateway/main.go:26`(zap NewProductionConfig+loglevel.Level);slog 199 非测试调用点;`slog.SetDefault` 生产零调用,仅 `internal/privacy/logger.go:78` 建自有实例、:89 包级函数用 `slog.Default()`;`internal/userkey/userkey.go:177` nil→`slog.Default()`。
- 缺口①:`internal/billing/lease_sweep.go:66`、`internal/billing/reconciliation_worker.go:82`(`_, _ =`,包内无 logger)。
- 缺口②:`internal/quota/reconciliation_worker.go:70`(`_, _ =`);`internal/quota/reconciler.go:118`(sweep 上限 info)、`:222`(max attempts warn)。
- 缺口③:`internal/channelhealth/service.go:656` emitTransitionEvents(调用点 :119/163/203/246/308/408),全包 0 处 slog。
- 缺口④:`internal/gatewayhttp/chat_completions_error.go:170-197` signalFromClassification(:181 `return ""`)、:149-154 recordChannelHealthSignal(`class==""` 早退)。
- 缺口⑥:`internal/settlementrecovery/handler.go`(无日志)、`internal/obs/dlq/worker.go:169`(仅 panic Error)。
- 骨架:`internal/accesslog/accesslog.go:24-31`(每请求一行、不记 body/query/IP)、`internal/loglevel/loglevel.go:14`(AtomicLevel Info,仅管 zap)、`internal/privacy/default_redactor.go:141/258/316`(deny-by-default 三层)、`internal/gateway/forwarder.go:905`(ledger 失败分级+入队)。