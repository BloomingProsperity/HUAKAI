use std::collections::BTreeMap;

use core_gateway::mimicry::{
    BuiltinProfile, DispatchDecision, FingerprintProfile, ProfileMatchPolicy, ProfileMode,
    ProfileVendor, decide_dispatch,
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

#[cfg(not(feature = "mimicry-openssl"))]
#[test]
fn dispatch_blocks_codex_profile_when_openssl_adapter_is_not_compiled() {
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let decision = decide_dispatch(&profile);

    match &decision {
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            assert!(
                reason.contains("mimicry-openssl"),
                "feature-off build 必须阻断 Codex OpenSSL dispatch，实际: {reason}"
            );
        }
        decision => {
            panic!("feature-off Codex profile 必须被 dispatch gate 拒绝，实际: {decision:?}")
        }
    }
    assert!(!is_dispatch_allowed(&decision));
}

#[test]
fn dispatch_blocks_kiro_rustls_profile_after_burn_the_boats() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    let decision = decide_dispatch(&profile);

    match &decision {
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            assert!(reason.contains("tls_backend=rustls"));
            assert!(reason.contains("mimicry path"));
        }
        decision => panic!("kiro rustls profile 必须被生产 dispatch 阻断，实际: {decision:?}"),
    }
    assert!(!is_dispatch_allowed(&decision));
}

#[test]
fn dispatch_blocks_gemini_unsupported_template_profile() {
    let profile =
        load_builtin_profile(BuiltinProfile::GeminiAdvanced).expect("gemini profile 应加载");

    let decision = decide_dispatch(&profile);

    match &decision {
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            assert!(
                reason.contains("nodejs"),
                "gemini nodejs backend 必须留在 unsupported block，实际: {reason}"
            );
        }
        decision => panic!("gemini builtin 必须被 UnsupportedTemplate 拒绝，实际: {decision:?}"),
    }
    assert!(!is_dispatch_allowed(&decision));
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

#[cfg(not(feature = "mimicry-openssl"))]
#[test]
fn dispatch_blocks_stable_native_tls_openssl_profile_when_adapter_is_not_compiled() {
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
    match &decision {
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            assert!(
                reason.contains("mimicry-openssl"),
                "feature-off build 必须阻断 OpenSSL dispatch，实际: {reason}"
            );
        }
        decision => panic!(
            "feature-off build 的 OpenSSL profile 必须被 dispatch gate 拒绝，实际: {decision:?}"
        ),
    }
    assert!(!is_dispatch_allowed(&decision));
}

#[test]
fn dispatch_blocks_native_tls_openssl_profile_without_encrypt_then_mac() {
    let profile = native_openssl_profile_without_ext_22();

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
    let decision = decide_dispatch(&profile);
    match &decision {
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            if cfg!(feature = "mimicry-openssl") {
                assert!(
                    reason.contains("encrypt_then_mac") && reason.contains("22"),
                    "OpenSSL dispatch 必须阻断无法禁用 extension 22 的 profile，实际: {reason}"
                );
            } else {
                assert!(
                    reason.contains("mimicry-openssl"),
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

#[test]
fn dispatch_blocks_native_tls_openssl_profile_with_non_native_ec_point_formats() {
    let mut raw = serde_json::from_str::<serde_json::Value>(BuiltinProfile::KiroCli.raw_json())
        .expect("kiro raw JSON 应可解析");
    let ja3_hash = raw["ja3_hash"]
        .as_str()
        .expect("ja3_hash 应存在")
        .to_owned();
    let ja4 = raw["ja4"].as_str().expect("ja4 应存在").to_owned();

    raw["tls_backend"] = serde_json::json!("native-tls/openssl");
    raw["extension_order"] = serde_json::json!("stable");
    raw["ja3_hash_samples"] = serde_json::json!([ja3_hash]);
    raw["ja4_samples"] = serde_json::json!([ja4]);

    let profile = FingerprintProfile::from_json(&raw.to_string())
        .expect("合成 unsupported openssl ec_point_formats profile 应加载");

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
    let decision = decide_dispatch(&profile);
    match &decision {
        DispatchDecision::BlockUnsupportedTemplate { reason } => {
            if cfg!(feature = "mimicry-openssl") {
                assert!(
                    reason.contains("ec_point_formats") && reason.contains("[0]"),
                    "OpenSSL dispatch 必须阻断 adapter 无法构造的 ec_point_formats，实际: {reason}"
                );
            } else {
                assert!(
                    reason.contains("mimicry-openssl"),
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
