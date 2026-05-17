use super::{BuiltinProfile, FingerprintProfile, load_builtin_profile};
use crate::mimicry::{
    client_hello_builder::{build_boring_connector, configure_boring_connection},
    ja3_wire::{ClientHelloLayout, is_grease},
    wire_capture_fixture::{
        ClientHelloFields, parse_client_hello, spawn_capture_duplex, try_spawn_capture_listener,
    },
};
use boring::ssl::SslConnector;
use tokio::io::{AsyncRead, AsyncWrite};

// byte-level wire 匹配 Anthropic profile
// (R-2-B-2-extend 注入 ECH/OCSP/SCT 后 PASS)。
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
            drive_client_hello(&profile, &connector, tcp).await;
            capture_handle.await.expect("capture task 不应 panic")
        }
        Err(error) if error.kind() == std::io::ErrorKind::PermissionDenied => {
            eprintln!("sandbox denied loopback bind; falling back to in-memory TLS record capture");
            let (stream, capture_handle) = spawn_capture_duplex();
            drive_client_hello(&profile, &connector, stream).await;
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

    assert_profile_extension_order(&observed_ext, &expected_ext);

    let observed_ja3 = ja3_from_fields(&fields, &profile);
    assert_eq!(
        observed_ja3, "de88744b20558d50f03a5f0ea176ee98",
        "wire JA3 必须跟 Anthropic profile sample 一致"
    );

    assert_eq!(fields.sni_hostname.as_deref(), Some("api.anthropic.com"));
}

async fn drive_client_hello<S>(profile: &FingerprintProfile, connector: &SslConnector, stream: S)
where
    S: AsyncRead + AsyncWrite + Unpin,
{
    let config = configure_boring_connection(connector, profile)
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

fn assert_profile_extension_order(observed: &[u16], expected: &[u16]) {
    if observed == expected {
        return;
    }

    // HUAKAI profile 同时记录了 JA4 15-ext 与 14-ext 样本；差异只在
    // padding(21)。boring 5.1 公开 API 没有 padding 强制 setter 或
    // custom extension 注入口，所以这里严格比较非 padding 顺序，并允许
    // boring 自动省略末尾 padding。ECH/OCSP/SCT 仍必须真实出现在 wire。
    let expected_without_padding = expected
        .iter()
        .copied()
        .filter(|value| *value != 21)
        .collect::<Vec<_>>();
    assert_eq!(
        observed, expected_without_padding,
        "Anthropic profile 非 padding extension 顺序必须 byte-level 一致"
    );
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
