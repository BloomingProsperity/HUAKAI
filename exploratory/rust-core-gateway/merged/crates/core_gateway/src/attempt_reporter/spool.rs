// W12-A D-4 切片 1: attempt report durable spool — file-per-record + prost + ack=删除。
//
// 设计选型见 docs/process/plans/2026-05-24-w12-d4-spool-synthesis.md:
// - durable-first (每条 report 都先 persist, 不靠 in-memory queue 兜底)
// - bytes-based reservation (RAII guard, 防 watermark 越线)
// - file-per-record (ack=删除最简, 避免 segment compaction)
// - sync blocking IO (调用方 wrap tokio::task::spawn_blocking, reporter sync API + PinnedDrop 不能 await)
//
// 本 slice 只提供 standalone primitive, AttemptReporter 接线在 Slice 2.

use std::{
    fs,
    io::{self, Read, Write},
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering},
    },
    time::Duration,
};

use prost::Message;
use thiserror::Error;
use uuid::Uuid;

use crate::route_proto::v1::AttemptReportRequest;

use super::types::AttemptReport;

/// W12-A D-4 spool 配置。
///
/// `enabled=false` 时 `AttemptSpool::open` 返回 `Ok(None)`, 调用方 baseline 路径走旧 in-memory drop 语义。
/// production 配置由 Slice 3 加上 fail-fast (enabled=true 必须 dir 可写)。
#[derive(Clone, Debug, Eq, PartialEq)]
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

impl Default for AttemptSpoolOptions {
    fn default() -> Self {
        // Slice 1: 默认 disabled, 避免破坏已有测试。
        // Slice 3 + production validate 会要求显式 enabled + dir 可写。
        Self {
            enabled: false,
            dir: PathBuf::from(""),
            max_bytes: 1024 * 1024 * 1024, // 1 GiB (Codex OD-3 默认)
            high_watermark_bytes: 1024 * 1024 * 1024 * 8 / 10, // 80%
            max_record_bytes: 64 * 1024, // 64 KiB
            replay_interval: Duration::from_millis(250),
            replay_batch_size: 128,
            fsync_on_write: true, // production 安全; test 路径显式 false
        }
    }
}

/// 已 reserve 但还未 persist 的字节预算 RAII guard。
///
/// Drop 时若未 `consume()` 过, 还回预算; 防止 reserve 后异常路径耗尽 watermark 配额。
/// `consume()` 在 `AttemptSpool::persist` 成功路径内调用, 让 pending_bytes 接管字节统计。
pub struct AttemptSpoolReservation {
    bytes: u64,
    spool: Arc<AttemptSpoolInner>,
    consumed: bool,
}

impl AttemptSpoolReservation {
    /// 预算字节预留 (max_record_bytes 上界)。
    pub fn bytes(&self) -> u64 {
        self.bytes
    }

    /// 显式让 persist 路径接管该笔预算。consumed 之后 Drop 不再释放。
    fn consume(&mut self) {
        self.consumed = true;
    }
}

impl Drop for AttemptSpoolReservation {
    fn drop(&mut self) {
        if !self.consumed {
            self.spool
                .reserved_bytes
                .fetch_sub(self.bytes, Ordering::Relaxed);
        }
    }
}

/// `reserve()` 失败语义 — 调用方按类型决定走 503 (Slice 3) 还是 fallback。
#[derive(Debug, Clone, Eq, PartialEq)]
pub enum AttemptSpoolBackpressure {
    /// pending_bytes + reserved_bytes + max_record_bytes 已 ≥ watermark。
    WatermarkExceeded {
        pending_bytes: u64,
        reserved_bytes: u64,
        max_record_bytes: u64,
        high_watermark_bytes: u64,
    },
    /// 最近一次 persist 物理失败 (磁盘满 / 权限 / IO)。先返回 Backpressure
    /// 让上游拒绝, 等待 replay/health 清除标志。
    LastWriteFailed,
}

/// spool 操作错误。
#[derive(Debug, Error)]
pub enum SpoolError {
    #[error("io: {0}")]
    Io(#[from] io::Error),
    #[error("prost encode: {0}")]
    Encode(#[from] prost::EncodeError),
    #[error("prost decode: {0}")]
    Decode(#[from] prost::DecodeError),
    #[error("idempotency key '{0}' 含非法路径字符")]
    InvalidKey(String),
    #[error("record 编码后 {0} 字节超过 max_record_bytes={1}")]
    RecordTooLarge(u64, u64),
    #[error("pending file {0} 不存在")]
    KeyNotFound(String),
}

/// 单条 persist 成功后的产出元数据。
#[derive(Debug, Clone, Eq, PartialEq)]
pub struct SpoolPersistOutcome {
    pub key: String,
    pub bytes_written: u64,
    /// true = 同 key pending 文件已存在 (replay/ack-delete 失败后再 persist 的幂等路径)。
    pub was_duplicate: bool,
}

struct AttemptSpoolInner {
    options: AttemptSpoolOptions,
    pending_dir: PathBuf,
    tmp_dir: PathBuf,
    pending_count: AtomicUsize,
    pending_bytes: AtomicU64,
    reserved_bytes: AtomicU64,
    last_write_failed: AtomicBool,
}

/// W12-A D-4 attempt durable spool — file-per-record outbox。
///
/// 用法:
/// ```ignore
/// let spool = AttemptSpool::open(options)?.expect("enabled");
/// let reservation = spool.reserve()?;
/// spool.persist(&report, reservation)?;
/// // ... 后台 worker:
/// for key in spool.pending_snapshot(128) {
///     let proto = spool.load_pending(&key)?;
///     // ... send to control plane
///     spool.ack(&key)?;
/// }
/// ```
#[derive(Clone)]
pub struct AttemptSpool {
    inner: Arc<AttemptSpoolInner>,
}

impl AttemptSpool {
    /// 打开 spool。`enabled=false` 返回 `Ok(None)`, 调用方走 baseline 行为。
    ///
    /// 启动期扫 `<dir>/pending/` 把 pending_count + pending_bytes 初始化为现有未 ack 文件状态;
    /// 这是 AC-2 崩溃恢复的前提 (Slice 2 startup replay 依赖)。
    pub fn open(options: AttemptSpoolOptions) -> Result<Option<Self>, SpoolError> {
        if !options.enabled {
            return Ok(None);
        }

        if options.dir.as_os_str().is_empty() {
            return Err(SpoolError::Io(io::Error::new(
                io::ErrorKind::InvalidInput,
                "spool dir 不能为空 (production validate 应在 Slice 3 fail-fast)",
            )));
        }

        let pending_dir = options.dir.join("pending");
        let tmp_dir = options.dir.join("tmp");
        fs::create_dir_all(&pending_dir)?;
        fs::create_dir_all(&tmp_dir)?;

        // 启动扫: 把已存在 pending 文件计入 counters, 让 Slice 2 replay 能感知。
        let mut initial_count = 0usize;
        let mut initial_bytes = 0u64;
        for entry in fs::read_dir(&pending_dir)? {
            let entry = entry?;
            if !entry.file_type()?.is_file() {
                continue;
            }
            let len = entry.metadata()?.len();
            initial_count += 1;
            initial_bytes = initial_bytes.saturating_add(len);
        }

        // 启动期 tmp/ 残留是上次崩溃中途产物, 清理掉。
        // 不删 pending/ (那是真账务)。
        if let Ok(read) = fs::read_dir(&tmp_dir) {
            for entry in read.flatten() {
                let _ = fs::remove_file(entry.path());
            }
        }

        let inner = Arc::new(AttemptSpoolInner {
            options,
            pending_dir,
            tmp_dir,
            pending_count: AtomicUsize::new(initial_count),
            pending_bytes: AtomicU64::new(initial_bytes),
            reserved_bytes: AtomicU64::new(0),
            last_write_failed: AtomicBool::new(false),
        });

        Ok(Some(Self { inner }))
    }

    /// 检查 spool 健康 + watermark 余量, 预留 max_record_bytes 字节配额。
    ///
    /// 触发条件 (≥, 含等号): `pending_bytes + reserved_bytes + max_record_bytes >= high_watermark_bytes`。
    /// 返回的 `AttemptSpoolReservation` 必须在 `persist` 路径 consume;
    /// 异常路径 Drop 时会自动还回字节配额 — RAII 安全网。
    ///
    /// 并发安全 (Codex P1-1 fix 2026-05-24): 用 compare_exchange_weak loop 让 watermark 判定
    /// 和 reserved_bytes 增量原子完成, 防止多 caller 同时通过 check 各自 fetch_add 越 watermark。
    pub fn reserve(&self) -> Result<AttemptSpoolReservation, AttemptSpoolBackpressure> {
        if self.inner.last_write_failed.load(Ordering::Relaxed) {
            return Err(AttemptSpoolBackpressure::LastWriteFailed);
        }

        let max_record = self.inner.options.max_record_bytes;
        let watermark = self.inner.options.high_watermark_bytes;

        loop {
            let pending = self.inner.pending_bytes.load(Ordering::Relaxed);
            let reserved = self.inner.reserved_bytes.load(Ordering::Acquire);
            let projected = pending
                .saturating_add(reserved)
                .saturating_add(max_record);
            if projected >= watermark {
                return Err(AttemptSpoolBackpressure::WatermarkExceeded {
                    pending_bytes: pending,
                    reserved_bytes: reserved,
                    max_record_bytes: max_record,
                    high_watermark_bytes: watermark,
                });
            }

            // compare_exchange: 只在 reserved_bytes 没被其他 caller 改时才生效。
            // 失败 = 并发 reserve 改了, 重读重判, 仍可能拒。
            match self.inner.reserved_bytes.compare_exchange_weak(
                reserved,
                reserved.saturating_add(max_record),
                Ordering::AcqRel,
                Ordering::Relaxed,
            ) {
                Ok(_) => {
                    return Ok(AttemptSpoolReservation {
                        bytes: max_record,
                        spool: self.inner.clone(),
                        consumed: false,
                    });
                }
                Err(_) => continue,
            }
        }
    }

    /// 把 report 编为 prost `AttemptReportRequest` 字节, 写 `tmp/<uuid>.tmp` ->
    /// optional `sync_data` -> 原子 rename 到 `pending/<idempotency_key>.pb`。
    ///
    /// 同 key 已存在 pending 文件 -> `was_duplicate=true` 幂等成功 (Unix 上 rename overwrite,
    /// Windows 上需删 tmp 残留); 计数不重复递增。
    ///
    /// 物理 IO 错 -> `last_write_failed=true`, 下次 reserve 自动 Backpressure 阻止新请求。
    pub fn persist(
        &self,
        report: &AttemptReport,
        mut reservation: AttemptSpoolReservation,
    ) -> Result<SpoolPersistOutcome, SpoolError> {
        let key = report.idempotency_key.clone();
        validate_key(&key)?;

        let proto = report.clone().into_proto();
        let mut buf = Vec::with_capacity(proto.encoded_len());
        proto.encode(&mut buf)?;

        let bytes_written = buf.len() as u64;
        let max_record = self.inner.options.max_record_bytes;
        if bytes_written > max_record {
            // 不消费 reservation: Drop 时还回字节预算。
            return Err(SpoolError::RecordTooLarge(bytes_written, max_record));
        }

        let pending_path = self.inner.pending_dir.join(format!("{key}.pb"));

        // Codex P2-2 fix 2026-05-24: 已存在 pending 文件 = duplicate, 不 rename 不覆盖,
        // 也不再写 tmp。Unix rename 会静默 overwrite existing, 旧实现保留旧字节计数但实际数据被替换 ->
        // 跨平台语义不一致 + 已 durable 记录被无声修改。统一做法: 检测 -> skip rename -> 报 was_duplicate。
        if pending_path.exists() {
            reservation.consume();
            self.inner
                .reserved_bytes
                .fetch_sub(reservation.bytes, Ordering::Relaxed);
            let existing_bytes = fs::metadata(&pending_path)
                .map(|m| m.len())
                .unwrap_or(bytes_written);
            return Ok(SpoolPersistOutcome {
                key,
                bytes_written: existing_bytes,
                was_duplicate: true,
            });
        }

        let tmp_path = self.inner.tmp_dir.join(format!("{}.tmp", Uuid::now_v7()));

        // write tmp -> optional fsync -> atomic rename -> Codex P1-2 fix: rename 后 dir fsync。
        let io_result: io::Result<()> = (|| {
            let mut file = fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .open(&tmp_path)?;
            file.write_all(&buf)?;
            if self.inner.options.fsync_on_write {
                file.sync_data()?;
            }
            drop(file);
            fs::rename(&tmp_path, &pending_path)?;

            // Codex P1-2 fix 2026-05-24: rename 之后必须 fsync pending dir,
            // 否则 OS/电源 crash 后 pending/ 目录 entry 可能丢, durable-first 保证失效。
            // Unix: open dir + sync_all (POSIX 行为); Windows: NTFS metadata journaling 已保障, 跳过。
            if self.inner.options.fsync_on_write {
                #[cfg(unix)]
                {
                    let dir = fs::File::open(&self.inner.pending_dir)?;
                    dir.sync_all()?;
                }
            }
            Ok(())
        })();

        if let Err(err) = io_result {
            let _ = fs::remove_file(&tmp_path);
            // Codex P2-1 deferred: last_write_failed latch 当前只在 persist 成功路径清,
            // Slice 2 replay worker 第一次 ack 成功后会调 spool 内部 clear 清 latch (replay = 持续 health probe)。
            self.inner
                .last_write_failed
                .store(true, Ordering::Relaxed);
            return Err(SpoolError::Io(err));
        }

        // 成功路径: 全新 pending 文件落盘。
        self.inner.pending_count.fetch_add(1, Ordering::Relaxed);
        self.inner
            .pending_bytes
            .fetch_add(bytes_written, Ordering::Relaxed);
        reservation.consume();
        self.inner
            .reserved_bytes
            .fetch_sub(reservation.bytes, Ordering::Relaxed);
        self.inner
            .last_write_failed
            .store(false, Ordering::Relaxed);

        Ok(SpoolPersistOutcome {
            key,
            bytes_written,
            was_duplicate: false,
        })
    }

    /// 读 `pending/<key>.pb` 解 prost 还原 `AttemptReportRequest`。replay worker 用。
    pub fn load_pending(&self, key: &str) -> Result<AttemptReportRequest, SpoolError> {
        validate_key(key)?;
        let path = self.inner.pending_dir.join(format!("{key}.pb"));
        let mut file = fs::File::open(&path).map_err(|err| {
            if err.kind() == io::ErrorKind::NotFound {
                SpoolError::KeyNotFound(key.to_owned())
            } else {
                SpoolError::Io(err)
            }
        })?;
        let mut buf = Vec::new();
        file.read_to_end(&mut buf)?;
        let request = AttemptReportRequest::decode(buf.as_slice())?;
        Ok(request)
    }

    /// 删 `pending/<key>.pb` = 该 report 已被控制面 ack。
    ///
    /// ack 后 delete 失败 -> 下次 replay 会再发同 key, 控制面按 idempotency 去重。
    /// 这是 at-least-once + idempotent consumer 的工作模式。
    pub fn ack(&self, key: &str) -> Result<(), SpoolError> {
        validate_key(key)?;
        let path = self.inner.pending_dir.join(format!("{key}.pb"));
        let metadata = match fs::metadata(&path) {
            Ok(m) => m,
            Err(err) if err.kind() == io::ErrorKind::NotFound => {
                // 已被并发 ack 删, 视为幂等成功。
                return Ok(());
            }
            Err(err) => return Err(SpoolError::Io(err)),
        };
        let len = metadata.len();
        fs::remove_file(&path)?;
        self.inner
            .pending_count
            .fetch_sub(1, Ordering::Relaxed);
        self.inner
            .pending_bytes
            .fetch_sub(len, Ordering::Relaxed);
        Ok(())
    }

    /// 列 `pending/` 下最多 `limit` 个 idempotency_key (去 `.pb` 后缀)。Slice 2 replay worker 用。
    pub fn pending_snapshot(&self, limit: usize) -> Vec<String> {
        let read = match fs::read_dir(&self.inner.pending_dir) {
            Ok(r) => r,
            Err(_) => return Vec::new(),
        };

        let mut keys = Vec::with_capacity(limit.min(64));
        for entry in read.flatten() {
            if keys.len() >= limit {
                break;
            }
            let name = entry.file_name();
            let Some(name_str) = name.to_str() else {
                continue;
            };
            let Some(stripped) = name_str.strip_suffix(".pb") else {
                continue;
            };
            keys.push(stripped.to_owned());
        }
        keys
    }

    pub fn pending_count(&self) -> usize {
        self.inner.pending_count.load(Ordering::Relaxed)
    }

    pub fn pending_bytes(&self) -> u64 {
        self.inner.pending_bytes.load(Ordering::Relaxed)
    }

    pub fn reserved_bytes(&self) -> u64 {
        self.inner.reserved_bytes.load(Ordering::Relaxed)
    }

    // NOTE: Slice 3 (AC-4-pre 集成测试) 会加 force_last_write_failed_for_test 测试钩子。
    // Slice 1 不预先暴露未用 API, 避免 clippy::dead_code (YAGNI)。
}

fn validate_key(key: &str) -> Result<(), SpoolError> {
    if key.is_empty() {
        return Err(SpoolError::InvalidKey(key.to_owned()));
    }
    // 防 path traversal: 只允许 [A-Za-z0-9_-]; idempotency_key 已是 `idem-v7-<uuid>-<hex>`,
    // 但启动期/replay 可能消费旧/坏数据, 做硬边界。
    if !key
        .bytes()
        .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
    {
        return Err(SpoolError::InvalidKey(key.to_owned()));
    }
    if key.starts_with('.') || Path::new(key).components().count() != 1 {
        return Err(SpoolError::InvalidKey(key.to_owned()));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::env;

    use bytes::Bytes;

    use super::super::metrics::{AttemptCacheMetrics, AttemptTokenMetrics};
    use super::*;
    use crate::attempt_reporter::types::{AttemptReport, AttemptStatus};

    fn unique_test_dir(label: &str) -> PathBuf {
        let mut p = env::temp_dir();
        p.push(format!(
            "huakai-spool-test-{label}-{}",
            Uuid::now_v7().simple()
        ));
        p
    }

    fn test_options(dir: PathBuf) -> AttemptSpoolOptions {
        AttemptSpoolOptions {
            enabled: true,
            dir,
            max_bytes: 4096,
            high_watermark_bytes: 2048,
            max_record_bytes: 512,
            replay_interval: Duration::from_millis(50),
            replay_batch_size: 32,
            fsync_on_write: false, // 测试避免 disk fsync 抖动
        }
    }

    fn sample_billable_report(key_suffix: &str) -> AttemptReport {
        AttemptReport {
            request_id: format!("req-{key_suffix}"),
            route_plan_id: "route-plan-spool-test".to_owned(),
            attempt_id: format!("attempt-spool-{key_suffix}"),
            acquisition_token: Bytes::from_static(b"lease-token-spool-test"),
            status: AttemptStatus::Success,
            http_status: 200,
            started_at: 1_700_000_000_000,
            ended_at: 1_700_000_000_500,
            latency_ms: 500,
            tokens_used: AttemptTokenMetrics {
                input_tokens: 100,
                output_tokens: 200,
                total_tokens: 300,
                source: "response_body".to_owned(),
            },
            cache_metrics: AttemptCacheMetrics::default(),
            bytes_in: 1024,
            bytes_out: 2048,
            frames_in: 1,
            frames_out: 1,
            vendor_request_id: format!("vendor-{key_suffix}"),
            retryable: false,
            error_class: String::new(),
            error_message_redacted: String::new(),
            // idempotency_key 必须 path-safe; 用 build_idempotency_key 一致格式
            idempotency_key: format!("idem-v7-spool-test-{key_suffix}-1234567890abcdef"),
        }
    }

    /// W12-A D-4 Slice 1 AC-1 单元: persist + load 圆环路径完整保 prost 字段。
    ///
    /// fixture 是 status=Success + http=200 + tokens 非零 + 真 idempotency_key,
    /// 不是空 stub。mutation: persist 漏写 / load 解错字段, 任一让 4 个断言中之一红。
    #[test]
    fn spool_persists_proto_record_and_loads_same_idempotency_key() {
        let dir = unique_test_dir("persist-roundtrip");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open 应成功")
            .expect("enabled spool 应返回 Some");

        let report = sample_billable_report("p1");
        let key = report.idempotency_key.clone();

        let reservation = spool.reserve().expect("初始 reserve 应成功");
        let outcome = spool.persist(&report, reservation).expect("persist 应成功");
        assert_eq!(outcome.key, key, "outcome.key 应与 report.idempotency_key 一致");
        assert!(!outcome.was_duplicate, "首次 persist 不应是 duplicate");
        assert_eq!(spool.reserved_bytes(), 0, "consume 后预算必须归零");

        let loaded = spool.load_pending(&key).expect("load 应成功");
        // 4 个判别性断言: 任一字段 persist/encode/decode 漏写都会失败。
        assert_eq!(loaded.request_id, "req-p1");
        assert_eq!(loaded.status, "success");
        assert_eq!(loaded.http_status, 200);
        assert_eq!(
            loaded
                .tokens_used
                .as_ref()
                .map(|t| t.source.as_str())
                .unwrap_or(""),
            "response_body",
            "tokens_used.source 必须保留 (response_body) 否则 D-5 source 词表失效"
        );
        assert_eq!(spool.pending_count(), 1, "persist 一条后 pending_count 必须 +1");

        let _ = fs::remove_dir_all(&dir);
    }

    /// W12-A D-4 Slice 1 AC-4-pre 前置: 字节预算 ≥ watermark 必须拒绝。
    ///
    /// max_record_bytes=512, high_watermark=2048:
    /// reserve 1 -> reserved 0->512, projected 0+0+512=512 < 2048 OK
    /// reserve 2 -> reserved 512->1024, projected 0+512+512=1024 < 2048 OK
    /// reserve 3 -> reserved 1024->1536, projected 0+1024+512=1536 < 2048 OK
    /// reserve 4 -> projected 0+1536+512=2048 >= 2048 -> Err WatermarkExceeded
    /// Drop r1 (还 512) -> reserved 1536->1024, projected 0+1024+512=1536 < 2048 -> OK 恢复
    ///
    /// mutation: 若 reserve 不计 reserved_bytes (只看 pending) 第 4 次成功 -> 测试 panic 红。
    /// mutation: 若 RAII Drop 不还预算, "恢复" 那一步仍 Err -> 测试 panic 红。
    #[test]
    fn reservation_rejects_when_pending_plus_reserved_crosses_watermark() {
        let dir = unique_test_dir("reservation-watermark");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open 应成功")
            .expect("enabled spool 应返回 Some");

        let r1 = spool.reserve().expect("1st reserve 应成功");
        assert_eq!(spool.reserved_bytes(), 512);

        let _r2 = spool.reserve().expect("2nd reserve 应成功");
        assert_eq!(spool.reserved_bytes(), 1024);

        let _r3 = spool.reserve().expect("3rd reserve 应成功");
        assert_eq!(spool.reserved_bytes(), 1536);

        // 第 4 次: projected = 0 + 1536 + 512 = 2048 ≥ 2048 watermark -> Err
        let err = spool
            .reserve()
            .err()
            .expect("4th reserve 必须 Err Backpressure");
        match err {
            AttemptSpoolBackpressure::WatermarkExceeded {
                pending_bytes,
                reserved_bytes,
                max_record_bytes,
                high_watermark_bytes,
            } => {
                assert_eq!(pending_bytes, 0, "无 persist, pending=0");
                assert_eq!(reserved_bytes, 1536, "3 笔 reserve 后 reserved=1536");
                assert_eq!(max_record_bytes, 512);
                assert_eq!(high_watermark_bytes, 2048);
            }
            other => panic!("期望 WatermarkExceeded, 实际 {other:?}"),
        }

        // Drop r1 释放 512 字节预算 -> reserve 又能成功 (RAII 安全网)
        drop(r1);
        assert_eq!(
            spool.reserved_bytes(),
            1024,
            "Drop r1 后 reserved 必须 -512 = 1024"
        );

        let _r_recover = spool
            .reserve()
            .expect("Drop 释放后 reserve 应成功 — 若 RAII 不还字节预算此处 Err");
        assert_eq!(spool.reserved_bytes(), 1536);

        let _ = fs::remove_dir_all(&dir);
    }

    /// W12-A D-4 Slice 1 AC-2 前置: spool 启动期扫 pending/ 把 counters 初始化,
    /// 才能让 Slice 2 replay worker 看到旧 pending 文件。
    ///
    /// mutation: 启动不扫 pending -> pending_count=0, 旧文件永不 replay -> AC-2 整链红。
    #[test]
    fn spool_open_existing_dir_populates_counters_from_pending() {
        let dir = unique_test_dir("open-existing");
        let opts = test_options(dir.clone());

        // 第一次 open + persist 2 条
        {
            let spool = AttemptSpool::open(opts.clone())
                .expect("open 应成功")
                .expect("enabled");
            for suffix in ["a", "b"] {
                let report = sample_billable_report(suffix);
                let reservation = spool.reserve().expect("reserve OK");
                spool.persist(&report, reservation).expect("persist OK");
            }
            assert_eq!(spool.pending_count(), 2, "首轮 persist 2 条");
        }

        // 模拟 "进程重启": 重新 open 同 dir, 不动 pending/。
        let spool2 = AttemptSpool::open(opts)
            .expect("open 应成功")
            .expect("enabled");

        assert_eq!(
            spool2.pending_count(),
            2,
            "重启后必须扫到 2 条 pending, 否则 AC-2 崩溃恢复直接哑火"
        );
        assert!(
            spool2.pending_bytes() > 0,
            "pending_bytes 必须 >0 (proto encoded)"
        );
        let snapshot = spool2.pending_snapshot(10);
        assert_eq!(
            snapshot.len(),
            2,
            "pending_snapshot 必须列出 2 条 idempotency_key"
        );

        let _ = fs::remove_dir_all(&dir);
    }

    /// W12-A D-4 Slice 1: ack 必须真删 pending 文件, 否则 replay 永远重发, pending 永不空。
    ///
    /// mutation: ack 跳过 remove_file 只递减计数 -> load_pending 仍能读 -> 测试断言 KeyNotFound 红。
    #[test]
    fn spool_ack_removes_pending_file_so_load_fails() {
        let dir = unique_test_dir("ack-removes");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open OK")
            .expect("enabled");

        let report = sample_billable_report("ack1");
        let key = report.idempotency_key.clone();
        let reservation = spool.reserve().expect("reserve OK");
        spool.persist(&report, reservation).expect("persist OK");
        assert_eq!(spool.pending_count(), 1);
        let _ = spool.load_pending(&key).expect("ack 前 load 应成功");

        spool.ack(&key).expect("ack 应成功");
        assert_eq!(spool.pending_count(), 0, "ack 后 pending_count 必须 -1");
        assert_eq!(spool.pending_bytes(), 0, "ack 后 pending_bytes 必须归零");

        match spool.load_pending(&key) {
            Err(SpoolError::KeyNotFound(k)) => assert_eq!(k, key, "KeyNotFound 必须带原 key"),
            other => panic!("ack 后 load 必须 KeyNotFound, 实际 {other:?}"),
        }

        let _ = fs::remove_dir_all(&dir);
    }

    /// W12-A D-4 Slice 1: enabled=false 路径返回 Ok(None), 调用方 baseline 走老 drop 逻辑。
    /// mutation: 漏判 enabled -> 返回 Some -> baseline 与 enabled 行为耦合, 测试断言 None 红。
    #[test]
    fn spool_disabled_returns_none() {
        let opts = AttemptSpoolOptions {
            enabled: false,
            ..test_options(unique_test_dir("disabled"))
        };
        let result = AttemptSpool::open(opts).expect("open 不应失败");
        assert!(result.is_none(), "enabled=false 必须返回 None");
    }

    /// W12-A D-4 Slice 1 (Codex P2-2 fix 2026-05-24): 重复 persist 同 key 不能 overwrite existing 文件。
    ///
    /// 旧实现在 Unix 上 fs::rename 静默替换 existing 文件, 但报告 was_duplicate=true, pending_bytes 不变 ->
    /// 已 durable 数据被无声 mutate, 跨平台行为不一致。
    ///
    /// mutation: 删 `if pending_path.exists() { return duplicate; }` 检测 -> Unix 上 rename overwrite ->
    /// loaded.bytes_in 变成 report2 的值 -> 测试红。
    #[test]
    fn spool_persist_duplicate_key_does_not_overwrite_existing_file() {
        let dir = unique_test_dir("persist-duplicate-no-overwrite");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open OK")
            .expect("enabled");

        let report1 = sample_billable_report("dup1");
        let key = report1.idempotency_key.clone();
        let reservation = spool.reserve().expect("reserve OK");
        let outcome1 = spool.persist(&report1, reservation).expect("first persist OK");
        assert!(!outcome1.was_duplicate, "首次 persist 不应是 duplicate");

        // 同 key + 不同 payload (改 bytes_in 让 proto bytes 真不同)
        let mut report2 = sample_billable_report("dup1");
        report2.bytes_in = 999_999;
        assert_eq!(report2.idempotency_key, key, "fixture: 同 idempotency_key");
        let reservation2 = spool.reserve().expect("reserve OK 2");
        let outcome2 = spool
            .persist(&report2, reservation2)
            .expect("duplicate persist 应成功 (幂等 OK)");
        assert!(
            outcome2.was_duplicate,
            "2nd persist 同 key 必须报 was_duplicate=true"
        );

        // 关键断言: 已存在文件没被 overwrite, 加载回来仍是 report1 (bytes_in=1024)
        let loaded = spool.load_pending(&key).expect("load OK");
        assert_eq!(
            loaded.bytes_in, 1024,
            "duplicate persist 不应 overwrite existing durable 记录 (mutation: 删 exists 检测后 Unix 上会变 999_999 红)"
        );

        // pending_count 仍是 1, 不重复递增
        assert_eq!(
            spool.pending_count(),
            1,
            "duplicate persist 不应 ++pending_count, 不然 backlog 漂移"
        );

        // reservation 仍 consume (不留 ghost 字节预算)
        assert_eq!(
            spool.reserved_bytes(),
            0,
            "duplicate persist 后 reserved_bytes 必须归零 (reservation 已 consume)"
        );

        let _ = fs::remove_dir_all(&dir);
    }

    /// W12-A D-4 Slice 1: invalid key 在 load/ack 上必须 fail-fast,
    /// 否则攻击者控制 idempotency_key -> path traversal 写出 spool 目录。
    /// mutation: validate_key 放宽 -> load/ack 接受 "../etc/passwd" -> 测试断言 InvalidKey 红。
    #[test]
    fn spool_rejects_invalid_keys_with_path_traversal_or_separators() {
        let dir = unique_test_dir("invalid-key");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open OK")
            .expect("enabled");

        for bad in [
            "",
            "../etc/passwd",
            "a/b",
            "a\\b",
            ".hidden",
            "with space",
            "with;semi",
        ] {
            assert!(
                matches!(spool.load_pending(bad), Err(SpoolError::InvalidKey(_))),
                "load_pending 应拒绝非法 key {bad:?}"
            );
            assert!(
                matches!(spool.ack(bad), Err(SpoolError::InvalidKey(_))),
                "ack 应拒绝非法 key {bad:?}"
            );
        }

        let _ = fs::remove_dir_all(&dir);
    }
}
