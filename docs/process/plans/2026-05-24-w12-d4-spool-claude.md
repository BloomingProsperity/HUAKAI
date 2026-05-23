# W12-A D-4 attempt spool durable + replay 实施计划（Claude 平行稿）

日期：2026-05-24
作者：Claude（独立稿，未读 Codex 稿）
契约：本稿是 Claude 独立草稿，与 `2026-05-24-w12-d4-spool-codex.md` 平行产出后合成。
来源 spec：`docs/process/plans/2026-05-22-rust-hardening-plan.md` §3 D-4（AC-1 ~ AC-5 + 切片 1/2/3）
synthesis 优先级：`docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md` §5.1 P0-7（最重单项 2-3 codex-day）

## §1 范围 + 不做

### §1.1 做

- `AttemptSpool` 新模块：append-only file + bounded cap + 启动重放 + idempotency 去重
- `AttemptReporter::reserve()` API：返回 RAII `Reservation` guard；forwarder 上游转发**前**调用，失败 → 503
- 配置：`AttemptReporterOptions` 加 `spool_path` / `spool_max_bytes` / `spool_watermark_ratio`（默认 0.8）
- `worker_loop` 改造：消费内存队列时若 send_with_retry 仍失败 → 落 spool；启动时先扫 spool 重放
- 替换 `let _ =`：`relay.rs:382`、`listener.rs:160` 入队结果显式处理 + metric
- 新 metric：`spool_depth_total` / `spool_replay_acked_total` / `spool_replay_failed_total` / `spool_write_failed_total` / `spool_drop_billable_total` / `reserve_rejected_total`
- 6 个 AC 各一条 mutation-resistant 测试 + 头条 self-proving 测试（ENABLED vs BASELINE 双路）

### §1.2 不做

- ❌ exactly-once（依赖控制面 idempotency_key 去重 = at-least-once + idempotent consumer ≈ exactly-once，本地侧只保 at-least-once）
- ❌ spool 加密 / 签名（spool 文件在数据面本地磁盘，威胁模型已假定本地可信）
- ❌ 多 worker 并发 replay（单 worker 顺序消费够用，不引锁争）
- ❌ spool rotation 复杂策略（单文件按 size 写满 → 拒新写并发警，等 replay 排空再清；不做时间 roll、不做多文件链）
- ❌ proto 改动（idempotency_key 已存在 `route.proto:97`）
- ❌ 控制面侧（控制面去重在 Go 控制面消费侧，本计划只保证客户端 at-least-once）

## §2 子切片划分

### §2.1 切片 1：spool 数据结构 + 写路径 + 关闭条件（A1=AC-1）

**目标**：内存队列满 + 提交可计费成功报告 → 报告落 spool 并保留至下次启动。

**新文件**：`crates/core_gateway/src/attempt_reporter/spool.rs`

**核心 API**：
```
pub struct AttemptSpool { /* path, max_bytes, current_size, file_handle */ }
impl AttemptSpool {
    pub fn open(path: PathBuf, max_bytes: u64) -> Result<Self, SpoolError>;
    pub fn append(&mut self, report: &AttemptReport) -> Result<(), SpoolError>;
    pub fn iter_pending(&mut self) -> Result<Vec<SpoolEntry>, SpoolError>;
    pub fn mark_acked(&mut self, key: &str) -> Result<(), SpoolError>;
    pub fn depth(&self) -> usize; // pending - acked
    pub fn current_bytes(&self) -> u64;
    pub fn watermark_reached(&self, ratio: f64) -> bool;
}
```

**On-disk 格式**：append-only newline-delimited JSON，一行一 entry：
```
{"v":1,"key":"<idempotency_key>","ts":<unix_ms>,"report":<AttemptReport JSON>,"crc":<u32>}
```

**Ack 标记**：同目录另一文件 `<spool>.acked`，也是 append-only line：
```
{"key":"<idempotency_key>","acked_at":<unix_ms>}
```
启动时合并两文件求差集 = pending。

**关闭条件**：
- `current_size >= max_bytes` → `append()` 返回 `SpoolFull`
- IO 错误（磁盘满 / 权限） → `SpoolError::Io`
- 解析错（启动重放遇坏行）→ 跳过 + warn + 计 `spool_corrupt_lines_total`

**IO 策略**：用 `tokio::fs::OpenOptions::new().append(true).open()` + `tokio::io::AsyncWriteExt`，每次 `append()` 后 `flush()` + `sync_all()`。慢但崩溃可生。

**测试 (slice 1)**：
- `spool_append_then_iter_returns_same_entry`（基本）
- `spool_full_returns_spool_full_error`（cap=128 bytes，写第 2 条满）— **mutation**：去掉 size check → 红
- `spool_acked_entries_excluded_from_iter`（ack 标记起效）
- `spool_corrupt_line_skipped_and_warning`（手工注入坏行）
- `spool_persists_across_open_close`（写 → close → open → iter 仍见）

### §2.2 切片 2：replay worker + ack + metrics（A2=AC-2 + A3=AC-3）

**目标**：进程重启 → 重放 worker 重读 spool 并投递；重复 replay → 控制面按 idempotency_key 去重 → 单一效应。

**改动**：
- `AttemptReporter::spawn_with_options` 接 `spool: Option<AttemptSpool>`
- 新 `replay_worker_loop(route_client, spool, inner)`：启动时一次性扫 pending → 逐条 `route_client.report_attempt` → 成功 → `spool.mark_acked(key)`
- `worker_loop` 失败路径（重试耗尽）→ 改写到 spool 而非 `failed_reports++`
- Metrics（新 atomic counters in `AttemptReporterInner`）：
  - `spool_depth` (gauge)
  - `spool_replay_acked`
  - `spool_replay_failed`
  - `spool_write_failed`

**至少一次语义**：worker 投递成功后**再** ack；崩在 "成功 → 未 ack" 之间 → 重启 replay 重发 → 控制面去重。这是 at-least-once 的标准模式。

**幂等假设**：控制面 mock 当前 `route_client.report_attempt` 是 mocked → 测试侧需 capture 重复 idempotency_key 调用次数。**测试断言**：mock 控制面收到同 key 2 次但内部只产生 1 次副作用（用 mock 内 `acked_keys: HashSet`）。

**测试 (slice 2)**：
- `replay_worker_resends_pending_on_startup`：测试前手工写 spool 一条 → 启 reporter → 等 mock 控制面收到该 key → 断言。**mutation**：去掉 replay → 红
- `replay_idempotent_under_duplicate`：mock 控制面 ack 后但故意"丢"ack 反馈 → reporter 看到非 ack → 触发再次 replay → mock 控制面 dedup → 终态单一效应。**mutation**：去掉 ack key 跟踪 → 红
- `worker_failure_writes_spool`：mock 控制面长期返回错 → 内存 worker 重试耗尽 → 检 spool depth > 0。**mutation**：去掉 spool 写 fallback → 红

### §2.3 切片 3：满盘降级 + reserve() pre-commit gate + post-commit loud failure（A4-pre + A4-post + A5）

**目标**：spool 接近 cap → 新请求转发**前** 503；spool 已写不下 + 响应头已送出 → loud metric + 结构化日志，HTTP 不变。

**新 API**：
```
pub struct Reservation { /* RAII guard, drops on success path */ }
impl AttemptReporter {
    pub fn reserve(&self) -> Result<Reservation, ReserveError>;
}
pub enum ReserveError {
    SpoolWatermarkExceeded,
    MemoryQueueFull,
    LastWriteFailed,
}
```

**Pre-commit gate 接线**（`proxy_engine/mod.rs` 或 `proxy_engine/forward.rs`）：
- 在 `forward_planned()` 进上游 client send **之前**调用 `state.attempt_reporter().reserve()`
- `Err(ReserveError::*)` → 立即返回 `503 Service Unavailable` + metric `reserve_rejected_total{reason=...}`
- `Ok(reservation)` → 把 reservation 存入 `RelayContext` 类似结构，传到 terminal report 路径；正常 report 路径消费它（`Reservation::consume()` 之类，正常路径不 Drop = 已消费）
- **关键**：reserve 在响应头送出**前**，否则不构成 pre-commit gate

**Post-commit drop**（响应头已送出 + spool 写失败）：
- terminal report 路径替换 `let _ =` 为：
  ```
  match reporter.report(report) {
      ReportEnqueueResult::Enqueued => {}
      ReportEnqueueResult::DroppedFull | ReportEnqueueResult::DroppedClosed => {
          // 试 spool
          if spool.append(report).is_err() {
              metrics::counter("spool_drop_billable_total").inc();
              tracing::error!(
                  request_id = %report.request_id,
                  idempotency_key = %report.idempotency_key,
                  tenant = %report.tenant_id,
                  tokens = report.total_tokens,
                  "BILLABLE REPORT DROPPED post-commit; HTTP unchanged"
              );
          }
      }
  }
  ```
- **HTTP 状态码与响应体 100% 不变**——这是 post-commit 不可逆性的尊重

**Watermark 触发**（A5）：
- `spool.watermark_reached(0.8)` → 下次 reserve 直接 SpoolWatermarkExceeded
- 控制面恢复 → worker 排空 → watermark 退回 → reserve 恢复 OK

**配置改动**（`config.rs`）：
```
pub struct AttemptSpoolConfig {
    pub path: PathBuf,                    // default: state_dir/attempt_spool.log
    pub max_bytes: u64,                   // default: 64 MiB
    pub watermark_ratio: f64,             // default: 0.8
    pub enabled: bool,                    // default: false (Phase 1 opt-in，避免破坏现有测试)
}
```

**测试 (slice 3)**：
- `reserve_rejected_when_spool_watermark_exceeded`：手工把 spool 写满到 watermark → reserve() 返回 Err。**mutation**：去掉 watermark check → 红
- `forwarder_returns_503_when_reservation_fails`：构造 reserve() 失败的 fixture → 发请求 → 断言 503 + 上游 client 0 调用。**mutation**：去掉 forwarder 处对 Err 的转 503 → 红
- `post_commit_drop_metric_increments_when_spool_full_after_response`：响应头送出后 enqueue + spool 全失败 → metric `spool_drop_billable_total` +1；**HTTP 状态 200 不变**（双断言：metric 增 + 状态码不变）。**mutation**：去掉 metric → 红
- `worker_drains_backlog_when_control_plane_recovers`：mock 控制面长期错 → spool 接近 cap → 突切回 OK → worker 排空 → spool depth → 0 + reserve() 恢复 OK。**mutation**：去掉 watermark 退回逻辑 → reserve 一直拒绝 → 红

**头条 self-proving 测试**（穿过 slice 1+2+3）：
```
test_spool_enabled_vs_baseline_diverge_on_overflow_billable {
    // 1. fixture: 1 billable successful report, memory queue cap=1
    // 2. ENABLED path: spool 配 path + enabled=true → 第 2 条入队失败 → 落 spool → replay → 控制面 acked_count == 2
    // 3. BASELINE path: spool 配 enabled=false → 第 2 条入队失败 → DroppedFull → 控制面 acked_count == 1
    // 4. assert acked_count(enabled) != acked_count(baseline)
    // 5. assert acked_count(enabled) == 2 && acked_count(baseline) == 1
    // mutation: 把 ENABLED 的 spool fallback 删掉 → enabled == baseline → 红
}
```
fixture 必须是 status=Success + http_status=Some(200) + 真 token 数（非 0），否则 baseline 丢失"无所谓"测不出意义。

## §3 模块文件影响

| 文件 | 改动类型 | 行数估 |
|---|---|---|
| `attempt_reporter/spool.rs` | 新增 | ~250 |
| `attempt_reporter/mod.rs` | 修改（spawn 接 spool / worker loop fallback / reserve API） | ~80 修 + 60 新 |
| `attempt_reporter/types.rs` | 修改（Reservation / ReserveError） | ~30 新 |
| `attempt_reporter/metrics.rs` | 修改（spool gauges + counters） | ~20 新 |
| `config.rs` | 修改（AttemptSpoolConfig 字段 + load） | ~40 新 |
| `proxy_engine/mod.rs` 或 `forward.rs` | 修改（reserve() 接线 + Err → 503） | ~40 |
| `listener.rs` | 修改（替换 `let _ =`） | ~20 |
| `lib.rs` | 修改（reporter spawn 传 spool） | ~10 |
| 测试 | 各 slice 测试文件 + 共享 fixture | ~400 |

合计代码 ~700 + 测试 ~400 = ~1100 行。**严格 tree-vertical**：全部落在 `attempt_reporter/` + `proxy_engine/` + `listener.rs` + `config.rs` 已有模块内，0 个新顶层包，符合 CLAUDE.md #13 包结构纪律。

## §4 风险 + 缓解

| R | 风险 | 缓解 |
|---|---|---|
| R-1 | tokio::fs sync_all 阻塞 async 运行时（每条 append 都 fsync） | 短期接受；后续优化用 `spawn_blocking` 或批量 fsync |
| R-2 | mock 控制面无 idempotency dedup → AC-3 测试失败 | 切片 2 同步给 `mock_control_plane.rs` 加 `acked_keys: HashSet`（同步 P1-5 mock fault knobs 中一项；早做一项） |
| R-3 | spool 路径无设默认 → 现存测试构造 reporter 失败 | `enabled=false` 默认 → 现有测试不受影响；只新测试显式 enable |
| R-4 | reserve()→consume() RAII 接线复杂（reservation 必须传到 terminal_reporter 终态消费点） | 用 `Reservation` 内含 `Option<Sender<()>>`，consume 时 send；Drop 不消费 = 还回 slot；测试覆盖正常 / 错误 / panic 三路 |
| R-5 | crash 测试不好做（rust async runtime 内重启 = 困难） | 用"open A → close A → open B 同 path"模拟而非真 process kill；语义等价 |
| R-6 | `proxy_engine/forward.rs` 不存在（我未验证）→ 接线点不准 | 切片 3 前先 grep `proxy_engine` 结构 |
| R-7 | clean-room：sub2api 也用 spool/outbox 模式 → 抄袭嫌疑 | 行为 parity 不构成抄袭（CLAUDE.md #11 L0）；不读 sub2api spool 实现源码；on-disk 格式 = HUAKAI 自选（newline JSON + ack 分文件，未读 sub2api）；attestation 必加 |
| R-8 | 6 AC 测试加上 self-proving 共 7+ 测试，单 commit 全做完风险大 | 按 3 sub-slice 拆 3 commit，每 commit 跑 codex review |

## §5 执行顺序 + commit 计划

```
commit 1 (slice 1): core_gateway/attempt_reporter W12-A D-4 切片 1 spool 数据结构 + append-only 持久化
  - spool.rs 新增（基本 API + on-disk 格式 + ack 标记）
  - 5 个 spool 单元测试
  - 不接入 reporter，纯 standalone 模块
  - acceptance: AC-1 头条（spool append 后能 iter 出来；满 cap 拒新写）

commit 2 (slice 2): core_gateway/attempt_reporter W12-A D-4 切片 2 重放 worker + 至少一次幂等
  - replay_worker_loop 新增
  - mod.rs spawn 接 Option<AttemptSpool>
  - worker_loop 失败 fallback 写 spool
  - mock_control_plane.rs 加 idempotency dedup
  - 3 个 reporter 集成测试（replay / idempotent / fallback）
  - acceptance: AC-2 AC-3

commit 3 (slice 3): core_gateway/attempt_reporter + proxy_engine W12-A D-4 切片 3 reserve() pre-commit + watermark + post-commit drop
  - Reservation RAII + reserve() API
  - proxy_engine forward 接 reserve() → Err 转 503
  - listener.rs + relay.rs 替换 let _ =
  - 4 个集成测试 + 1 self-proving 测试
  - acceptance: AC-4-pre / AC-4-post / AC-5 + self-proving 头条
```

每 commit：
1. cargo test -p core_gateway（必须全绿）
2. cargo clippy -p core_gateway --all-targets -- -D warnings（必须 0 warning）
3. codex per-commit review（CLAUDE.md #8，超 2 round 走 P2/P3 termination）
4. Clean-room-attestation 签字
5. push https://github.com/BloomingProsperity/HUAKAI.git claude/rust-hardening

## §6 Owner 决策点

| OD | 项 | Claude 推荐 |
|---|---|---|
| OD-1 | spool 默认 enabled=true 还是 enabled=false？ | **enabled=false** Phase 1（不破坏现存测试 + opt-in），enabled=true 留 Phase 2 |
| OD-2 | spool 路径默认在哪？ | `${state_dir}/attempt_spool.log`，state_dir 取 config field（不存在则 fallback `/tmp/huakai/attempt_spool.log`），dev/test 默认 tempdir |
| OD-3 | reserve() RAII Drop 行为：默认还 slot 还是默认消费？ | **默认还**（保守，未消费 = 请求未到 terminal），terminal 路径必须显式 `consume()` |
| OD-4 | 切 3 commit 还是合 1 commit push？ | **3 commit**（codex review 颗粒度小 + 出问题易 rollback） |
| OD-5 | mock_control_plane.rs 改 vs P1-5 单独 commit？ | **改 mock 与 slice 2 同 commit**（slice 2 测试硬依赖 mock dedup），P1-5 改归 "spec 已合并提前实施" 记录 |

## §7 验收清单（all-green 才 push）

```
[ ] cargo test -p core_gateway (含 5 slice1 + 3 slice2 + 4 slice3 + 1 self-proving = 13 新测试)
[ ] cargo clippy -p core_gateway --all-targets -- -D warnings
[ ] codex per-commit review 收敛（≤2 round 或剩 P2/P3 form）
[ ] Clean-room-attestation 签字
[ ] push claude/rust-hardening 成功
[ ] synthesis §5.1 P0-7 标 done → P0 = 10/10
```

---

**Clean-room-attestation**: original HUAKAI implementation plan; reference projects (sub2api/clewdr/litellm-rs) 未读其 spool/outbox 源码作 specifier 输入；本计划 on-disk 格式 + ack 标记策略 + Reservation RAII 均 Claude 设计。
