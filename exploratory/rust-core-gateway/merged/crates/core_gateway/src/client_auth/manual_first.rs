//! W11-A D-1b Phase 1 Manual First 静态 tenant 兜底 (synthesis §6 step 6, D-5 + D-11)。
//!
//! 角色 (synthesis §7-J):
//! - 仅 mock/staging/internal-smoke 流量使用 — **生产模式 ON 必启动 fail-fast** (`config.rs`)。
//! - Phase 1 桥接: Go control plane P-1 消费侧未上线时, 通过本地静态 `secret_sha256 → tenant_id`
//!   map 让 Rust 数据面在不 trust x-tenant-id (A3) 的前提下仍能跑 dev/staging。
//! - Phase 2 Go 上线后 Owner 显式关 flag → 强制走 control plane 派生。
//! - Phase 3 整模块下线。
//!
//! 安全 (D-5):
//! - 配置载体 = JSON file (`HUAKAI_MANUAL_FIRST_KEYS_FILE`), entry 仅含 SHA-256 hex,
//!   **raw secret 永不入 config/env/log** (operator 用 sha2 命令离线哈希 raw key 写入)。
//! - file 路径必须绝对 (相对路径在不同 CWD 重启时指向不同文件 = 静默 tenant 漂移)。
//!
//! D-11: Manual First ON + credential hash 未匹配 = 401 (fail-closed by listener)。

use std::{fs, path::PathBuf};

use serde::Deserialize;
use thiserror::Error;

use super::credential::{ClientCredential, ClientCredentialKind};

/// Manual First 启动配置 — 由 StartupConfig.client_auth_manual_first_* 填充。
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ManualFirstConfig {
    /// 启用开关 — 默认 false (D-7 OFF default + 生产模式 ON 启动 fail-fast)。
    pub enabled: bool,
    /// JSON file 路径 — enabled=true 时必须非空。
    pub keys_file: Option<PathBuf>,
}

/// 单条静态映射 entry — file JSON 的元素 schema。
#[derive(Clone, Debug, Deserialize, Eq, PartialEq)]
pub struct ManualFirstEntry {
    /// 凭据 kind, 必须与请求解析出的 kind 匹配 (避免 Bearer fingerprint 误匹 x-api-key 表)。
    pub kind: ManualFirstKindWire,
    /// 64 hex chars = SHA-256("bearer:<token>" 或 "x-api-key:<key>")。
    /// operator 离线计算: `echo -n "bearer:<token>" | sha256sum`。
    pub secret_sha256: String,
    /// 派生 tenant — 写入 RouteQueryRequest.tenant_id (Phase 1 only)。
    pub tenant_id: String,
    /// 运维标签 — 仅供 startup log 显示 entry 数量与 label, 不进 attempt report。
    pub label: String,
}

/// JSON 接线 enum: 字符串 ↔ ClientCredentialKind。
/// 避免直接 derive(Deserialize) on ClientCredentialKind 让该枚举依赖 serde。
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "kebab-case")]
pub enum ManualFirstKindWire {
    Bearer,
    XApiKey,
}

impl ManualFirstKindWire {
    fn matches(self, k: ClientCredentialKind) -> bool {
        matches!(
            (self, k),
            (Self::Bearer, ClientCredentialKind::Bearer)
                | (Self::XApiKey, ClientCredentialKind::XApiKey)
        )
    }
}

/// 启动期构造的 resolver — 持已校验的 entries vector。
///
/// Debug 安全: entries 内 `secret_sha256` 是 SHA-256 哈希字符串 (operator 离线计算),
/// 非 raw secret; tenant_id + label 是非敏感运维标签。derive(Debug) 输出可入 log。
#[derive(Debug)]
pub struct ManualFirstResolver {
    enabled: bool,
    entries: Vec<ManualFirstEntry>,
}

impl ManualFirstResolver {
    /// 从 config 构造。enabled=false → 返回空 resolver, 永不命中。
    pub fn from_config(cfg: &ManualFirstConfig) -> Result<Self, ManualFirstError> {
        if !cfg.enabled {
            return Ok(Self {
                enabled: false,
                entries: Vec::new(),
            });
        }
        let path = cfg.keys_file.as_ref().ok_or(ManualFirstError::MissingFile)?;
        if !path.is_absolute() {
            return Err(ManualFirstError::RelativePath(path.clone()));
        }
        let bytes = fs::read(path).map_err(|err| {
            ManualFirstError::FileReadFailed {
                path: path.clone(),
                source_msg: err.to_string(),
            }
        })?;
        let parsed: Vec<ManualFirstEntry> = serde_json::from_slice(&bytes).map_err(|err| {
            ManualFirstError::InvalidJson {
                path: path.clone(),
                source_msg: err.to_string(),
            }
        })?;
        // Schema 校验: tenant 非空 + hash 严格 64 hex chars + label 非空。
        for entry in &parsed {
            if entry.tenant_id.trim().is_empty() {
                return Err(ManualFirstError::EmptyTenant(entry.label.clone()));
            }
            if entry.secret_sha256.len() != 64
                || !entry.secret_sha256.chars().all(|c| c.is_ascii_hexdigit())
            {
                return Err(ManualFirstError::InvalidHash {
                    label: entry.label.clone(),
                    got_len: entry.secret_sha256.len(),
                });
            }
        }
        Ok(Self {
            enabled: true,
            entries: parsed,
        })
    }

    /// 测试用直接构造 — 不读 disk, 直接喂 entries。
    #[cfg(test)]
    pub fn with_entries(entries: Vec<ManualFirstEntry>) -> Self {
        Self {
            enabled: true,
            entries,
        }
    }

    pub fn enabled(&self) -> bool {
        self.enabled
    }

    pub fn entry_count(&self) -> usize {
        self.entries.len()
    }

    /// 解析 credential 对应的 tenant id。
    /// - 未启用 → `None`
    /// - 启用 + 匹配命中 → `Some(tenant_id)`
    /// - 启用 + 未命中 → `None` (listener 按 D-11 转 401)。
    pub fn resolve_tenant(&self, credential: &ClientCredential) -> Option<String> {
        if !self.enabled {
            return None;
        }
        let full_hash = credential.full_sha256_hex();
        let cred_kind = credential.kind();
        self.entries
            .iter()
            .find(|entry| {
                entry.kind.matches(cred_kind)
                    && entry.secret_sha256.eq_ignore_ascii_case(&full_hash)
            })
            .map(|entry| entry.tenant_id.clone())
    }
}

#[derive(Debug, Error)]
pub enum ManualFirstError {
    #[error("HUAKAI_MANUAL_FIRST_KEYS_FILE must be set when HUAKAI_MANUAL_FIRST_ENABLED=true")]
    MissingFile,
    #[error("HUAKAI_MANUAL_FIRST_KEYS_FILE must be absolute path (got {0:?}); relative paths drift across CWD restarts → tenant mis-attribution")]
    RelativePath(PathBuf),
    #[error("Manual First keys file {path:?} read failed: {source_msg}")]
    FileReadFailed { path: PathBuf, source_msg: String },
    #[error("Manual First keys file {path:?} invalid JSON: {source_msg}")]
    InvalidJson { path: PathBuf, source_msg: String },
    #[error("Manual First entry {0:?} has empty tenant_id (operator misconfig)")]
    EmptyTenant(String),
    #[error("Manual First entry {label:?} has invalid secret_sha256 (expected 64 hex chars, got len={got_len})")]
    InvalidHash { label: String, got_len: usize },
}

#[cfg(test)]
mod tests {
    use super::*;
    use http::{HeaderMap, HeaderValue, header::AUTHORIZATION};

    const FAKE_BEARER: &str = "FAKE-d1b-bearer-manual-first-fixture";
    const FAKE_APIKEY: &str = "FAKE-d1b-apikey-manual-first-fixture";

    fn make_bearer_credential() -> ClientCredential {
        let mut h = HeaderMap::new();
        h.insert(
            AUTHORIZATION,
            HeaderValue::from_str(&format!("Bearer {FAKE_BEARER}")).unwrap(),
        );
        ClientCredential::from_headers(&h).unwrap().unwrap()
    }

    fn make_apikey_credential() -> ClientCredential {
        let mut h = HeaderMap::new();
        h.insert("x-api-key", HeaderValue::from_static(FAKE_APIKEY));
        ClientCredential::from_headers(&h).unwrap().unwrap()
    }

    fn sha256_hex(canonical: &str) -> String {
        use sha2::{Digest, Sha256};
        let mut hasher = Sha256::new();
        hasher.update(canonical.as_bytes());
        hasher.finalize().iter().fold(String::with_capacity(64), |mut acc, b| {
            use std::fmt::Write;
            let _ = write!(acc, "{:02x}", b);
            acc
        })
    }

    #[test]
    fn disabled_resolver_never_resolves() {
        let r = ManualFirstResolver::from_config(&ManualFirstConfig {
            enabled: false,
            keys_file: None,
        })
        .expect("disabled 应总成功不读 file");
        assert!(!r.enabled());
        assert_eq!(r.entry_count(), 0);
        let cred = make_bearer_credential();
        assert!(r.resolve_tenant(&cred).is_none(), "disabled 必返 None");
    }

    #[test]
    fn enabled_but_missing_file_path_fails_fast() {
        let err = ManualFirstResolver::from_config(&ManualFirstConfig {
            enabled: true,
            keys_file: None,
        })
        .expect_err("enabled + no file → 必拒");
        assert!(matches!(err, ManualFirstError::MissingFile));
    }

    #[test]
    fn enabled_with_relative_path_fails_fast() {
        let err = ManualFirstResolver::from_config(&ManualFirstConfig {
            enabled: true,
            keys_file: Some(PathBuf::from("relative/path.json")),
        })
        .expect_err("relative path → 必拒");
        assert!(matches!(err, ManualFirstError::RelativePath(_)));
    }

    /// 主路径: enabled + 匹配命中 → 派 tenant。
    /// mutation: 把 resolve_tenant 的 .eq_ignore_ascii_case 改 false → 测试红。
    #[test]
    fn resolve_tenant_matches_when_kind_and_hash_align() {
        let cred = make_bearer_credential();
        let canonical = format!("bearer:{FAKE_BEARER}");
        let entry = ManualFirstEntry {
            kind: ManualFirstKindWire::Bearer,
            secret_sha256: sha256_hex(&canonical),
            tenant_id: "tenant-bearer-fixture".to_owned(),
            label: "bearer-fixture".to_owned(),
        };
        let r = ManualFirstResolver::with_entries(vec![entry]);
        let resolved = r.resolve_tenant(&cred);
        assert_eq!(resolved.as_deref(), Some("tenant-bearer-fixture"));
    }

    /// D-11 守门核心: 启用但 hash 不匹配 → `None` → listener 转 401。
    /// mutation: 把 .find(...) 永远返 entries.first().cloned() → 测试红。
    #[test]
    fn resolve_tenant_returns_none_when_hash_does_not_match() {
        let cred = make_bearer_credential();
        let unrelated_canonical = "bearer:totally-unrelated-FAKE-key";
        let entry = ManualFirstEntry {
            kind: ManualFirstKindWire::Bearer,
            secret_sha256: sha256_hex(unrelated_canonical),
            tenant_id: "tenant-unrelated".to_owned(),
            label: "unrelated".to_owned(),
        };
        let r = ManualFirstResolver::with_entries(vec![entry]);
        assert!(
            r.resolve_tenant(&cred).is_none(),
            "D-11: hash 未匹配必返 None, listener 据此 401"
        );
    }

    /// kind 必须区分: 即便 Bearer SHA 与 x-api-key SHA 都在表里, 也要按 kind 选对。
    /// mutation: 删 ManualFirstKindWire::matches 调用 → resolve_tenant 错配 → 此测试红。
    #[test]
    fn resolve_tenant_distinguishes_bearer_vs_x_api_key_kind() {
        let bearer_cred = make_bearer_credential();
        let apikey_cred = make_apikey_credential();

        let bearer_entry = ManualFirstEntry {
            kind: ManualFirstKindWire::Bearer,
            secret_sha256: sha256_hex(&format!("bearer:{FAKE_BEARER}")),
            tenant_id: "tenant-bearer".to_owned(),
            label: "bearer".to_owned(),
        };
        let apikey_entry = ManualFirstEntry {
            kind: ManualFirstKindWire::XApiKey,
            secret_sha256: sha256_hex(&format!("x-api-key:{FAKE_APIKEY}")),
            tenant_id: "tenant-apikey".to_owned(),
            label: "apikey".to_owned(),
        };
        let r = ManualFirstResolver::with_entries(vec![bearer_entry, apikey_entry]);

        assert_eq!(
            r.resolve_tenant(&bearer_cred).as_deref(),
            Some("tenant-bearer")
        );
        assert_eq!(
            r.resolve_tenant(&apikey_cred).as_deref(),
            Some("tenant-apikey")
        );
    }

    #[test]
    fn from_config_rejects_empty_tenant_entry() {
        // 直接 use parse path: 写临时 JSON file 验。
        let tmp = std::env::temp_dir().join(format!(
            "huakai-manual-first-empty-tenant-{}.json",
            std::process::id()
        ));
        std::fs::write(
            &tmp,
            r#"[{"kind":"bearer","secret_sha256":"0000000000000000000000000000000000000000000000000000000000000000","tenant_id":"","label":"bad"}]"#,
        )
        .unwrap();
        let err = ManualFirstResolver::from_config(&ManualFirstConfig {
            enabled: true,
            keys_file: Some(tmp.clone()),
        })
        .expect_err("空 tenant_id 必拒");
        let _ = std::fs::remove_file(&tmp);
        assert!(matches!(err, ManualFirstError::EmptyTenant(_)));
    }

    #[test]
    fn from_config_rejects_invalid_hash_length() {
        let tmp = std::env::temp_dir().join(format!(
            "huakai-manual-first-bad-hash-{}.json",
            std::process::id()
        ));
        std::fs::write(
            &tmp,
            r#"[{"kind":"bearer","secret_sha256":"deadbeef","tenant_id":"t","label":"bad"}]"#,
        )
        .unwrap();
        let err = ManualFirstResolver::from_config(&ManualFirstConfig {
            enabled: true,
            keys_file: Some(tmp.clone()),
        })
        .expect_err("hash 长度非 64 必拒");
        let _ = std::fs::remove_file(&tmp);
        assert!(matches!(err, ManualFirstError::InvalidHash { got_len: 8, .. }));
    }

    #[test]
    fn from_config_parses_valid_json_file_end_to_end() {
        let tmp = std::env::temp_dir().join(format!(
            "huakai-manual-first-valid-{}.json",
            std::process::id()
        ));
        let canonical = format!("bearer:{FAKE_BEARER}");
        let hash = sha256_hex(&canonical);
        let json = format!(
            r#"[{{"kind":"bearer","secret_sha256":"{hash}","tenant_id":"tenant-from-file","label":"prod-canary"}}]"#
        );
        std::fs::write(&tmp, json).unwrap();
        let r = ManualFirstResolver::from_config(&ManualFirstConfig {
            enabled: true,
            keys_file: Some(tmp.clone()),
        })
        .expect("有效 JSON 应解析成功");
        let _ = std::fs::remove_file(&tmp);
        assert!(r.enabled());
        assert_eq!(r.entry_count(), 1);

        let cred = make_bearer_credential();
        assert_eq!(
            r.resolve_tenant(&cred).as_deref(),
            Some("tenant-from-file"),
            "从 file 加载 + 匹配 → tenant-from-file"
        );
    }
}
