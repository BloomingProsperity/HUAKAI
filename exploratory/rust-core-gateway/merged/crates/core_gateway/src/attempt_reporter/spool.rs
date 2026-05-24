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

// Codex round 2 P2 fix 2026-05-24: 给 quarantine rename 加 cfg(test) 注入点。
// production build 直接 fs::rename, 0 开销; test build 检查 thread_local 标志,
// 允许测试确定性触发 EACCES 让 fail-fast 路径可被验证 (WSL root 模式 chmod 不挡,
// env var 又被 workspace `unsafe_code = forbid` 禁)。
#[cfg(test)]
thread_local! {
    pub(super) static FORCE_QUARANTINE_RENAME_FAIL: std::cell::Cell<bool> =
        const { std::cell::Cell::new(false) };
}

#[cfg(test)]
fn quarantine_rename_with_test_injection(src: &Path, dst: &Path) -> io::Result<()> {
    if FORCE_QUARANTINE_RENAME_FAIL.with(|c| c.get()) {
        return Err(io::Error::new(
            io::ErrorKind::PermissionDenied,
            "test-forced rename failure (FORCE_QUARANTINE_RENAME_FAIL)",
        ));
    }
    fs::rename(src, dst)
}

#[cfg(not(test))]
fn quarantine_rename_with_test_injection(src: &Path, dst: &Path) -> io::Result<()> {
    fs::rename(src, dst)
}

struct AttemptSpoolInner {
    options: AttemptSpoolOptions,
    pending_dir: PathBuf,
    tmp_dir: PathBuf,
    /// W12-A D-4 第三方 P2 finding 2026-05-24: 损坏 / 非法文件移到此目录避免长期占用 quota,
    /// 又保留磁盘文件供运维事后审计 (不静默删, 留证据)。
    quarantine_dir: PathBuf,
    pending_count: AtomicUsize,
    pending_bytes: AtomicU64,
    reserved_bytes: AtomicU64,
    last_write_failed: AtomicBool,
    /// W12-A D-4 第三方 P2 finding 2026-05-24: 启动 + replay 期累计 quarantine 文件数。
    quarantined_count: AtomicU64,
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
        let quarantine_dir = options.dir.join("quarantine");
        fs::create_dir_all(&pending_dir)?;
        fs::create_dir_all(&tmp_dir)?;
        fs::create_dir_all(&quarantine_dir)?;

        // 启动扫 (W12-A D-4 第三方 P2 finding 2026-05-24):
        // 只把合法 `.pb` 文件计入 counters; pending_snapshot 也只列 `.pb`, 这两层语义必须对齐,
        // 否则非 .pb 垃圾文件永远不会进 replay -> 永久占 quota -> watermark 卡死所有请求。
        // 非 .pb 文件直接 (启动期一次性) 移到 quarantine_dir 留证据, 不静默删。
        let mut initial_count = 0usize;
        let mut initial_bytes = 0u64;
        let mut initial_quarantined = 0u64;
        for entry in fs::read_dir(&pending_dir)? {
            let entry = entry?;
            if !entry.file_type()?.is_file() {
                continue;
            }
            let name = entry.file_name();
            // 第三方 P2 finding 2026-05-24 (round 3): 旧实现仅按 `.pb` 后缀计入 quota,
            // 不校验 stem 是否合法 idempotency key。`pending/with space.pb` / `.hidden.pb` /
            // `pending/../escape.pb` 全部能通过后缀过滤被计入 pending_bytes/count, 但随后
            // load_pending(stem) 会撞 validate_key 返 InvalidKey -> drain_pending warn+continue ->
            // 永久占 watermark -> 503 backpressure。
            //
            // 新流程: 后缀必须 .pb + stem 必须 validate_key 通过, 否则同样进 quarantine。
            let stem_valid = name
                .to_str()
                .and_then(|s| s.strip_suffix(".pb"))
                .is_some_and(|stem| validate_key(stem).is_ok());
            let metadata = entry.metadata()?;
            let len = metadata.len();
            if stem_valid {
                initial_count += 1;
                initial_bytes = initial_bytes.saturating_add(len);
            } else {
                // 移走防永久占 quota。命名带时间戳避免与同名重名碰撞。
                let dest_name = format!(
                    "startup-non-pb-{}-{}",
                    std::time::SystemTime::now()
                        .duration_since(std::time::UNIX_EPOCH)
                        .unwrap_or_default()
                        .as_nanos(),
                    name.to_string_lossy()
                );
                let dest = quarantine_dir.join(dest_name);
                // Codex round 1 P2 fix 2026-05-24: rename 失败必须 fail-fast 而非静默跳过,
                // 否则文件留在 pending/ 但 pending_snapshot 也不列它 = 既不占 counter 也不
                // 进 replay = disk 一直涨但 watermark/audit 看不见 (隐形 disk 泄漏)。
                // 启动期 fail-fast 让运维清掉那一个文件比 production 跑着跑着 disk 满更安全。
                //
                // Codex round 2 P2 fix 2026-05-24: 用 cfg(test) 注入点让测试可确定性触发
                // rename 失败 (WSL root 模式 chmod 不挡, env var 又被 unsafe_code 禁)。
                // thread_local cell 既线程隔离 (每 test 独占), 又零 unsafe; production build
                // cfg(test)=false 分支直接 fs::rename — 0 production 开销。
                let rename_result = quarantine_rename_with_test_injection(&entry.path(), &dest);
                rename_result.map_err(|err| {
                    SpoolError::Io(io::Error::new(
                        io::ErrorKind::Other,
                        format!(
                            "startup quarantine of non-.pb file {:?} -> {:?} failed: {err}; \
                             文件留在 pending/ 但 pending_snapshot 不列它 = 隐形 disk 泄漏, \
                             fail-fast 让运维手动处理 (mv/rm) 后重启。",
                            entry.path(),
                            dest
                        ),
                    ))
                })?;
                initial_quarantined = initial_quarantined.saturating_add(1);
            }
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
            quarantine_dir,
            pending_count: AtomicUsize::new(initial_count),
            pending_bytes: AtomicU64::new(initial_bytes),
            reserved_bytes: AtomicU64::new(0),
            last_write_failed: AtomicBool::new(false),
            quarantined_count: AtomicU64::new(initial_quarantined),
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
        // 用 checked_sub 防 underflow: 多 reporter 共享 dir 场景下 standalone 写 + 不同 reporter
        // 的 in-memory counter 可能不同步, 删文件成功时若 counter 已 0 直接 no-op (truth=disk)。
        let _ = self
            .inner
            .pending_count
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |cur| {
                cur.checked_sub(1)
            });
        let _ = self
            .inner
            .pending_bytes
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |cur| {
                cur.checked_sub(len)
            });
        Ok(())
    }

    /// 列 `pending/` 下最多 `limit` 个 idempotency_key (去 `.pb` 后缀)。Slice 2 replay worker 用。
    ///
    /// 第三方 P2 finding 2026-05-24 (round 3): 必须用 validate_key 过滤, 跳过非法 key
    /// (open() 启动期已 quarantine 过一遍, 此处是防御深度 — 进程运行时若有人手动
    /// drop 非法名文件到 pending/, 也不会让 drain_pending 撞 InvalidKey 错并停留)。
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
            // 非法 idempotency key (validate_key 拒) 直接 skip — open() 启动 quarantine
            // 已处理一次, 此处兜底防止运行时被注入。
            if validate_key(stripped).is_err() {
                continue;
            }
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

    pub fn last_write_failed(&self) -> bool {
        self.inner.last_write_failed.load(Ordering::Relaxed)
    }

    /// W12-A D-4 第三方 P2 finding 2026-05-24: 累计 quarantine 文件数 (启动期 + replay 期合计)。
    /// heartbeat / metrics 可暴露此值, 高位代表上游写了非法记录或磁盘出过 corrupt event。
    pub fn quarantined_count(&self) -> u64 {
        self.inner.quarantined_count.load(Ordering::Relaxed)
    }

    /// W12-A D-4 第三方 P2 finding 2026-05-24: 把 `pending/<key>.pb` 移到 quarantine 目录,
    /// 同步扣减 pending_count / pending_bytes, 释放 watermark 配额。
    ///
    /// 调用方: replay worker 在 decode 失败时调用本方法替代 "warn 后 continue"。
    /// 旧路径下损坏文件永远占 quota -> watermark 卡死所有请求 = backpressure 卡死生产。
    ///
    /// 设计:
    /// - 物理 rename 到 quarantine/, 保留文件供运维事后审计 (不删, 留证据)。
    /// - 命名带时间戳避免重复 key 多次 quarantine 冲突。
    /// - 扣减 counter 用 checked_sub 防 underflow (多 reporter 共享 dir 时可能不同步)。
    /// - rename 失败时仍然扣减 in-memory counter (磁盘文件保持 — 下次 startup 重扫会发现非 .pb
    ///   再 quarantine, 不会重复计 quota 因为 startup 用 fs::read_dir 重新数)。等等不,
    ///   rename 失败说明文件仍叫 `<key>.pb` 在 pending/, 下次 startup 仍会按 .pb 计入,
    ///   所以 in-memory 扣减但 disk truth 不一致 = 危险。改: rename 失败时返回 Err, in-memory
    ///   counter 不动, 让 caller 知道并下次再试。
    pub fn quarantine_pending(&self, key: &str) -> Result<u64, SpoolError> {
        validate_key(key)?;
        let path = self.inner.pending_dir.join(format!("{key}.pb"));
        let metadata = match fs::metadata(&path) {
            Ok(m) => m,
            Err(err) if err.kind() == io::ErrorKind::NotFound => {
                // 并发 ack 删了, 视为 noop
                return Ok(0);
            }
            Err(err) => return Err(SpoolError::Io(err)),
        };
        let len = metadata.len();
        let dest_name = format!(
            "{key}-{}.pb",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_nanos()
        );
        let dest = self.inner.quarantine_dir.join(dest_name);
        fs::rename(&path, &dest)?;
        let _ = self
            .inner
            .pending_count
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |cur| {
                cur.checked_sub(1)
            });
        let _ = self
            .inner
            .pending_bytes
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |cur| {
                cur.checked_sub(len)
            });
        self.inner
            .quarantined_count
            .fetch_add(1, Ordering::Relaxed);
        Ok(len)
    }

    /// W12-A D-4 Slice 2 (Codex P2-2 catch-22 break): 写一个 0-byte 临时文件 + 立即删除,
    /// 测试 spool 目录是否真磁盘可写。成功 -> 清 last_write_failed latch -> reserve() 可恢复;
    /// 失败 -> latch 保持。
    ///
    /// 为何需要: 原设计只在 persist() 成功路径清 latch。但 latch=true 时 reserve() 拒所有,
    /// persist() 永不被调用 -> latch 永不清 -> 必须重启才能恢复。本探针在 replay tick 周期触发,
    /// 让 transient 磁盘错可在不重启情况下恢复。
    pub fn probe_write_health(&self) -> Result<(), SpoolError> {
        let probe_path = self
            .inner
            .tmp_dir
            .join(format!("health-{}.probe", Uuid::now_v7()));
        let result = (|| -> io::Result<()> {
            let file = fs::OpenOptions::new()
                .create_new(true)
                .write(true)
                .open(&probe_path)?;
            drop(file);
            fs::remove_file(&probe_path)
        })();

        match result {
            Ok(()) => {
                self.inner
                    .last_write_failed
                    .store(false, Ordering::Relaxed);
                Ok(())
            }
            Err(err) => {
                self.inner
                    .last_write_failed
                    .store(true, Ordering::Relaxed);
                Err(SpoolError::Io(err))
            }
        }
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

    /// W12-A D-4 第三方 P2 finding 2026-05-24: 启动时 pending/ 下非 .pb 文件
    /// 必须不计 pending_bytes (防永久占 quota), 且移到 quarantine 留证据。
    ///
    /// 判别性 + mutation 设计 — 4 个独立断言, mutation 任一失败:
    /// 1) pending_count == 1 (只算 .pb)
    /// 2) pending_bytes == legit_pb 大小 (不含 garbage.txt)
    /// 3) quarantined_count == 1 (启动期移走 1 个)
    /// 4) quarantine/ 下有以 "startup-non-pb-" 前缀命名的文件
    ///
    /// mutation:
    /// - 删 `is_pb` 过滤 -> pending_count == 2 / pending_bytes 含 .txt -> (1)+(2) 红
    /// - 删 rename 到 quarantine -> quarantined_count == 0 / 文件还在 pending -> (3)+(4) 红
    #[test]
    fn open_only_counts_pb_files_and_quarantines_non_pb_garbage() {
        let dir = unique_test_dir("non-pb-garbage");
        // 预播 pending/ 含 1 个合法命名 .pb + 1 个非 .pb 垃圾文件
        let pending_dir = dir.join("pending");
        fs::create_dir_all(&pending_dir).expect("mkdir pending");
        let legit_pb_path = pending_dir.join("idem-fake-1.pb");
        fs::write(&legit_pb_path, b"fake-prost-bytes-not-decoded-here-just-counted")
            .expect("write legit pb");
        let legit_size = fs::metadata(&legit_pb_path).unwrap().len();
        let garbage_path = pending_dir.join("readme.txt");
        fs::write(&garbage_path, b"garbage that should not count toward quota")
            .expect("write garbage");

        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open 应成功")
            .expect("enabled");

        assert_eq!(
            spool.pending_count(),
            1,
            "pending_count 只算 .pb (mutation: 删 is_pb 过滤 -> 2)"
        );
        assert_eq!(
            spool.pending_bytes(),
            legit_size,
            "pending_bytes 不应含非 .pb 文件大小 (mutation: 删过滤 -> 含 garbage.txt)"
        );
        assert_eq!(
            spool.quarantined_count(),
            1,
            "启动期非 .pb 应移到 quarantine, 计数 +1 (mutation: 删 rename -> 0)"
        );

        // 验证垃圾文件真被移到 quarantine 目录
        let quarantine_dir = dir.join("quarantine");
        let quarantined: Vec<_> = fs::read_dir(&quarantine_dir)
            .expect("quarantine 目录应存在")
            .flatten()
            .map(|e| e.file_name().to_string_lossy().into_owned())
            .collect();
        assert!(
            quarantined.iter().any(|n| n.starts_with("startup-non-pb-")),
            "quarantine 应含 startup-non-pb-* 前缀的文件, 实际: {quarantined:?}"
        );
        assert!(
            !garbage_path.exists(),
            "原 pending/readme.txt 应已被移走 (mutation: 删 rename -> 仍在 pending)"
        );

        let _ = fs::remove_dir_all(&dir);
    }

    /// Codex round 1 P2 fix 2026-05-24: startup quarantine 阶段失败必须 fail-fast,
    /// 否则非 .pb 文件留在 pending/ 但被 pending_snapshot 忽略 = 隐形 disk 泄漏。
    ///
    /// 本测试覆盖 **quarantine 目录无法创建** 路径 (open() 在 pending 扫描之前就拒)。
    /// 单独的 open_fails_fast_when_startup_quarantine_rename_fails_unix 覆盖 **rename
    /// 失败** 路径 (Codex round 2 P2 fix: 旧测试只覆盖 dir 创建失败, 不能 catch rename
    /// 失败被静默忽略的 mutation)。
    #[test]
    fn open_fails_fast_when_startup_quarantine_dir_cannot_be_created() {
        let dir = unique_test_dir("quarantine-dir-blocked");
        let pending_dir = dir.join("pending");
        fs::create_dir_all(&pending_dir).expect("mkdir pending");
        // 在 dir 下放一个名为 "quarantine" 的普通文件让 create_dir_all 失败
        fs::write(
            dir.join("quarantine"),
            "placeholder forcing mkdir quarantine failure".as_bytes(),
        )
        .expect("write quarantine placeholder");

        let result = AttemptSpool::open(test_options(dir.clone()));
        assert!(
            result.is_err(),
            "quarantine 目录无法创建时 open 应 fail-fast"
        );

        let _ = fs::remove_file(dir.join("quarantine"));
        let _ = fs::remove_dir_all(&dir);
    }

    /// Codex round 2 P2 fix 2026-05-24: 上一测试只覆盖 create_dir_all 失败路径,
    /// 不能 catch 真正的 rename 失败被静默忽略 mutation (CLAUDE.md #14 真实判别性要求)。
    /// 用 cfg(test)-gated env var (HUAKAI_TEST_FORCE_QUARANTINE_RENAME_FAIL) 注入点
    /// 让测试可确定性触发 rename 失败 — chmod / FS perm 在 WSL root 模式下没法挡 root,
    /// env var 注入跨平台稳定。
    ///
    /// 判别性 + mutation:
    /// 1) pending/ 含 1 个非 .pb 文件 (open 必经 rename 分支)
    /// 2) 设 HUAKAI_TEST_FORCE_QUARANTINE_RENAME_FAIL=1 让 cfg(test) 注入路径返回 EACCES
    /// 3) open() 必须返回 Err (验证 fail-fast)
    ///
    /// mutation:
    /// - 把 rename_result.map_err(...)? 改回旧 if rename.is_ok() 静默跳 -> open 返 Ok -> 红。
    /// - 把 cfg(test) 注入点删 -> 此测试只跑真实 fs::rename 总是 OK -> 永远不触发失败路径 ->
    ///   测试名称失实 (但代码仍可工作); 已在生产代码注释里固化注入点必要性。
    ///
    /// 安全: env var 仅在 cfg!(test) = true 时被读, production build 折叠为 false。
    #[test]
    fn open_fails_fast_when_startup_quarantine_rename_fails() {
        let dir = unique_test_dir("quarantine-rename-fail");
        let pending_dir = dir.join("pending");
        fs::create_dir_all(&pending_dir).expect("mkdir pending");
        fs::write(pending_dir.join("garbage.txt"), b"x").expect("write garbage");

        // thread_local 标志 — 仅本测试线程可见, 不影响并发测试。
        super::FORCE_QUARANTINE_RENAME_FAIL.with(|c| c.set(true));
        let result = AttemptSpool::open(test_options(dir.clone()));
        super::FORCE_QUARANTINE_RENAME_FAIL.with(|c| c.set(false));

        let err = match result {
            Ok(_) => panic!(
                "quarantine rename 失败时 open 应 fail-fast (mutation: 旧 if rename.is_ok() \
                 静默跳 -> Ok, 文件留 pending/ -> 复现 P2 finding 隐形 disk 泄漏)"
            ),
            Err(e) => e,
        };
        let err_msg = err.to_string();
        assert!(
            err_msg.contains("startup quarantine"),
            "错误消息应明确指出 startup quarantine 失败, 实际: {err_msg}"
        );

        let _ = fs::remove_dir_all(&dir);
    }

    /// 第三方 P2 finding 2026-05-24 (round 3): 旧 open() 只按 .pb 后缀过滤,
    /// 不校验 idempotency key 合法性。`with space.pb` / `.hidden.pb` / `..escape.pb`
    /// 这种文件全部被计入 pending_bytes/count, 但随后 drain_pending 撞 validate_key
    /// 拒 InvalidKey -> warn+continue -> 文件永久占 quota -> backpressure 503。
    ///
    /// 修法: open() 启动期同时校验 stem (后缀去掉 .pb 后) 是否过 validate_key,
    /// 不过则按 startup-non-pb 路径 quarantine。
    ///
    /// 判别性 + mutation:
    /// 1) 预播 1 个合法 .pb (idem-spool-test-XX.pb) + 1 个含空格的 .pb +
    ///    1 个隐藏点开头的 .pb + 1 个非 .pb txt
    /// 2) open() 后 pending_count == 1 (只有合法 stem)
    /// 3) pending_bytes 不含非法 .pb 的字节
    /// 4) quarantined_count == 3 (with space + .hidden + readme.txt 都被移)
    ///
    /// mutation:
    /// - 删 stem_valid 校验 (回旧 is_pb 后缀过滤) -> pending_count == 3 (with space + .hidden 也被计) -> 红
    /// - 把 quarantine 路径只针对非 .pb -> 非法 .pb 留 pending -> quarantined_count == 1 -> 红
    #[test]
    fn open_quarantines_pb_files_with_invalid_idempotency_keys() {
        let dir = unique_test_dir("invalid-key-pb-quarantine");
        let pending_dir = dir.join("pending");
        fs::create_dir_all(&pending_dir).expect("mkdir pending");

        // 合法 stem (validate_key 通过 — alphanumeric + - 即可)
        let legit_path = pending_dir.join("idem-spool-test-legit-1234567890abcdef.pb");
        fs::write(&legit_path, b"fake-prost-bytes").expect("write legit");
        let legit_size = fs::metadata(&legit_path).unwrap().len();

        // 非法 stem 1: 含空格 (validate_key 拒)
        fs::write(pending_dir.join("with space.pb"), b"AAAAAAAAAA").expect("write with-space");
        // 非法 stem 2: 隐藏点开头 (validate_key 拒)
        fs::write(pending_dir.join(".hidden.pb"), b"BBBBBBBBBB").expect("write .hidden");
        // 非 .pb (旧 quarantine 路径仍处理)
        fs::write(pending_dir.join("readme.txt"), b"CCCCCCCCCC").expect("write readme");

        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open OK")
            .expect("enabled");

        assert_eq!(
            spool.pending_count(),
            1,
            "pending_count 只算合法 stem; with space.pb 和 .hidden.pb 必须被 quarantine \
             (mutation: 删 stem_valid 校验 -> 3, 红)"
        );
        assert_eq!(
            spool.pending_bytes(),
            legit_size,
            "pending_bytes 不应含非法 stem .pb 的字节 (mutation: 删 stem_valid -> 含 with-space + .hidden)"
        );
        assert_eq!(
            spool.quarantined_count(),
            3,
            "启动期应 quarantine 3 个: with space.pb + .hidden.pb + readme.txt; \
             实际 {} (mutation: 非法 .pb 路径不 quarantine -> 1)",
            spool.quarantined_count()
        );

        // 验证 pending_snapshot 也只返回合法 stem
        let snapshot = spool.pending_snapshot(10);
        assert_eq!(snapshot.len(), 1, "pending_snapshot 只返回合法 key");
        assert_eq!(snapshot[0], "idem-spool-test-legit-1234567890abcdef");

        let _ = fs::remove_dir_all(&dir);
    }

    /// 第三方 P2 finding 2026-05-24 (round 3, Codex P2 wiring fix): runtime-injection
    /// 路径必须由 pending_snapshot 自己 filter — open() 已 quarantine 过启动期文件,
    /// 但进程运行中若有人 (运维 / 工具 bug) drop 非法名 .pb 到 pending/, 仍要在
    /// pending_snapshot 阶段 skip 让 drain_pending 不撞 InvalidKey 错。
    ///
    /// 判别性: 在 open() 之后注入非法 .pb, pending_snapshot 返回长度严格为 0。
    /// mutation: 删 pending_snapshot 的 validate_key filter -> 返回 1 -> 红 (复现 P2 wiring 缺口)。
    #[test]
    fn pending_snapshot_filters_invalid_keys_injected_after_open() {
        let dir = unique_test_dir("snapshot-runtime-inject");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open OK")
            .expect("enabled");

        // open() 后空 spool. 此时模拟外部注入非法名 .pb (绕过 startup quarantine)。
        let pending_dir = dir.join("pending");
        fs::write(pending_dir.join("with space.pb"), b"X").expect("inject with-space pb");
        fs::write(pending_dir.join(".hidden.pb"), b"Y").expect("inject hidden pb");

        let snapshot = spool.pending_snapshot(10);
        assert_eq!(
            snapshot.len(),
            0,
            "pending_snapshot 必须 filter 非法 key (mutation: 删 validate_key filter -> 返回 2)"
        );

        let _ = fs::remove_dir_all(&dir);
    }

    /// W12-A D-4 第三方 P2 finding 2026-05-24: quarantine_pending 必须移文件 +
    /// 同步扣减 pending_count / pending_bytes / 增 quarantined_count, 否则坏文件
    /// 永远占 watermark = backpressure 卡死。
    ///
    /// 判别性 + mutation 设计:
    /// 1) persist 后 pending_count == 1, pending_bytes > 0
    /// 2) quarantine_pending 返 Ok(bytes_freed) 且 == 持久化字节数
    /// 3) 调用后 pending_count == 0 / pending_bytes == 0 / quarantined_count == 1
    /// 4) pending/<key>.pb 文件消失, quarantine/<key>-<ts>.pb 出现
    ///
    /// mutation:
    /// - 删 quarantine_pending 的 fetch_update pending_count -> count 仍是 1 -> (3) 红
    /// - 删 fetch_update pending_bytes -> bytes != 0 -> (3) 红
    /// - 删 fetch_add quarantined_count -> count == 0 -> (3) 红
    /// - 删 fs::rename -> 文件还在 pending -> (4) 红
    #[test]
    fn quarantine_pending_moves_file_and_decrements_counters() {
        let dir = unique_test_dir("quarantine-pending");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open OK")
            .expect("enabled");

        let report = sample_billable_report("q1");
        let key = report.idempotency_key.clone();
        let reservation = spool.reserve().expect("reserve OK");
        let outcome = spool.persist(&report, reservation).expect("persist OK");

        let persisted_bytes = outcome.bytes_written;
        assert_eq!(spool.pending_count(), 1, "persist 后 count=1");
        assert_eq!(
            spool.pending_bytes(),
            persisted_bytes,
            "persist 后 pending_bytes == 写入字节数"
        );
        assert_eq!(spool.quarantined_count(), 0, "初始 quarantined_count=0");

        let bytes_freed = spool
            .quarantine_pending(&key)
            .expect("quarantine_pending 应成功");
        assert_eq!(
            bytes_freed, persisted_bytes,
            "quarantine 应返回正确释放字节数"
        );

        assert_eq!(spool.pending_count(), 0, "quarantine 后 pending_count=0 (mutation: 删 fetch_update -> 仍 1)");
        assert_eq!(
            spool.pending_bytes(),
            0,
            "quarantine 后 pending_bytes=0 (mutation: 删 fetch_update -> 仍非 0)"
        );
        assert_eq!(
            spool.quarantined_count(),
            1,
            "quarantine 后计数 +1 (mutation: 删 fetch_add -> 仍 0)"
        );

        // pending/<key>.pb 消失, quarantine/ 含以 key 前缀命名的文件
        assert!(
            !dir.join("pending").join(format!("{key}.pb")).exists(),
            "pending/{key}.pb 应被 rename 走 (mutation: 删 rename -> 仍在 pending)"
        );
        let quarantined: Vec<_> = fs::read_dir(dir.join("quarantine"))
            .expect("quarantine 目录")
            .flatten()
            .map(|e| e.file_name().to_string_lossy().into_owned())
            .collect();
        assert!(
            quarantined.iter().any(|n| n.starts_with(&key)),
            "quarantine 应含以 key 前缀命名的文件, 实际: {quarantined:?}"
        );

        let _ = fs::remove_dir_all(&dir);
    }

    /// W12-A D-4 第三方 P2 finding 2026-05-24: quarantine 之后 watermark 应释放,
    /// 让先前卡 backpressure 的 reserve 重新成功 — 这是修复 "持续 503" 的关键不变量。
    ///
    /// 判别性 + mutation 设计:
    /// 1) 用 test_options 的 watermark=2048 + max_record=512, persist 4 条接近水位
    /// 2) 第 5 次 reserve 应 WatermarkExceeded (验证 watermark 真起作用)
    /// 3) quarantine 一条后, reserve 应 Ok (watermark 让出)
    ///
    /// mutation:
    /// - quarantine_pending 删 pending_bytes 扣减 -> watermark 不让出 -> (3) 红
    /// - quarantine_pending 直接 NOOP (假实现) -> 既不让出 watermark 也不释放文件 -> (3) 红
    #[test]
    fn quarantine_pending_releases_watermark_for_subsequent_reserve() {
        let dir = unique_test_dir("quarantine-watermark");
        let spool = AttemptSpool::open(test_options(dir.clone()))
            .expect("open OK")
            .expect("enabled");

        // 用一系列 reserve+persist 把 pending_bytes 推到接近 watermark
        let mut persisted_keys: Vec<String> = Vec::new();
        loop {
            match spool.reserve() {
                Ok(reservation) => {
                    let report = sample_billable_report(&format!("w{}", persisted_keys.len()));
                    let key = report.idempotency_key.clone();
                    spool.persist(&report, reservation).expect("persist OK");
                    persisted_keys.push(key);
                    if persisted_keys.len() > 32 {
                        panic!("意外: test_options watermark 太大, 超过 32 个仍未饱和");
                    }
                }
                Err(_) => break, // watermark hit
            }
        }
        assert!(
            !persisted_keys.is_empty(),
            "至少应 persist 1 条才能演示 quarantine 让出 watermark"
        );

        // 验证 watermark 真起作用
        assert!(
            matches!(
                spool.reserve(),
                Err(AttemptSpoolBackpressure::WatermarkExceeded { .. })
            ),
            "watermark 越线 reserve 应失败 (此前 persist 已饱和)"
        );

        // quarantine 第一个 key 释放 watermark
        let first_key = persisted_keys[0].clone();
        spool
            .quarantine_pending(&first_key)
            .expect("quarantine OK");

        // 现在 reserve 应能成功 (watermark 让出至少 1 条 record 空间)
        let reservation = spool.reserve().expect(
            "quarantine 后 reserve 应成功 — mutation: quarantine 不扣 pending_bytes 时此处 \
             仍 WatermarkExceeded -> 测试红 (复现 P2 finding: 持续 backpressure)",
        );
        drop(reservation); // Drop RAII 释放配额

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
