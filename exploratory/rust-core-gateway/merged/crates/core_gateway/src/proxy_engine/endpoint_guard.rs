// W11-C D-3 vendor endpoint guard
// 职责: 控制面返回的 vendor_endpoint 在生产模式下必须 https + 公网, 防控制面被攻陷
// 把流量打到内部服务 / metadata endpoint / 攻击者本地服务。dev/test 模式只 warn 不阻断,
// 让本地 mock 上游测试不破。

use std::net::{IpAddr, Ipv4Addr};

use http::Uri;
use thiserror::Error;

use crate::config::RuntimeMode;

/// 已知 intranet / local-network 顶级后缀 (P1 codex 2026-05-23)。
/// 单 label hostname (无 dot) 也按 intranet 拒绝。
/// 注: 这是 best-effort pre-connect 检查; 真正完整的 DNS rebinding 防御需要
/// HTTP connector 层 (在 resolve DNS 后) 再次检查所有 A/AAAA 记录, 不在本层 scope。
const UNSAFE_HOSTNAME_SUFFIXES: &[&str] = &[
    ".local",     // mDNS / Bonjour
    ".localhost",
    ".internal",  // 常见企业内网
    ".intranet",
    ".lan",
    ".home",
    ".private",
    ".corp",
];

#[derive(Debug, Error, PartialEq, Eq)]
pub enum EndpointGuardError {
    #[error(
        "vendor endpoint scheme {scheme:?} is not https; production refuses plaintext upstream"
    )]
    NonHttpsScheme { scheme: String },
    #[error(
        "vendor endpoint host {host:?} is not allowed in production (loopback / private / link-local / multicast / unspecified)"
    )]
    UnsafeHost { host: String },
    #[error("vendor endpoint is missing a host component")]
    MissingHost,
}

/// 生产模式: scheme 必须 https + host 不能 IP literal 命中私网/loopback/link-local/multicast,
/// 也不能是字面量 "localhost"。hostname (非 IP) 在本层 trust (DNS rebinding 是 HTTP client
/// connector 层的额外职责, 不在 listener pre-forward gate)。
///
/// dev/test 模式: log warn (在调用方) 不阻断, 让 mock 上游 / 本地 vendor 反代继续可用。
///
/// mutation marker: 删 is_production() 早 return → development_allows_http_and_loopback
/// 测试仍绿但 production_rejects_* 系列测试不变绿; 把 production gate 改成 always-pass
/// → production_rejects_* 测试断言红。
pub fn validate_vendor_endpoint(
    uri: &Uri,
    runtime_mode: RuntimeMode,
) -> Result<(), EndpointGuardError> {
    let scheme = uri.scheme_str().unwrap_or("").to_owned();
    if runtime_mode.is_production() && !scheme.eq_ignore_ascii_case("https") {
        return Err(EndpointGuardError::NonHttpsScheme { scheme });
    }

    let raw_host = uri.host().ok_or(EndpointGuardError::MissingHost)?;
    // http::Uri::host() 对 IPv6 literal `[::1]` 返回 `[::1]` (带方括号),
    // 需要剥掉再做 IpAddr::parse 否则误判公网 hostname。
    let host = raw_host
        .strip_prefix('[')
        .and_then(|s| s.strip_suffix(']'))
        .unwrap_or(raw_host)
        .to_owned();

    if !runtime_mode.is_production() {
        return Ok(());
    }

    // production: 先尝试解析 IP literal (含 IPv6 form), 再尝试 hostname pattern。
    // IP literal 检查可挡 ::ffff:127.0.0.1 这类 IPv4-mapped IPv6 SSRF。
    if let Ok(ip) = host.parse::<IpAddr>() {
        if is_unsafe_ip(ip) {
            return Err(EndpointGuardError::UnsafeHost { host });
        }
        return Ok(());
    }

    // hostname: 拒已知 intranet TLD + 单 label 主机名 + 字面量 "localhost"
    if is_unsafe_hostname(&host) {
        return Err(EndpointGuardError::UnsafeHost { host });
    }

    Ok(())
}

/// P1 codex 2026-05-23: 拒已知 intranet hostname (best-effort pre-connect 防御)。
/// DNS rebinding 完整防御需在 HTTP connector 解析 DNS 后二次 IP 检查, 不在本层。
fn is_unsafe_hostname(host: &str) -> bool {
    let lower = host.to_ascii_lowercase();
    if lower == "localhost" {
        return true;
    }
    // 单 label 主机名 (无 dot) — 大概率 intranet, 拒
    if !lower.contains('.') {
        return true;
    }
    UNSAFE_HOSTNAME_SUFFIXES
        .iter()
        .any(|suffix| lower.ends_with(suffix))
}

fn is_unsafe_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v4) => is_unsafe_v4(v4),
        IpAddr::V6(v6) => {
            // P1 codex 2026-05-23: IPv4-mapped IPv6 (::ffff:127.0.0.1) 必须按底层
            // IPv4 unsafe-check, 否则攻击者拿 IPv6 form 绕过。
            if let Some(mapped) = v6.to_ipv4_mapped() {
                return is_unsafe_v4(mapped);
            }
            v6.is_loopback()       // ::1
                || v6.is_unspecified() // ::
                || v6.is_multicast() // ff00::/8
                // unique local fc00::/7 (RFC 4193)
                || (v6.segments()[0] & 0xfe00 == 0xfc00)
                // link local fe80::/10
                || (v6.segments()[0] & 0xffc0 == 0xfe80)
        }
    }
}

fn is_unsafe_v4(v4: Ipv4Addr) -> bool {
    v4.is_loopback()           // 127/8
        || v4.is_private()      // 10/8, 172.16/12, 192.168/16
        || v4.is_link_local()   // 169.254/16
        || v4.is_multicast()    // 224/4
        || v4.is_broadcast()    // 255.255.255.255
        || v4.is_unspecified()  // 0.0.0.0
        || v4.is_documentation() // 192.0.2/24, 198.51.100/24, 203.0.113/24
}

#[cfg(test)]
mod tests {
    use super::*;

    fn uri(s: &str) -> Uri {
        s.parse().expect("test uri 应可解析")
    }

    // ===== production 拒绝 =====

    #[test]
    fn production_rejects_http_scheme_even_for_public_host() {
        let err = validate_vendor_endpoint(
            &uri("http://api.openai.com"),
            RuntimeMode::Production,
        )
        .expect_err("http 在 production 必须被拒");
        assert!(
            matches!(err, EndpointGuardError::NonHttpsScheme { .. }),
            "实际: {err:?}"
        );
    }

    #[test]
    fn production_rejects_127_0_0_1_loopback() {
        let err = validate_vendor_endpoint(
            &uri("https://127.0.0.1:8080"),
            RuntimeMode::Production,
        )
        .expect_err("127.0.0.1 在 production 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_localhost_hostname_literal() {
        let err =
            validate_vendor_endpoint(&uri("https://localhost"), RuntimeMode::Production)
                .expect_err("localhost 字面量在 production 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_private_10_network() {
        let err =
            validate_vendor_endpoint(&uri("https://10.0.5.5:443"), RuntimeMode::Production)
                .expect_err("10.0.0.0/8 私网在 production 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_private_172_16_network() {
        let err =
            validate_vendor_endpoint(&uri("https://172.16.0.1"), RuntimeMode::Production)
                .expect_err("172.16.0.0/12 私网在 production 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_private_192_168_network() {
        let err =
            validate_vendor_endpoint(&uri("https://192.168.1.1"), RuntimeMode::Production)
                .expect_err("192.168.0.0/16 私网在 production 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_link_local_169_254_metadata() {
        // AWS / GCP metadata endpoint 是典型 SSRF 目标
        let err = validate_vendor_endpoint(
            &uri("https://169.254.169.254/latest/meta-data"),
            RuntimeMode::Production,
        )
        .expect_err("169.254/16 link-local (cloud metadata) 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_ipv6_loopback() {
        let err = validate_vendor_endpoint(&uri("https://[::1]"), RuntimeMode::Production)
            .expect_err("IPv6 ::1 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_ipv6_link_local() {
        let err = validate_vendor_endpoint(
            &uri("https://[fe80::1]"),
            RuntimeMode::Production,
        )
        .expect_err("IPv6 fe80::/10 link-local 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_ipv6_unique_local() {
        let err = validate_vendor_endpoint(
            &uri("https://[fc00::1]"),
            RuntimeMode::Production,
        )
        .expect_err("IPv6 fc00::/7 unique-local 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_unspecified_0_0_0_0() {
        let err = validate_vendor_endpoint(&uri("https://0.0.0.0"), RuntimeMode::Production)
            .expect_err("0.0.0.0 unspecified 必须被拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    // ===== production 放行 =====

    #[test]
    fn production_allows_public_https_hostname() {
        assert!(
            validate_vendor_endpoint(
                &uri("https://api.openai.com/v1/messages"),
                RuntimeMode::Production
            )
            .is_ok(),
            "公网 vendor 必须可达"
        );
    }

    #[test]
    fn production_allows_public_https_ip() {
        // 8.8.8.8 公网 IP (不是私网/loopback/link-local) 应放行
        assert!(
            validate_vendor_endpoint(&uri("https://8.8.8.8"), RuntimeMode::Production).is_ok()
        );
    }

    // ===== dev/test 放行所有 =====

    #[test]
    fn development_allows_http_and_loopback() {
        assert!(
            validate_vendor_endpoint(
                &uri("http://127.0.0.1:8080"),
                RuntimeMode::Development
            )
            .is_ok(),
            "dev 模式不阻断本地 mock"
        );
        assert!(
            validate_vendor_endpoint(&uri("http://localhost:9000"), RuntimeMode::Test).is_ok(),
            "test 模式不阻断 localhost"
        );
    }

    #[test]
    fn development_allows_private_ip() {
        assert!(
            validate_vendor_endpoint(
                &uri("http://192.168.1.50/v1/chat"),
                RuntimeMode::Development
            )
            .is_ok(),
            "dev 模式可对内网 vendor 反代做开发测试"
        );
    }

    // ===== P1 codex 2026-05-23: SSRF 旁路防御 =====

    /// IPv4-mapped IPv6 (::ffff:127.0.0.1) 必须按 IPv4 unsafe-check, 否则 SSRF 绕过。
    /// mutation: 删 to_ipv4_mapped 分支 → 此测试断言 expect_err 红。
    #[test]
    fn production_rejects_ipv4_mapped_ipv6_loopback() {
        let err = validate_vendor_endpoint(
            &uri("https://[::ffff:127.0.0.1]"),
            RuntimeMode::Production,
        )
        .expect_err("::ffff:127.0.0.1 必须按 IPv4 unsafe 拒绝");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_ipv4_mapped_ipv6_private_192_168() {
        let err = validate_vendor_endpoint(
            &uri("https://[::ffff:192.168.1.1]"),
            RuntimeMode::Production,
        )
        .expect_err("::ffff:192.168.1.1 必须按 IPv4 私网拒绝");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    /// Best-effort: 已知 intranet TLD 拒, 不依赖 DNS 解析。
    #[test]
    fn production_rejects_dot_local_hostname() {
        let err = validate_vendor_endpoint(
            &uri("https://printer.local"),
            RuntimeMode::Production,
        )
        .expect_err("*.local mDNS hostname 必须拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_dot_internal_hostname() {
        let err = validate_vendor_endpoint(
            &uri("https://vault.internal"),
            RuntimeMode::Production,
        )
        .expect_err("*.internal hostname 必须拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    #[test]
    fn production_rejects_single_label_hostname() {
        let err =
            validate_vendor_endpoint(&uri("https://internal-service"), RuntimeMode::Production)
                .expect_err("单 label hostname (无 dot) 必须拒");
        assert!(matches!(err, EndpointGuardError::UnsafeHost { .. }));
    }

    /// 公网 hostname (含 dot 且非已知 intranet TLD) 应放行。
    #[test]
    fn production_allows_normal_public_hostname_with_dot() {
        assert!(
            validate_vendor_endpoint(
                &uri("https://api.openai.com"),
                RuntimeMode::Production
            )
            .is_ok()
        );
        assert!(
            validate_vendor_endpoint(
                &uri("https://generativelanguage.googleapis.com"),
                RuntimeMode::Production
            )
            .is_ok()
        );
    }

    // 注: MissingHost 分支是 defensive — RoutePlan.vendor_endpoint 来自 account_planner
    // planned_attempt 已先做过 scheme+authority validation (account_planner.rs:298),
    // 所以正常流程到达 validate_vendor_endpoint 时必有 host。本层不再写人工 missing-host
    // fixture (http::Uri 解析也很难造出 scheme 存在但 host 缺失的 URI 串)。
    //
    // 注: hostname 解析后指向私网 (DNS rebinding, 例如 attacker.example.com → 192.168.x.x)
    // 不能在本 pre-connect 层挡 — 那需要 HTTP connector 在 resolve 后再次检查 IP。
    // 列为 L4+ follow-up, 不在 W11-C scope。
}
