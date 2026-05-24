use super::{
    AvailableMimicryFeatures, BuiltinProfile, MimicryBackend, ProfileMatchPolicy, ProfileMode,
    ProfileVendor, load_builtin_profile, resolve_profile_mimicry_backend,
};

#[test]
fn anthropic_profile_loads_with_sampled_tls_fields() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("Anthropic Claude Code profile 应加载");

    assert_eq!(profile.mode, ProfileMode::AnthropicClaudeCode);
    assert_eq!(profile.mode_name, "anthropic-cli-mimicry-v1");
    assert_eq!(profile.vendor, ProfileVendor::Anthropic);
    assert_eq!(profile.target_host, "api.anthropic.com");
    assert_eq!(profile.sample_count, 1);
    assert_eq!(profile.tls.ja3_hash, "55ba290366f110228d176d92fe6f6180");
    assert_eq!(
        profile.tls.alpn_protocols,
        vec!["h2".to_owned(), "http/1.1".to_owned()]
    );
    assert_eq!(profile.tls.ec_point_formats, vec![0, 1, 2]);
    assert!(profile.tls.extensions.contains(&0));
    assert!(profile.tls.extensions.contains(&22));
    assert!(!profile.h2_settings.available);
    assert!(!profile.h2_settings_frame.available);
    assert!(!profile.h2_pseudo_header_capture.available);
    assert_eq!(profile.match_policy(), ProfileMatchPolicy::ExactStable);
}

#[test]
fn anthropic_backend_resolver_prefers_openssl_when_boring_absent() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("Anthropic Claude Code profile 应加载");

    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: true,
            boring: false,
        },
    )
    .expect("Anthropic profile 应能回退到 OpenSSL backend");

    assert_eq!(backend, MimicryBackend::Openssl);
}

#[test]
fn anthropic_backend_resolver_prefers_boring_when_available() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("Anthropic Claude Code profile 应加载");

    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: true,
            boring: true,
        },
    )
    .expect("Anthropic profile 应优先使用 Boring backend");

    assert_eq!(backend, MimicryBackend::Boring);
}

#[test]
fn anthropic_backend_resolver_blocked_when_no_backend() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("Anthropic Claude Code profile 应加载");

    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: false,
            boring: false,
        },
    )
    .expect("Anthropic profile 无可用 backend 时应返回显式阻断 backend");

    match backend {
        MimicryBackend::KnownGapBlocked { reason } => {
            assert!(reason.contains("mimicry-boring"));
            assert!(reason.contains("mimicry-openssl"));
        }
        backend => panic!("Anthropic profile 无 backend 时应阻断，实际: {backend:?}"),
    }
}

#[test]
fn codex_cli_backend_resolver_prefers_boring_when_available() {
    assert_vendor_backend_prefers_boring(BuiltinProfile::CodexCli);
}

#[test]
fn codex_cli_backend_resolver_falls_back_to_openssl_when_boring_absent() {
    assert_vendor_backend_falls_back_to_openssl(BuiltinProfile::CodexCli);
}

#[test]
fn codex_cli_backend_resolver_blocked_when_no_backend() {
    assert_vendor_backend_blocked(BuiltinProfile::CodexCli);
}

#[test]
fn kiro_backend_resolver_prefers_boring_when_available() {
    assert_vendor_backend_prefers_boring(BuiltinProfile::KiroCli);
}

#[test]
fn kiro_backend_resolver_falls_back_to_openssl_when_boring_absent() {
    assert_vendor_backend_falls_back_to_openssl(BuiltinProfile::KiroCli);
}

#[test]
fn kiro_backend_resolver_blocked_when_no_backend() {
    assert_vendor_backend_blocked(BuiltinProfile::KiroCli);
}

#[test]
fn gemini_advanced_backend_resolver_prefers_boring_when_available() {
    assert_vendor_backend_prefers_boring(BuiltinProfile::GeminiAdvanced);
}

#[test]
fn gemini_advanced_backend_resolver_falls_back_to_openssl_when_boring_absent() {
    assert_vendor_backend_falls_back_to_openssl(BuiltinProfile::GeminiAdvanced);
}

#[test]
fn gemini_advanced_backend_resolver_blocked_when_no_backend() {
    assert_vendor_backend_blocked(BuiltinProfile::GeminiAdvanced);
}

fn assert_vendor_backend_prefers_boring(builtin: BuiltinProfile) {
    let profile = load_builtin_profile(builtin).expect("builtin profile 应加载");

    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: true,
            boring: true,
        },
    )
    .expect("vendor profile 应优先使用 Boring backend");

    assert_eq!(backend, MimicryBackend::Boring);
}

fn assert_vendor_backend_falls_back_to_openssl(builtin: BuiltinProfile) {
    let profile = load_builtin_profile(builtin).expect("builtin profile 应加载");

    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: true,
            boring: false,
        },
    )
    .expect("vendor profile 应能回退到 OpenSSL backend");

    assert_eq!(backend, MimicryBackend::Openssl);
}

fn assert_vendor_backend_blocked(builtin: BuiltinProfile) {
    let profile = load_builtin_profile(builtin).expect("builtin profile 应加载");

    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: false,
            boring: false,
        },
    )
    .expect("vendor profile 无可用 backend 时应返回显式阻断 backend");

    match backend {
        MimicryBackend::KnownGapBlocked { reason } => {
            assert!(reason.contains(profile.vendor.as_str()));
            assert!(reason.contains("mimicry-boring"));
            assert!(reason.contains("mimicry-openssl"));
        }
        backend => panic!("vendor profile 无 backend 时应阻断，实际: {backend:?}"),
    }
}

// R-1 deferred test preserved as history: OpenSSL Rust public API auto-injects
// native extensions `[1, 2]` and cannot byte-level reorder per profile sample.
// R-2-B-4 is covered by `anthropic_boring_client_hello_byte_level_matches_profile`;
// this OpenSSL path remains ignored because the mismatch is fundamental.
#[cfg(feature = "mimicry-openssl")]
#[ignore = "superseded by anthropic_boring_client_hello_byte_level_matches_profile (R-2-B-4); OpenSSL public API cannot byte-level reorder"]
#[tokio::test]
async fn anthropic_openssl_adapter_completes_mock_tls_handshake() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("Anthropic Claude Code profile 应加载");
    let server = match tls_fixture::try_spawn_local_tls_server() {
        Ok(server) => server,
        Err(error) if error.contains("Operation not permitted") => {
            eprintln!("skipping local TLS mock handshake: sandbox denied loopback bind: {error}");
            return;
        }
        Err(error) => panic!("本地 TLS listener 应能绑定: {error}"),
    };
    let adapter =
        super::openssl_adapter::OpenSslMimicryAdapter::new_with_profile_and_extra_trust_anchor(
            &profile,
            server.ca_certificate,
        )
        .expect("Anthropic OpenSSL adapter 应能应用 profile 并加载测试 CA");

    assert!(
        adapter.preflight_passed(),
        "Anthropic profile adapter 必须先跑本地 ClientHello preflight"
    );
    assert!(
        adapter
            .preflight_extras()
            .wire_ec_point_format_extras
            .contains(&1),
        "OpenSSL public API 无法把 sampled [0] point-format 精确裁剪到 wire；extra 必须显式暴露"
    );

    let tls_stream = tokio::time::timeout(
        tls_fixture::TEST_TIMEOUT,
        adapter.connect(server.addr, "localhost"),
    )
    .await
    .expect("Anthropic OpenSSL mock handshake 不应超时")
    .expect("Anthropic OpenSSL adapter 应能完成本地 TLS 握手");

    drop(tls_stream);
    server
        .join
        .join()
        .expect("TLS test server thread 不应 panic")
        .expect("TLS test server 应完成一次握手");
}

#[cfg(feature = "mimicry-openssl")]
mod tls_fixture {
    use std::{
        net::{SocketAddr, TcpListener, TcpStream as StdTcpStream},
        thread,
        time::{Duration, Instant},
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

    pub const TEST_TIMEOUT: Duration = Duration::from_secs(10);

    pub struct LocalTlsServer {
        pub addr: SocketAddr,
        pub ca_certificate: X509,
        pub join: thread::JoinHandle<Result<(), String>>,
    }

    pub fn try_spawn_local_tls_server() -> Result<LocalTlsServer, String> {
        let listener = TcpListener::bind("127.0.0.1:0").map_err(|error| error.to_string())?;
        let addr = listener.local_addr().map_err(|error| error.to_string())?;
        let certs = localhost_test_cert_chain()?;
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

            let acceptor =
                build_test_acceptor(&certs.server_private_key, &certs.server_certificate)?;
            let _tls_stream = acceptor
                .accept(tcp_stream)
                .map_err(|error| error.to_string())?;
            Ok(())
        });

        Ok(LocalTlsServer {
            addr,
            ca_certificate,
            join,
        })
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
        let mut builder = SslAcceptor::mozilla_intermediate(SslMethod::tls())
            .map_err(|error| error.to_string())?;
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
}
