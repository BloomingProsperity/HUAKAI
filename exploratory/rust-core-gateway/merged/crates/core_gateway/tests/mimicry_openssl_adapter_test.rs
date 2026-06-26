#![cfg(feature = "mimicry-openssl")]

// L2-A4 smoke: 只验证 OpenSSL client adapter 能完成本地 TLS 握手。

mod common;

use std::{
    net::{SocketAddr, TcpListener, TcpStream as StdTcpStream},
    thread,
    time::{Duration, Instant},
};

use common::{
    capture_diff::{
        CaptureDiff, ExtensionsListStatus, ListFieldStatus, diff_capture_against_profile,
    },
    tls_capture::{CapturedClientHello, spawn_capture_once},
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

#[tokio::test]
async fn profile_driven_ec_point_formats_capture_diff() {
    let codex_profile =
        load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");
    let adapter = OpenSslMimicryAdapter::new_with_profile(&codex_profile)
        .expect("OpenSSL adapter 应能应用 profile ec_point_formats");
    assert!(
        adapter.preflight_passed(),
        "new_with_profile 必须记录 OpenSSL runtime preflight provenance"
    );
    let diff = capture_profile_diff(&adapter, &codex_profile, "ec_point_formats").await;

    eprintln!(
        "profile_driven_ec_point_formats_capture_diff ec_point_formats={:?}",
        diff.ec_point_formats
    );

    assert!(
        matches!(
            &diff.ec_point_formats,
            ListFieldStatus::OrderedMatch { value } if value == &vec![0, 1, 2]
        ),
        "ec_point_formats 应与 Codex profile [0, 1, 2] 顺序一致，实际 diff: {diff:?}"
    );
}

#[tokio::test]
async fn profile_driven_extension_22_capture_diff() {
    let profile = openssl_native_ec_point_formats_profile();
    let adapter = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect("OpenSSL adapter 应能应用包含 extension 22 的 profile");
    assert!(
        adapter.preflight_passed(),
        "new_with_profile 必须记录 extension 22 runtime preflight provenance"
    );

    let diff = capture_profile_diff(&adapter, &profile, "extension_22").await;

    match &diff.extensions {
        ExtensionsListStatus::Subset { value, .. } => {
            assert!(
                value.contains(&22),
                "extensions Subset 应包含 encrypt_then_mac 22，实际 diff: {diff:?}"
            );
        }
        status => {
            panic!("ExactStable OpenSSL profile 应输出 extensions Subset，实际: {status:?}")
        }
    }
}

#[test]
fn profile_driven_ec_point_formats_preflight_records_provenance() {
    let profile = openssl_native_ec_point_formats_profile();

    let adapter = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect("OpenSSL native ec_point_formats runtime preflight 必须通过");

    assert!(
        adapter.preflight_passed(),
        "OpenSSL adapter 应记录 new_with_profile 已通过 ec_point_formats preflight"
    );
}

#[test]
fn profile_driven_extension_22_preflight_provenance() {
    let profile = openssl_native_ec_point_formats_profile();

    let adapter = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect("OpenSSL native encrypt_then_mac runtime preflight 必须通过");

    assert!(
        adapter.preflight_passed(),
        "OpenSSL adapter 应记录 new_with_profile 已通过 extension 22 preflight"
    );
}

#[test]
fn profile_driven_extension_22_missing_fails_fast() {
    let profile = profile_without_encrypt_then_mac_extension();

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("缺少 extension 22 的 OpenSSL profile 必须 fail-fast，不能构造 adapter");

    match error {
        OpenSslAdapterError::UnsupportedExtension { id: 22, reason } => {
            assert!(
                reason.contains("cannot disable native ETM extension"),
                "extension 22 fail-fast 必须说明 OpenSSL public API 无 disable 路径，实际: {reason}"
            );
        }
        error => panic!("缺少 extension 22 应返回 UnsupportedExtension 22，实际: {error:?}"),
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

#[test]
fn profile_driven_ec_point_formats_empty_list_fails_fast() {
    let profile = profile_with_ec_point_formats(Vec::new());

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("empty ec_point_formats 必须 fail-fast，不能构造 adapter");

    assert_ec_point_formats_profile_apply_failed(error, "[]");
}

#[test]
fn profile_driven_ec_point_formats_partial_fails_fast() {
    let profile = profile_with_ec_point_formats(vec![0]);

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("[0] ec_point_formats 必须 fail-fast，不能构造 adapter");

    assert_ec_point_formats_profile_apply_failed(error, "[0]");
}

#[test]
fn profile_driven_ec_point_formats_wrong_order_fails_fast() {
    let profile = profile_with_ec_point_formats(vec![2, 1, 0]);

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("[2,1,0] ec_point_formats 必须 fail-fast，不能构造 adapter");

    assert_ec_point_formats_profile_apply_failed(error, "[2, 1, 0]");
}

#[test]
fn extensions_subset_independent_vector() {
    let baseline = openssl_native_ec_point_formats_profile();
    let baseline_adapter = OpenSslMimicryAdapter::new_with_profile(&baseline)
        .expect("baseline OpenSSL profile preflight 应通过");
    let baseline_wire_extras = baseline_adapter
        .preflight_extras()
        .wire_extension_extras
        .clone();
    let runtime_extensions = runtime_extensions_from_missing_probe(&baseline);
    assert_eq!(
        baseline_wire_extras,
        expected_wire_extras(&runtime_extensions, &baseline.tls.extensions),
        "baseline extras 应等于 runtime capture 中 baseline profile 未声明的 extensions"
    );

    let mut profile = baseline.clone();
    let skipped = vec![baseline.tls.extensions[1], baseline.tls.extensions[3]];
    profile
        .tls
        .extensions
        .retain(|extension| !skipped.contains(extension));
    assert!(
        !profile.tls.extensions.is_empty()
            && profile.tls.extensions.len() < baseline.tls.extensions.len(),
        "子集 profile 必须保留真子集 extensions"
    );

    let adapter = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect("OpenSSL profile extension 真子集应通过 ordered-subset preflight");
    let wire_extras = adapter.preflight_extras().wire_extension_extras.clone();
    let expected_wire_extras = expected_wire_extras(&runtime_extensions, &profile.tls.extensions);
    assert_eq!(
        wire_extras, expected_wire_extras,
        "wire_extension_extras 应暴露 runtime capture 中 profile 未声明的 extensions"
    );

    assert!(
        skipped
            .iter()
            .all(|extension| wire_extras.contains(extension)),
        "wire_extension_extras 必须包含从 baseline profile 跳过的 extensions"
    );

    let captured = captured_from_runtime_extensions(&baseline, runtime_extensions);
    let diff = diff_capture_against_profile(&captured, &profile);
    match &diff.extensions {
        ExtensionsListStatus::Subset { value, unexpected } => {
            assert_eq!(value, &profile.tls.extensions);
            assert!(
                !unexpected.is_empty(),
                "subset diff 必须把跳过的 runtime extension 记为 unexpected"
            );
        }
        status => panic!("extension 真子集应输出 Subset，实际: {status:?}"),
    }
}

#[test]
fn extensions_missing_independent_vector() {
    let baseline = openssl_native_ec_point_formats_profile();
    let runtime_extensions = runtime_extensions_from_missing_probe(&baseline);
    let synthetic_missing = pick_missing_id_not_in_wire(&runtime_extensions);

    let mut profile = baseline;
    profile.tls.extensions.push(synthetic_missing);

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("profile 声明 runtime 不会发送的 extension 必须 preflight fail-closed");

    match error {
        OpenSslAdapterError::PreflightFailed {
            field,
            missing,
            unexpected,
            ..
        } => {
            assert_eq!(field, "extensions");
            assert!(
                missing.contains(&synthetic_missing),
                "missing 必须包含 runtime-derived 合成 extension {synthetic_missing:#06x}，实际: {missing:?}"
            );
            assert!(
                !unexpected.contains(&synthetic_missing),
                "runtime 未发送的 extension 不应被归入 unexpected，实际: {unexpected:?}"
            );
        }
        error => panic!("extension 缺失应返回 PreflightFailed，实际: {error:?}"),
    }
}

#[test]
fn extensions_wrong_order_independent_vector() {
    let baseline = openssl_native_ec_point_formats_profile();
    let _baseline_extras = OpenSslMimicryAdapter::new_with_profile(&baseline)
        .expect("baseline OpenSSL profile preflight 应通过")
        .preflight_extras()
        .wire_extension_extras
        .clone();

    let first = baseline
        .tls
        .extensions
        .iter()
        .position(|extension| *extension == 22)
        .and_then(|index| baseline.tls.extensions.get(index + 1).copied())
        .expect("baseline profile 中 extension 22 后应存在另一个 extension");
    let mut profile = baseline;
    profile.tls.extensions = vec![first, 22];

    let error = OpenSslMimicryAdapter::new_with_profile(&profile)
        .expect_err("profile extension 顺序乱序时必须 preflight fail-closed");

    match error {
        OpenSslAdapterError::PreflightFailed { field, missing, .. } => {
            assert_eq!(field, "extensions");
            assert!(
                missing.is_empty(),
                "乱序场景应是集合存在但顺序错误，missing 应为空，实际: {missing:?}"
            );
        }
        error => panic!("extension 乱序应返回 PreflightFailed，实际: {error:?}"),
    }
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

    eprintln!("capture_profile_diff label={label} captured={captured:?} diff={diff:?}");

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

fn openssl_native_ec_point_formats_profile() -> FingerprintProfile {
    let mut profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");
    profile.tls.curves = vec![0x001d, 0x0017, 0x0018];
    profile.tls.supported_groups = profile.tls.curves.clone();
    profile.tls.sig_algos = vec![0x0403, 0x0804, 0x0401];
    profile.tls.signature_algorithms = profile.tls.sig_algos.clone();
    profile.tls.ec_point_formats = vec![0, 1, 2];
    profile
}

fn runtime_extensions_from_missing_probe(profile: &FingerprintProfile) -> Vec<u16> {
    let mut probe = profile.clone();
    let baseline_actual = actual_wire_extensions_from_preflight(profile);
    let synthetic_missing = pick_missing_id_not_in_wire(&baseline_actual);
    probe.tls.extensions.push(synthetic_missing);

    match OpenSslMimicryAdapter::new_with_profile(&probe)
        .expect_err("probe profile 应因合成 extension 缺失触发 preflight 失败")
    {
        OpenSslAdapterError::PreflightFailed { field, actual, .. } => {
            assert_eq!(field, "extensions");
            actual
        }
        error => panic!("probe profile 应返回 extensions PreflightFailed，实际: {error:?}"),
    }
}

fn actual_wire_extensions_from_preflight(profile: &FingerprintProfile) -> Vec<u16> {
    let adapter = OpenSslMimicryAdapter::new_with_profile(profile)
        .expect("baseline OpenSSL profile preflight 应通过");
    let mut actual = profile.tls.extensions.clone();
    for extension in &adapter.preflight_extras().wire_extension_extras {
        if !actual.contains(extension) {
            actual.push(*extension);
        }
    }
    actual
}

fn pick_missing_id_not_in_wire(actual: &[u16]) -> u16 {
    // GREASE/reserved 风格的候选 ID 仅在当前 runtime wire extensions 中不存在时
    // 才使用, 从而让 missing 场景保持 runtime 派生。
    const SYNTHETIC_EXTENSION_CANDIDATES: &[u16] = &[
        0x0a0a, 0x1a1a, 0x2a2a, 0x3a3a, 0x4a4a, 0x5a5a, 0x6a6a, 0x7a7a, 0x8a8a, 0x9a9a, 0xaaaa,
        0xbaba, 0xcaca, 0xdada, 0xeaea, 0xfafa,
    ];

    SYNTHETIC_EXTENSION_CANDIDATES
        .iter()
        .copied()
        .find(|extension| !actual.contains(extension))
        .expect("synthetic extension candidate pool 必须至少有一个未出现在 runtime wire 中")
}

fn expected_wire_extras(runtime_extensions: &[u16], profile_extensions: &[u16]) -> Vec<u16> {
    runtime_extensions
        .iter()
        .copied()
        .filter(|extension| !profile_extensions.contains(extension))
        .collect()
}

fn captured_from_runtime_extensions(
    profile: &FingerprintProfile,
    extensions: Vec<u16>,
) -> CapturedClientHello {
    CapturedClientHello {
        legacy_version: 772,
        cipher_suites: profile.tls.cipher_suites.clone(),
        extensions,
        supported_groups: profile.tls.supported_groups.clone(),
        signature_algorithms: profile.tls.signature_algorithms.clone(),
        ec_point_formats: profile.tls.ec_point_formats.clone(),
        alpn_protocols: profile.tls.alpn_protocols.clone(),
    }
}

fn profile_with_ec_point_formats(ec_point_formats: Vec<u8>) -> FingerprintProfile {
    let mut profile = openssl_native_ec_point_formats_profile();
    profile.tls.ec_point_formats = ec_point_formats;
    profile
}

fn profile_without_encrypt_then_mac_extension() -> FingerprintProfile {
    let mut profile = openssl_native_ec_point_formats_profile();
    profile.tls.extensions.retain(|extension| *extension != 22);
    profile
}

fn assert_ec_point_formats_profile_apply_failed(error: OpenSslAdapterError, expected: &str) {
    match error {
        OpenSslAdapterError::ProfileApplyFailed(message) => {
            assert!(
                message.contains("unsupported ec_point_formats"),
                "ec_point_formats fail-fast 应说明 unsupported，实际: {message}"
            );
            assert!(
                message.contains(expected),
                "ec_point_formats fail-fast 应包含输入 {expected}，实际: {message}"
            );
        }
        error => panic!("ec_point_formats fail-fast 应返回 ProfileApplyFailed，实际: {error:?}"),
    }
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
