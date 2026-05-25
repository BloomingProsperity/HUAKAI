#![cfg(feature = "mimicry-openssl")]

//! L2-A4/L2-A5 OpenSSL TLS mimicry backend skeleton.
//!
//! 本文件建立 native OpenSSL async handshake 边界，并先接入 L2-A5.1 的
//! cipher_suites / ALPN profile 注入。生产 dispatch 接线留给后续 atom。

use std::{
    error::Error,
    net::{SocketAddr, TcpListener as StdTcpListener, TcpStream as StdTcpStream},
    pin::Pin,
    thread,
    time::{Duration, Instant},
};

use openssl::{
    error::ErrorStack,
    ssl::{
        ExtensionContext, Ssl, SslAlert, SslContext, SslContextBuilder, SslMethod,
        SslStream as BlockingSslStream, SslVerifyMode, StatusType,
    },
    x509::X509,
};
use thiserror::Error;
use tokio::net::TcpStream;
use tokio_openssl::SslStream as TokioSslStream;

use super::{FingerprintProfile, ProfileMatchPolicy, tls_capture};

const OPENSSL_NATIVE_EC_POINT_FORMATS: &[u8] = &[0, 1, 2];
const OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION: u16 = 22;
const OPENSSL_STATUS_REQUEST_EXTENSION: u16 = 5;
const OPENSSL_NATIVE_EXTENSION_IDS: &[u16] =
    &[0, 10, 11, 13, 16, 21, 22, 23, 35, 43, 45, 51, 65281];
const OPENSSL_PREFLIGHT_TIMEOUT: Duration = Duration::from_secs(10);
const OPENSSL_PREFLIGHT_SNI: &str = "localhost";

#[derive(Debug)]
pub struct OpenSslMimicryAdapter {
    ssl_ctx: SslContext,
    client_hello_options: ClientHelloProfileOptions,
    preflight_passed: bool,
    preflight_extras: PreflightExtras,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
struct ClientHelloProfileOptions {
    status_request_ocsp: bool,
    custom_extension_ids: Vec<u16>,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct PreflightExtras {
    pub wire_extension_extras: Vec<u16>,
    pub wire_ec_point_format_extras: Vec<u8>,
}

#[derive(Debug, Error)]
pub enum OpenSslAdapterError {
    #[error("failed to build OpenSSL TLS context")]
    BuildContextFailed(#[source] ErrorStack),
    #[error("failed to connect TCP target")]
    ConnectFailed(#[source] std::io::Error),
    #[error("unsupported OpenSSL profile cipher suite 0x{0:04x}")]
    UnsupportedCipher(u16),
    #[error("unsupported OpenSSL profile supported group 0x{0:04x}")]
    UnsupportedGroup(u16),
    #[error("unsupported OpenSSL profile signature algorithm 0x{0:04x}")]
    UnsupportedSigalg(u16),
    #[error("unsupported OpenSSL profile extension {id}: {reason}")]
    UnsupportedExtension { id: u16, reason: &'static str },
    #[error("failed to apply OpenSSL fingerprint profile: {0}")]
    ProfileApplyFailed(String),
    #[error(
        "OpenSSL runtime preflight failed for {field}: expected {expected:?}, actual {actual:?}, missing {missing:?}, unexpected {unexpected:?}"
    )]
    PreflightFailed {
        field: &'static str,
        expected: Vec<u16>,
        actual: Vec<u16>,
        missing: Vec<u16>,
        unexpected: Vec<u16>,
    },
    #[error("OpenSSL runtime preflight capture failed: {0}")]
    PreflightCaptureFailed(String),
    #[error("failed to complete OpenSSL TLS handshake")]
    TlsHandshakeFailed(#[source] Box<dyn Error + Send + Sync>),
}

impl OpenSslMimicryAdapter {
    pub fn new() -> Result<Self, OpenSslAdapterError> {
        let mut builder = verified_context_builder()?;
        builder
            .set_default_verify_paths()
            .map_err(OpenSslAdapterError::BuildContextFailed)?;

        Ok(Self {
            ssl_ctx: builder.build(),
            client_hello_options: ClientHelloProfileOptions::default(),
            preflight_passed: false,
            preflight_extras: PreflightExtras::default(),
        })
    }

    pub fn new_with_profile(profile: &FingerprintProfile) -> Result<Self, OpenSslAdapterError> {
        let mut builder = verified_context_builder()?;
        builder
            .set_default_verify_paths()
            .map_err(OpenSslAdapterError::BuildContextFailed)?;
        Self::from_profile_builder(builder, profile)
    }

    pub fn new_with_profile_and_extra_trust_anchor(
        profile: &FingerprintProfile,
        ca_cert: X509,
    ) -> Result<Self, OpenSslAdapterError> {
        let mut builder = verified_context_builder()?;
        builder
            .set_default_verify_paths()
            .map_err(OpenSslAdapterError::BuildContextFailed)?;
        builder
            .cert_store_mut()
            .add_cert(ca_cert)
            .map_err(OpenSslAdapterError::BuildContextFailed)?;

        Self::from_profile_builder(builder, profile)
    }

    fn from_profile_builder(
        mut builder: SslContextBuilder,
        profile: &FingerprintProfile,
    ) -> Result<Self, OpenSslAdapterError> {
        apply_cipher_suites(&mut builder, profile)?;
        apply_alpn(&mut builder, profile)?;
        apply_supported_groups(&mut builder, profile)?;
        apply_signature_algorithms(&mut builder, profile)?;
        apply_ec_point_formats(&mut builder, profile)?;
        let client_hello_options = apply_extensions(&mut builder, profile)?;

        let mut adapter = Self {
            ssl_ctx: builder.build(),
            client_hello_options,
            preflight_passed: false,
            preflight_extras: PreflightExtras::default(),
        };
        adapter.preflight_extras = adapter.run_profile_preflight(profile)?;
        adapter.preflight_passed = true;
        Ok(adapter)
    }

    pub fn new_with_extra_trust_anchor(ca_cert: X509) -> Result<Self, OpenSslAdapterError> {
        let mut builder = verified_context_builder()?;
        builder
            .set_default_verify_paths()
            .map_err(OpenSslAdapterError::BuildContextFailed)?;
        builder
            .cert_store_mut()
            .add_cert(ca_cert)
            .map_err(OpenSslAdapterError::BuildContextFailed)?;

        Ok(Self {
            ssl_ctx: builder.build(),
            client_hello_options: ClientHelloProfileOptions::default(),
            preflight_passed: false,
            preflight_extras: PreflightExtras::default(),
        })
    }

    pub const fn preflight_passed(&self) -> bool {
        self.preflight_passed
    }

    pub const fn preflight_extras(&self) -> &PreflightExtras {
        &self.preflight_extras
    }

    pub async fn connect(
        &self,
        target: SocketAddr,
        sni: &str,
    ) -> Result<TokioSslStream<TcpStream>, OpenSslAdapterError> {
        let tcp_stream = TcpStream::connect(target)
            .await
            .map_err(OpenSslAdapterError::ConnectFailed)?;

        let mut ssl = Ssl::new(&self.ssl_ctx).map_err(tls_error)?;
        ssl.set_connect_state();
        ssl.set_hostname(sni).map_err(tls_error)?;
        ssl.param_mut().set_host(sni).map_err(tls_error)?;
        apply_connection_options(&mut ssl, &self.client_hello_options)?;

        let mut tls_stream = TokioSslStream::new(ssl, tcp_stream).map_err(tls_error)?;
        Pin::new(&mut tls_stream)
            .connect()
            .await
            .map_err(tls_error)?;

        Ok(tls_stream)
    }

    fn run_profile_preflight(
        &self,
        profile: &FingerprintProfile,
    ) -> Result<PreflightExtras, OpenSslAdapterError> {
        let captured = self.capture_runtime_client_hello()?;
        let wire_ec_point_format_extras = verify_ec_point_formats_preflight(profile, &captured)?;
        let mut extras = verify_extensions_preflight(profile, &captured)?;
        extras.wire_ec_point_format_extras = wire_ec_point_format_extras;
        Ok(extras)
    }

    fn capture_runtime_client_hello(
        &self,
    ) -> Result<tls_capture::CapturedClientHello, OpenSslAdapterError> {
        let listener = StdTcpListener::bind("127.0.0.1:0").map_err(|source| {
            OpenSslAdapterError::PreflightCaptureFailed(format!(
                "binding local capture listener failed: {source}"
            ))
        })?;
        listener.set_nonblocking(true).map_err(|source| {
            OpenSslAdapterError::PreflightCaptureFailed(format!(
                "setting local capture listener nonblocking failed: {source}"
            ))
        })?;
        let capture_addr = listener.local_addr().map_err(|source| {
            OpenSslAdapterError::PreflightCaptureFailed(format!(
                "reading local capture listener address failed: {source}"
            ))
        })?;

        let capture_thread = thread::spawn(move || capture_first_client_hello(listener));
        let client_result =
            drive_preflight_client(&self.ssl_ctx, &self.client_hello_options, capture_addr);

        let captured = capture_thread
            .join()
            .map_err(|_| {
                OpenSslAdapterError::PreflightCaptureFailed(
                    "local capture thread panicked".to_owned(),
                )
            })?
            .map_err(|capture_error| {
                let client_context = client_result
                    .as_ref()
                    .err()
                    .map(|error| format!("; client_result={error}"))
                    .unwrap_or_default();
                OpenSslAdapterError::PreflightCaptureFailed(format!(
                    "{capture_error}{client_context}"
                ))
            })?;

        Ok(captured)
    }
}

fn verified_context_builder() -> Result<SslContextBuilder, OpenSslAdapterError> {
    let mut builder =
        SslContext::builder(SslMethod::tls()).map_err(OpenSslAdapterError::BuildContextFailed)?;
    builder.set_verify(SslVerifyMode::PEER);
    Ok(builder)
}

fn apply_cipher_suites(
    builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<(), OpenSslAdapterError> {
    let mut tls13_names = Vec::new();
    let mut legacy_names = Vec::new();

    for cipher_id in &profile.tls.cipher_suites {
        let name = cipher_id_to_name(*cipher_id)
            .ok_or(OpenSslAdapterError::UnsupportedCipher(*cipher_id))?;
        if (0x1301..=0x1305).contains(cipher_id) {
            tls13_names.push(name);
        } else {
            legacy_names.push(name);
        }
    }

    if !legacy_names.is_empty() {
        // OpenSSL cipher-list 语法接受冒号/空格分隔；这里保留模板顺序。
        builder
            .set_cipher_list(&legacy_names.join(" "))
            .map_err(|error| {
                OpenSslAdapterError::ProfileApplyFailed(format!(
                    "set_cipher_list failed for legacy cipher_suites: {error}"
                ))
            })?;
    }

    if !tls13_names.is_empty() {
        builder
            .set_ciphersuites(&tls13_names.join(":"))
            .map_err(|error| {
                OpenSslAdapterError::ProfileApplyFailed(format!(
                    "set_ciphersuites failed for TLS 1.3 cipher_suites: {error}"
                ))
            })?;
    }

    Ok(())
}

fn apply_alpn(
    builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<(), OpenSslAdapterError> {
    if profile.tls.alpn_protocols.is_empty() {
        return Ok(());
    }

    let wire_format = alpn_wire_format(&profile.tls.alpn_protocols)?;
    builder.set_alpn_protos(&wire_format).map_err(|error| {
        OpenSslAdapterError::ProfileApplyFailed(format!("set_alpn_protos failed: {error}"))
    })
}

fn alpn_wire_format(protocols: &[String]) -> Result<Vec<u8>, OpenSslAdapterError> {
    let mut wire_format = Vec::new();
    for protocol in protocols {
        let bytes = protocol.as_bytes();
        if bytes.is_empty() || bytes.len() > u8::MAX as usize {
            return Err(OpenSslAdapterError::ProfileApplyFailed(format!(
                "invalid ALPN protocol length for {protocol:?}"
            )));
        }
        wire_format.push(bytes.len() as u8);
        wire_format.extend(bytes);
    }
    Ok(wire_format)
}

fn apply_supported_groups(
    builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<(), OpenSslAdapterError> {
    if profile.tls.supported_groups.is_empty() {
        return Ok(());
    }

    let mut group_names = Vec::new();
    for group_id in &profile.tls.supported_groups {
        group_names.push(supported_group_id_to_name(*group_id)?);
    }

    builder
        .set_groups_list(&group_names.join(":"))
        .map_err(|error| {
            OpenSslAdapterError::ProfileApplyFailed(format!(
                "set_groups_list failed for supported_groups: {error}"
            ))
        })
}

fn apply_signature_algorithms(
    builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<(), OpenSslAdapterError> {
    if profile.tls.signature_algorithms.is_empty() {
        return Ok(());
    }

    let mut sigalg_names = Vec::new();
    for sigalg_id in &profile.tls.signature_algorithms {
        sigalg_names.push(signature_algorithm_id_to_name(*sigalg_id)?);
    }

    builder
        .set_sigalgs_list(&sigalg_names.join(":"))
        .map_err(|error| {
            OpenSslAdapterError::ProfileApplyFailed(format!(
                "set_sigalgs_list failed for signature_algorithms: {error}"
            ))
        })
}

fn apply_ec_point_formats(
    _builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<(), OpenSslAdapterError> {
    if profile.tls.ec_point_formats == OPENSSL_NATIVE_EC_POINT_FORMATS {
        return Ok(());
    }

    if profile.match_policy() == ProfileMatchPolicy::SampleSetRandomized
        && is_ordered_u8_subset(
            &profile.tls.ec_point_formats,
            OPENSSL_NATIVE_EC_POINT_FORMATS,
        )
    {
        return Ok(());
    }

    // rust-openssl / OpenSSL 当前不暴露 setter，且 custom extension 不能覆盖内建 type 11。
    Err(OpenSslAdapterError::ProfileApplyFailed(format!(
        "unsupported ec_point_formats {:?}; OpenSSL native client profile only exposes {:?}",
        profile.tls.ec_point_formats, OPENSSL_NATIVE_EC_POINT_FORMATS
    )))
}

fn apply_extensions(
    builder: &mut SslContextBuilder,
    profile: &FingerprintProfile,
) -> Result<ClientHelloProfileOptions, OpenSslAdapterError> {
    let allows_native_extras = profile.match_policy() == ProfileMatchPolicy::SampleSetRandomized;
    if !profile
        .tls
        .extensions
        .contains(&OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION)
        && !allows_native_extras
    {
        return Err(OpenSslAdapterError::UnsupportedExtension {
            id: OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION,
            reason: "OpenSSL cannot disable native ETM extension via public API",
        });
    }

    let mut options = ClientHelloProfileOptions::default();
    for extension_id in &profile.tls.extensions {
        if *extension_id == OPENSSL_STATUS_REQUEST_EXTENSION {
            options.status_request_ocsp = true;
            continue;
        }
        if OPENSSL_NATIVE_EXTENSION_IDS.contains(extension_id) {
            continue;
        }

        add_empty_client_hello_extension(builder, *extension_id)?;
        options.custom_extension_ids.push(*extension_id);
    }

    Ok(options)
}

fn verify_ec_point_formats_preflight(
    profile: &FingerprintProfile,
    captured: &tls_capture::CapturedClientHello,
) -> Result<Vec<u8>, OpenSslAdapterError> {
    let allows_native_extras = profile.match_policy() == ProfileMatchPolicy::SampleSetRandomized;
    let exact_match = captured.ec_point_formats == profile.tls.ec_point_formats;
    let subset_match = allows_native_extras
        && is_ordered_u8_subset(&profile.tls.ec_point_formats, &captured.ec_point_formats);
    if exact_match || subset_match {
        return Ok(captured
            .ec_point_formats
            .iter()
            .copied()
            .filter(|value| !profile.tls.ec_point_formats.contains(value))
            .collect());
    }

    Err(OpenSslAdapterError::PreflightFailed {
        field: "ec_point_formats",
        expected: profile
            .tls
            .ec_point_formats
            .iter()
            .copied()
            .map(u16::from)
            .collect(),
        actual: captured
            .ec_point_formats
            .iter()
            .map(|value| u16::from(*value))
            .collect(),
        missing: profile
            .tls
            .ec_point_formats
            .iter()
            .copied()
            .filter(|value| !captured.ec_point_formats.contains(value))
            .map(u16::from)
            .collect(),
        unexpected: captured
            .ec_point_formats
            .iter()
            .copied()
            .filter(|value| !profile.tls.ec_point_formats.contains(value))
            .map(u16::from)
            .collect(),
    })
}

fn verify_extensions_preflight(
    profile: &FingerprintProfile,
    captured: &tls_capture::CapturedClientHello,
) -> Result<PreflightExtras, OpenSslAdapterError> {
    let check = check_extension_subset(
        &profile.tls.extensions,
        &captured.extensions,
        profile.match_policy(),
    );
    if check.matches {
        return Ok(PreflightExtras {
            wire_extension_extras: check.unexpected,
            wire_ec_point_format_extras: Vec::new(),
        });
    }

    Err(OpenSslAdapterError::PreflightFailed {
        field: "extensions",
        expected: profile.tls.extensions.to_vec(),
        actual: captured.extensions.clone(),
        missing: check.missing,
        unexpected: check.unexpected,
    })
}

#[derive(Debug, Clone, PartialEq, Eq)]
struct ExtensionSubsetCheck {
    matches: bool,
    missing: Vec<u16>,
    unexpected: Vec<u16>,
}

fn check_extension_subset(
    expected: &[u16],
    actual: &[u16],
    match_policy: ProfileMatchPolicy,
) -> ExtensionSubsetCheck {
    let missing = expected
        .iter()
        .copied()
        .filter(|extension| !actual.contains(extension))
        .collect::<Vec<_>>();
    let unexpected = actual
        .iter()
        .copied()
        .filter(|extension| !expected.contains(extension))
        .collect::<Vec<_>>();
    let matches = missing.is_empty()
        && match match_policy {
            ProfileMatchPolicy::SampleSetRandomized => true,
            ProfileMatchPolicy::ExactStable | ProfileMatchPolicy::KnownGapBlocked => {
                is_ordered_u16_subset(expected, actual)
            }
        };

    ExtensionSubsetCheck {
        matches,
        missing,
        unexpected,
    }
}

fn add_empty_client_hello_extension(
    builder: &mut SslContextBuilder,
    extension_id: u16,
) -> Result<(), OpenSslAdapterError> {
    builder
        .add_custom_ext(
            extension_id,
            ExtensionContext::CLIENT_HELLO,
            |_, _, _| -> Result<Option<Vec<u8>>, SslAlert> { Ok(Some(Vec::new())) },
            |_, _, _, _| -> Result<(), SslAlert> { Ok(()) },
        )
        .map_err(|error| OpenSslAdapterError::UnsupportedExtension {
            id: extension_id,
            reason: Box::leak(
                format!("OpenSSL rejected custom ClientHello extension: {error}").into_boxed_str(),
            ),
        })
}

fn capture_first_client_hello(
    listener: StdTcpListener,
) -> Result<tls_capture::CapturedClientHello, tls_capture::CaptureError> {
    let started = Instant::now();
    loop {
        match listener.accept() {
            Ok((mut stream, _)) => {
                stream
                    .set_read_timeout(Some(OPENSSL_PREFLIGHT_TIMEOUT))
                    .map_err(|source| tls_capture::CaptureError::Io {
                        context: "setting TLS capture read timeout",
                        source,
                    })?;
                return tls_capture::capture_from_std_stream(&mut stream);
            }
            Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                if started.elapsed() >= OPENSSL_PREFLIGHT_TIMEOUT {
                    return Err(tls_capture::CaptureError::Timeout(
                        "accepting first TLS client",
                    ));
                }
                thread::sleep(Duration::from_millis(10));
            }
            Err(source) => {
                return Err(tls_capture::CaptureError::Io {
                    context: "accepting first TLS client",
                    source,
                });
            }
        }
    }
}

fn drive_preflight_client(
    ssl_ctx: &SslContext,
    options: &ClientHelloProfileOptions,
    target: SocketAddr,
) -> Result<(), OpenSslAdapterError> {
    let tcp_stream = StdTcpStream::connect(target).map_err(OpenSslAdapterError::ConnectFailed)?;
    tcp_stream
        .set_read_timeout(Some(OPENSSL_PREFLIGHT_TIMEOUT))
        .map_err(OpenSslAdapterError::ConnectFailed)?;
    tcp_stream
        .set_write_timeout(Some(OPENSSL_PREFLIGHT_TIMEOUT))
        .map_err(OpenSslAdapterError::ConnectFailed)?;

    let mut ssl = Ssl::new(ssl_ctx).map_err(tls_error)?;
    ssl.set_connect_state();
    ssl.set_hostname(OPENSSL_PREFLIGHT_SNI).map_err(tls_error)?;
    ssl.param_mut()
        .set_host(OPENSSL_PREFLIGHT_SNI)
        .map_err(tls_error)?;
    apply_connection_options(&mut ssl, options)?;

    let mut tls_stream = BlockingSslStream::new(ssl, tcp_stream).map_err(tls_error)?;
    tls_stream.connect().map_err(tls_error)
}

fn apply_connection_options(
    ssl: &mut Ssl,
    options: &ClientHelloProfileOptions,
) -> Result<(), OpenSslAdapterError> {
    if options.status_request_ocsp {
        ssl.set_status_type(StatusType::OCSP).map_err(tls_error)?;
    }
    Ok(())
}

fn is_ordered_u16_subset(expected_subset: &[u16], actual: &[u16]) -> bool {
    let mut actual_iter = actual.iter();
    expected_subset
        .iter()
        .all(|expected| actual_iter.any(|actual_value| actual_value == expected))
}

fn is_ordered_u8_subset(expected_subset: &[u8], actual: &[u8]) -> bool {
    let mut actual_iter = actual.iter();
    expected_subset
        .iter()
        .all(|expected| actual_iter.any(|actual_value| actual_value == expected))
}

fn cipher_id_to_name(id: u16) -> Option<&'static str> {
    match id {
        0x1301 => Some("TLS_AES_128_GCM_SHA256"),
        0x1302 => Some("TLS_AES_256_GCM_SHA384"),
        0x1303 => Some("TLS_CHACHA20_POLY1305_SHA256"),
        0x002f => Some("AES128-SHA"),
        0x0033 => Some("DHE-RSA-AES128-SHA"),
        0x0035 => Some("AES256-SHA"),
        0x0039 => Some("DHE-RSA-AES256-SHA"),
        0x003c => Some("AES128-SHA256"),
        0x003d => Some("AES256-SHA256"),
        0x0067 => Some("DHE-RSA-AES128-SHA256"),
        0x006b => Some("DHE-RSA-AES256-SHA256"),
        0x009c => Some("AES128-GCM-SHA256"),
        0x009d => Some("AES256-GCM-SHA384"),
        0x009e => Some("DHE-RSA-AES128-GCM-SHA256"),
        0x009f => Some("DHE-RSA-AES256-GCM-SHA384"),
        0xc009 => Some("ECDHE-ECDSA-AES128-SHA"),
        0xc00a => Some("ECDHE-ECDSA-AES256-SHA"),
        0xc013 => Some("ECDHE-RSA-AES128-SHA"),
        0xc014 => Some("ECDHE-RSA-AES256-SHA"),
        0xc023 => Some("ECDHE-ECDSA-AES128-SHA256"),
        0xc024 => Some("ECDHE-ECDSA-AES256-SHA384"),
        0xc027 => Some("ECDHE-RSA-AES128-SHA256"),
        0xc028 => Some("ECDHE-RSA-AES256-SHA384"),
        0xc02b => Some("ECDHE-ECDSA-AES128-GCM-SHA256"),
        0xc02c => Some("ECDHE-ECDSA-AES256-GCM-SHA384"),
        0xc02f => Some("ECDHE-RSA-AES128-GCM-SHA256"),
        0xc030 => Some("ECDHE-RSA-AES256-GCM-SHA384"),
        0xcca8 => Some("ECDHE-RSA-CHACHA20-POLY1305"),
        0xcca9 => Some("ECDHE-ECDSA-CHACHA20-POLY1305"),
        0xccaa => Some("DHE-RSA-CHACHA20-POLY1305"),
        _ => None,
    }
}

fn supported_group_id_to_name(id: u16) -> Result<&'static str, OpenSslAdapterError> {
    let candidates = match id {
        0x11ec => &["X25519MLKEM768"][..],
        0x001d => &["X25519"][..],
        0x0017 => &["P-256"][..],
        0x001e => &["X448"][..],
        0x0018 => &["P-384"][..],
        0x0019 => &["P-521"][..],
        0x0100 => &["ffdhe2048"][..],
        0x0101 => &["ffdhe3072"][..],
        _ => return Err(OpenSslAdapterError::UnsupportedGroup(id)),
    };

    for candidate in candidates {
        if openssl_accepts_group_name(candidate)? {
            return Ok(candidate);
        }
    }

    Err(OpenSslAdapterError::UnsupportedGroup(id))
}

fn signature_algorithm_id_to_name(id: u16) -> Result<&'static str, OpenSslAdapterError> {
    let name = match id {
        0x0904 => "mldsa44",
        0x0905 => "mldsa65",
        0x0906 => "mldsa87",
        0x0403 => "ecdsa_secp256r1_sha256",
        0x0503 => "ecdsa_secp384r1_sha384",
        0x0603 => "ecdsa_secp521r1_sha512",
        0x0807 => "ed25519",
        0x0808 => "ed448",
        0x081a => "ecdsa_brainpoolP256r1tls13_sha256",
        0x081b => "ecdsa_brainpoolP384r1tls13_sha384",
        0x081c => "ecdsa_brainpoolP512r1tls13_sha512",
        0x0809 => "rsa_pss_pss_sha256",
        0x080a => "rsa_pss_pss_sha384",
        0x080b => "rsa_pss_pss_sha512",
        0x0804 => "rsa_pss_rsae_sha256",
        0x0805 => "rsa_pss_rsae_sha384",
        0x0806 => "rsa_pss_rsae_sha512",
        0x0401 => "rsa_pkcs1_sha256",
        0x0501 => "rsa_pkcs1_sha384",
        0x0601 => "rsa_pkcs1_sha512",
        0x0201 => "rsa_pkcs1_sha1",
        0x0303 => "ecdsa_sha224",
        0x0301 => "rsa_pkcs1_sha224",
        0x0302 => "dsa_sha224",
        0x0402 => "dsa_sha256",
        0x0502 => "dsa_sha384",
        0x0602 => "dsa_sha512",
        _ => return Err(OpenSslAdapterError::UnsupportedSigalg(id)),
    };

    if openssl_accepts_sigalg_name(name)? {
        Ok(name)
    } else {
        Err(OpenSslAdapterError::UnsupportedSigalg(id))
    }
}

fn openssl_accepts_group_name(name: &str) -> Result<bool, OpenSslAdapterError> {
    let mut builder = verified_context_builder()?;
    Ok(builder.set_groups_list(name).is_ok())
}

fn openssl_accepts_sigalg_name(name: &str) -> Result<bool, OpenSslAdapterError> {
    let mut builder = verified_context_builder()?;
    Ok(builder.set_sigalgs_list(name).is_ok())
}

fn tls_error<E>(source: E) -> OpenSslAdapterError
where
    E: Error + Send + Sync + 'static,
{
    OpenSslAdapterError::TlsHandshakeFailed(Box::new(source))
}
