# W3 公开错误安全模型 —— 实施 spec

> 补救波 W3。源:`docs/process/plans/2026-05-22-audit-remediation-wave.md` 第 53 行。
> 覆盖 8 个发现:GW-02 / GW-04 / GW-05 / GW-06 / GW-09(Zone A)、
> C-12 / C-18(Zone C #12 #18)、B-11(Zone B #11)。
> 本 spec 前置一次写全(含已知难点),目标 ≤2 轮 review 收敛。
>
> **rev1(2026-05-22)**:经 codex 交叉评审(APPROVE-WITH-CHANGES)后已补:
> 漏掉的 call site(handler_headers / validate / idempotency_replay)、helper
> 路径规则、GW-02 分类不回归测试、C-12 三类分类矩阵、B-11 `ErrHandlerTimeout`
> 覆盖 + `-race`、C-18 全 `rawSSE` 路径覆盖、错误目录表格化。

## 1. 背景与核心风险

网关把内部 `err.Error()` 原文直接喂给客户端 —— JSON body、HTTP header、
SSE 错误帧三条路径都有。`err.Error()` 可能携带:上游供应商错误体、账号线索、
Postgres 表名/列名、内部状态字段。这是**信息泄露**(S1),且因为 call site
没有日志 sink,运维同时**看不到**这些错误。

根因二:`ChatHandlerDeps` 没有 logger 字段,chat-completions call site 无处
记录 raw error,于是把它写进响应体当"日志"用。

## 2. 切分(小切片闭合纪律)

W3 拆两个独立闭合切片,各自 spec 达成 + 测过 + codex review + 提交:

- **W3a — gatewayhttp 公开错误面**:GW-02 / GW-04 / GW-05 / GW-06 / GW-09。
  全部落在 `internal/gatewayhttp/` + `internal/gateway/upstream_http_error.go`。
  一个内聚单元:「客户端经 HTTP 能看到的一切都已脱敏」。先做。
- **W3b — 流式 + eventbus 错误脱敏**:C-12 / C-18 / B-11。
  落在 `internal/gateway/event_scanner.go`、`internal/gateway/forwarder.go`、
  `internal/eventbus/`。「流式协议错误帧 + scanner 分类 + DLQ 失败可见性」。后做。

W3a 闭合后才开 W3b。两切片合计 ~2 天。

## 3. 核心设计 —— 客户端安全错误三元组

**不引入大型新类型,不做枚举大迁移。** 现有 `classifiedAttemptFailure` 已经
把 public(`ClientStatus`/`ClientCode`/`ClientMessage`)与 internal(`Cause error`)
分开 —— 架构已就位,bug 是 call site 把 `err.Error()` 灌进了 `ClientMessage`。

W3a 落三件事:

### 3.1 错误文案目录(新文件 `internal/gatewayhttp/public_error.go`)

- 定义一组**稳定 code 常量** + 每个 code 配一句**固定中性文案**。
  文案绝不含动态内部细节(无表名、无 err 串、无上游 body)。
- 提供 `messageForPublicCode(code string) string`:已知 code 返回固定文案,
  未知 code 返回通用兜底(`"request failed"`)。
- code 集合由 codex 按实际 call site 归纳(call site 清单见 §4 GW-04/GW-05)。
  现有 ad-hoc code(`registry_unknown_error` / `router_plan_error` /
  `cache_key_error` 等)**保留不改名**(客户端可能已依赖),只把它们登记进
  目录、配固定文案。**新增不删除、不改名**。
- **目录必须以表格形式落在 `public_error.go` 顶部注释或同目录文档**:
  每行 `code | HTTP status | 固定文案 | 覆盖测试名`。这张表是「漏登检查表」
  —— 实现完成后逐行核对,确保每个 call site 的 code 都在表里、都有测试。

### 3.2 内部错误日志 sink(新 helper)

- 新 package 级 helper `logInternalError(ctx, requestID, code string, err error)`,
  用 `log/slog`(与 `auth_handler.go`/`audit_verify_handler.go` 已有 slog 用法
  一致),走 `slog.Default()`。不改 `ChatHandlerDeps`、不动 `cmd/gateway` 接线
  —— 零接线 blast radius。
- 字段:`request_id` / `public_code` / 完整 `err`。这是 raw error 的唯一去处。
- **已知难点**:`slog.Default()` 默认写 stderr,与本进程 zap 主管道不统一。
  W3 不做 zap 整合(超范围)。记路线图 RR-W3-001(可选:slog→zap 桥接)。

### 3.3 call site 改写规则

每一处当前 `writeJSONError(w, status, code, err.Error())` 或
`classifiedFailureFromDecision(code, err.Error(), …)`:
1. message 改成 `messageForPublicCode(code)`(固定文案),**绝不传 `err.Error()`**;
2. 紧接着调 `logInternalError(ctx, requestID, code, err)` 把 raw error 落日志;
3. `classifiedAttemptFailure` 路径:raw error 放进已有的 `Cause error` 字段
   (`chat_completions_attempt.go:64`),`ClientMessage` 只放固定文案。
4. **helper 路径同样适用(codex 评审补)**:
   - `retryableLocalAttemptFailure` / `terminalLocalAttemptFailure`
     (`chat_completions_attempt.go:81/93`)的 `message` 形参,所有调用方
     必须传固定 public 文案,**绝不传 `err.Error()`**;raw error 经 `cause` 形参
     进 `Cause` 字段。
   - `degradeFailureIfAbortFailed`(`chat_completions_attempt.go:100-119`)当前
     `attempt.go:116` 把 raw `abortErr.Error()` 拼进 `AbortReason`
     (`AbortReason` 会进 header / 日志,属公开面)。改为只写 safe 短 code
     (如 `;abort_failed=1`),raw `abortErr` 经 `logInternalError` 落日志。
   - `classifiedFailureFromDecision` 经由 `dispatch.go:241/251/257` 等 helper
     间接把 `err.Error()` 放进 `ClientMessage` 的路径,一并按本规则改。

## 4. W3a 逐发现 spec

### GW-02 — 上游错误 body 透传客户端(S1)

- 证据:`gateway/upstream_http_error.go:24-33` `Error()` 拼了 256 字节上游 body;
  `chat_completions_dispatch.go:406`、`chat_completions_handler.go:272` 把
  `err.Error()` 喂进 `classifiedFailureFromDecision` 的 message 参数 →
  `writeAttemptFailure` → `writeJSONError` → 客户端。
- 修复:
  1. `UpstreamHTTPError.Error()` **去掉 body 摘要**,只返回
     `"dispatcher: 上游状态码 %d"`。body 仍保留在 `UpstreamHTTPError.Body` 字段
     供分类/日志读取 —— 分类逻辑与日志直接读 `.Body`,不经 `Error()`。
  2. `dispatch.go:406` / `handler.go:272` 两处:传给
     `classifiedFailureFromDecision` 的 message 改固定文案(按 class 选,如
     `"upstream request failed"`),raw `UpstreamHTTPError` 进 `Cause`。
  3. **保持分类入口顺序(codex 评审补)**:HCSF 分支
     `chat_completions_dispatch.go:378-382`、raw buffered 分支
     `chat_completions_handler.go:300-310` 都直接读 `.Body` 做分类。
     `errors.As(err, **UpstreamHTTPError)` 分支必须**仍排在** generic
     `err.Error()` 字符串分类分支之前 —— 去掉 `Error()` body 后,任何依赖
     `Error()` 文本里上游 body 关键字的分类都会失效,必须确认分类只读 `.Body`。
- 风险测试(两类,都要):
  1. **不泄露**:构造 `UpstreamHTTPError{StatusCode:400, Body:[]byte("SENSITIVE_UPSTREAM_MARKER")}`,
     走 buffered + HCSF 两路,断言客户端响应 body **不含** `SENSITIVE_UPSTREAM_MARKER`。
  2. **分类不回归**:body 含 `invalid_grant` / rate-limit 关键字时,断言
     仍得到正确的 retry / auth-failover / channel-health 决策 —— 证明分类
     由 `.Body` 字段驱动,不依赖 `Error()` 字符串。

### GW-04 — err.Error() 直写客户端 JSON(S1)

- 证据(逐行):`chat_completions_dispatch.go:54,71,154,178,336,343,357`;
  `chat_completions_billing.go:47,57,66,86`;`chat_completions_handler.go:297,318,329`;
  `chat_completions_stream.go:45,307,317,338`;
  **(codex 评审补)** `chat_completions_handler_headers.go:175,197,213,253`(JSON body)、
  `chat_completions_validate.go:51,80`、`chat_completions_idempotency_replay.go:76`。
- 修复:每处按 §3.3 规则改写。codex 须逐行访问、登记 code 进目录、配固定文案。
  注意 `validate.go:51` 的 `invalid_json` 是客户端自己请求体的解析错误 ——
  仍按从严原则:固定文案(如 `"request body is not valid JSON"`)+ 落日志,
  不回显 parser 原文。
- **已知难点**:个别 call site 的 err 本身可能已是安全固定串(如纯校验错误)。
  判定从严 —— **拿不准一律当不安全**(固定文案 + 落日志)。宁可多记一条日志,
  不可漏脱敏一处。
- 风险测试:每条错误路径注入一个含敏感 marker 的 err,断言客户端 JSON body
  只含目录里的固定文案、不含 marker。

### GW-05 — err.Error() 直写 HTTP header(S1)

- 证据:`X-Huakai-Abort-Failed`(`dispatch.go:249,334,341,355`;`billing.go:45`;
  `handler.go:292,316,324`;`stream.go:305,315,322,336`;
  **(codex 评审补)** `handler_headers.go:172,211`)、
  `X-Huakai-Forward-Error`(`stream.go:211`)、`X-Huakai-Settle-Error`(`stream.go:234`)
  全塞 `*.Error()`。
- 修复:header value 改为**稳定短 code**(如 `abort_failed`、`forward_failed`、
  `settle_failed`,可带 `;reason=<safe-enum>`),raw error 经 `logInternalError` 落日志。
  header 永不含 `err.Error()`。
- 风险测试:注入含 marker 的 abort/forward/settle err,断言响应 header 值
  只是 code、不含 marker。

### GW-06 — SSE 错误 header 在流开始后才 Set(S3)

- 证据:`chat_completions_stream.go:211/234/246` 三个错误 header 在
  `streamForwarder.Forward` 之后 Set —— 此时 200 OK + SSE header 已 flush,
  客户端永远收不到。
- **决策(已定,记录理由)**:改为**仅服务端结构化日志**,不发 trailer。
  理由:trailer 对 SSE 客户端实际不可见(浏览器 EventSource 完全读不到
  trailer;多数 HTTP 库需显式 trailer 处理),pre-declare trailer 是"假承诺";
  这三个 header 在流式路径本来就是死信道。诚实的修法 = 删死信道、落日志。
- 修复:`stream.go` 中 `Forward` 之后的这三处 `w.Header().Set(...)` 删除,
  改 `logInternalError`。**非流式路径**的同名 header(在 WriteHeader 之前 Set)
  不动 —— 那是 GW-05 范畴(值改安全 code 即可)。
- 风险测试:流式 forward/settle 出错 → 断言 raw error 进了服务端日志、
  且 `Forward` 之后没有再 `Set` 任何 header(可用 ResponseRecorder 在首字节后
  检测 header 冻结)。

### GW-09 — 截断 body 不经 GW-02 外泄(验证项,LOW)

- 证据:`readRawBufferedUpstreamBody`(`handler.go:239-249`)对非 2xx 返回截断 body
  供分类 —— 设计如此。
- 修复:GW-02 修好后(`Error()` 不再带 body、客户端 message 固定)本项自动覆盖。
  **只需加一条断言测试**:截断 body 含 marker → 客户端响应不含 marker。
  无需独立代码改动。

## 5. W3b 逐发现 spec

### C-18 — 流式 event:error payload 原样透传(S1)

- 证据:`gateway/forwarder.go:264-273` `handleEventWithAdapter` 对
  `evt.Type == "error"` 旁路 adapter,直接 `writeAndFlush(w, rawSSE(evt))`。
  注释点名 Bedrock exception payload 走这条 —— 含 provider 内部诊断/账号 hint。
- 修复:`evt.Type == "error"` 分支改为:
  1. raw `evt.Data` 经 `logInternalError`(或 gateway 包内等价 slog)落日志;
  2. 向客户端写一个**脱敏后的 canonical SSE 错误帧** —— 固定 code(如
     `upstream_error`)+ 固定 message,不含 raw payload;
  3. 返回值维持原语义(`terminalSeen, true, 0, nil`),让后续
     `ErrBedrockException` 正常终止流。
- **已知难点**:脱敏错误帧的 wire 形态要让客户端能解析。用最小 SSE:
  `event: error\ndata: {"error":{"code":"upstream_error","message":"upstream returned an error"}}\n\n`。
  不要尝试套 adapter(注释已说明 error 帧喂 adapter 会 JSON 解析失败)。
- **覆盖完整性(codex 评审补)**:`handleEventWithAdapter` 内对 error 类型帧
  必须**没有任何** `rawSSE(evt)` 直接写客户端的残留路径 —— 改完后全函数搜
  `rawSSE`,确认 error-payload 不经任何 raw 输出。`adapter == nil` 分支
  (`forwarder.go:257-262`)若也可能透传 error 帧,同样要先判 `evt.Type=="error"`
  走脱敏分支。
- 风险测试:scanner 产出 `SSEEvent{Type:"error", Data: 含 "SENSITIVE_BEDROCK_MARKER"}`,
  断言客户端 SSE 输出**不含** marker、含固定 code;断言 raw 进了日志。
  `adapter == nil` 与 `adapter != nil` 两种情况都要测。

### C-12 — SSE scanner 把所有读错误归类成 overflow(S2)

- 证据:`gateway/event_scanner.go:79-80` `if err := scanner.Err(); err != nil`
  无差别 `yield(SSEEvent{}, ErrScannerOverflow)`。`bufio.Scanner.Err()` 真正
  overflow 时是 `bufio.ErrTooLong`,但 TCP reset / TLS read error / 其它 IO
  错误也走同一行 → 经 `forwarder.go:496-499 classifyScanError` 被错判成
  `ResponseEventTooLarge`,污染重试决策与 channel health 信号。
- 修复:
  1. `event_scanner.go:79`:
     ```
     if err := scanner.Err(); err != nil {
         if errors.Is(err, bufio.ErrTooLong) { yield(SSEEvent{}, ErrScannerOverflow) }
         else { yield(SSEEvent{}, err) }   // 原样传播
         return
     }
     ```
     注意 `event_scanner.go:71-73` 自己的 size guard 仍 yield `ErrScannerOverflow`
     —— 那是真 overflow,保留不动。
  2. `forwarder.go:496 classifyScanError`:`default` 分支当前返回
     `UnknownTermination`。新增:把网络类 IO 读错误映射到一个上游/网络类
     `StreamEndClass`(优先复用已有值;`io.ErrUnexpectedEOF` 已映 `UpstreamError5xx`,
     通用网络读错误同样映 `UpstreamError5xx` 即可 —— 关键是**绝不能**是
     `ResponseEventTooLarge`)。
- **已知难点**:`StreamEndClass` 没有专门的"网络错误"枚举值。不新增枚举值
  (会牵动 usage/health 一连串 switch);复用 `UpstreamError5xx` 表达"上游侧
  断了"。若 codex 发现更贴切的既有值可用,但须在 review 说明。
- 风险测试 —— **三类分类矩阵(codex 评审补),逐类断言**:
  1. 真实单 event 超限(scanner 自己 size guard 或 `bufio.ErrTooLong`)
     → err 是 `ErrScannerOverflow`,`classifyScanError` → `ResponseEventTooLarge`。
  2. `context.Canceled`(ctx 取消)→ `classifyScanError` → `OrchestratorCancel`。
  3. 普通 reader IO error(非 `ErrTooLong`,模拟 TCP reset)→ err **不是**
     `ErrScannerOverflow`,`classifyScanError` 返回的 class **不是**
     `ResponseEventTooLarge`(按 spec 映射到上游/网络类)。
  三类必须分别得到**不同**的 class —— 这是 C-12 的核心:分类不能塌成一种。

### B-11 — eventbus raw handler error 进 state/DLQ + DLQ 失败被吞(S2)

- 证据:`eventbus/bus.go:205`(`setState` 的 `Error: errString(err)`)、
  `bus.go:262`(DLQ `FailureReason: handlerErr.Error()`)、
  `types.go:246`(`dlqPayload` 的 `"failure_reason": errString(err)`)
  都把 raw handler error 原文落进内存 state、DLQ 表、DLQ payload。
  handler error 常 wrap SQL/表名/列名/provider/account 上下文。
  更坏:`bus.go:256` `_, _ = b.dlq.Enqueue(...)` 吞掉返回值,DLQ 写失败时
  运维既无可重放 DLQ、也不知道 DLQ 丢了。
- 修复:
  1. 新增 handler error 分类器 `classifyHandlerFailure(err error) string` —— 把
     handler error 收敛成小枚举 sanitized code:`handler_timeout`、
     `handler_canceled`(`context.Canceled`)、`handler_invalid_event`(`ErrInvalidEvent`)、
     `handler_error`(兜底)。setState 的 `Error`、DLQ `FailureReason`、
     `dlqPayload` 的 `failure_reason` 全改存这个 sanitized code。
     **(codex 评审补)** `handler_timeout` 的判定**必须覆盖 `ErrHandlerTimeout`**
     —— eventbus 真实超时错误是 `runner.go:156` 用 `ErrHandlerTimeout`
     (`types.go:50`)wrap 的,不是裸 `context.DeadlineExceeded`;分类器用
     `errors.Is(err, ErrHandlerTimeout)`(并可同时收 `context.DeadlineExceeded`)。
  2. raw error:经受控内部日志(`slog`,ERROR 级,带 `event_id`/`handler_id`)
     落地 —— 不进 state、不进 DLQ 表。
  3. `bus.go:256` 的 DLQ Enqueue:接住 error。失败时:
     - `slog` ERROR 级单独日志(区别于普通 handler 失败,便于运维 grep);
     - `Bus` 上加 `atomic.Int64` 计数 `dlqPersistFailures`,加导出方法
       `DLQPersistFailures() int64`(供测试 + 未来 metrics 抓取);
     - 把对应 handler state 的 `Error` 标成 `dlq_persist_failed`(让运维从
       state 也能看到 DLQ 丢失这一事实)。
- **已知难点**:eventbus 当前无 metrics facility,做不了真正的 alert。
  W3b 的范围 = 让 DLQ 丢失**可见**(日志 + 计数器 + state 标记),不是接告警
  系统。真 alerting 接线记路线图 RR-W3-002。
- 风险测试:
  (a) handler 返回含 `SENSITIVE_SQL_MARKER` 的 error → 断言 state.Error、
      DLQ FailureReason、dlqPayload.failure_reason 三处都只有 sanitized code、
      不含 marker;
  (b) 注入一个 Enqueue 必失败的 fake DLQ → 断言 `DLQPersistFailures()` 增加、
      对应 state.Error == `dlq_persist_failed`。此测试**必须带 `-race`**
      —— `atomic.Int64` 计数与 `setState`(走 `b.mu` mutex)是 DLQ 失败时的
      二次状态更新,需证明与并发 handler 无竞态。

## 6. 已知难点清单(汇总,review 不必再"发现")

1. slog.Default() 写 stderr,与 zap 主管道不统一 —— W3 不整合,记 RR-W3-001。
2. GW-04 部分 err 可能本就安全 —— 判定从严,拿不准当不安全。
3. GW-06 决策已定 = 服务端日志,不发 trailer(理由见 4.GW-06)。
4. C-12 `StreamEndClass` 无网络错误专用枚举 —— 复用 `UpstreamError5xx`,不新增枚举。
5. C-18 脱敏错误帧不套 adapter(error 帧喂 adapter 会 JSON 解析失败)。
6. B-11 eventbus 无 metrics —— 只做"可见"(日志+计数器+state 标记),记 RR-W3-002。
7. 现有 ad-hoc client error code 一律保留不改名(客户端可能已依赖),只登记+配文案。
8. (codex 评审补)eventbus 真实超时错误是 `ErrHandlerTimeout` 不是裸
   `context.DeadlineExceeded` —— 分类器用 `errors.Is`。
9. (codex 评审补)`UpstreamHTTPError` 分类必须由 `.Body` 字段驱动;`errors.As`
   分支排在 generic `err.Error()` 分类之前;GW-02 必须有"分类不回归"测试。

## 7. 验收标准

W3a / W3b 各自:
- `cd backend && GOCACHE=/tmp/go-cache go build ./...` exit 0。
- 改动包 + 受影响包 `go test ... -race -count=1` exit 0;最后跑一次全量
  `go test ./...` exit 0。
- 每个发现的"风险测试"全部新增并通过(见各节)。
- 全仓 grep 自检(codex 评审补,范围扩到所有 `chat_completions*.go`):
  - `writeJSONError(` 的 message 实参不再出现 `.Error()`;
  - `classifiedFailureFromDecision(` / `retryableLocalAttemptFailure(` /
    `terminalLocalAttemptFailure(` 的 message 实参不再出现 `.Error()`;
  - `ClientMessage` / `AbortReason` 赋值不再拼 `.Error()`;
  - `w.Header().Set("X-Huakai-*", *.Error())` 清零。
- codex per-commit review(`codex exec review --uncommitted`)无 S0/S1 真实缺陷。

## 8. 提交方式

按"一 commit 一模块":
- W3a:`gatewayhttp` 一个 commit(`upstream_http_error.go` 属 `gateway` 包但与
  GW-02 强耦合,可同 commit,提交信息说明)。标题
  `gatewayhttp 公开错误面脱敏`。
- W3b:`gateway` 一个 commit(`forwarder.go`+`event_scanner.go`,标题
  `gateway 流式错误帧与扫描分类脱敏`)、`eventbus` 一个 commit(标题
  `eventbus DLQ 失败可见与错误脱敏`)。
- 每个 commit 结尾 `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>`,
  无 type/无阶段号/无 PASS 字样。

## 9. clean-room

W3 全部改 HUAKAI 内部代码(`backend/`),不读任何参照项目源码 —— 无 clean-room
约束。收尾对照阶段(W3a / W3b 各闭合后)才读 MIT 参照项目的错误处理模块
(LiteLLM / Portkey / llmgateway 等),走 paraphrase 纪律。

---
作者:Claude。日期:2026-05-22。源波计划已 parallel-draft + 交叉评审
(`2026-05-22-audit-remediation-wave-{claude,codex}.md`);本 spec 是该已批准
波的实施细化,不另起 parallel-draft。
