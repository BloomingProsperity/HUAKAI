use super::{
    AvailableMimicryFeatures, BuiltinProfile, MimicryBackend, ProfileMatchPolicy, ProfileMode,
    ProfileVendor, load_builtin_profile, resolve_profile_mimicry_backend,
};

#[test]
fn anthropic_profile_loads_with_sampled_tls_fields() {
    let profile = load_builtin_profile(BuiltinProfile::AnthropicClaudeCode)
        .expect("Anthropic Claude Code profile 应加载");

    assert_eq!(profile.mode, ProfileMode::AnthropicClaudeCode);
    assert_eq!(profile.mode_name, "anthropic-claude-code");
    assert_eq!(profile.vendor, ProfileVendor::Anthropic);
    assert_eq!(profile.target_host, "api.anthropic.com");
    assert_eq!(profile.sample_count, 5);
    assert_eq!(profile.tls.ja3_hash, "de88744b20558d50f03a5f0ea176ee98");
    assert_eq!(profile.tls.alpn_protocols, vec!["http/1.1".to_owned()]);
    assert_eq!(profile.tls.ec_point_formats, vec![0]);
    assert!(profile.tls.extensions.contains(&65037));
    assert!(!profile.h2_settings.available);
    assert!(!profile.h2_settings_frame.available);
    assert!(!profile.h2_pseudo_header_capture.available);
    assert_eq!(
        profile.match_policy(),
        ProfileMatchPolicy::SampleSetRandomized
    );
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

// W11-F F-2.2 (synthesis D-S3/D-S4 Owner-approved 2026-05-24): kiro 与 gemini
// 模板的 backend_intent 路径已重新分类:
//   - KiroCli: kiro_cli_known_gap_fields 非空 → match_policy=KnownGapBlocked →
//     backend_intent=KnownGapBlocked → resolver 返 Ok(KnownGapBlocked) 而非
//     Err(UnsupportedTemplate). 永久 KnownGap (rustls wire 不可在 OpenSSL 精确复刻),
//     等 F-3 (byte-level builder 或 vendor rustls). 任何 feature 组合都同结果.
//   - GeminiAdvanced: known_gap 空 + tls_backend=nodejs → OpenSslAdapter intent.
//     Resolver 据 feature 选 backend: 优先 boring (pre-existing 行为, F-2.3c 会加
//     per-profile boring 验证), 退 openssl (preflight 把关 wire bytes), 都无 → KnownGap.
//
// Mutation: backend_resolver::resolve_vendor_mimicry_backend 改回早 return Boring/
// Openssl 绕过 backend_intent → KnownGap reason 字符串缺失, 红.
//
// 历史: D-10 (2026-05-23) 老测试 assert 二者都 UnsupportedTemplate. F-2.2 (2026-05-24)
// 修分类 — Kiro permanent KnownGap, Gemini push 到 runtime preflight. 见
// docs/process/plans/2026-05-24-w11f-f2-fingerprint-l1-synthesis.md §5.4 + §6.

/// Kiro 任何 feature 组合下都必须返 KnownGapBlocked, 永远不返 Allow*.
/// Mutation: 把 kiro_cli_known_gap_fields() 返 empty Vec → 此测试红
/// (Kiro 退化到 SampleSetRandomized → backend_intent rustls UnsupportedTemplate).
#[test]
fn kiro_backend_resolver_returns_known_gap_with_boring_feature() {
    assert_kiro_known_gap_regardless_of_feature(AvailableMimicryFeatures {
        openssl: true,
        boring: true,
    });
}

#[test]
fn kiro_backend_resolver_returns_known_gap_with_openssl_only() {
    assert_kiro_known_gap_regardless_of_feature(AvailableMimicryFeatures {
        openssl: true,
        boring: false,
    });
}

#[test]
fn kiro_backend_resolver_returns_known_gap_when_no_feature() {
    assert_kiro_known_gap_regardless_of_feature(AvailableMimicryFeatures {
        openssl: false,
        boring: false,
    });
}

fn assert_kiro_known_gap_regardless_of_feature(available_features: AvailableMimicryFeatures) {
    let profile = load_builtin_profile(BuiltinProfile::KiroCli)
        .expect("Kiro builtin profile should load");

    let backend = resolve_profile_mimicry_backend(&profile, available_features)
        .expect("F-2.2 D-S3: Kiro must resolve to KnownGapBlocked (not Err)");

    match backend {
        MimicryBackend::KnownGapBlocked { reason } => {
            // Reason corrected 2026-05-24 post-spec-dig: Kiro byte-level wire
            // test PASSES (boring_wire.rs::kiro_*); KnownGap is cautious default
            // until real-upstream capture verification (F-2.5). Either
            // "real_upstream_capture" or "pending" must appear.
            assert!(
                reason.contains("real_upstream_capture") || reason.contains("pending"),
                "Kiro KnownGap reason must cite real-upstream verification gap, not rustls (got: {reason})"
            );
        }
        other => panic!(
            "Kiro must resolve to KnownGapBlocked (D-S3 cautious default), got {other:?}"
        ),
    }
}

/// Gemini boring+openssl: 偏好 boring (pre-existing behavior; F-2.3c will add
/// per-profile boring byte-level verification to fail-closed if Gemini ja3 doesn't match).
/// Mutation: 把 backend_intent NodeJs arm 改回 UnsupportedTemplate → 此测试红
/// (resolver 返 Err 而非 Boring).
#[test]
fn gemini_backend_resolver_prefers_boring_with_both_features() {
    let profile = load_builtin_profile(BuiltinProfile::GeminiAdvanced)
        .expect("Gemini builtin profile should load");
    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: true,
            boring: true,
        },
    )
    .expect("F-2.2 D-S4: Gemini NodeJs → OpenSslAdapter intent → boring preference");
    // Note: this prefers Boring per resolver pre-existing logic; F-2.3c adds
    // per-profile byte-level Boring verification so this doesn't silently
    // serve Gemini through Anthropic-only Boring builder.
    assert_eq!(backend, MimicryBackend::Boring);
}

/// Gemini openssl only: ensure_selected_backend_matches_template runs since
/// Gemini ec_point_formats=[0,1,2] (OpenSSL native) + ext22 ETM ✓ → Openssl OK.
#[test]
fn gemini_backend_resolver_falls_back_to_openssl_with_openssl_only() {
    let profile = load_builtin_profile(BuiltinProfile::GeminiAdvanced)
        .expect("Gemini builtin profile should load");
    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: true,
            boring: false,
        },
    )
    .expect(
        "F-2.2 D-S4: Gemini NodeJs → OpenSslAdapter; openssl feature on → Openssl backend; \
         OpenSSL native ec_point_formats + ETM ext22 match Gemini template",
    );
    assert_eq!(backend, MimicryBackend::Openssl);
}

/// Gemini no feature: backend_intent OpenSslAdapter but no adapter compiled →
/// resolver returns KnownGapBlocked with adapter-missing reason.
#[test]
fn gemini_backend_resolver_returns_known_gap_when_no_feature() {
    let profile = load_builtin_profile(BuiltinProfile::GeminiAdvanced)
        .expect("Gemini builtin profile should load");
    let backend = resolve_profile_mimicry_backend(
        &profile,
        AvailableMimicryFeatures {
            openssl: false,
            boring: false,
        },
    )
    .expect("F-2.2 D-S4: Gemini with no mimicry feature must KnownGapBlocked (not Err)");
    match backend {
        MimicryBackend::KnownGapBlocked { reason } => {
            assert!(reason.contains(profile.vendor.as_str()));
            assert!(
                reason.contains("mimicry-boring") || reason.contains("mimicry-openssl"),
                "no-feature KnownGap reason must cite missing adapter (got: {reason})"
            );
        }
        other => panic!(
            "Gemini with no feature must be KnownGapBlocked, got {other:?}"
        ),
    }
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
