use std::{
    collections::BTreeSet,
    fs,
    path::{Path, PathBuf},
};

use core_gateway::mimicry::profile::scan_template_for_secrets;
use core_gateway::mimicry::tls_profile::{ExtensionOrder, TlsBackend};
use core_gateway::mimicry::{
    BackendIntent, BuiltinProfile, FingerprintProfile, ProfileMatchPolicy, ProfileMode,
    ProfileVendor, load_builtin_profile,
};

#[test]
fn mimicry_profile_loads_builtin_real_templates() {
    for builtin in BuiltinProfile::ALL {
        let profile = load_builtin_profile(builtin).unwrap_or_else(|error| {
            panic!(
                "{} profile 应能反序列化并通过校验: {error}",
                builtin.template_name()
            )
        });

        assert_eq!(profile.mode_name, profile.mode.as_str());
        assert!(
            profile.sample_count > 0,
            "{} sample_count 必须来自真实样本",
            builtin.template_name()
        );
        assert!(
            !profile.tls.ja3.is_empty() && !profile.tls.ja4.is_empty(),
            "{} 必须是 real TLS template, 不能回退成 stub",
            builtin.template_name()
        );
        assert!(
            profile.auth_layer.authorization_header.contains('<'),
            "{} authorization_header 必须保留脱敏占位符",
            builtin.template_name()
        );
    }
}

#[test]
fn mimicry_profile_templates_have_zero_secret_scanner_hits() {
    for template_path in top_level_template_paths() {
        let template_name = template_file_name(&template_path);
        let raw_json = fs::read_to_string(&template_path)
            .unwrap_or_else(|error| panic!("{template_name} 应可读取: {error}"));
        let findings = scan_template_for_secrets(&raw_json)
            .unwrap_or_else(|error| panic!("{template_name} secret scan JSON parse 失败: {error}"));
        assert!(
            findings.is_empty(),
            "{} secret scanner 必须 0 命中，实际: {}",
            template_name,
            format_secret_findings(&findings)
        );
    }
}

#[test]
fn mimicry_top_level_production_templates_are_builtin_covered() {
    let builtin_templates = BuiltinProfile::ALL
        .into_iter()
        .map(|builtin| builtin.template_name())
        .collect::<BTreeSet<_>>();

    for template_path in top_level_template_paths() {
        let template_name = template_file_name(&template_path);
        assert!(
            builtin_templates.contains(template_name.as_str()),
            "生产模板 {template_name} 必须先 wire 到 BuiltinProfile::ALL；未 backfill 模板应放入 _pending-backfill/"
        );
    }
}

#[test]
fn mimicry_profile_secret_scanner_detects_lowercase_bearer_and_raw_token_fields() {
    let sample = r#"{
        "auth_layer": {
            "authorization_header": "authorization: bearer real-lowercase-secret-token-123456",
            "access_token": "ya29.real-access-token-value",
            "token": "plain-token-value-1234567890"
        }
    }"#;

    let findings = scan_template_for_secrets(sample).expect("测试 JSON 应可解析");
    assert!(
        findings
            .iter()
            .any(|finding| finding.pattern == "Bearer token"),
        "应检测 lowercase bearer 泄漏，实际: {}",
        format_secret_findings(&findings)
    );
    assert!(
        findings
            .iter()
            .filter(|finding| finding.pattern == "raw token field")
            .count()
            >= 2,
        "应检测 access_token/token 裸字段，实际: {}",
        format_secret_findings(&findings)
    );
}

#[test]
fn mimicry_profile_vendor_and_mode_mapping_is_explicit() {
    let codex = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");
    assert_eq!(codex.vendor, ProfileVendor::OpenAi);
    assert_eq!(codex.vendor.as_str(), "openai");
    assert_eq!(codex.mode, ProfileMode::CodexCli);
    assert_eq!(codex.tls.backend, TlsBackend::NativeTlsOpenSsl);
    assert_eq!(codex.tls.extension_order, ExtensionOrder::Stable);

    let kiro = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");
    assert_eq!(kiro.vendor, ProfileVendor::Kiro);
    assert_eq!(kiro.vendor.as_str(), "kiro");
    assert_eq!(kiro.mode, ProfileMode::KiroCli);
    assert_eq!(kiro.tls.backend, TlsBackend::Rustls);
    assert_eq!(kiro.tls.extension_order, ExtensionOrder::Randomized);

    let gemini =
        load_builtin_profile(BuiltinProfile::GeminiAdvanced).expect("gemini profile 应加载");
    assert_eq!(gemini.vendor, ProfileVendor::Gemini);
    assert_eq!(gemini.vendor.as_str(), "gemini");
    assert_eq!(gemini.mode, ProfileMode::GeminiAdvanced);
    assert_eq!(gemini.tls.backend, TlsBackend::NodeJs);
    assert_eq!(gemini.tls.variants.len(), 2);

    let anthropic = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("anthropic profile 应加载");
    assert_eq!(anthropic.vendor, ProfileVendor::Anthropic);
    assert_eq!(anthropic.vendor.as_str(), "anthropic");
    assert_eq!(anthropic.mode, ProfileMode::AnthropicClaudeCode);
    assert_eq!(anthropic.tls.backend, TlsBackend::NativeTlsOpenSsl);
    assert_eq!(anthropic.tls.extension_order, ExtensionOrder::Randomized);
    assert_eq!(anthropic.tls.ja3_hash, "de88744b20558d50f03a5f0ea176ee98");
}

#[test]
fn mimicry_profile_match_policy_follows_template_evidence() {
    let codex = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");
    assert_eq!(
        codex.match_policy(),
        ProfileMatchPolicy::KnownGapBlocked,
        "codex profile 有已知 TLS 字段差异，不能作为 capture pass"
    );

    let kiro = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");
    assert_eq!(
        kiro.match_policy(),
        ProfileMatchPolicy::SampleSetRandomized,
        "kiro extension_order=randomized，应使用样本集合策略"
    );

    let gemini =
        load_builtin_profile(BuiltinProfile::GeminiAdvanced).expect("gemini profile 应加载");
    assert_eq!(
        gemini.match_policy(),
        ProfileMatchPolicy::SampleSetRandomized,
        "gemini Node.js 模板含 2 个 TLS 变体，应使用样本集合策略"
    );

    let anthropic = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("anthropic profile 应加载");
    assert_eq!(
        anthropic.match_policy(),
        ProfileMatchPolicy::SampleSetRandomized,
        "anthropic Claude Code 模板含多 JA4 样本，应使用样本集合策略"
    );
}

#[test]
fn mimicry_profile_codex_known_gap_blocks_capture_pass_with_field_diffs() {
    let codex = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");
    let gaps = codex.known_gap_fields();
    let gap_report = gaps
        .iter()
        .map(|gap| gap.message())
        .collect::<Vec<_>>()
        .join(" | ");

    assert_eq!(
        codex.match_policy(),
        ProfileMatchPolicy::KnownGapBlocked,
        "codex 不能被当成 pass；字段级差异: {gap_report}"
    );
    assert!(
        !gaps.is_empty(),
        "codex KnownGapBlocked 必须携带字段级 diff，不能只返回 mismatch"
    );

    assert_gap_field(&gaps, "extensions", "capture diff");
    assert_gap_field(&gaps, "supported_groups", "4588");
    assert_gap_field(&gaps, "signature_algorithms", "26 template ids");

    assert!(
        codex.tls.extensions.contains(&22),
        "codex template extensions 必须包含 22；字段级差异: {gap_report}"
    );
    assert_eq!(
        codex.tls.supported_groups.first(),
        Some(&4588),
        "codex template supported_groups 首项必须是 4588；字段级差异: {gap_report}"
    );
    assert_eq!(
        codex.tls.signature_algorithms.len(),
        26,
        "codex template signature_algorithms 长度必须锁定为 26；字段级差异: {gap_report}"
    );
    assert_eq!(
        codex.tls.ec_point_formats,
        vec![0, 1, 2],
        "codex template ec_point_formats 必须锁定为 [0,1,2]；字段级差异: {gap_report}"
    );
}

#[test]
fn mimicry_backend_intent_blocks_codex_known_gap() {
    let codex = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    match codex.backend_intent() {
        BackendIntent::KnownGapBlocked { reason } => {
            assert!(
                !reason.trim().is_empty(),
                "KnownGapBlocked 必须携带 gap reason"
            );
            assert!(
                reason.contains("cipher_suites") && reason.contains("extensions"),
                "KnownGapBlocked reason 必须来自既有字段级 gap 描述，实际: {reason}"
            );
        }
        intent => panic!("codex KnownGapBlocked profile 不允许 dispatch，实际: {intent:?}"),
    }
}

#[test]
fn mimicry_backend_intent_blocks_kiro_rustls_after_burn_the_boats() {
    let kiro = load_builtin_profile(BuiltinProfile::KiroCli).expect("kiro profile 应加载");

    match kiro.backend_intent() {
        BackendIntent::UnsupportedTemplate { reason } => {
            assert!(reason.contains("tls_backend=rustls"));
            assert!(reason.contains("mimicry path"));
        }
        intent => panic!("kiro rustls backend 必须停在 UnsupportedTemplate，实际: {intent:?}"),
    }
}

#[test]
fn mimicry_backend_intent_rejects_gemini_unsupported_backend() {
    let gemini =
        load_builtin_profile(BuiltinProfile::GeminiAdvanced).expect("gemini profile 应加载");

    match gemini.backend_intent() {
        BackendIntent::UnsupportedTemplate { reason } => {
            assert!(
                reason.contains("nodejs"),
                "gemini 当前 nodejs TLS backend 不能静默 dispatch，实际: {reason}"
            );
        }
        intent => panic!("gemini unsupported backend 应停在 UnsupportedTemplate，实际: {intent:?}"),
    }
}

#[test]
fn mimicry_backend_intent_accepts_stable_native_tls_openssl() {
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
        .expect("合成 stable openssl profile 应加载");

    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
    assert_eq!(profile.backend_intent(), BackendIntent::OpenSslAdapter);
}

#[test]
fn mimicry_backend_intent_accepts_anthropic_openssl_profile() {
    let anthropic = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("anthropic profile 应加载");

    assert_eq!(anthropic.backend_intent(), BackendIntent::OpenSslAdapter);
}

fn assert_gap_field(
    gaps: &[core_gateway::mimicry::tls_profile::TlsFieldGap],
    field: &'static str,
    expected_fragment: &str,
) {
    let found = gaps.iter().any(|gap| {
        gap.field == field
            && (gap.template_value.contains(expected_fragment)
                || gap.reason.contains(expected_fragment))
    });
    assert!(
        found,
        "codex KnownGap 缺少字段 {field} / {expected_fragment}；实际字段级差异: {}",
        gaps.iter()
            .map(|gap| gap.message())
            .collect::<Vec<_>>()
            .join(" | ")
    );
}

fn format_secret_findings(findings: &[core_gateway::mimicry::profile::SecretFinding]) -> String {
    findings
        .iter()
        .map(|finding| {
            format!(
                "{} {} len={} hash={}",
                finding.path, finding.pattern, finding.match_len, finding.match_hash
            )
        })
        .collect::<Vec<_>>()
        .join(" | ")
}

fn top_level_template_paths() -> Vec<PathBuf> {
    let template_dir = Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../../../../tools/fingerprint-collector/templates");
    let mut paths = fs::read_dir(&template_dir)
        .unwrap_or_else(|error| panic!("template dir 应可读取 {:?}: {error}", template_dir))
        .map(|entry| entry.expect("template dir entry 应可读取").path())
        .filter(|path| {
            path.is_file()
                && path
                    .extension()
                    .and_then(|extension| extension.to_str())
                    .is_some_and(|extension| extension == "json")
        })
        .collect::<Vec<_>>();
    paths.sort();
    paths
}

fn template_file_name(path: &Path) -> String {
    path.file_name()
        .and_then(|name| name.to_str())
        .expect("template filename 应为 UTF-8")
        .to_owned()
}
