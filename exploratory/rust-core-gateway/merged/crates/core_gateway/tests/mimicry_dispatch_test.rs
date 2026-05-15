use core_gateway::mimicry::{
    BuiltinProfile, DispatchDecision, FingerprintProfile, ProfileMatchPolicy, decide_dispatch,
    is_dispatch_allowed, load_builtin_profile,
};

#[test]
fn dispatch_blocks_codex_known_gap_profile() {
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let decision = decide_dispatch(&profile);

    match &decision {
        DispatchDecision::BlockKnownGap { reason } => {
            assert!(
                !reason.trim().is_empty(),
                "KnownGapBlocked dispatch 必须携带非空 reason"
            );
            assert!(
                reason.contains("cipher_suites") && reason.contains("extensions"),
                "KnownGapBlocked reason 必须来自字段级 gap，实际: {reason}"
            );
        }
        decision => panic!("codex builtin 必须被生产 dispatch 拒绝，实际: {decision:?}"),
    }
    assert!(!is_dispatch_allowed(&decision));
}

#[test]
fn dispatch_allows_kiro_rustls_profile() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    let decision = decide_dispatch(&profile);

    assert_eq!(decision, DispatchDecision::AllowRustls);
    assert!(is_dispatch_allowed(&decision));
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
