#![cfg(feature = "mimicry-openssl")]

// L2-A4 smoke: 只验证 OpenSSL client adapter 能完成本地 TLS 握手。

mod common;

use std::{
    net::{SocketAddr, TcpListener, TcpStream as StdTcpStream},
    thread,
    time::{Duration, Instant},
};

use common::{
    capture_diff::{CaptureDiff, ListFieldStatus, diff_capture_against_profile},
    tls_capture::spawn_capture_once,
};
use core_gateway::mimicry::{
    BuiltinProfile, FingerprintProfile, load_builtin_profile,
    openssl_adapter::{OpenSslAdapterError, OpenSslMimicryAdapter},
};
use openssl::{
    asn1::Asn1Time,
    bn::{BigNum, MsbOption},
    hash::MessageDigest,
    nid::Nid,
    pkey::{PKey, Private},
    rsa::Rsa,
    ssl::{SslAcceptor, SslMethod},
    x509::{
        X509, X509Builder, X509Name,
        extension::{BasicConstraints, ExtendedKeyUsage, KeyUsage, SubjectAlternativeName},
    },
};

const TEST_TIMEOUT: Duration = Duration::from_secs(10);

#[tokio::test]
async fn smoke_test_openssl_adapter_connects_to_local_tls_server() {
    let server = spawn_local_tls_server();
    let adapter = OpenSslMimicryAdapter::new_with_extra_trust_anchor(server.ca_certificate)
        .expect("OpenSSL adapter context 应能创建并加载测试 CA");

    let tls_stream = tokio::time::timeout(TEST_TIMEOUT, adapter.connect(server.addr, "localhost"))
        .await
        .expect("OpenSSL client handshake 不应超时")
        .expect("OpenSSL client 应能完成本地 TLS 握手");

    drop(tls_stream);
    server
        .join
        .join()
        .expect("TLS test server thread 不应 panic")
        .expect("TLS test server 应完成一次握手");
}

#[tokio::test]
async fn profile_driven_cipher_and_alpn_capture_diff() {
    let mut codex_profile =
        load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");
    if codex_profile.tls.alpn_protocols.is_empty() {
        // 当前 Codex 抓包模板未观察到 ALPN；本 isolated adapter test 补一个非空族来覆盖注入路径。
        codex_profile.tls.alpn_protocols = vec!["h2".to_owned(), "http/1.1".to_owned()];
    }
    let alpn_wire = alpn_wire_format_for_test(&codex_profile.tls.alpn_protocols);
    assert_eq!(
        alpn_wire, b"\x02h2\x08http/1.1",
        "测试 ALPN wire format 应为 h2 + http/1.1 的 length-prefix bytes"
    );

    let bind_addr: SocketAddr = "127.0.0.1:0"
        .parse()
        .expect("本地 ephemeral capture 地址应合法");
    let (capture_addr, capture_task) = spawn_capture_once(bind_addr)
        .await
        .expect("capture listener 应可启动");
    let adapter = OpenSslMimicryAdapter::new_with_profile(&codex_profile)
        .expect("OpenSSL adapter 应能应用 profile cipher_suites 与 ALPN");

    let connect_result =
        tokio::time::timeout(TEST_TIMEOUT, adapter.connect(capture_addr, "localhost"))
            .await
            .expect("OpenSSL profile capture connect 不应超时");
    assert!(
        connect_result.is_err(),
        "capture helper 只读取 ClientHello 后关闭连接，不应完成 TLS 握手"
    );

    let captured = tokio::time::timeout(TEST_TIMEOUT, capture_task)
        .await
        .expect("capture task 应在本地请求失败后结束")
        .expect("capture task 不应 panic")
        .expect("ClientHello 应能按 wire length 成功解析");
    let diff = diff_capture_against_profile(&captured, &codex_profile);

    eprintln!(
        "profile_driven_cipher_and_alpn_capture_diff captured={captured:?} diff={diff:?} alpn_wire_len={}",
        alpn_wire.len()
    );

    assert!(
        matches!(&diff.cipher_suites, ListFieldStatus::OrderedMatch { .. }),
        "cipher_suites 应与 Codex profile 顺序一致，实际 diff: {diff:?}"
    );
    assert!(
        matches!(&diff.alpn_protocols, ListFieldStatus::OrderedMatch { .. }),
        "alpn_protocols 应与测试 profile 顺序一致，实际 diff: {diff:?}"
    );
}

#[tokio::test]
async fn profile_driven_groups_and_sigalgs_capture_diff() {
    let codex_profile =
        load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    match OpenSslMimicryAdapter::new_with_profile(&codex_profile) {
        Ok(adapter) => {
            let diff = capture_profile_diff(&adapter, &codex_profile, "full_codex_profile").await;

            assert!(
                matches!(&diff.supported_groups, ListFieldStatus::OrderedMatch { .. }),
                "supported_groups 应与 Codex profile 顺序一致，实际 diff: {diff:?}"
            );
            assert!(
                matches!(
                    &diff.signature_algorithms,
                    ListFieldStatus::OrderedMatch { .. }
                ),
                "signature_algorithms 应与 Codex profile 顺序一致，实际 diff: {diff:?}"
            );
        }
        Err(OpenSslAdapterError::UnsupportedGroup(4588)) => {
            eprintln!(
                "profile_driven_groups_and_sigalgs_capture_diff full_codex_profile unsupported_group=4588"
            );
            let without_pq_group = codex_profile_without_supported_group(4588);
            match OpenSslMimicryAdapter::new_with_profile(&without_pq_group) {
                Ok(adapter) => {
                    let diff = capture_profile_diff(
                        &adapter,
                        &without_pq_group,
                        "codex_profile_without_4588",
                    )
                    .await;

                    assert!(
                        matches!(&diff.supported_groups, ListFieldStatus::OrderedMatch { .. }),
                        "4588-stripped supported_groups 应与 profile 顺序一致，实际 diff: {diff:?}"
                    );
                    assert!(
                        matches!(
                            &diff.signature_algorithms,
                            ListFieldStatus::OrderedMatch { .. }
                        ),
                        "4588-stripped signature_algorithms 应与 profile 顺序一致，实际 diff: {diff:?}"
                    );
                }
                Err(OpenSslAdapterError::UnsupportedSigalg(sigalg_id)) => {
                    assert!(
                        without_pq_group
                            .tls
                            .signature_algorithms
                            .contains(&sigalg_id),
                        "UnsupportedSigalg 应来自 profile.signature_algorithms，实际 id=0x{sigalg_id:04x}"
                    );
                    eprintln!(
                        "profile_driven_groups_and_sigalgs_capture_diff codex_profile_without_4588 unsupported_sigalg=0x{sigalg_id:04x}"
                    );
                }
                Err(error) => {
                    panic!(
                        "4588-stripped profile 应只可能成功或暴露 UnsupportedSigalg，实际错误: {error:?}"
                    );
                }
            }
        }
        Err(OpenSslAdapterError::UnsupportedSigalg(sigalg_id)) => {
            assert!(
                codex_profile.tls.signature_algorithms.contains(&sigalg_id),
                "UnsupportedSigalg 应来自 profile.signature_algorithms，实际 id=0x{sigalg_id:04x}"
            );
            eprintln!(
                "profile_driven_groups_and_sigalgs_capture_diff full_codex_profile unsupported_sigalg=0x{sigalg_id:04x}"
            );
        }
        Err(error) => {
            panic!(
                "Codex profile 应只可能成功或暴露 typed unsupported group/sigalg，实际错误: {error:?}"
            );
        }
    }
}

#[test]
fn profile_driven_unsupported_group_fails_fast() {
    let profile = codex_profile_with_first_supported_group(0xffff);

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("unsupported group 0xffff 必须 fail-fast，不能构造 adapter");

    assert!(
        matches!(error, OpenSslAdapterError::UnsupportedGroup(0xffff)),
        "unsupported group 应精确返回 0xffff，实际错误: {error:?}"
    );
}

#[test]
fn profile_driven_unsupported_sigalg_fails_fast() {
    let profile = codex_profile_with_first_signature_algorithm(0xffff);

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("unsupported sigalg 0xffff 必须 fail-fast，不能构造 adapter");

    assert!(
        matches!(error, OpenSslAdapterError::UnsupportedSigalg(0xffff)),
        "unsupported sigalg 应精确返回 0xffff，实际错误: {error:?}"
    );
}

struct LocalTlsServer {
    addr: SocketAddr,
    ca_certificate: X509,
    join: thread::JoinHandle<Result<(), String>>,
}

fn spawn_local_tls_server() -> LocalTlsServer {
    let listener = TcpListener::bind("127.0.0.1:0").expect("本地 TLS listener 应能绑定");
    let addr = listener.local_addr().expect("本地 TLS listener 地址应存在");
    let certs = localhost_test_cert_chain().expect("本地 TLS 测试证书链应能创建");
    let ca_certificate = certs.ca_certificate.clone();

    let join = thread::spawn(move || {
        listener
            .set_nonblocking(true)
            .map_err(|error| error.to_string())?;
        let tcp_stream = accept_with_timeout(&listener)?;
        tcp_stream
            .set_read_timeout(Some(TEST_TIMEOUT))
            .map_err(|error| error.to_string())?;
        tcp_stream
            .set_write_timeout(Some(TEST_TIMEOUT))
            .map_err(|error| error.to_string())?;

        let acceptor = build_test_acceptor(&certs.server_private_key, &certs.server_certificate)?;
        let _tls_stream = acceptor
            .accept(tcp_stream)
            .map_err(|error| error.to_string())?;
        Ok(())
    });

    LocalTlsServer {
        addr,
        ca_certificate,
        join,
    }
}

fn accept_with_timeout(listener: &TcpListener) -> Result<StdTcpStream, String> {
    let started = Instant::now();
    loop {
        match listener.accept() {
            Ok((stream, _)) => return Ok(stream),
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                if started.elapsed() >= TEST_TIMEOUT {
                    return Err("TLS test server accept timed out".to_owned());
                }
                thread::sleep(Duration::from_millis(10));
            }
            Err(error) => return Err(error.to_string()),
        }
    }
}

fn build_test_acceptor(
    private_key: &PKey<Private>,
    certificate: &X509,
) -> Result<SslAcceptor, String> {
    let mut builder =
        SslAcceptor::mozilla_intermediate(SslMethod::tls()).map_err(|error| error.to_string())?;
    builder
        .set_private_key(private_key)
        .map_err(|error| error.to_string())?;
    builder
        .set_certificate(certificate)
        .map_err(|error| error.to_string())?;
    builder
        .check_private_key()
        .map_err(|error| error.to_string())?;
    Ok(builder.build())
}

struct TestCertChain {
    ca_certificate: X509,
    server_private_key: PKey<Private>,
    server_certificate: X509,
}

fn localhost_test_cert_chain() -> Result<TestCertChain, String> {
    let (ca_private_key, ca_certificate) = test_ca_cert()?;
    let server_private_key =
        PKey::from_rsa(Rsa::generate(2048).map_err(|error| error.to_string())?)
            .map_err(|error| error.to_string())?;

    let name = x509_name("localhost")?;
    let mut certificate = X509::builder().map_err(|error| error.to_string())?;
    certificate
        .set_version(2)
        .map_err(|error| error.to_string())?;
    certificate
        .set_subject_name(&name)
        .map_err(|error| error.to_string())?;
    certificate
        .set_issuer_name(ca_certificate.subject_name())
        .map_err(|error| error.to_string())?;
    set_validity_and_serial(&mut certificate)?;
    certificate
        .set_pubkey(&server_private_key)
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            BasicConstraints::new()
                .critical()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            KeyUsage::new()
                .critical()
                .digital_signature()
                .key_encipherment()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            ExtendedKeyUsage::new()
                .server_auth()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    let subject_alternative_name = SubjectAlternativeName::new()
        .dns("localhost")
        .build(&certificate.x509v3_context(Some(&ca_certificate), None))
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(subject_alternative_name)
        .map_err(|error| error.to_string())?;
    certificate
        .sign(&ca_private_key, MessageDigest::sha256())
        .map_err(|error| error.to_string())?;

    Ok(TestCertChain {
        ca_certificate,
        server_private_key,
        server_certificate: certificate.build(),
    })
}

fn test_ca_cert() -> Result<(PKey<Private>, X509), String> {
    let private_key = PKey::from_rsa(Rsa::generate(2048).map_err(|error| error.to_string())?)
        .map_err(|error| error.to_string())?;

    let name = x509_name("HUAKAI local TLS test CA")?;
    let mut certificate = X509::builder().map_err(|error| error.to_string())?;
    certificate
        .set_version(2)
        .map_err(|error| error.to_string())?;
    certificate
        .set_subject_name(&name)
        .map_err(|error| error.to_string())?;
    certificate
        .set_issuer_name(&name)
        .map_err(|error| error.to_string())?;
    set_validity_and_serial(&mut certificate)?;
    certificate
        .set_pubkey(&private_key)
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            BasicConstraints::new()
                .critical()
                .ca()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .append_extension(
            KeyUsage::new()
                .critical()
                .key_cert_sign()
                .crl_sign()
                .build()
                .map_err(|error| error.to_string())?,
        )
        .map_err(|error| error.to_string())?;
    certificate
        .sign(&private_key, MessageDigest::sha256())
        .map_err(|error| error.to_string())?;

    Ok((private_key, certificate.build()))
}

fn x509_name(common_name: &str) -> Result<X509Name, String> {
    let mut name = X509Name::builder().map_err(|error| error.to_string())?;
    name.append_entry_by_nid(Nid::COMMONNAME, common_name)
        .map_err(|error| error.to_string())?;
    Ok(name.build())
}

fn set_validity_and_serial(certificate: &mut X509Builder) -> Result<(), String> {
    let not_before = Asn1Time::days_from_now(0).map_err(|error| error.to_string())?;
    certificate
        .set_not_before(&not_before)
        .map_err(|error| error.to_string())?;

    let not_after = Asn1Time::days_from_now(1).map_err(|error| error.to_string())?;
    certificate
        .set_not_after(&not_after)
        .map_err(|error| error.to_string())?;

    let mut serial = BigNum::new().map_err(|error| error.to_string())?;
    serial
        .rand(128, MsbOption::MAYBE_ZERO, false)
        .map_err(|error| error.to_string())?;
    let serial_asn1 = serial
        .to_asn1_integer()
        .map_err(|error| error.to_string())?;
    certificate
        .set_serial_number(&serial_asn1)
        .map_err(|error| error.to_string())
}

fn alpn_wire_format_for_test(protocols: &[String]) -> Vec<u8> {
    let mut wire_format = Vec::new();
    for protocol in protocols {
        let bytes = protocol.as_bytes();
        wire_format.push(bytes.len() as u8);
        wire_format.extend(bytes);
    }
    wire_format
}

async fn capture_profile_diff(
    adapter: &OpenSslMimicryAdapter,
    profile: &FingerprintProfile,
    label: &str,
) -> CaptureDiff {
    let bind_addr: SocketAddr = "127.0.0.1:0"
        .parse()
        .expect("本地 ephemeral capture 地址应合法");
    let (capture_addr, capture_task) = spawn_capture_once(bind_addr)
        .await
        .expect("capture listener 应可启动");

    let connect_result =
        tokio::time::timeout(TEST_TIMEOUT, adapter.connect(capture_addr, "localhost"))
            .await
            .expect("OpenSSL profile capture connect 不应超时");
    assert!(
        connect_result.is_err(),
        "capture helper 只读取 ClientHello 后关闭连接，不应完成 TLS 握手"
    );

    let captured = tokio::time::timeout(TEST_TIMEOUT, capture_task)
        .await
        .expect("capture task 应在本地请求失败后结束")
        .expect("capture task 不应 panic")
        .expect("ClientHello 应能按 wire length 成功解析");
    let diff = diff_capture_against_profile(&captured, profile);

    eprintln!(
        "profile_driven_groups_and_sigalgs_capture_diff label={label} captured={captured:?} diff={diff:?}"
    );

    diff
}

fn codex_profile_without_supported_group(group_id: u16) -> FingerprintProfile {
    let mut raw: serde_json::Value =
        serde_json::from_str(BuiltinProfile::CodexCli.raw_json()).expect("codex raw JSON 应合法");

    for field in ["curves", "supported_groups"] {
        raw.get_mut(field)
            .and_then(serde_json::Value::as_array_mut)
            .expect("codex profile 应包含 curves/supported_groups 数组")
            .retain(|value| value.as_u64() != Some(u64::from(group_id)));
    }

    let raw_json = serde_json::to_string(&raw).expect("变种 codex profile 应可序列化");
    FingerprintProfile::from_json(&raw_json).expect("去掉 4588 后的 codex profile 应仍合法")
}

fn codex_profile_with_first_supported_group(group_id: u16) -> FingerprintProfile {
    let mut raw: serde_json::Value =
        serde_json::from_str(BuiltinProfile::CodexCli.raw_json()).expect("codex raw JSON 应合法");

    for field in ["curves", "supported_groups"] {
        prepend_u16_json_field(&mut raw, field, group_id);
    }

    codex_profile_from_raw_value(
        raw,
        "插入 unsupported supported_group 后的 codex profile 应仍合法",
    )
}

fn codex_profile_with_first_signature_algorithm(sigalg_id: u16) -> FingerprintProfile {
    let mut raw: serde_json::Value =
        serde_json::from_str(BuiltinProfile::CodexCli.raw_json()).expect("codex raw JSON 应合法");

    for field in ["curves", "supported_groups"] {
        set_u16_json_field(&mut raw, field, &[0x001d]);
    }
    for field in ["sig_algos", "signature_algorithms"] {
        prepend_u16_json_field(&mut raw, field, sigalg_id);
    }

    codex_profile_from_raw_value(
        raw,
        "插入 unsupported signature_algorithm 后的 codex profile 应仍合法",
    )
}

fn prepend_u16_json_field(raw: &mut serde_json::Value, field: &str, value: u16) {
    raw.get_mut(field)
        .and_then(serde_json::Value::as_array_mut)
        .expect("codex profile 应包含目标 u16 数组")
        .insert(0, serde_json::Value::from(u64::from(value)));
}

fn set_u16_json_field(raw: &mut serde_json::Value, field: &str, values: &[u16]) {
    let field_value = raw
        .get_mut(field)
        .and_then(serde_json::Value::as_array_mut)
        .expect("codex profile 应包含目标 u16 数组");
    field_value.clear();
    field_value.extend(
        values
            .iter()
            .map(|value| serde_json::Value::from(u64::from(*value))),
    );
}

fn codex_profile_from_raw_value(raw: serde_json::Value, message: &str) -> FingerprintProfile {
    let raw_json = serde_json::to_string(&raw).expect("变种 codex profile 应可序列化");
    FingerprintProfile::from_json(&raw_json).expect(message)
}
