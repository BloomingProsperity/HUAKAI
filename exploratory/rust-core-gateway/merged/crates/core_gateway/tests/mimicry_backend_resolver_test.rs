mod common;

use common::{
    capture_diff::{ExtensionsListStatus, diff_capture_against_resolved_backend},
    tls_capture::CapturedClientHello,
};
use core_gateway::mimicry::{
    AvailableMimicryFeatures, BackendResolverError, BuiltinProfile, MimicryBackend,
    anthropic_known_gap_reason, load_builtin_profile, resolve_mimicry_backend,
};

#[test]
fn resolves_codex_to_openssl() {
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let backend = resolve_mimicry_backend("codex", &profile, all_features())
        .expect("codex profile 应解析到 OpenSSL backend");

    assert_eq!(backend, MimicryBackend::Openssl);
}

#[test]
fn resolves_kiro_to_rustls_and_keeps_capture_diff_extension_shape() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    let backend = resolve_mimicry_backend("kiro", &profile, all_features())
        .expect("kiro profile 应解析到 rustls backend");
    assert_eq!(backend, MimicryBackend::Rustls);

    let diff = diff_capture_against_resolved_backend(
        "kiro",
        &kiro_fixture_capture(),
        &profile,
        all_features(),
    )
    .expect("kiro rustls backend 应能进入同一 capture diff path");
    assert!(
        matches!(
            diff.extensions,
            ExtensionsListStatus::Subset {
                unexpected,
                ..
            } if unexpected.is_empty()
        ),
        "kiro rustls path 必须复用 ExtensionsListStatus::Subset 表达 extension diff"
    );
}

#[test]
fn resolves_anthropic_to_known_gap_blocked() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    let backend = resolve_mimicry_backend("anthropic", &profile, all_features())
        .expect("anthropic profile selector 应返回 KnownGapBlocked backend");

    assert_eq!(
        backend,
        MimicryBackend::KnownGapBlocked {
            reason: anthropic_known_gap_reason().to_owned()
        }
    );
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
        rustls: true,
    }
}

fn kiro_fixture_capture() -> CapturedClientHello {
    CapturedClientHello {
        legacy_version: 772,
        cipher_suites: vec![
            52392, 49199, 49200, 52393, 49195, 49196, 4867, 4865, 4866, 255,
        ],
        extensions: vec![13, 23, 35, 5, 11, 45, 0, 51, 43, 10],
        supported_groups: vec![24, 23, 29, 4588],
        signature_algorithms: vec![1025, 1281, 1537, 2052, 2053, 2054, 2055, 1539, 1027, 1283],
        ec_point_formats: vec![0],
        alpn_protocols: Vec::new(),
    }
}
