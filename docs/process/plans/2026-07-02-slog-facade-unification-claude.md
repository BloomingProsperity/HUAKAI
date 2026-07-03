# 日志片 D:slog 门面统一 + /loglevel 联动(修 S1 双栈割裂缺口⑤)

日期:2026-07-02(实施 2026-07-03)。分支 `feat/slog-facade`,基底 `426fb3c0`。

## 1. 问题(亲核真码,file:line 均为本基底)

生产日志是**真双栈且割裂**:

- **zap 栈**:`backend/cmd/gateway/main.go:26-28` 用 `zap.NewProductionConfig()` 构建,
  `loggerCfg.Level = loglevel.Level`(`backend/internal/loglevel/loglevel.go:14` 的
  `zap.NewAtomicLevelAt(zapcore.InfoLevel)`),JSON 落 **stderr**,级别可被
  `/admin/v1/loglevel`(`backend/internal/adminhttp/loglevel_handler.go:26-52`,platform_admin
  专属,GET/PUT 委托 `loglevel.Level.ServeHTTP`)运行时热调。约 23 个文件接收注入的 zap logger。
- **slog 栈**:全仓非测试代码 **72 处直接发射调用**(`slog.Warn` 17 + `slog.WarnContext` 47 +
  `slog.ErrorContext` 5 + `slog.InfoContext` 3;含 attr 构造等 slog 引用行共 223 行——历史口径
  "199 调用点" 与本次实测口径不同,散栈事实不变),外加大量 worker 构造器在 nil-logger 时兜底
  `slog.Default()`(windowcost/proxyhealth/mediatask/quota/billing 对账、userkey、tlsfphealth 等,
  见 `backend/internal/*/worker.go`)。**`slog.SetDefault` 生产从不调用**(grep 全仓非测试零命中),
  因此这些全部落 Go 内建默认 handler:**纯文本(logfmt)、Info+ 固定、不受 /loglevel 管辖、
  不过任何脱敏**,输出同为 stderr。
- **privacy 包**:`backend/internal/privacy/logger.go:88-90` 包级 `LogSystem` 用
  `slog.Default()`(生产 16 个调用点,forwarder/eventbus/audit_verify/billing 等),即同样落
  文本 handler;`logger.go:78` 的 `Logger` 实例自建 `slog.NewJSONHandler(systemOut, nil)`
  (stdout,`cmd/gateway/middleware.go:47-48` 装配)。脱敏单一真相源是
  `privacy.DefaultRedactor()`(`backend/internal/privacy/default_redactor.go` 的
  deny-by-default AllowlistRedactor:字段白名单 + 敏感 key 黑名单 + 值级禁写标记扫描)。

后果(S1):事故排查时运维把 /loglevel 调到 debug,slog 通道纹丝不动;采集器面对
JSON+文本混流;slog 通道存在把含密钥文本原样写日志的结构性风险(无 redactor)。

## 2. §16 三镜调研(specifier lane,clean-room:读源码→行为摘要,无逐字标识符拷贝)

### shape inventory(日志门面统一 + 运行时级别控制的三种形态)

1. **单门面 + 全桥接**(sub2api):自研 logger 包持全局 zap 实例(atomic 指针),
   **把 log/slog 与标准库 log 都桥接进同一 zap core**——启动期 slog.SetDefault 一个自定义
   slog.Handler,其 Enabled 查 zap core 的级别、Handle 把 slog record 翻译成 zap 字段转发。
   运行时调级只动一个 AtomicLevel,所有通道(zap 直用/slog/std log)同时生效。
2. **单栈原生**(CLIProxyAPI):全程只用一个全局 logrus 单例,gin 的输出 writer 也接进它;
   运行时调级 = 配置热重载回调里比较 debug 布尔并 SetLevel,单栈所以天然全生效。
3. **无门面 printf**(new-api):自研文本 logger 直写 gin 的 writer(stdout=info/stderr=err),
   无结构化、无运行时级别(debug 由启动期布尔 gate),按 request-id 前缀行文本。

### 三镜对照表

| feature | sub2api cite | new-api cite | CLIProxyAPI cite | HUAKAI delta | dimension |
|---|---|---|---|---|---|
| 门面形态:单 logger 还是多栈 | 单 zap 门面包,slog+std log 均桥入(backend/internal/pkg/logger/logger.go:44-91、:233-239 slog 桥、:208-231 std log 桥) | 无门面,自研 printf 直写 gin writer(logger/logger.go:97-117) | 单全局 logrus,gin writer 接入(internal/logging/global_logger.go:105-111) | HUAKAI 真双栈割裂 → 本片"双轨定型":保留 zap 注入面不动,给 slog 装 JSON 门面,两栈共享一个级别真相源 | 架构 |
| 运行时调级如何覆盖所有通道 | SetLevel 写同一 AtomicLevel(logger.go:107-118);slog 桥的 Enabled 逐条查 zap core(slog_handler.go:30-41)→ 一次调级全通道生效 | 无运行时调级,debug 为启动期布尔(logger.go:88-95) | 配置热重载 → 比较 debug 布尔 → logrus SetLevel(internal/util/util.go:59-72),单栈全生效 | /loglevel 已热调 zap 的 AtomicLevel;本片让 slog handler 的 Enabled 也查同一 `loglevel.Level` → PUT /loglevel 同时管两栈 | 算法+生态 |
| 全局字段(service/env/version)注入 | 构建 logger 末尾 With(service、env) 两个常驻字段,值来自配置带默认(logger.go:311-314、options.go:58-65) | 无(仅行内 request-id 前缀,logger.go:98-107) | 无常驻字段(request_id 从 entry data 提取,global_logger.go:55) | HUAKAI zap 侧现状也无;本片在 slog 门面 handler 构造期 WithAttrs 注入 service/env/version(env 取 HUAKAI_RELEASE_MODE,version 取 buildinfo.Version) | 架构 |
| 从 context 取子 logger | ctx value 存 zap logger,取不到回落全局(logger.go:522-530) | ctx 只带 request-id | logrus entry-from-context 辅助函数(sdk/cliproxy/auth/selector.go:474-479) | 本片不做 context 子 logger(slog 的 *Context 变体已把 ctx 传进 Handle;HUAKAI 现有调用点自带结构化字段),避免无消费者 helper | 生态 |
| 日志脱敏 | 有独立脱敏工具包(敏感 key 表+正则,backend/internal/util/logredact/redact.go)但**不在门面 hot path**,调用方显式使用 | 无系统性日志脱敏 | 无系统性日志脱敏 | HUAKAI 的 privacy AllowlistRedactor 本就最强;本片把其**值级禁写扫描**接进 slog 门面 hot path(超越三镜);检测逻辑 100% 复用 privacy,不新造 | 架构 |
| 级别域映射(slog↔宿主) | slog 级别按阈值折算到 zap 四档(slog_handler.go:30-41) | 不适用 | 不适用(logrus 原生) | 同样按阈值折算:>=Error→Error、>=Warn→Warn、>=Info→Info、其余→Debug(slog.Handler 接口形态与阈值折算是 API 必然形态,非实现拷贝) | 算法 |

**三镜共识**:单一级别真相源 + 把散栈桥进主级别域,而不是重写调用点。本片正是该共识在
HUAKAI 约束下的最小落地(不动 23 文件 zap 注入面、零 slog 调用点改动)。

## 3. Scope 与成功标准

**做**:
- 新包 `backend/internal/logfacade`:自定义 slog.Handler 包裹 `slog.NewJSONHandler`,
  三要点 = 级别桥接 `loglevel.Level` / 全局字段 service+env+version / privacy 值级脱敏。
- `cmd/gateway/main.go` 启动期 `slog.SetDefault` 装配一次(zap 构建成功后立刻,
  **必须在 buildGatewayRuntime 之前**——多个 worker 构造器在构造期捕获 `slog.Default()`,
  晚装配它们就永远拿旧 handler)。
- `internal/loglevel/loglevel.go` 包注释更新(现在同时驱动 zap 与 slog 门面;纯文档)。

**不做(禁令)**:
- 不碰 72 处 slog 发射调用点/223 行 slog 引用中的任何一行。
- 不新造脱敏检测逻辑(复用 `privacy.ContainsForbiddenRawData`,redactor 语义单一真相源)。
- 不动 zap 配置、不动 privacy.Logger 自建实例、不加无消费者 helper、不引新依赖。

**成功标准**:
1. `/loglevel` PUT debug 后,slog Debug 记录可见;PUT warn 后 slog Info 被抑制(热调,零重启)。
2. 所有经 slog.Default 的日志变合法 JSON,含 service/env/version。
3. 含 privacy 禁写标记(sk-、bearer 等)的 attr 值不落明文。
4. 全量 `go test ./...` FAIL=0,quality-gate PASS,零调用点 diff。

## 4. 设计决策(含取舍)

### D1 级别桥接:handler 直查 `loglevel.Level`,不提供注入点
`zap.AtomicLevel` 内部是共享指针,handler 每次 `Enabled` 都读它 → /loglevel 热调即时生效,
无缓存失效问题。不做 Level 注入选项:单一真相源就是本片目的,注入点=多余旋钮。
映射:slog(Debug=-4/Info=0/Warn=4/Error=8) 按阈值折算 zapcore 四档(见对照表末行)。
内层 JSONHandler 的 Level 放开到 Debug,闸门唯一归外层 Enabled。

### D2 全局字段:构造期 WithAttrs,一次注入
service 固定 `huakai-gateway`;env 取 `HUAKAI_RELEASE_MODE` 原值(main 启动即校验合法);
version 取 `internal/buildinfo.Version`(-ldflags 可覆盖,默认 "dev",与 /version 端点同源)。
zap 侧暂不加同名字段(不动 zap 配置=控 blast radius);两栈字段对称留后续片。

### D3 redactor:值级扫描 fail-closed,消息与 key 不扫(取舍已权衡)
- **不能**对普通 slog 记录整体跑 `SanitizePayload`(deny-by-default 白名单):现有调用点大量
  使用 `mode`/`err`/`env` 等非白名单运维字段,会把正常日志全打成 [REDACTED];且包级
  `privacy.LogSystem` 的 `event` 外层 key 不在白名单,16 个已脱敏的 privacy 事件会被二次抹掉
  ——比现状更差,违背"至少不更差"底线。
- **采用**:对每个 attr **值**跑 `privacy.ContainsForbiddenRawData`(privacy 导出的值级禁写
  扫描:JSON 可解析则递归查敏感 key+禁写字符串,否则按裸文本查禁写标记),命中即整值替换为
  `[REDACTED]`,序列化失败的 Any 值同样替换(fail-closed)。检测逻辑零新造。
- **消息与 attr key 不扫**:按 slog 约定两者是编译期常量、动态数据只进 attr 值;且禁写标记含
  "credential" 这类宽词,扫消息会把 "credentialworker provider adapter missing" 这类正常运维
  消息整条打掉(比现状更差)。残余风险(有人把动态串拼进消息)记录于此,现状 slog 通道
  完全无脱敏,本设计严格不更差、只更好。
- **error 值特判**:`slog.Any("err", err)` 在 JSONHandler 下会被 json.Marshal 成 `{}`(错误
  类型多为不可导出字段),消息全丢——门面把 error 值先取 `Error()` 文本再扫再输出,
  保住可观测性(TextHandler 时代能看到错误文本,不许倒退)。
- privacy 包级 `LogSystem`(16 点)在 SetDefault 后自动走门面:其 payload 已过 SanitizePayload,
  再过值级扫描必然通过(白名单字段+已抹值不含禁写标记),仅多一次轻量复扫;severity=debug 的
  privacy 事件从"永不可见"变为"/loglevel=debug 时可见"——这是修复不是回归。
  privacy.Logger **实例**(middleware.go:47-48,stdout JSON)自带 handler,不受 SetDefault
  影响,维持现状不碰(它已是 JSON+已脱敏;输出流 stdout 与门面 stderr 的既有差异不属本片)。

### D4 输出流:stderr,与 zap 对齐
`zap.NewProductionConfig()` 默认 stderr;slog 旧默认(经 log 包)也是 stderr。门面默认
`os.Stderr` → 采集器看到的流不变,只有格式从文本→JSON。注意键名差异:zap 用
`ts`(epoch 秒)/`level`/`msg`,slog JSONHandler 用 `time`(RFC3339)/`level`/`msg`——
统一键名要改 zap encoder 或给 slog 写 ReplaceAttr,均超出本片"零生产语义变更之外"的最小
半径,留后续片(片 E 候选)。

### D5 装配点:main() 内 zap 构建成功后立刻
`setupSlogFacade()` 小函数(可测试)在 `main.go` zap logger 构建成功后调用——早于
`run()`→`buildGatewayRuntime`,保证所有构造期捕获 `slog.Default()` 的 worker 拿到门面。
其他 cmd/(mvp-seed、openapi-check 等工具进程)不装配,维持默认——它们是短命工具,
不在生产日志采集面上。

## 5. Blast radius(需 Owner 知悉)

- **生产 stderr 格式变化**:slog 通道从 logfmt 文本 → JSON(zap 行不变)。若有按文本
  grep slog 行的采集/告警规则会失配。自部署形态、无已知外部采集依赖 → 风险低,但属
  单向格式切换,合并前应向 Owner surface。
- **slog Debug/更低级别行为**:旧默认 handler 固定 Info+;现在 /loglevel=debug 会放出
  slog Debug 行(这正是缺口⑤要修的),/loglevel=warn 还会**抑制** slog Info 行(旧行为放行)。
  级别语义统一到与 zap 完全一致。
- **含禁写标记的 attr 值变 [REDACTED]**:例如 err 文本含 "credential" 的值会被整值替换
  (privacy 既有政策,fail-closed);观测性小损,安全性净增。
- 三维 delta:架构=双轨定型(slog 门面+zap 各自编码、单级别真相源);算法=slog↔zapcore
  级别阈值映射;生态=/loglevel 运维面从只管 zap 扩为同时管 slog。

## 6. 测试矩阵(§14 判别性,变异前先 commit)

| 判别 | 测试 | 变异(预期红) |
|---|---|---|
| 级别桥接 | loglevel=Warn 时 Enabled(Info)=false、Enabled(Warn)=true;设回 Debug 后 Debug 可见;同一 logger 实例热切换生效 | zapLevelFor 的 Info↔Warn 映射写反 |
| JSON+全局字段 | 输出行是合法 JSON 且 service/env/version 三字段值正确 | 删 WithAttrs 全局字段注入 |
| redactor | attr 值含假 token(sk- 前缀)输出无明文、出现 [REDACTED];嵌套 group/Any-map 同判;干净 event map 字段存活(防过度脱敏回归) | scrubAttr 改为原样返回 |
| /loglevel 覆盖 slog | loglevel.Level.SetLevel(debug) 后 slog Debug 记录出现在输出 | (由级别桥接变异共同覆盖) |
| error 值渲染 | slog.Any("err", errors.New(...)) 输出含错误文本;含密钥的错误文本被替换 | — |
| wiring | setupSlogFacade 后 slog.Default() 的 Enabled 跟随 loglevel.Level | — |

门禁:`go build ./... && go vet ./...` + 目标包测试 + 全量 `go test ./...` FAIL=0 +
`./scripts/quality-gate.sh` PASS。

## 7. Source files read(specifier lane)

- ~/refs/sub2api/backend/internal/pkg/logger/logger.go(门面/桥接/调级/全局字段)、
  internal/pkg/logger/slog_handler.go(slog→zap 桥)、internal/pkg/logger/options.go、
  internal/pkg/logger/config_adapter.go、internal/util/logredact/redact.go
- ~/refs/new-api/logger/logger.go(printf 门面/调级缺失/debug 布尔)
- ~/refs/CLIProxyAPI/internal/util/util.go(SetLogLevel)、internal/logging/global_logger.go、
  cmd/server/main.go、sdk/cliproxy/auth/selector.go(entry-from-context)
- HUAKAI 侧:backend/cmd/gateway/main.go、config.go、middleware.go:47-48、
  internal/loglevel/loglevel.go、internal/adminhttp/loglevel_handler.go、
  internal/adminhttp/version_handler.go、internal/buildinfo/buildinfo.go、
  internal/privacy/{logger.go,redactor.go,default_redactor.go}、slog 调用点全仓 grep。

Lane: specifier(读源码→行为摘要进对照表,未拷贝任何上游标识符/注释/代码块;
file:line 引用仅作证据定位)。Timestamp: 2026-07-03T00:00Z 前后完成调研,同日实施。
