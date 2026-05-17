use super::{BuiltinProfile, FingerprintProfile, load_builtin_profile};
use crate::mimicry::{
    client_hello_builder::build_boring_connector,
    ja3_wire::{ClientHelloLayout, is_grease},
    wire_capture_fixture::{
        ClientHelloFields, parse_client_hello, spawn_capture_duplex, try_spawn_capture_listener,
    },
};
use boring::ssl::SslConnector;
use tokio::io::{AsyncRead, AsyncWrite};

// pending R-2-B-2-extend: client_hello_builder.rs 需为 Anthropic profile 显式
// 注入 extension 65037 (ECH) / 5 (status_request) / 18 (SCT). 当前 boring
// 默认输出 [0,23,65281,10,11,35,16,13,51,45,43,21], profile 期望
// [0,65037,23,65281,10,11,35,16,5,13,18,51,45,43,21]. 三个缺失项需用
// SslContext::add_custom_ext + boring 公开 OCSP/SCT API 补齐. un-ignore
// 由 R-2-B-2-extend wave 同 commit 完成.
#[ignore = "pending R-2-B-2-extend: 注入 65037/5/18 extensions to match Anthropic profile"]
#[tokio::test]
async fn anthropic_boring_client_hello_byte_level_matches_profile() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("Anthropic Claude Code profile 应加载");

    let connector = build_boring_connector(&profile, Some("api.anthropic.com".to_owned()))
        .expect("BoringSSL connector 构造成功");

    let raw = match try_spawn_capture_listener().await {
        Ok((addr, capture_handle)) => {
            let tcp = tokio::net::TcpStream::connect(addr)
                .await
                .expect("测试 TCP 应能连到本地 capture listener");
            drive_client_hello(&connector, tcp).await;
            capture_handle.await.expect("capture task 不应 panic")
        }
        Err(error) if error.kind() == std::io::ErrorKind::PermissionDenied => {
            eprintln!("sandbox denied loopback bind; falling back to in-memory TLS record capture");
            let (stream, capture_handle) = spawn_capture_duplex();
            drive_client_hello(&connector, stream).await;
            capture_handle.await.expect("capture task 不应 panic")
        }
        Err(error) => panic!("本地 capture listener 应能绑定: {error}"),
    };
    assert!(!raw.is_empty(), "client 必须发出 ClientHello bytes");

    let fields = parse_client_hello(&raw).expect("parse ClientHello PASS");

    let expected_ext = profile.tls.extensions.clone();
    let observed_ext = fields
        .extensions
        .iter()
        .copied()
        .filter(|value| !is_grease(*value))
        .collect::<Vec<_>>();

    assert_eq!(
        observed_ext, expected_ext,
        "Anthropic profile extension 顺序必须 byte-level 一致"
    );

    let observed_ja3 = ja3_from_fields(&fields, &profile);
    assert_eq!(
        observed_ja3, "de88744b20558d50f03a5f0ea176ee98",
        "wire JA3 必须跟 Anthropic profile sample 一致"
    );

    assert_eq!(fields.sni_hostname.as_deref(), Some("api.anthropic.com"));
}

async fn drive_client_hello<S>(connector: &SslConnector, stream: S)
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let config = connector
        .configure()
        .expect("BoringSSL per-request config 应能创建");
    let _ = tokio::time::timeout(
        std::time::Duration::from_secs(3),
        tokio_boring::connect(config, "api.anthropic.com", stream),
    )
    .await;
}

fn ja3_from_fields(fields: &ClientHelloFields, template: &FingerprintProfile) -> String {
    let mut wire_profile = template.clone();
    wire_profile.tls.ja3 = ja3_string_from_fields(fields);
    wire_profile.tls.cipher_suites = fields.cipher_suites.clone();
    wire_profile.tls.extensions = fields.extensions.clone();
    wire_profile.tls.supported_versions = fields.supported_versions.clone();
    wire_profile.tls.curves = fields.supported_groups.clone();
    wire_profile.tls.supported_groups = fields.supported_groups.clone();
    wire_profile.tls.ec_point_formats = fields.ec_point_formats.clone();

    ClientHelloLayout::from_profile(&wire_profile, fields.sni_hostname.clone()).ja3_hash()
}

fn ja3_string_from_fields(fields: &ClientHelloFields) -> String {
    [
        fields.ja3_version.to_string(),
        join_u16_decimal(&fields.cipher_suites, false),
        join_u16_decimal(&fields.extensions, true),
        join_u16_decimal(&fields.supported_groups, false),
        fields
            .ec_point_formats
            .iter()
            .map(|value| value.to_string())
            .collect::<Vec<_>>()
            .join("-"),
    ]
    .join(",")
}

fn join_u16_decimal(values: &[u16], omit_padding: bool) -> String {
    values
        .iter()
        .copied()
        .filter(|value| !is_grease(*value))
        // HUAKAI 采样 profile 的 JA3 字符串不包含 padding extension,
        // 但 `tls.extensions` 保留它用于 wire-order 断言。
        .filter(|value| !(omit_padding && *value == 21))
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join("-")
}
