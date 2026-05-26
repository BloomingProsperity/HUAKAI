mod common;

use common::{
    capture_diff::{ExtensionsListStatus, diff_capture_against_profile},
    tls_capture::CapturedClientHello,
};
use core_gateway::mimicry::{
    AvailableMimicryFeatures, BackendResolverError, BuiltinProfile, MimicryBackend,
    load_builtin_profile, resolve_mimicry_backend, resolve_profile_mimicry_backend,
};

#[test]
fn resolves_codex_to_openssl() {
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let backend = resolve_mimicry_backend("codex", &profile, all_features())
        .expect("codex profile 应解析到 OpenSSL backend");

    assert_eq!(backend, MimicryBackend::Openssl);
}

/// W11-F F-2.2 synthesis D-S3 (Owner-approved 2026-05-24): kiro_cli now
/// stays `KnownGapBlocked` until real-upstream capture verification against
/// `q.us-east-1.amazonaws.com` lands (F-2.5). Earlier the resolver returned
/// `Err(BackendResolverError::UnsupportedTemplate { tls_backend=rustls })`;
/// the new contract returns `Ok(MimicryBackend::KnownGapBlocked { reason })`
/// because real-upstream evidence outranks "template tls_backend is rustls"
/// per synthesis §4. Locking the new contract here so a future commit that
/// accidentally clears `kiro_cli_known_gap_fields()` before F-2.5 lands
/// turns this test red.
#[test]
fn blocks_kiro_rustls_template_after_burn_the_boats() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    let backend = resolve_mimicry_backend("kiro", &profile, all_features())
        .expect("kiro 现在走 KnownGapBlocked (Ok)，不再是 UnsupportedTemplate (Err)");

    match backend {
        MimicryBackend::KnownGapBlocked { reason } => {
            assert!(
                reason.contains("real_upstream_capture"),
                "kiro F-2.2 D-S3 KnownGap reason 必须来自 real_upstream_capture 缺失，\
                 实际: {reason}"
            );
        }
        other => panic!(
            "kiro F-2.2 D-S3 现在应走 KnownGapBlocked (real-upstream capture pending)，\
             实际: {other:?}"
        ),
    }

    let diff = diff_capture_against_profile(&kiro_fixture_capture(), &profile);
    assert!(
        matches!(
            diff.extensions,
            ExtensionsListStatus::Subset {
                unexpected,
                ..
            } if unexpected.is_empty()
        ),
        "kiro 模板字段 diff 仍应复用 ExtensionsListStatus::Subset 表达 extension diff"
    );
}

#[test]
fn resolves_anthropic_to_boring_after_r2_b5_binding() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("anthropic profile 应加载");

    let backend = resolve_profile_mimicry_backend(&profile, all_features())
        .expect("anthropic profile selector 应返回 Boring backend");

    assert_eq!(backend, MimicryBackend::Boring);
}

#[test]
fn rejects_rustls_template_with_openssl_only_feature() {
    let mut raw = serde_json::from_str::<serde_json::Value>(BuiltinProfile::KiroCli.raw_json())
        .expect("kiro raw JSON 应可解析");
    raw["extensions"] = serde_json::json!([13, 23, 35, 5, 11, 45, 0, 51, 43, 10, 22]);
    raw["ec_point_formats"] = serde_json::json!([0, 1, 2]);
    let profile = core_gateway::mimicry::FingerprintProfile::from_json(&raw.to_string())
        .expect("合成 rustls/OpenSSL mismatch profile 应加载");

    let error = resolve_mimicry_backend("kiro", &profile, all_features())
        .expect_err("rustls template 携带 OpenSSL-only 字段必须在 dispatch 前失败");

    match error {
        BackendResolverError::ProfileBackendMismatch { reason } => {
            assert!(
                reason.contains("tls_backend=rustls")
                    && reason.contains("encrypt_then_mac")
                    && reason.contains("ec_point_formats"),
                "ProfileBackendMismatch 必须指出 rustls/OpenSSL-only 字段冲突，实际: {reason}"
            );
        }
        error => panic!("应返回 ProfileBackendMismatch，实际: {error:?}"),
    }
}

fn all_features() -> AvailableMimicryFeatures {
    AvailableMimicryFeatures {
        openssl: true,
        boring: true,
    }
}

fn kiro_fixture_capture() -> CapturedClientHello {
    // W11-F F-2.2 D-S3: kiro's match_policy is now KnownGapBlocked (gap fields
    // non-empty), so `compare_extension_ordered_subset` is used instead of the
    // earlier `compare_extension_set`. The fixture's extension order must
    // match the kiro template's extension order, not the reverse.
    CapturedClientHello {
        legacy_version: 772,
        cipher_suites: vec![
            52392, 49199, 49200, 52393, 49195, 49196, 4867, 4865, 4866, 255,
        ],
        extensions: vec![10, 43, 51, 0, 45, 11, 5, 35, 23, 13],
        supported_groups: vec![24, 23, 29, 4588],
        signature_algorithms: vec![1025, 1281, 1537, 2052, 2053, 2054, 2055, 1539, 1027, 1283],
        ec_point_formats: vec![0],
        alpn_protocols: Vec::new(),
    }
}
