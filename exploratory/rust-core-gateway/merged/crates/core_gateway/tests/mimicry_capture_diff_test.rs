mod common;

use common::{
    capture_diff::{FieldStatus, ListFieldStatus, diff_capture_against_profile, diff_has_mismatch},
    tls_capture::CapturedClientHello,
};
use core_gateway::mimicry::{
    BackendIntent, BuiltinProfile, FingerprintProfile, ProfileMatchPolicy, load_builtin_profile,
};

#[test]
fn codex_known_gap_profile_is_blocked_but_diff_still_completes() {
    let captured = fixture_capture();
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let diff = diff_capture_against_profile(&captured, &profile);

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::KnownGapBlocked);
    assert!(
        diff.profile_blocked,
        "KnownGapBlocked profile 必须标记 blocked"
    );
    assert!(
        diff_has_mismatch(&diff),
        "codex fixture 与模板存在已知差异，diff 应能完整表达 mismatch"
    );
    assert!(
        matches!(diff.cipher_suites, ListFieldStatus::OrderMismatch { .. }),
        "KnownGapBlocked 仍按稳定顺序输出字段级 diff"
    );
}

#[test]
fn kiro_sample_set_profile_reports_set_match_and_set_mismatch() {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");
    assert_eq!(
        profile.match_policy(),
        ProfileMatchPolicy::SampleSetRandomized
    );

    let matching_diff = diff_capture_against_profile(&fixture_capture(), &profile);
    assert!(
        !matching_diff.profile_blocked,
        "kiro rustls profile 不应 blocked"
    );
    assert!(
        matches!(matching_diff.extensions, ListFieldStatus::SetMatch { .. }),
        "SampleSetRandomized 应忽略 extension 顺序，只核验集合"
    );
    assert!(
        matches!(
            matching_diff.signature_algorithms,
            ListFieldStatus::SetMatch { .. }
        ),
        "signature_algorithms 集合相同应为 SetMatch"
    );

    let mut mismatched = fixture_capture();
    mismatched.supported_groups = vec![29, 23, 24, 65000];
    let mismatch_diff = diff_capture_against_profile(&mismatched, &profile);

    match &mismatch_diff.supported_groups {
        ListFieldStatus::SetMismatch { extra, missing } => {
            assert_eq!(extra, &vec![65000]);
            assert_eq!(missing, &vec![4588]);
        }
        status => panic!("supported_groups 应输出 SetMismatch，实际: {status:?}"),
    }
    assert!(diff_has_mismatch(&mismatch_diff));
}

#[test]
fn exact_stable_profile_positive_verifies_every_field_status() {
    let profile = stable_kiro_profile();
    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);

    let mut captured = capture_matching_profile(&profile);
    captured.extensions = fixture_capture().extensions;
    captured.signature_algorithms = Vec::new();

    let diff = diff_capture_against_profile(&captured, &profile);

    assert!(!diff.profile_blocked);
    assert_eq!(
        diff.legacy_version,
        FieldStatus::Match { value: 772 },
        "JA3 第一段必须正向核验 legacy_version"
    );
    assert_eq!(
        diff.cipher_suites,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.cipher_suites.clone()
        }
    );
    assert!(
        matches!(diff.extensions, ListFieldStatus::OrderMismatch { .. }),
        "ExactStable 应把相同集合但不同顺序标记为 OrderMismatch"
    );
    assert_eq!(
        diff.supported_groups,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.supported_groups.clone()
        }
    );
    assert_eq!(
        diff.signature_algorithms,
        ListFieldStatus::OrderMismatch {
            expected: profile.tls.signature_algorithms.clone(),
            actual: Vec::new()
        },
        "列表缺失通过实际值为空的 OrderMismatch 表达"
    );
    assert_eq!(
        diff.ec_point_formats,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.ec_point_formats.clone()
        }
    );
    assert_eq!(
        diff.alpn_protocols,
        ListFieldStatus::OrderedMatch { value: Vec::new() }
    );
    assert!(diff_has_mismatch(&diff));
}

#[test]
fn unsupported_template_profile_is_blocked_but_diff_still_completes() {
    let mut raw = stable_kiro_profile_json();
    raw["tls_backend"] = serde_json::json!("unknown-backend");
    let profile = FingerprintProfile::from_json(&raw.to_string())
        .expect("合成 unsupported transport profile 应加载");
    let captured = capture_matching_profile(&profile);

    let diff = diff_capture_against_profile(&captured, &profile);

    assert!(
        matches!(
            profile.backend_intent(),
            BackendIntent::UnsupportedTemplate { .. }
        ),
        "unknown-backend 合成 profile 应进入 UnsupportedTemplate"
    );
    assert!(diff.profile_blocked);
    assert_eq!(diff.legacy_version, FieldStatus::Match { value: 772 });
    assert_eq!(
        diff.cipher_suites,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.cipher_suites.clone()
        }
    );
    assert_eq!(
        diff.extensions,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.extensions.clone()
        }
    );
    assert_eq!(
        diff.supported_groups,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.supported_groups.clone()
        }
    );
    assert_eq!(
        diff.signature_algorithms,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.signature_algorithms.clone()
        }
    );
    assert_eq!(
        diff.ec_point_formats,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.ec_point_formats.clone()
        }
    );
    assert_eq!(
        diff.alpn_protocols,
        ListFieldStatus::OrderedMatch {
            value: profile.tls.alpn_protocols.clone()
        }
    );
    assert!(
        !diff_has_mismatch(&diff),
        "UnsupportedTemplate blocking 不应妨碍字段级 diff 完整产出"
    );
}

#[test]
fn scalar_status_can_report_mismatch_and_mark_diff_mismatched() {
    let profile = stable_kiro_profile();
    let mut captured = capture_matching_profile(&profile);
    captured.legacy_version = 771;

    let diff = diff_capture_against_profile(&captured, &profile);

    assert_eq!(
        diff.legacy_version,
        FieldStatus::Mismatch {
            expected: 772,
            actual: 771
        }
    );
    assert!(diff_has_mismatch(&diff));
}

#[test]
fn scalar_status_can_report_not_in_template_and_not_captured() {
    let mut raw = stable_kiro_profile_json();
    raw["ja3"] = serde_json::json!("not-a-ja3-value");
    let profile_without_ja3_version = FingerprintProfile::from_json(&raw.to_string())
        .expect("缺少可解析 JA3 版本的合成 profile 仍应加载");
    let not_in_template =
        diff_capture_against_profile(&fixture_capture(), &profile_without_ja3_version);
    assert_eq!(
        not_in_template.legacy_version,
        FieldStatus::NotInTemplate { actual: 772 }
    );

    let profile = stable_kiro_profile();
    let mut missing_legacy = fixture_capture();
    missing_legacy.legacy_version = 0;
    let not_captured = diff_capture_against_profile(&missing_legacy, &profile);
    assert_eq!(
        not_captured.legacy_version,
        FieldStatus::NotCaptured { expected: 772 }
    );
}

fn fixture_capture() -> CapturedClientHello {
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

fn capture_matching_profile(profile: &FingerprintProfile) -> CapturedClientHello {
    let mut captured = fixture_capture();
    captured.cipher_suites = profile.tls.cipher_suites.clone();
    captured.extensions = profile.tls.extensions.clone();
    captured.supported_groups = profile.tls.supported_groups.clone();
    captured.signature_algorithms = profile.tls.signature_algorithms.clone();
    captured.ec_point_formats = profile.tls.ec_point_formats.clone();
    captured.alpn_protocols = profile.tls.alpn_protocols.clone();
    captured
}

fn stable_kiro_profile() -> FingerprintProfile {
    let raw = stable_kiro_profile_json();
    FingerprintProfile::from_json(&raw.to_string()).expect("合成 stable kiro profile 应加载")
}

fn stable_kiro_profile_json() -> serde_json::Value {
    let mut raw = serde_json::from_str::<serde_json::Value>(BuiltinProfile::KiroCli.raw_json())
        .expect("kiro raw JSON 应可解析");
    let ja3_hash = raw["ja3_hash"]
        .as_str()
        .expect("ja3_hash 应存在")
        .to_owned();
    let ja4 = raw["ja4"].as_str().expect("ja4 应存在").to_owned();

    raw["extension_order"] = serde_json::json!("stable");
    raw["ja3_hash_samples"] = serde_json::json!([ja3_hash]);
    raw["ja4_samples"] = serde_json::json!([ja4]);
    raw
}
