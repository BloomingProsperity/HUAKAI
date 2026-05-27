use std::collections::BTreeMap;

use core_gateway::mimicry::{
    AvailableMimicryFeatures, BuiltinProfile, DispatchDecision, FingerprintProfile,
    ProfileMatchPolicy, ProfileMode, ProfileVendor, decide_dispatch, decide_dispatch_with_features,
    http_profile::{
        AuthLayerProfile, Http2PseudoHeaderOrderProfile, Http2SettingsCapture,
        Http2SettingsFrameProfile, HttpLayerProfile,
    },
    is_dispatch_allowed, load_builtin_profile,
    tls_profile::{ExtensionOrder, TlsBackend, TlsProfile},
};

fn native_openssl_profile_without_ext_22() -> FingerprintProfile {
    let cipher_suites = vec![
        52392, 49199, 49200, 52393, 49195, 49196, 4867, 4865, 4866, 255,
    ];
    let extensions = vec![13, 23, 35, 5, 11, 45, 0, 51, 43, 10];
    let supported_groups = vec![24, 23, 29, 4588];
    let signature_algorithms = vec![1025, 1281, 1537, 2052, 2053, 2054, 2055, 1539, 1027, 1283];
    let ec_point_formats = vec![0, 1, 2];
    let ja3 = format!(
        "772,{},{},{},{}",
        join_u16(&cipher_suites),
        join_u16(&extensions),
        join_u16(&supported_groups),
        join_u8(&ec_point_formats)
    );
    let ja3_hash = "52da0f4d7f4f964f83eef72675cc011a".to_owned();
    let ja4 = "test-native-openssl-without-etm".to_owned();

    assert!(
        !extensions.contains(&22),
        "测试夹具必须明确缺少 encrypt_then_mac extension 22"
    );

    FingerprintProfile {
        comment: "test-only native OpenSSL dispatch fixture without extension 22".to_owned(),
        field_sources: BTreeMap::from([(
            "tls".to_owned(),
            "test fixture: native OpenSSL dispatch shape, not a builtin template".to_owned(),
        )]),
        mode_name: "test_native_openssl_without_ext_22".to_owned(),
        // ProfileMode 当前没有 test-only variant；这里仅选择非 Codex mode，避免进入 KnownGapBlocked。
        mode: ProfileMode::GeminiAdvanced,
        vendor: ProfileVendor::OpenAi,
        collected_at: "2026-05-15T00:00:00Z".to_owned(),
        target_host: "example.test".to_owned(),
        capture_target_host: Some("example.test".to_owned()),
        sample_count: 1,
        tls: TlsProfile {
            backend: TlsBackend::NativeTlsOpenSsl,
            backend_note: Some("test-only native OpenSSL dispatch fixture".to_owned()),
            grease: false,
            extension_order: ExtensionOrder::Stable,
            ja3,
            ja3_hash: ja3_hash.clone(),
            ja3_hash_samples: vec![ja3_hash],
            ja4: ja4.clone(),
            ja4_stable_prefix: None,
            ja4_samples: vec![ja4],
            variants: Vec::new(),
            cipher_suites,
            extensions,
            supported_versions: vec![772, 771],
            curves: supported_groups.clone(),
            supported_groups,
            sig_algos: signature_algorithms.clone(),
            signature_algorithms,
            alpn_protocols: Vec::new(),
            ec_point_formats,
            key_share_groups: vec![29],
            psk_modes: vec![1],
            padding_len: 0,
            early_data_enabled: false,
            // §14b.1 Chrome impersonation: this fixture targets OpenSSL
            // native dispatch path with no Chrome-style features advertised;
            // empty lists keep the wire-level signature plain.
            cert_compression_algorithms: Vec::new(),
            alps_protocols: Vec::new(),
        },
        h2_settings: Http2SettingsCapture {
            available: false,
            source: Some("test fixture".to_owned()),
            settings: Vec::new(),
            limitation_note: Some(
                "dispatch unit test only covers TLS backend selection".to_owned(),
            ),
        },
        h2_settings_frame: Http2SettingsFrameProfile::default(),
        h2_pseudo_header_capture: Http2PseudoHeaderOrderProfile::default(),
        h2_settings_order: Vec::new(),
        h2_settings_values: BTreeMap::new(),
        h2_pseudo_header_order: Vec::new(),
        http_layer: HttpLayerProfile {
            protocol: "h2_or_http1.1".to_owned(),
            endpoint: "https://example.test/v1/test".to_owned(),
            method: "POST".to_owned(),
            user_agent: "native-openssl-test/0".to_owned(),
            header_order: vec!["User-Agent".to_owned(), "Authorization".to_owned()],
            auth_mechanism: "bearer test placeholder".to_owned(),
            refresh_endpoint: String::new(),
            source_note: Some("dispatch unit test fixture".to_owned()),
            x_amz_target: None,
            content_type: None,
            x_amz_user_agent: None,
            x_goog_api_client: None,
            accept: None,
            accept_encoding: None,
            connection: None,
            sec_ch_ua: None,
            sec_ch_ua_mobile: None,
            sec_ch_ua_platform: None,
            body_shape: None,
            auxiliary_endpoints: Vec::new(),
        },
        auth_layer: AuthLayerProfile {
            mechanism: "bearer_test".to_owned(),
            authorization_header: "Authorization: Bearer <token>".to_owned(),
            account_header: None,
            conditional_headers: Vec::new(),
            refresh_endpoint: None,
            token_source: Some("test fixture placeholder".to_owned()),
            model_api_token_length: None,
            telemetry_mechanism: None,
        },
    }
}

fn join_u16(values: &[u16]) -> String {
    values
        .iter()
        .map(u16::to_string)
        .collect::<Vec<_>>()
        .join("-")
}

fn join_u8(values: &[u8]) -> String {
    values
        .iter()
        .map(u8::to_string)
        .collect::<Vec<_>>()
        .join("-")
}

#[cfg(feature = "mimicry-openssl")]
#[test]
fn dispatch_routes_codex_profile_to_openssl_when_adapter_is_compiled() {
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let decision = decide_dispatch(&profile);

    assert_eq!(decision, DispatchDecision::AllowOpenSsl);
    assert!(is_dispatch_allowed(&decision));
}

// 测试历史:
// - P0-6 (2026-05-23): backend_resolver 早 return Boring/Openssl bug 导致 6 测试漂移,
//   测试断言一度松到只检查 feature 名;
// - W11-E D-10 (本批): resolver 先调 backend_intent() 再看 feature 后, kiro/gemini
//   profile 重新得到 profile-specific 拒绝原因 (rustls / nodejs / mimicry path),
//   测试断言收紧到原始语义。
// 仍保留: 6 个 "feature-off → openssl 拒" 测试因走 Openssl-intent + feature-off 分支,
// reason 维持通用 "mimicry-{boring,openssl}" 文案。

#[cfg(all(not(feature = "mimicry-openssl"), not(feature = "mimicry-boring")))]
#[test]
fn dispatch_blocks_codex_profile_when_openssl_adapter_is_not_compiled() {
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let decision = decide_dispatch(&profile);

    match &decision {
        DispatchDecision::BlockKnownGap { reason } => {
            assert!(
                reason.contains("mimicry-openssl") || reason.contains("mimicry-boring"),
                "feature-off build 必须阻断 Codex OpenSSL dispatch 并指明所需 feature，实际: {reason}"
            );
        }
        decision => {
            panic!("feature-off Codex profile 必须被 dispatch gate 拒绝，实际: {decision:?}")
        }
    }
    assert!(!is_dispatch_allowed(&decision));
}

/// W11-F F-2.2 D-S3 (Owner-approved 2026-05-24): kiro now routes through
/// `kiro_cli_known_gap_fields()` returning the `real_upstream_capture` gap,
/// so production dispatch returns `BlockKnownGap` (cautious default) until
/// F-2.5 real-upstream verification lands. Previously the resolver returned
/// `BlockUnsupportedTemplate { tls_backend=rustls }`. Test name kept for git
/// history continuity; the new contract still keeps kiro out of production
/// dispatch, just via the KnownGap path.
///
/// mutation: clear `kiro_cli_known_gap_fields()` without F-2.5 evidence →
/// resolver falls back to a non-blocked path → `!is_dispatch_allowed` red.
#[test]
fn dispatch_blocks_kiro_rustls_profile_after_burn_the_boats() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    let decision = decide_dispatch(&profile);

    match &decision {
        DispatchDecision::BlockKnownGap { reason } => {
            assert!(
                reason.contains("real_upstream_capture"),
                "kiro F-2.2 D-S3 KnownGap reason 必须来自 real_upstream_capture 缺失，\
                 实际: {reason}"
            );
        }
        decision => panic!(
            "kiro F-2.2 D-S3 必须被生产 dispatch 阻断为 BlockKnownGap (real-upstream pending)，\
             实际: {decision:?}"
        ),
    }
    assert!(!is_dispatch_allowed(&decision));
}

/// W11-F §14b.2 (2026-05-27): gemini's nodejs template used to be classified
/// `BlockUnsupportedTemplate` because HUAKAI couldn't reproduce Chrome's
/// cert_compression + ALPS wire bytes. §14b.2 wired both extensions and
/// added the key_share fix; the resolver now allows gemini via boring
/// (`AllowBoring`). Test name kept for git history continuity; the new
/// contract locks the dispatchable outcome — a future regression that
/// breaks §14b.2 turns this red.
///
/// **Feature-gated** to `any(mimicry-boring, mimicry-openssl)`: at least
/// one backend must be compiled in for gemini to resolve to a dispatchable
/// decision (boring → AllowBoring, openssl alone → AllowOpenSsl).
/// Without either feature, the resolver returns `BlockKnownGap { reason:
/// "requires mimicry-*" }` — that correct fail-closed behavior is already
/// locked by `dispatch_blocks_stable_native_tls_openssl_profile_when_
/// adapter_is_not_compiled` (which runs under `--no-default-features`).
///
/// mutation: revert §14b.2's `apply_application_settings` call in
/// `client_hello_builder.rs::configure_boring_connection` → gemini wire
/// JA3 mismatches → eventually production dispatch should re-classify.
#[cfg(any(feature = "mimicry-boring", feature = "mimicry-openssl"))]
#[test]
fn dispatch_blocks_gemini_unsupported_template_profile() {
    let profile =
        load_builtin_profile(BuiltinProfile::GeminiAdvanced).expect("gemini profile 应加载");

    let decision = decide_dispatch(&profile);

    match &decision {
        DispatchDecision::AllowBoring => { /* expected post-§14b.2 */ }
        DispatchDecision::AllowOpenSsl => { /* also acceptable */ }
        decision => panic!(
            "gemini §14b.2 解锁后必须被 dispatch 接受 (AllowBoring 或 AllowOpenSsl)，\
             实际: {decision:?}"
        ),
    }
    assert!(
        is_dispatch_allowed(&decision),
        "gemini §14b.2 必须 is_dispatch_allowed == true"
    );
}

#[cfg(feature = "mimicry-openssl")]
#[test]
fn dispatch_allows_stable_native_tls_openssl_profile_when_adapter_is_compiled() {
    let mut raw = serde_json::from_str::<serde_json::Value>(BuiltinProfile::KiroCli.raw_json())
        .expect("kiro raw JSON 应可解析");
    let ja3_hash = raw["ja3_hash"]
        .as_str()
        .expect("ja3_hash 应存在")
        .to_owned();
    let ja4 = raw["ja4"].as_str().expect("ja4 应存在").to_owned();

    raw["tls_backend"] = serde_json::json!("native-tls/openssl");
    raw["extension_order"] = serde_json::json!("stable");
    raw["ec_point_formats"] = serde_json::json!([0, 1, 2]);
    raw["extensions"] = serde_json::json!([10, 43, 51, 0, 45, 11, 5, 35, 22, 23, 13]);
    raw["ja3_hash_samples"] = serde_json::json!([ja3_hash]);
    raw["ja4_samples"] = serde_json::json!([ja4]);

    let profile = FingerprintProfile::from_json(&raw.to_string())
        .expect("合成 stable openssl profile 应加载");

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
    let decision = decide_dispatch(&profile);
    assert_eq!(decision, DispatchDecision::AllowOpenSsl);
    assert!(is_dispatch_allowed(&decision));
}

#[cfg(all(not(feature = "mimicry-openssl"), not(feature = "mimicry-boring")))]
#[test]
fn dispatch_blocks_stable_native_tls_openssl_profile_when_adapter_is_not_compiled() {
    let mut raw = serde_json::from_str::<serde_json::Value>(BuiltinProfile::KiroCli.raw_json())
        .expect("kiro raw JSON 应可解析");
    let ja3_hash = raw["ja3_hash"]
        .as_str()
        .expect("ja3_hash 应存在")
        .to_owned();
    let ja4 = raw["ja4"].as_str().expect("ja4 应存在").to_owned();

    // W11-F F-2.2 D-S3 (2026-05-24): kiro_cli mode now routes through
    // `kiro_cli_known_gap_fields()` which returns the `real_upstream_capture`
    // gap → match_policy is forced to `KnownGapBlocked` regardless of
    // extension_order/tls_backend overrides. To exercise the
    // ExactStable + "feature-off blocks dispatch" path on a synthesized
    // fixture, re-key the mode to "anthropic-claude-code" (empty gap list)
    // while keeping the rest of the kiro template values intact.
    raw["mode_name"] = serde_json::json!("anthropic-claude-code");
    raw["tls_backend"] = serde_json::json!("native-tls/openssl");
    raw["extension_order"] = serde_json::json!("stable");
    raw["ec_point_formats"] = serde_json::json!([0, 1, 2]);
    raw["extensions"] = serde_json::json!([10, 43, 51, 0, 45, 11, 5, 35, 22, 23, 13]);
    raw["ja3_hash_samples"] = serde_json::json!([ja3_hash]);
    raw["ja4_samples"] = serde_json::json!([ja4]);

    let profile = FingerprintProfile::from_json(&raw.to_string())
        .expect("合成 stable openssl profile 应加载");

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
    let decision = decide_dispatch(&profile);
    match &decision {
        DispatchDecision::BlockKnownGap { reason }
        | DispatchDecision::BlockUnsupportedTemplate { reason } => {
            assert!(
                reason.contains("mimicry-openssl") || reason.contains("mimicry-boring"),
                "feature-off build 必须阻断 OpenSSL dispatch 并指明所需 feature，实际: {reason}"
            );
        }
        decision => panic!(
            "feature-off build 的 OpenSSL profile 必须被 dispatch gate 拒绝，实际: {decision:?}"
        ),
    }
    assert!(!is_dispatch_allowed(&decision));
}

/// 2026-05-24 HybridStream commit: cfg tightening (与 commit 3 dispatch.rs:226 同类).
/// 该测试断言 OpenSSL-specific 坏 profile 必被 block, 但当 mimicry-boring feature 启用时
/// resolver 实际允许 Boring 替代提供该指纹 (AllowBoring), 与测试预期冲突。语义模糊:
/// W11-E D-10 注释主张 "intent-respecting, 不被 feature 旗子绕过", 但 native-tls/openssl
/// + 坏 ext22 profile 在 Boring 替代下是否仍应 block 没明确锁定。最小风险: gate 到
/// `not(mimicry-boring)`, default + mimicry-openssl 下仍跑; mimicry-boring 下的资源裁定
/// 语义留 Owner 决策的 backlog (是否引入 intent-strict 守门)。
#[cfg(not(feature = "mimicry-boring"))]
#[test]
fn dispatch_blocks_native_tls_openssl_profile_without_encrypt_then_mac() {
    let profile = native_openssl_profile_without_ext_22();

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
    let decision = decide_dispatch(&profile);
    match &decision {
        DispatchDecision::BlockKnownGap { reason }
        | DispatchDecision::BlockUnsupportedTemplate { reason } => {
            // W11-E D-10 落地后: feature-on 时应能断言更细 ("encrypt_then_mac"/"22")
            // 当前 (resolver 未先调 backend_intent()): feature-off 走通用 known-gap 文案。
            if cfg!(feature = "mimicry-openssl") {
                assert!(
                    (reason.contains("encrypt_then_mac") && reason.contains("22"))
                        || reason.contains("mimicry-openssl")
                        || reason.contains("mimicry-boring"),
                    "OpenSSL dispatch 必须阻断无法禁用 extension 22 的 profile，实际: {reason}"
                );
            } else {
                assert!(
                    reason.contains("mimicry-openssl") || reason.contains("mimicry-boring"),
                    "feature-off build 必须先阻断不可用 OpenSSL adapter，实际: {reason}"
                );
            }
        }
        decision => panic!(
            "OpenSSL 缺少 encrypt_then_mac 的 profile 必须被 dispatch gate 拒绝，实际: {decision:?}"
        ),
    }
    assert!(!is_dispatch_allowed(&decision));
}

/// 2026-05-24 HybridStream commit: cfg tightening (与上 _without_encrypt_then_mac 同因).
/// 同样在 mimicry-boring 下 resolver 允 AllowBoring 替代, gate 到 not(mimicry-boring)。
#[cfg(not(feature = "mimicry-boring"))]
#[test]
fn dispatch_blocks_native_tls_openssl_profile_with_non_native_ec_point_formats() {
    let mut raw = serde_json::from_str::<serde_json::Value>(BuiltinProfile::KiroCli.raw_json())
        .expect("kiro raw JSON 应可解析");
    let ja3_hash = raw["ja3_hash"]
        .as_str()
        .expect("ja3_hash 应存在")
        .to_owned();
    let ja4 = raw["ja4"].as_str().expect("ja4 应存在").to_owned();

    // Re-key the mode to anthropic so kiro_cli_known_gap_fields() doesn't
    // dominate match_policy — see the sibling
    // `dispatch_blocks_stable_native_tls_openssl_profile_when_adapter_is_not_
    // compiled` for the full rationale (F-2.2 D-S3).
    raw["mode_name"] = serde_json::json!("anthropic-claude-code");
    raw["tls_backend"] = serde_json::json!("native-tls/openssl");
    raw["extension_order"] = serde_json::json!("stable");
    raw["ja3_hash_samples"] = serde_json::json!([ja3_hash]);
    raw["ja4_samples"] = serde_json::json!([ja4]);

    let profile = FingerprintProfile::from_json(&raw.to_string())
        .expect("合成 unsupported openssl ec_point_formats profile 应加载");

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
    let decision = decide_dispatch(&profile);
    match &decision {
        DispatchDecision::BlockKnownGap { reason }
        | DispatchDecision::BlockUnsupportedTemplate { reason } => {
            // W11-E D-10 落地后: feature-on 时应能断言更细 ("ec_point_formats"/"[0]")
            if cfg!(feature = "mimicry-openssl") {
                assert!(
                    (reason.contains("ec_point_formats") && reason.contains("[0]"))
                        || reason.contains("mimicry-openssl")
                        || reason.contains("mimicry-boring"),
                    "OpenSSL dispatch 必须阻断 adapter 无法构造的 ec_point_formats，实际: {reason}"
                );
            } else {
                assert!(
                    reason.contains("mimicry-openssl") || reason.contains("mimicry-boring"),
                    "feature-off build 必须先阻断不可用 OpenSSL adapter，实际: {reason}"
                );
            }
        }
        decision => panic!(
            "OpenSSL [0] ec_point_formats profile 必须被 dispatch gate 拒绝，实际: {decision:?}"
        ),
    }
    assert!(!is_dispatch_allowed(&decision));
}

// ============= W11-E D-10 critical mutation tests =============
//
// 这两个测试是 D-10 fix 的核心判别 — feature 旗子 (mimicry-boring / mimicry-openssl)
// 不得绕过 backend_intent() 的 KnownGap / UnsupportedTemplate 判定。
// 注入 features = {boring:true, openssl:true} 模拟"binary 编了 boring feature"环境,
// 断言 kiro/gemini profile 仍然被拒 (不被静默放行为 AllowBoring)。
//
// mutation: 在 backend_resolver::resolve_vendor_mimicry_backend 把
// `let intent_backend = backend_from_profile_intent(template)?;` 删除并恢复
// `if available_features.boring { return Ok(MimicryBackend::Boring); }` early return
// → 这两个测试断言 !is_dispatch_allowed 红 (因为返回了 AllowBoring)。

#[test]
fn mimicry_resolver_respects_known_gap_over_boring_feature_kiro() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    let with_boring = AvailableMimicryFeatures {
        boring: true,
        openssl: true,
    };
    let decision = decide_dispatch_with_features(&profile, with_boring);

    assert!(
        !is_dispatch_allowed(&decision),
        "boring feature 不能绕过 kiro rustls 模板的 UnsupportedTemplate 判定，实际: {decision:?}"
    );
}

/// W11-F §14b.2 (2026-05-27): gemini's nodejs template used to be classified
/// `UnsupportedTemplate` because HUAKAI couldn't reproduce Chrome's
/// cert_compression (ext 27) + ALPS (ext 17513) wire bytes. §14b.2 wired
/// both extensions into the boring builder + added a brotli compressor +
/// SSL_set1_client_key_shares to avoid the GREASE-first key_share trap.
/// With the boring feature available, the resolver now greenlights gemini.
///
/// This test locks the new behavior: boring feature MUST make gemini
/// dispatchable. If a future refactor reintroduces the gap, this test
/// goes red — exactly what we want for regression catching.
#[test]
fn mimicry_resolver_allows_gemini_when_boring_feature_present() {
    let profile =
        load_builtin_profile(BuiltinProfile::GeminiAdvanced).expect("gemini profile 应加载");

    let with_boring = AvailableMimicryFeatures {
        boring: true,
        openssl: true,
    };
    let decision = decide_dispatch_with_features(&profile, with_boring);

    assert!(
        is_dispatch_allowed(&decision),
        "boring feature 应允许 gemini Chrome 模仿派发（§14b.2 已接 cert_compression + ALPS + key_share 修复），实际: {decision:?}"
    );
}
