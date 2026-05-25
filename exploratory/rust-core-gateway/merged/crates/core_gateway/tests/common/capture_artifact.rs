// R-D test-only artifact writer.
// 只写入本地 capture 的 raw 字段和可复核派生字段；不把未实现的 JA3 hash/JA4 伪装成真值。

use std::{
    env, fs, io,
    path::{Path, PathBuf},
};

use serde_json::{Value, json};

use super::tls_capture::CapturedClientHello;

pub fn write_tls_clienthello_artifact(
    name: &str,
    capture: &CapturedClientHello,
) -> io::Result<PathBuf> {
    let value = json!({
        "artifact_version": 1,
        "phase": "R-D",
        "capture_kind": "local_tls_clienthello",
        "feature_combo": feature_combo(),
        "name": name,
        "clienthello": clienthello_json(capture),
        "ja3": {
            "normalized_input_no_grease": ja3_input_no_grease(capture),
            "hash": null,
            "hash_status": "not_computed_in_rust_ci_without_new_dependency"
        },
        "ja4": {
            "status": "not_computed_in_this_atom",
            "raw_fields_available": true
        },
        "notes": [
            "local capture closes after first ClientHello and does not complete TLS",
            "GREASE values stay in raw ClientHello fields but are excluded from JA3 normalized input",
            "Owner real-upstream validation remains required before Released-spec gate"
        ]
    });

    write_json_artifact(&format!("{name}-tls-clienthello.json"), &value)
}

pub fn write_json_artifact(filename: &str, value: &Value) -> io::Result<PathBuf> {
    let dir = artifact_dir().join(feature_combo());
    fs::create_dir_all(&dir)?;
    let path = dir.join(filename);
    let mut body = serde_json::to_string_pretty(value).map_err(io::Error::other)?;
    body.push('\n');
    fs::write(&path, body)?;
    Ok(path)
}

pub fn artifact_dir() -> PathBuf {
    if let Some(dir) = non_empty_env("HUAKAI_R3RD_ARTIFACT_DIR") {
        return PathBuf::from(dir);
    }
    if let Some(target_dir) = non_empty_env("CARGO_TARGET_DIR") {
        return Path::new(&target_dir).join("huakai-r3rd-capture-artifacts");
    }
    PathBuf::from("target/huakai-r3rd-capture-artifacts")
}

fn clienthello_json(capture: &CapturedClientHello) -> Value {
    json!({
        "legacy_version": capture.legacy_version,
        "cipher_suites": &capture.cipher_suites,
        "extensions": &capture.extensions,
        "supported_groups": &capture.supported_groups,
        "signature_algorithms": &capture.signature_algorithms,
        "ec_point_formats": &capture.ec_point_formats,
        "alpn_protocols": &capture.alpn_protocols,
    })
}

fn ja3_input_no_grease(capture: &CapturedClientHello) -> String {
    format!(
        "{},{},{},{},{}",
        capture.legacy_version,
        join_u16_without_grease(&capture.cipher_suites),
        join_u16_without_grease(&capture.extensions),
        join_u16_without_grease(&capture.supported_groups),
        join_u8_without_grease(&capture.ec_point_formats),
    )
}

fn join_u16_without_grease(values: &[u16]) -> String {
    values
        .iter()
        .copied()
        .filter(|value| !is_grease_u16(*value))
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join("-")
}

fn join_u8_without_grease(values: &[u8]) -> String {
    values
        .iter()
        .copied()
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join("-")
}

fn is_grease_u16(value: u16) -> bool {
    let [high, low] = value.to_be_bytes();
    high == low && high & 0x0f == 0x0a
}

fn feature_combo() -> &'static str {
    match (
        cfg!(feature = "mimicry-openssl"),
        cfg!(feature = "mimicry-http2-fork"),
    ) {
        (false, false) => "default",
        (true, false) => "mimicry-openssl",
        (false, true) => "mimicry-http2-fork",
        (true, true) => "mimicry-openssl+mimicry-http2-fork",
    }
}

fn non_empty_env(name: &str) -> Option<String> {
    env::var(name)
        .ok()
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
}
