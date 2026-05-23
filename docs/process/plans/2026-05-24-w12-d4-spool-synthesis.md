# W12-A D-4 attempt spool durable + replay 合成定稿

日期：2026-05-24
合成自：`2026-05-24-w12-d4-spool-claude.md` + `2026-05-24-w12-d4-spool-codex.md`
契约：本稿权威；两份平行稿留作历史。
触发：synthesis §5.1 P0-7（最后一项 P0）

---

## §0 双稿差异 + 合成决策

| # | 项 | Claude | Codex | 合成 | 理由 |
|---|---|---|---|---|---|
| D-1 | **Spool 写时机** | fail-fallback（只 Full / retry 耗尽时写） | **Durable-first**（每条 report 都先写 spool） | **Codex** | spec rev2→rev3 措辞偏 failure-only，但 AC-2 "崩溃恢复" 测试要求"已写 spool 未 ack"——成功入队后崩溃在 failure-only 模式下无法满足。Codex 论点更强。 |
| D-2 | **Spool 文件格式** | 单 append-only newline JSON + ack 同目录 log | **file-per-record + prost + ack=删除** | **Codex** | ack 语义最简，corrupt 隔离细，复用已有 prost 依赖。代价：高 QPS inode 多——Codex 明示后续可换 segment，API 不变。 |
| D-3 | **reserve() 时机** | `mark_forwarding()` 之后 | **`mark_forwarding()` 之前** | **Codex** | reserve 失败 → 503 不应进 Forwarding 状态。语义更清。 |
| D-4 | **Reservation 模型** | slot（容量计数） | **bytes（max_record_bytes 字节预算）** | **Codex** | spool 字节限是物理量，slot 是抽象量；watermark 用字节判定。 |
| D-5 | **IO 策略** | tokio::fs 异步 + flush + sync_all | **同步 blocking small file write + optional sync_data** | **Codex** | reporter 是 sync API，`PinnedDrop` 不能 await。Codex 论点对，但应 `tokio::task::spawn_blocking` 包装防阻塞 reactor。 |
| D-6 | **mock 暴露 helper** | `unique_keys_count()` | `unique_attempt_keys_acked_count()` | **两稿一致** | Codex 命名更准确（已 ack 才算 unique）。 |
| D-7 | **commit 数** | 3 commit | 3 commit | **3 commit 一致** | spec 明示 sub-slice 3 段。 |
| D-8 | **proxy_engine/error.rs 503 类别** | 未提 | **新增 `AttemptReportBackpressure` → 503** | **Codex** | Claude 漏掉：当前 ProxyError 只有 Timeout=504 + 其他=502，需新建 503 类别。 |
| D-9 | **lib.rs wiring** | 未细究 | **当前 `spawn(route_client)` 改 `spawn_with_options`** | **Codex** | Claude 漏掉接线点。trivial 1 行，归 Slice 3。 |
| D-10 | **tenant/account hint** | 未提 | **proto 无字段 → 只本地 log hint** | **Codex** | 重要：post-commit drop log 字段不能塞 tenant 到 proto（无字段），只能写到 tracing::error! 本地日志。 |
| D-11 | **测试 fixture 规模** | 单条 + cap=1 | 32 条 + queue_capacity=1 + attempt_report_delay | **Codex** | 多条更能反映真 backlog；delay 触发 try_send Full。 |
| D-12 | **报告 phase 区分** | 单一 report() | **`report_pre_response()` + `report_post_response()`** | **Codex** | post-commit 不可改 HTTP 是硬约束，API 分离更安全。 |

**采纳率**：Codex 12 处差异中 12 处采纳。Claude 平行稿主要价值：早识别 mock_control_plane.rs 已有 dedup map（337-343 引用）、Reservation RAII 概念、self-proving 测试形态。

---

## §1 范围 + 不做

### §1.1 做

- 新 `attempt_reporter/spool.rs`：`AttemptSpool` durable-first persist + file-per-record + prost + ack=删除
- `AttemptReporter::reserve()` 返回 `Reservation` RAII guard（字节预算模型）
- `AttemptReporterOptions` 扩展 `AttemptSpoolOptions { enabled, dir, max_bytes, high_watermark_bytes, max_record_bytes, replay_interval, replay_batch_size, fsync_on_write }`
- `worker_loop` 改造：消费 spool key 而非裸 report；live `try_send` Full → spool 兜底（不丢）
- 启动期 + 周期性 replay worker
- `proxy_engine/error.rs` 加 `AttemptReportBackpressure`（status=503, code=`attempt_report_spool_unavailable`）
- `forward_planned` 在 `mark_forwarding()` **前**调 `reserve()`；Err → 503
- 替换 `let _ =`：`proxy_engine/mod.rs:114`、`relay.rs:428`、`listener.rs:94-100/108-114/226-232/281-289`
- `mock_control_plane.rs` 加 `unique_attempt_keys_acked_count()` test helper
- `config.rs` 加 env：`HUAKAI_ATTEMPT_SPOOL_ENABLED/DIR/MAX_BYTES/HIGH_WATERMARK_BYTES/MAX_RECORD_BYTES/REPLAY_INTERVAL_MS/REPLAY_BATCH_SIZE/FSYNC_ON_WRITE`
- `lib.rs` 改 `AttemptReporter::spawn(route_client)` 为 `spawn_with_options`（带 spool options）
- 6 AC 各一条 mutation-resistant 测试 + 1 self-proving 头条
- post-commit drop loud metric (`spool_drop_billable_count`) + `tracing::error!` 含 request_id / idempotency_key / route_plan_id / tokens / bytes

### §1.2 不做

- ❌ exactly-once（at-least-once + idempotency_key + 控制面去重 ≈ exactly-once）
- ❌ 加密 / 签名 spool 文件（本地可信威胁模型）
- ❌ 跨进程文件锁（W12-A 假设 one process per dir）
- ❌ proto 改动（idempotency_key 已 field 20）
- ❌ tenant/account proto 字段（只本地 log hint）
- ❌ append-only segment 格式（首版 file-per-record，留 API 后续可换）
- ❌ 新增 runtime dependency（serde / serde_json / prost / tokio 已有；测试 dir 用 `std::env::temp_dir()`）
- ❌ Prometheus `/metrics` export 同 slice（counter 在 AttemptReporter 内，export 见 Owner 决策 OD-5）
- ❌ 控制面侧改动（去重在 Go 控制面消费侧）

---

## §2 子切片划分

### §2.1 Slice 1：`AttemptSpool` durable-first 持久化（Commit 1）

**目标**：每条 terminal report 先 persist `pending/<key>.pb`，再通知 live queue。live queue Full 不再是丢失，只是延迟。

**新文件**：`crates/core_gateway/src/attempt_reporter/spool.rs`

**API**：
```rust
pub struct AttemptSpool { /* dir, max_bytes, high_watermark_bytes, reserved_bytes, current_pending_bytes */ }
pub struct AttemptSpoolReservation { /* bytes, spool_ref */ }
pub enum AttemptSpoolBackpressure { WatermarkExceeded, LastWriteFailed, Disabled }
pub struct SpoolPersistOutcome { /* key, bytes_written */ }

impl AttemptSpool {
    pub fn open(options: AttemptSpoolOptions) -> Result<Option<Self>, GatewayError>;
    pub fn reserve(&self) -> Result<AttemptSpoolReservation, AttemptSpoolBackpressure>;
    pub fn persist(&self, report: &AttemptReport, reservation: AttemptSpoolReservation) -> Result<SpoolPersistOutcome, SpoolError>;
    pub fn load_pending(&self, key: &str) -> Result<AttemptReportRequest, SpoolReadError>;
    pub fn ack(&self, key: &str) -> Result<(), SpoolAckError>;
    pub fn pending_snapshot(&self, limit: usize) -> Vec<String>;
    pub fn pending_count(&self) -> usize;
    pub fn pending_bytes(&self) -> u64;
}
```

**On-disk layout**：
```
<dir>/
├── pending/<idempotency_key>.pb      # prost-encoded AttemptReportRequest
└── tmp/<uuid>.tmp                     # write-then-rename atomic
```

**写流程**：
1. `tokio::task::spawn_blocking` 包装同步 file IO
2. 写 `tmp/<uuid>.tmp` → optional `sync_data()` → 原子 `rename` 到 `pending/<key>.pb`
3. 失败 → `SpoolError`；duplicate-safe（rename 已存在 → 视为 OK）

**测试 (Slice 1)**：
1. `spool_persists_proto_record_and_loads_same_idempotency_key`
   - Success+200+tokens 非零 → persist → load → idempotency_key/request_id/status/source 全相等
   - **mutation**：persist 空写 / 漏字段 → load 红
2. `reporter_queue_full_spools_billable_attempt_instead_of_dropping`（self-proving 头条）
   - 同测内跑 spool disabled (baseline) + enabled (ENABLED) 两路
   - queue_capacity=1, mock attempt_report_delay=200ms, 提交 32 条 billable
   - baseline: `DroppedFull > 0` 或 unique ack < 32
   - enabled: `spooled_count > 0` 且 unique ack = 32
   - **mutation**：删除 Full→spool 分支 → enabled 退化 baseline → 红
3. `reservation_rejects_when_pending_plus_reserved_crosses_watermark`
   - max_bytes=4096, watermark=2048, max_record_bytes=1024
   - 拿 2 个 reservation OK，第 3 个 Backpressure；Drop 一个 guard → 再 reserve OK
   - **mutation**：去掉 reserved_bytes 计入 → 第 3 次 OK → 红

### §2.2 Slice 2：replay worker + ack + idempotency（Commit 2）

**目标**：启动期 + 周期性扫 `pending/`，重发 → ack=true → 删 pending；重复 replay 控制面按 idempotency_key 去重。

**改动**：
- `AttemptReporter::spawn_with_options` 同时启 live worker + replay worker
- `send_with_retry` 返回 `SendAttemptOutcome`（不再 `failed_reports++ return`），失败保留 pending file
- `mock_control_plane.rs:` 加 `unique_attempt_keys_acked_count() -> usize`
- 新 counters：`spooled_count` / `replayed_count` / `spool_pending_count` / `spool_pending_bytes` / `spool_delivery_failed_count` / `spool_corrupt_count`

**测试 (Slice 2)**：
1. `replay_worker_recovers_spooled_report_after_restart`
   - 直接用 `AttemptSpool::persist` 写 pending file
   - 构造新 `AttemptReporter` 指向同 dir → 等 `unique_attempt_keys_acked_count() == 1` → 断言 pending file 已删
   - **mutation**：跳过 startup scan 或 ack 前不删 → 红
2. `replay_keeps_same_idempotency_key_and_control_plane_dedups_duplicate`
   - 同 key 两次：一次 live, 一次模拟 ack-delete fail 后 replay
   - 断言 `attempt_reports_seen == 2` 但 `unique_attempt_keys_acked_count == 1`，且两次 ack_id 相同
   - **mutation**：replay 重新生成 key → unique=2 → 红
3. `retry_exhaustion_leaves_pending_spool_record`
   - mock 持续 Unavailable，retry_attempts=0
   - 提交 → 等 worker 失败 → 断言 `spool_delivery_failed_count > 0` 且 `pending_count == 1`
   - **mutation**：保留旧 "fail++ return + delete file" 语义 → pending=0 → 红

### §2.3 Slice 3：pre-commit reserve gate + post-commit loud failure + 配置接线（Commit 3）

**目标**：reserve fail → 503 in `forward_planned`；HTTP 已提交后 spool fail → metric + log + HTTP 不变。

**改动**：
- `proxy_engine/error.rs` 加 `AttemptReportBackpressure`（status=503）
- `proxy_engine/mod.rs::forward_planned`：在 `mark_forwarding()` **前**调 `state.attempt_reporter().reserve()`；Err → 503
- `AttemptTerminalReporter::report_pre_response()` / `report_post_response()` 区分
- post-commit drop 路径：`spool_drop_billable_count.fetch_add(1)` + `tracing::error!(request_id, idempotency_key, route_plan_id, tokens, bytes, ...)`
- 替换所有 `let _ = reporter.report(...)`
- `config.rs` 加 env + `AttemptSpoolOptions::from_env()` + production fail-fast if enabled+dir 不可写
- `lib.rs` 改 `AttemptReporter::spawn(rc)` 为 `spawn_with_options(rc, opts)`

**测试 (Slice 3)**：
1. `pre_commit_reserve_failure_returns_503_before_upstream`
   - watermark 已满 / test hook `force_reserve_failure=true`
   - listener 发 planned request → 断言 response=503, mock upstream `requests_seen == 0`, mock CP attempt_ack=0
   - **mutation**：删 reserve() 调用 → upstream 收到请求 → 红
2. `post_commit_spool_write_failure_keeps_http_200_and_increments_billable_drop`
   - streaming upstream 返 200 header+body
   - terminal report 前 test hook spool persist 失败 + CP 不可用
   - 断言 client HTTP=200 不变 + `spool_drop_billable_count == 1` + error log 含 request_id/idempotency_key/tokens
   - **mutation**：删 metric/log → HTTP 仍 200 但 counter=0 → 红
3. `long_control_plane_outage_spools_to_cap_then_drains_after_recovery`
   - CP 长期 Unavailable → 提交 N 条（N < cap）→ `pending_count == N`, unique ack=0
   - CP 切回 Normal → worker drain → unique ack=N, `pending_count == 0`
   - pending bytes 推近 watermark → 新 planned request → 断言 503 + upstream 0
   - **mutation**：删 replay loop → 恢复后 pending 不降 → 红；删 watermark gate → 新请求打到 upstream → 红

---

## §3 关键设计决策

1. **Durable-first**（Codex D-1）：每条 report 都 persist；live queue 只加速通知。这是 AC-2 崩溃恢复的物理前提。
2. **file-per-record + prost**（Codex D-2）：ack=删除最简，复用现有 prost 依赖，无 segment compaction。
3. **bytes-based reservation**（Codex D-4）：watermark 用字节判定更准确。RAII guard Drop 释放预算。
4. **同步 blocking IO + `spawn_blocking` 包装**（Codex D-5 + Claude IO 顾虑）：reporter 是 sync API，但用 `tokio::task::spawn_blocking` 防 reactor 阻塞。
5. **ack=删除 pending file**（Codex）：若 ack 后 delete 失败，下次 replay 重发同 key，控制面 idempotency_key 去重 → 单一效应。
6. **post-commit drop HTTP 不可逆**（两稿一致）：只 loud metric+log，绝不试图改 200→5xx。
7. **tenant/account 只本地 log**（Codex D-10）：proto 无字段，AC-4-post 测试用 tokens/bytes 作 evidence。
8. **mock 暴露 `unique_attempt_keys_acked_count`**（两稿一致）：AC-3 判别要求。
9. **测试 self-proving**（CLAUDE.md #14）：每个 AC 一条 mutation；头条 ENABLED vs BASELINE 同测内双路。

---

## §4 风险 + 缓解

| R | 风险 | 缓解 | 出处 |
|---|---|---|---|
| R-1 | blocking fsync 增 terminal latency | `max_record_bytes` 限制小记录 + load smoke 覆盖 + production `fsync_on_write` 由 Owner 选 | Codex |
| R-2 | file-per-record 高 QPS inode 压力 | 首版重 correctness，暴露 pending_count/bytes/replay lag 指标 → 后续切 segment（API 不变） | Codex |
| R-3 | ack delete 失败 → 重复 replay | 同 idempotency_key + AC-3 用 unique key 数证明去重 | Codex |
| R-4 | reserve 后磁盘突然满 | AC-4-post loud metric + watermark 提前挡新请求 | Codex |
| R-5 | tenant 不在 proto → 日志 evidence 不足 | 本地 `tenant_hint`/`account_hint` 从 PlannedAttempt 带入 log | Codex |
| R-6 | mock fault knobs 弱 → AC-5 不判别 | mock 增 unique key count + 运行时切 attempt_failures（部分 P1-5 提前实施） | Codex |
| R-7 | config 默认错 → 生产用 disabled spool 继续静默丢账 | production validate 要求 enabled+dir 可写，否则 fail-fast；Owner 决策 OD-1 | Codex |
| R-8 | reserve() RAII consume 接线复杂 | Reservation 内含 `consume()` 显式调用；Drop 不消费 = 还字节预算；测试覆盖正常/错误/panic 三路 | Claude |
| R-9 | clean-room：sub2api 也用 spool/outbox | 行为 parity 不构成抄袭（CLAUDE.md #11 L0）；不读 sub2api/clewdr/litellm-rs spool 源；on-disk 格式 = HUAKAI 自选；attestation 必加 | Claude |
| R-10 | Reservation 用 `tokio::task::spawn_blocking` 阻塞但 `PinnedDrop` 无法 await | drop path 用 `tokio::runtime::Handle::current().spawn_blocking` fire-and-forget；丢 spool 持久化失败计 `spool_drop_billable` | 合成 |

---

## §5 commit 计划（3 commit，强烈推荐）

每 commit：cargo test 全绿 + clippy `-D warnings` 0 告警 + codex per-commit review（≤2 round；剩 P2/P3 form 终止）+ Clean-room-attestation。

### Commit 1: `feat(rust-gateway): W12-A D-4 切片 1 attempt spool durable-first 持久化`

文件：
- 新 `attempt_reporter/spool.rs` (~280 行)
- 改 `attempt_reporter/mod.rs`：扩展 `AttemptReporterOptions` + `AttemptSpoolOptions` (~60 行)
- 改 `attempt_reporter/types.rs`：`tenant_hint`/`account_hint` 字段 + `report_pre_response`/`report_post_response` API (~50 行)
- 改 `tests/attempt_reporter_test.rs`：替换旧 "queue full drops" → "queue full spools" + 2 个新 spool 单元 (~150 行)
- 不接入 reporter 主路径（spool 独立可测）
- acceptance: AC-1 头条 (self-proving) + spool 基本 API

### Commit 2: `feat(rust-gateway): W12-A D-4 切片 2 replay worker + ack + idempotency`

文件：
- 改 `attempt_reporter/mod.rs`：`spawn_with_options` 启 live+replay worker；`send_with_retry` 改返 `SendAttemptOutcome` (~100 行)
- 改 `attempt_reporter/spool.rs`：pending scan + ack delete + corrupt quarantine (~80 行)
- 改 `mock_control_plane.rs`：加 `unique_attempt_keys_acked_count()` + `set_attempt_failures_remaining` 运行时切 (~30 行)
- 改 `tests/attempt_reporter_test.rs`：3 个新集成测试 AC-2/AC-3 (~200 行)
- acceptance: AC-2 + AC-3

### Commit 3: `feat(rust-gateway): W12-A D-4 切片 3 reserve() pre-commit + post-commit loud + 配置接线`

文件：
- 改 `proxy_engine/error.rs`：加 `AttemptReportBackpressure` (~15 行)
- 改 `proxy_engine/mod.rs`：`forward_planned` 加 reserve() + 替换 `let _ =` (~40 行)
- 改 `proxy_engine/relay.rs`：terminal helper 替换 `let _ =` + post-commit drop loud (~30 行)
- 改 `listener.rs`：4 处 `let _ =` 替换 (~25 行)
- 改 `config.rs`：8 个 env + `AttemptSpoolOptions::from_env` + production validate (~80 行)
- 改 `lib.rs`：`spawn(rc)` → `spawn_with_options(rc, opts)` (~10 行)
- 改 `tests/proxy_engine_test.rs` + `tests/listener_test.rs`：3 个集成测试 AC-4-pre/AC-4-post/AC-5 (~250 行)
- acceptance: AC-4-pre + AC-4-post + AC-5

**总估**：~700 行源码 + ~600 行测试 = ~1300 行；2-3 codex-day。

---

## §6 Owner 决策点

| OD | 项 | 推荐 | 备注 |
|---|---|---|---|
| **OD-1** | production spool dir 默认 | **要求显式 env** (`HUAKAI_ATTEMPT_SPOOL_DIR` 必填) + dir 可写 → 启动 fail-fast；dev/test 默认 `tempfile` 路径 | Codex 推荐，安全 |
| **OD-2** | `fsync_on_write` 默认 | **production=true / test=false** | Codex 推荐；OS crash 时不丢已 fsync 记录 |
| **OD-3** | 容量默认 | **max_bytes=1 GiB / high_watermark=80% / max_record_bytes=64 KiB / replay_batch_size=128 / replay_interval=250ms** | Codex 推荐 |
| **OD-4** | spool 默认 `enabled` | **production=true（必须显式 dir）/ dev=false（不破坏现有测试）** | 默认开是 anti-丢账；测试要 baseline 显式 false |
| **OD-5** | Prometheus `/metrics` export 同 slice 否 | **延后到独立小 commit**（W12-A 收尾）：counters 在 AttemptReporter 内已暴露，Prometheus export 需碰 `src/metrics.rs`（超 tree-vertical 范围），单独 commit 更清晰 | Codex 提醒 |
| **OD-6** | `lib.rs` 1 行 wiring + mock test helper 是否算超 tree-vertical | **算合理 glue**（lib.rs 是装配点，mock 是 test-only） | Codex 提醒 |
| **OD-7** | 切 3 commit vs 合 1 | **3 commit** | spec 明示 3 sub-slice；codex review 颗粒度小 |

---

## §7 验收清单

```
[ ] AC-1 溢出无丢: queue full enabled 32 条 unique ack=32; baseline disabled 路 ack<32
[ ] AC-2 崩溃恢复: pending file 在新 reporter 启动后 replay 并 ack 删除
[ ] AC-3 重放幂等: 同 key 2 次 attempt_reports_seen=2 但 unique_attempt_keys_acked_count=1, ack_id 相同
[ ] AC-4-pre: reserve fail → 503, upstream requests_seen=0, attempt_ack=0
[ ] AC-4-post: HTTP=200 不变 + spool_drop_billable_count+1 + error log evidence
[ ] AC-5: CP 长期 down → pending 增至 cap 内零丢; 恢复后 drain; 接近 watermark 新请求走 503
[ ] route.proto 0 改动
[ ] 0 新 runtime dependency
[ ] let _ = reporter.report(...) 在 D-4 相关路径清零或有 SAFETY 注释说明非账务
[ ] cargo test -p core_gateway 全绿
[ ] cargo clippy -p core_gateway --all-targets -- -D warnings 0 告警
[ ] codex per-commit review 收敛（≤2 round 或剩 P2/P3 form）
[ ] Clean-room-attestation 签字 + push claude/rust-hardening
[ ] synthesis §5.1 P0-7 → done → P0=10/10
```

---

## §8 执行顺序

```
T+0   Owner 看 §6 OD-1~OD-7 + 同意 → 开 Commit 1
T+1   Commit 1 实施 + 测试 + clippy + codex review + push
T+2   Commit 2 实施 + 测试 + clippy + codex review + push
T+3   Commit 3 实施 + 测试 + clippy + codex review + push
T+3   报 Owner: P0=10/10 真 done; 同时申请 OD-5 Prometheus export 跟进 commit
```

---

## §9 与 §5.1 P0-7 映射

| §5.1 P0-7 词 | 本稿对应 |
|---|---|
| "durable 本地 spool" | Slice 1 file-per-record + prost |
| "数据结构 + 写路径 + 关闭条件" | Slice 1 |
| "replay worker + ack + replay metrics" | Slice 2 |
| "满盘降级" | Slice 3 AC-4-pre + AC-4-post + AC-5 |
| "reserve() pre-commit gate" | Slice 3 + proxy_engine/error.rs 503 |
| "post-commit loud failure" | Slice 3 + spool_drop_billable + tracing::error! |

---

**Clean-room-attestation**: original HUAKAI synthesis from two independently-authored plans (Claude + Codex). Neither plan read the other prior to write. 0 reference-project source code consulted for D-4 mechanism design.

UTC: 2026-05-24
Lane: L0 HUAKAI-internal synthesis
Agent: Claude (synthesis owner)
Source plans: `2026-05-24-w12-d4-spool-claude.md` + `2026-05-24-w12-d4-spool-codex.md`
