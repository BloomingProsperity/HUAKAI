# 2026-05-24 W12-A D-4 Attempt Spool Durable + Replay Codex 平行计划

| Owner directive | "独立写 W12-A D-4 attempt spool durable + replay 实施计划平行稿" |
|---|---|
| Plan author | Codex Reviewer |
| UTC timestamp | 2026-05-23T17:23:42.9797707Z |
| Independence | 未读取 `docs/process/plans/2026-05-24-w12-d4-spool-claude.md`；仅使用 HUAKAI 内部 spec / 源码和本任务给定事实 |
| Clean-room lane | L0 HUAKAI-internal planning；未读取 sub2api / clewdr / litellm-rs / 其他参考项目 spool 源码 |
| Observed regions | 22 |
| Inferences | 8 |
| Open questions | 5 |

## §1 范围 + 不做

### 范围

D-4 只解决 Rust core gateway 的 attempt terminal report 可靠投递问题。权威 spec 已把方案定为 C2 durable spool，并把 AC 拆成 AC-1 / AC-2 / AC-3 / AC-4-pre / AC-4-post / AC-5；P0-7 要按 3 个 sub-slice 落地：spool 数据结构 + 写路径、replay worker + ack、满盘降级 + `reserve()` + post-commit loud failure（`docs/process/plans/2026-05-22-rust-hardening-plan.md:213`, `:231-252`, `:256-259`; `docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md:120`）。

当前必须修的两类静默丢账点已在 HUAKAI 源码中观察到：

- 入队丢：`AttemptReporter::report()` 对 bounded `mpsc::Sender` 调 `try_send`，Full / Closed 只返回 `DroppedFull` / `DroppedClosed` 并累加计数（`exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs:140-159`）。
- 投递耗尽丢：worker 在 ack=false 或最终错误后只累加 `failed_reports` 并返回（`exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs:215-276`）。
- 调用方忽略结果：`proxy_engine/mod.rs` 的 drop path 用 `let _ = reporter.report(...)`（`:113-120`），`relay.rs` 的 terminal helper 也忽略（`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:419-435`），listener 的 mock / planning error 分支也忽略（`exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs:94-100`, `:108-114`, `:226-232`, `:281-289`）。

实施范围按 tree-vertical 收敛在 Rust gateway 这棵树内：

- `attempt_reporter/`：spool 数据结构、持久化写入、reservation、replay worker、ack/delete、attempt-report 本地统计、测试用故障注入。
- `proxy_engine/`：planned forward 前的 pre-commit `reserve()` gate，post-commit report result 处理，新增 503 错误语义。
- `listener.rs`：去掉 `let _ =`，把 listener 侧 synthetic report 的失败也显式记录；planned 请求仍通过 proxy engine 统一 reserve。
- `config.rs`：新增 attempt spool 配置字段和校验。
- 必要的一行装配 glue：`lib.rs` 当前直接 `AttemptReporter::spawn(route_client)`（`exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs:72-84`），需要改为由 `StartupConfig` 生成 `AttemptReporterOptions` 后传入。这里不放业务逻辑，只接线。
- 测试支撑：`mock_control_plane.rs` 只加 test/public helper 以断言 idempotency 去重数量；mock 已有按 idempotency key 复用 ack 的内存 map（`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs:79`, `:335-343`），但现在只暴露 `attempt_reports_seen()` 和 `last_attempt_report()`（`:164-181`），不足以让 AC-3 mutation-resistant。

### 不做

- 不改 `route.proto`。`AttemptReportRequest.idempotency_key` 已存在于 field 20（`exploratory/rust-core-gateway/merged/proto/route.proto:77-97`）。
- 不改 Go control plane、billing ledger、quota enforcement、DB schema、migration、deployment scripts、真实 credentials、`LICENSE`。
- 不添加 runtime dependency。现有 crate 已有 `serde` / `serde_json` / `prost` / `tokio`（`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:45-49`）；测试临时目录用 `std::env::temp_dir()` + unique path，避免新增 `tempfile`。
- 不实现跨进程文件锁。W12-A 假设一个 gateway process 拥有一个 spool dir；多进程共享目录列为 Owner 决策点。
- 不把 spool 做成通用 billing ledger。它只是 Rust data-plane attempt report outbox；最终账务真相仍在控制面 / ledger。
- 不读取或借鉴任何参考项目 spool 源码；本计划只从 HUAKAI spec 和 HUAKAI 当前源码出发。

## §2 子切片划分

### Slice 1: spool 数据结构 + durable-first 写路径

**目标**

把 attempt terminal report 先写入本地 durable spool，再用内存队列作为加速通知。这样不只修 `try_send Full`，也覆盖"已入内存队列但进程在 ack 前崩溃"的真实丢账窗口。这个选择比 spec 里"Full 或 retry 耗尽时写 spool"更强；依据是当前内存队列由 `mpsc::channel(capacity)` 建立并只在进程内存在（`attempt_reporter/mod.rs:121-135`），不先落盘就无法满足崩溃恢复。

**文件影响**

- 新建 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/spool.rs`。目标模块是现有 Rust `attempt_reporter`，不是冻结 Go 包；符合 CLAUDE.md #13 的按职责拆分要求（`CLAUDE.md:139`; `AGENTS.md:526-568`）。
- 修改 `attempt_reporter/mod.rs`：接入 spool、扩展 options、worker 输入从裸 `AttemptReport` 调整为 spool key / report ref。
- 修改 `attempt_reporter/types.rs`：给 `AttemptReport` 增加本地-only `tenant_hint` / `account_hint`（不进 proto），或至少支持 drop log 从 `AttemptReportContext` 带出 request-scoped hint。`into_proto()` 继续只填现有 proto 字段（当前实现见 `attempt_reporter/types.rs:315-340`）。
- 修改 `config.rs`：只定义字段和默认值；实际接线在 Slice 3 完成。
- 测试：扩展 `tests/attempt_reporter_test.rs`，替换现有 "queue full drops" 测试。现测试明确期待 drop（`tests/attempt_reporter_test.rs:517-546`），D-4 后必须改成 "queue full spools and eventually acks"。

**API 设计**

`AttemptReporterOptions` 从 3 字段扩展为：

```rust
pub struct AttemptReporterOptions {
    pub queue_capacity: usize,
    pub retry_attempts: usize,
    pub retry_backoff: Duration,
    pub spool: AttemptSpoolOptions,
}

pub struct AttemptSpoolOptions {
    pub enabled: bool,
    pub dir: PathBuf,
    pub max_bytes: u64,
    pub high_watermark_bytes: u64,
    pub max_record_bytes: u64,
    pub replay_interval: Duration,
    pub replay_batch_size: usize,
    pub fsync_on_write: bool,
}
```

`spool.rs` 提供：

- `AttemptSpool::open(options) -> Result<Option<AttemptSpool>, GatewayError>`：disabled 时返回 None，只允许测试 baseline 使用；production config 是否允许 disabled 由 Owner 决策。
- `AttemptSpool::reserve() -> Result<AttemptSpoolReservation, AttemptSpoolBackpressure>`：检查 pending bytes + reserved bytes + `max_record_bytes` 是否低于 watermark，并检查最近一次写入健康。
- `AttemptSpool::persist(report, reservation) -> SpoolPersistOutcome`：把 report 编成 `AttemptReportRequest` prost bytes，写 `tmp/<uuid>.tmp`，`sync_data()`，原子 rename 到 `pending/<idempotency_key>.pb`，更新 in-memory bytes / records gauge。若 pending 文件已存在，视为 duplicate-safe success。
- `AttemptSpool::load_pending(key) -> Result<AttemptReportRequest, SpoolReadError>`。
- `AttemptSpool::ack(key) -> Result<(), SpoolAckError>`：成功 ack 后删除 pending file。若 ack 后删除失败，下次 replay 会重复发；这是可接受 at-least-once，因为控制面用 `idempotency_key` 去重。
- `AttemptSpool::pending_snapshot(limit) -> Vec<SpoolKey>`：startup replay 和 periodic replay 使用。

文件格式采用 file-per-record，而不是 append-only segment：

- `pending/<idempotency_key>.pb`：prost-encoded `AttemptReportRequest`。不新增 schema，不需要 serde derive。
- `tmp/`：写入临时文件后 rename。
- 不建 `acked/` 常驻目录；ack = 删除 pending。这样避免 compaction 在首版引入复杂度。

**测试 case + mutation**

1. `spool_persists_proto_record_and_loads_same_idempotency_key`
   - 构造一个 Success + http 200 + tokens non-zero 的 report，写 spool，load 回来。
   - 断言 idempotency_key / request_id / status / token source 全相等。
   - Mutation：把 persist 改成空写或漏字段，load 断言红。

2. `reporter_queue_full_spools_billable_attempt_instead_of_dropping`
   - 同一个测试内跑两路 self-proving fixture：baseline spool disabled + enabled spool。
   - queue_capacity=1，mock control plane attempt_report_delay 足够长，连续提交 32 条可计费 Success report。
   - baseline 必须出现 `DroppedFull` 或 ack 数小于 32；enabled 必须 `spooled_count > 0` 且最终 unique ack 数等于 32。
   - Mutation：删除 Full 分支的 spool persist，enabled 路退化为 baseline，测试红。

3. `reservation_rejects_when_pending_plus_reserved_crosses_watermark`
   - max_bytes=4096，watermark=2048，max_record_bytes=1024，连续拿 2 个 reservation 成功，第 3 个失败。
   - Drop 一个 guard 后再次 reserve 成功。
   - Mutation：去掉 reserved_bytes 计入，第三次错误成功，测试红。

### Slice 2: replay worker + ack + idempotency 断言

**目标**

启动时和运行中都能扫描 `pending/` 并重放；成功 ack 后删除 pending；控制面不可用时文件保留；重复重放不会造成重复入账。

**文件影响**

- `attempt_reporter/mod.rs`：`spawn_with_options` 同时启动 live worker 和 replay worker；`send_with_retry` 返回 `SendAttemptOutcome`，不再在 retry 耗尽时丢弃 report，只记录 delivery failure 并保留 pending file。
- `attempt_reporter/spool.rs`：pending scan、ack delete、corrupt file quarantine。
- `mock_control_plane.rs`：新增 `unique_attempt_keys_acked_count()`，返回 `attempt_ack_by_idempotency_key.len()`；必要时新增运行期切换 attempt behavior / failure count 的 test helper，支撑长期不可用恢复。
- `tests/attempt_reporter_test.rs`：AC-2 / AC-3。

**API 设计**

`AttemptReporter` 新增 counters/accessors：

- `spooled_count()`
- `replayed_count()`
- `spool_pending_count()`
- `spool_pending_bytes()`
- `spool_delivery_failed_count()`
- `spool_corrupt_count()`

`worker_loop` 不直接消费 `AttemptReport`，而消费 `SpoolKey`：

1. live `report()` 成功 persist 后，尝试 `try_send(key)` 通知 worker。
2. `try_send` Full 不再是丢失，只计 `queue_full_but_spooled`，等待 replay loop 扫描。
3. worker load pending file -> `route_client.report_attempt(proto)` -> ack true 删除 file。
4. ack false / transient exhausted / final error：计失败，保留 file，等待后续 replay。

**测试 case + mutation**

1. `replay_worker_recovers_spooled_report_after_restart`
   - 先直接用 `AttemptSpool` 或旧 reporter 把 report 写入 `pending/`，不 ack。
   - 构造新的 `AttemptReporter` 指向同一 spool dir，mock control plane normal。
   - 等到 `unique_attempt_keys_acked_count()==1`，并断言 pending file 被 ack 删除。
   - Mutation：不做 startup scan 或 ack 前删除文件，测试红。

2. `replay_keeps_same_idempotency_key_and_control_plane_dedups_duplicate`
   - 同一 idempotency_key 的 report 被发送两次：一次 live，一次模拟 ack-delete 失败后 replay。
   - 断言 `attempt_reports_seen()==2` 但 `unique_attempt_keys_acked_count()==1`，且两次 ack_id 相同。
   - Mutation：replay 重新生成 idempotency_key，unique count 变 2，测试红。

3. `retry_exhaustion_leaves_pending_spool_record`
   - mock attempt_report 持续 Unavailable，retry_attempts=0。
   - 提交 report 后等待 worker 失败一次。
   - 断言 `spool_delivery_failed_count()>0` 且 pending_count 仍为 1。
   - Mutation：保留旧 `failed_reports++ 后 return/drop` 语义并删除 file，pending_count 变 0，测试红。

### Slice 3: 满盘降级 + pre-commit reserve + post-commit loud failure

**目标**

把账务可靠性接到请求时序上：上游转发前可拒绝，响应头提交后不可改 HTTP，只能 loud metric/log。spec 明确这个时序拆分（`docs/process/plans/2026-05-22-rust-hardening-plan.md:227-233`, `:248-250`）。

**文件影响**

- `proxy_engine/error.rs`：新增 `AttemptReportBackpressure` 或 `AttemptSpoolUnavailable`，`status_code()` 返回 503，`code()` 返回 `attempt_report_spool_unavailable`。当前 `ProxyError` 没有 503 类别，只有 Timeout=504 和其他=502（`proxy_engine/error.rs:21-40`）。
- `proxy_engine/mod.rs`：
  - 在 `forward_planned` 进入上游前调用 `AttemptReporter::reserve()`；应放在 `mark_forwarding()` 之前或紧随其后但在 `forward_inner()` 之前。推荐放在 `mark_forwarding()` 之前，使 local lifecycle 不进入 Forwarding。
  - `terminal_reporter` 改为携带 reservation guard，terminal report 成功 persist 后 consume，未 report 则 Drop 释放。
  - `ReceiverByteStream` drop path 不再 `let _ =`，改为显式处理 `TerminalReportResult`（当前 drop path 在 `proxy_engine/mod.rs:101-127`）。
- `proxy_engine/relay.rs`：`report_terminal()` 返回/处理 result；post-commit drop 记录 loud counter + `tracing::error!`。
- `listener.rs`：mock upstream 和 planning error report 都显式调用 helper 处理失败；planned request 的 pre-commit gate 由 proxy engine 统一负责。
- `config.rs`：新增 env：
  - `HUAKAI_ATTEMPT_SPOOL_ENABLED`
  - `HUAKAI_ATTEMPT_SPOOL_DIR`
  - `HUAKAI_ATTEMPT_SPOOL_MAX_BYTES`
  - `HUAKAI_ATTEMPT_SPOOL_HIGH_WATERMARK_BYTES`
  - `HUAKAI_ATTEMPT_SPOOL_MAX_RECORD_BYTES`
  - `HUAKAI_ATTEMPT_SPOOL_REPLAY_INTERVAL_MS`
  - `HUAKAI_ATTEMPT_SPOOL_REPLAY_BATCH_SIZE`
  - `HUAKAI_ATTEMPT_SPOOL_FSYNC_ON_WRITE`
- `lib.rs`：将 `StartupConfig` 的 spool options 接入 `AttemptReporter::spawn_with_options`。

**API 设计**

`AttemptTerminalReporter` 增加 phase-aware helper：

- `report_pre_response(...)`：用于还没提交 response 的 synthetic / error path；失败记录 non-billable or pre-response metric。
- `report_post_response(...)`：用于 streaming terminal / drop path；如果 persist 失败，HTTP 不可变，只 increment `spool_drop_billable`（若 billable candidate）并 `tracing::error!`。

Drop log 字段：

- 必有：`request_id`, `idempotency_key`, `route_plan_id`, `attempt_id`, `status`, `http_status`, `tokens_total`, `bytes_out`, `spool_error`.
- 尽力：`tenant_hint`, `account_hint`。当前 `RouteQueryRequest` 有 tenant（`route.proto:28-38`），`PlannedAttempt` 有 account_id（`account_planner.rs:194-202`, `:310-317`），但 `AttemptReportRequest` 没有 tenant/account 字段（`route.proto:77-97`）。因此本 slice 只能把 tenant/account 作为本地 log hint，不改 proto。

`reserve()` 失败语义：

- 如果 `pending_bytes + reserved_bytes + max_record_bytes >= high_watermark_bytes`，返回 backpressure。
- 如果最近一次 persist 物理失败，返回 backpressure，直到 replay/health probe 成功清除。
- 如果 spool disabled 且 production config 不允许 disabled，启动期 fail-fast；如果 tests baseline 显式 disabled，`reserve()` 返回 OK 但不会提供 durability，测试只用于 self-proving baseline。

**测试 case + mutation**

1. `pre_commit_reserve_failure_returns_503_before_upstream`
   - 配置 watermark 已满或 test hook `force_reserve_failure=true`。
   - 通过 listener 发 planned request。
   - 断言 response 503，mock upstream `requests_seen()==0`，mock control plane没有 attempt ack。
   - Mutation：删除 `reserve()` 调用，request 打到 upstream 并返回 200/502，测试红。

2. `post_commit_spool_write_failure_keeps_http_200_and_increments_billable_drop`
   - streaming upstream 先返回 200 header + body；response status 已在 client 侧观察为 200。
   - 在 terminal report 前用 test-only hook 让 spool persist 失败，同时 control plane 不可用，确保不能靠 live send 掩盖。
   - 断言 client 看到的 HTTP status/body 不变；`spool_drop_billable_count()+1`；error log 字段含 request_id / idempotency_key / token or bytes。
   - Mutation：删除 metric/log 分支，HTTP 仍 200 但 counter 不增，测试红。

3. `long_control_plane_outage_spools_to_cap_then_drains_after_recovery`
   - mock control plane attempt_report 先长期 Unavailable。
   - 提交 N 条可计费 reports，N 控制在 cap 内；断言 pending_count=N、unique ack=0。
   - 切回 Normal，等待 replay worker drain；断言 unique ack=N、pending_count=0。
   - 再把 pending bytes 推近 watermark，发一个新 planned request；断言 AC-4-pre 503 且 upstream 0。
   - Mutation：删除 replay loop，恢复后 pending 不降；删除 watermark gate，新请求打到 upstream；两种 mutation 都红。

## §3 关键设计决策

1. **durable-first，而不是 failure-only spool。**
   - 观察：现有 live queue 是内存 `mpsc`，spawn 后 worker 异步消费（`attempt_reporter/mod.rs:121-135`, `:203-212`）。
   - 推断：如果只在 Full / retry exhausted 时写 spool，"成功入队但进程崩溃"仍会丢；这不满足 durable spool 的崩溃生还目标。
   - 决策：每个 terminal report 先 persist pending file，再通知 live queue；queue full 只影响延迟，不影响丢失。

2. **ack = 删除 pending file；重复 replay 依赖 idempotency key 去重。**
   - 观察：`idempotency_key` 已由 request_id + attempt_id + acquisition_token 构造（`attempt_reporter/idempotency.rs:3-14`），并透传到 proto field 20（`route.proto:77-97`）；mock 已按 idempotency key 复用 ack（`mock_control_plane.rs:335-343`）。
   - 决策：ack 后删除 pending file。若 ack 成功但 delete 失败，下次 replay 会重复发送同 key；控制面去重后不重复入账。

3. **file-per-record 首版优先于 append-only segment。**
   - 理由：首版重点是 AC 闭合和 mutation-resistant tests；file-per-record 避免 segment compaction / partial ack bitmap / torn write 恢复复杂度。
   - 代价：高 QPS 下 inode / fsync 成本更高；列入后续性能优化，不阻塞 W12-A。

4. **同步小文件 IO 是刻意选择。**
   - `AttemptTerminalReporter::report()` 当前是同步 API，且 `ReceiverByteStream` 的 `PinnedDrop` 无法 await（`proxy_engine/mod.rs:101-127`）。改成 async 会扩大时序风险。
   - 决策：persist 用 bounded-size blocking file write + optional `sync_data()`；通过 `max_record_bytes` 防止异常大记录阻塞。性能风险用 load smoke 和 Owner 的 fsync 默认决策控制。

5. **reservation 不是两阶段事务。**
   - `reserve()` 只证明"此刻 spool 未过 watermark 且最近写健康"，并通过 RAII guard 预留字节预算。它不能防止 reserve 后磁盘被外部写满或硬件错误。
   - 因此 AC-4-post 仍必须存在：响应头已提交后如果 persist 失败，HTTP 不改，metric/log 必须响。

6. **post-commit drop 的 HTTP 不可逆。**
   - 观察：`forward_inner` 获得上游 response 后把 body 包成 stream 返回 client（`proxy_engine/mod.rs:305-338`），terminal report 发生在 relay terminal / body drop（`proxy_engine/relay.rs:371-435`; `proxy_engine/mod.rs:101-127`）。
   - 决策：post-commit 只记录 loud metric/log；不得试图把 200 改 5xx。

7. **tenant/account 先做本地 log hint，不改 proto。**
   - 观察：route query 有 tenant，planned attempt 有 account，attempt report proto 没有 tenant/account 字段。
   - 决策：在本地 `AttemptReport` / log context 保存 hint；`into_proto()` 忽略它。若 Owner 要控制面也收到 tenant/account，需要另开 proto/API 决策，不放 D-4。

8. **测试必须 self-proving。**
   - CLAUDE.md #14 要求测试能在对应 mutation 下变红（`CLAUDE.md:141`; `AGENTS.md:570-608`）。
   - 每个 AC 测试都明确引入 baseline 或故障触发，不使用 "nil stub 绿灯"。

## §4 风险 + 缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| blocking fsync 增加 terminal report latency | 流式尾帧 / drop path 可能阻塞 tokio worker | `max_record_bytes` 限制，默认记录很小；load smoke 覆盖；Owner 决定 `fsync_on_write` production 默认 |
| file-per-record 在高 QPS 下 inode 压力 | replay / scan 变慢 | W12-A 先闭合 correctness；暴露 pending_count / pending_bytes / replay lag；后续可换 append-only segment，外部 API 不变 |
| ack 删除失败导致重复 replay | 控制面收到重复 attempt report | 保持同 idempotency_key；AC-3 用 unique key count 证明去重 |
| reserve 后磁盘突然满 | response 已提交后无法改 HTTP | AC-4-post loud metric/log；pre-commit watermark 尽量提前挡新请求 |
| tenant 字段不在 attempt proto | post-commit drop log 难满足 "tenant" 字段 | 本地 tenant_hint 从 request header 带入日志；Owner 决定是否未来 proto 化 |
| mock fault knobs 太弱导致 AC-5 测试不判别 | 测试可能只证明"能跑" | 给 mock 增 test helper：unique key count、attempt behavior/failure 切换；测试断言恢复前后 pending/ack 变化 |
| config 默认选错导致生产误用 disabled spool | 继续静默丢账 | 推荐 production validate 要求 spool enabled + dir 可写；Owner 决策见 §6 |
| 新增 runtime dependency | 触发 license / supply-chain review | 不新增 dependency；只用 std/tokio/prost/serde_json existing deps |

## §5 commit 计划

推荐 **3 commits**，对应 3 个 sub-slice。理由：D-4 是 W12 最重单项，三段各自有可运行测试，review 时能明确定位 correctness / replay / HTTP 时序问题。合 1 commit 会让 reviewer 同时审 IO、worker、proxy 时序和配置，风险过大。

### Commit 1: `feat(rust-gateway): add durable attempt spool write path`

- 新增 `attempt_reporter/spool.rs`。
- 扩展 `AttemptReporterOptions` / `ReportEnqueueResult`。
- 实现 durable-first persist + queue notification。
- 改写 queue-full 测试为 AC-1 self-proving。
- 运行：
  - `cd exploratory/rust-core-gateway/merged`
  - `cargo test -p core_gateway attempt_reporter`
  - `cargo fmt --check`

### Commit 2: `feat(rust-gateway): replay and ack spooled attempt reports`

- 启动 startup replay + periodic replay worker。
- worker ack 后删除 pending；retry exhausted 保留 pending。
- mock 增 `unique_attempt_keys_acked_count()` 和必要 fault helper。
- 覆盖 AC-2 / AC-3。
- 运行：
  - `cargo test -p core_gateway attempt_reporter`
  - `cargo test -p core_gateway duplicate_attempt_report_is_accepted_with_same_ack_id`

### Commit 3: `feat(rust-gateway): gate upstream forwarding on attempt spool reserve`

- config 字段 + `lib.rs` wiring。
- `proxy_engine` pre-commit reserve failure -> 503。
- post-commit drop loud counter/log；替换 `let _ =` report 调用。
- 覆盖 AC-4-pre / AC-4-post / AC-5。
- 运行：
  - `cargo test -p core_gateway attempt_reporter`
  - `cargo test -p core_gateway proxy_engine`
  - `cargo test -p core_gateway config`
  - `cargo test -p core_gateway`
  - `cargo clippy -p core_gateway --all-targets -- -D warnings`

每个 commit 前按项目规则 stage 后跑 `codex exec review --uncommitted --full-auto`；HIGH 必修，MED 修或在 commit body 记录。

## §6 Owner 决策点

1. **生产默认 spool dir。** 推荐：production 要求 `HUAKAI_ATTEMPT_SPOOL_ENABLED=true` 且 `HUAKAI_ATTEMPT_SPOOL_DIR` 显式配置、可创建、可写；缺失则启动 fail-fast。备选：默认 `./data/attempt-spool`，但 cwd 不稳定。

2. **`fsync_on_write` 默认。** 推荐 production=true，test=false 可显式覆盖。若 Owner 选择 false，需要承认 OS crash 时最后若干 report 可能只在 page cache。

3. **默认容量。** 推荐 `max_bytes=1GiB`、`high_watermark=80%`、`max_record_bytes=64KiB`、`replay_batch_size=128`、`replay_interval=250ms`。需要 Owner 判断本地磁盘预算。

4. **是否允许一行 `lib.rs` wiring + test-only `mock_control_plane.rs` helper。** 严格 tree-vertical 的产品代码仍在 attempt_reporter/proxy_engine/listener/config；但没有 `lib.rs` 接线 config 不生效，没有 mock unique count AC-3 不够判别。

5. **loud metric 是否必须进 Prometheus `/metrics` 同 slice。** 若必须，需要触碰 crate-level `src/metrics.rs`，超出用户给的树范围。推荐 W12-A 先提供 `AttemptReporter` counters + structured `tracing::error!`，Prometheus export 作为 Owner 明确批准的范围扩展或紧随小 commit。

## §7 验收清单

- [ ] AC-1 溢出无丢：queue full 时 enabled spool 的 32 条可计费 Success report 最终 unique ack=32；baseline disabled 路能证明旧行为会 drop。
- [ ] AC-2 崩溃恢复：pending file 在新 reporter 启动后 replay 并 ack 删除。
- [ ] AC-3 重放幂等：同一 idempotency_key 被发送两次时 `attempt_reports_seen()==2` 且 `unique_attempt_keys_acked_count()==1`。
- [ ] AC-4-pre：reserve fail 时 listener/proxy 返回 503，上游 `requests_seen()==0`，响应头未提交。
- [ ] AC-4-post：响应头已提交后 spool persist 失败，client HTTP status/body 不变，`spool_drop_billable` counter +1，error log 含 request_id / idempotency_key / route_plan_id / token-or-bytes evidence。
- [ ] AC-5 长期不可用：control plane down 时 pending 增长到 cap 内零丢；恢复后 replay drain；接近 watermark 后新请求走 503 pre-commit gate；超 cap 的 post-commit drop loud。
- [ ] 不改 `route.proto`，`idempotency_key` 沿用 field 20。
- [ ] 不新增 runtime dependency。
- [ ] `let _ = reporter.report(...)` 在 D-4 相关路径清零或有显式注释说明非账务路径。
- [ ] `cargo fmt --check` 通过。
- [ ] `cargo test -p core_gateway` 通过。
- [ ] `cargo clippy -p core_gateway --all-targets -- -D warnings` 通过，若环境缺依赖则记录真实失败原因。
- [ ] staged 后 `codex exec review --uncommitted --full-auto` 无 HIGH。

## Clean-room Attestation

我只读取了 HUAKAI 内部文档和 HUAKAI Rust gateway 源码；没有读取 `docs/process/plans/2026-05-24-w12-d4-spool-claude.md`；没有读取 sub2api / clewdr / litellm-rs / Portkey / Helicone / Envoy AI Gateway 等参考项目的 spool 源码。本计划没有复制非 MIT 参考实现、文件结构、注释、schema 或算法细节。D-4 的方案来自 HUAKAI 权威 spec 与当前 HUAKAI 代码缺口。

Signed: Codex Reviewer, 2026-05-23T17:23:42.9797707Z.

Source files read:

- `AGENTS.md`
- `CLAUDE.md`
- `docs/RULES.md`
- `docs/01_PROJECT_BRIEF.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `docs/15_RELEASE_GATES.md`
- `docs/process/plans/2026-05-22-rust-hardening-plan.md`
- `docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md`
- `exploratory/rust-core-gateway/merged/proto/route.proto`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/metrics.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/types.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/idempotency.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/metrics.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/error.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/attempt_reporter_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/proxy_engine_test.rs`

Lane: L0 HUAKAI-internal planner / reviewer.
Agent: Codex Reviewer.
UTC timestamp: 2026-05-23T17:23:42.9797707Z.
