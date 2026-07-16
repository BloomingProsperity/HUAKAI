use boring::ssl::{ConnectConfiguration, SslConnector, SslMethod, SslVersion};
use thiserror::Error;
use tokio::{
    io::{AsyncRead, AsyncReadExt},
    time::{Duration, timeout},
};

use crate::profile::TlsProfile;

// build_connector 是无 ALPN 覆盖的公共构造器,保留以兼容既有调用方;crate 内部统一走
// build_connector_with_alpn,故这里标注 allow(dead_code) 避免无引用告警。
#[allow(dead_code)]
pub fn build_connector(profile: &TlsProfile) -> Result<SslConnector, BoringCtxError> {
    build_connector_with_alpn(profile, None)
}

// build_connector_with_alpn 在 build_connector 基础上支持 ALPN 覆盖。
// alpn_override=Some(list) 时,握手广告的 ALPN 用 list 而非 profile.alpn——force_h1
// 场景传 ["http/1.1"],从根上不广告 h2。None 时与历史行为完全一致(按 profile.alpn 广告)。
pub fn build_connector_with_alpn(
    profile: &TlsProfile,
    alpn_override: Option<&[String]>,
) -> Result<SslConnector, BoringCtxError> {
    let mut builder = SslConnector::builder(SslMethod::tls()).map_err(BoringCtxError::from)?;
    builder.set_grease_enabled(profile.grease);
    builder.set_permute_extensions(false);
    apply_protocol_bounds(&mut builder, &profile.supported_versions)?;

    if !profile.cipher_list.is_empty() {
        builder
            .set_cipher_list(&profile.cipher_list)
            .map_err(BoringCtxError::from)?;
    }
    if !profile.tls13_cipher_order.is_empty() {
        builder
            .set_tls13_cipher_order(&profile.tls13_cipher_order)
            .map_err(BoringCtxError::from)?;
    }
    if !profile.curves.is_empty() {
        builder
            .set_curves_list(&profile.curves)
            .map_err(BoringCtxError::from)?;
    }
    if !profile.client_hello_profile.is_empty() {
        let ciphers = client_hello_profile_ciphers(profile);
        let ec_points = client_hello_ec_points_as_u8(&profile.client_hello_profile.ec_points)?;
        builder
            .set_client_hello_profile(&ciphers, &profile.client_hello_profile.groups, &ec_points)
            .map_err(BoringCtxError::from)?;
    }
    // ALPN 来源:override 优先(force_h1 收窄为仅 http/1.1),否则按 profile.alpn 广告。
    let alpn_protocols = alpn_override.unwrap_or(&profile.alpn);
    let alpn = serialize_alpn(alpn_protocols)?;
    if !alpn.is_empty() {
        builder
            .set_alpn_protos(&alpn)
            .map_err(BoringCtxError::from)?;
    }
    if !profile.signature_algorithms.is_empty() {
        builder
            .set_raw_verify_algorithm_prefs(&profile.signature_algorithms)
            .map_err(BoringCtxError::from)?;
    } else if !profile.sigalgs.is_empty() {
        builder
            .set_sigalgs_list(&profile.sigalgs)
            .map_err(BoringCtxError::from)?;
    }
    if profile.extensions.contains(&5) {
        builder.enable_ocsp_stapling();
    }
    if profile.extensions.contains(&18) {
        builder.enable_signed_cert_timestamps();
    }
    if !profile.extension_order.is_empty() {
        builder
            .set_extension_order(&profile.extension_order)
            .map_err(BoringCtxError::from)?;
    }
    Ok(builder.build())
}

pub fn connect_config(profile: &TlsProfile) -> Result<ConnectConfiguration, BoringCtxError> {
    connect_config_with_alpn(profile, None)
}

// connect_config_force_h1 构造一个握手只广告 ALPN=http/1.1 的连接配置。
// force_h1=true 时收窄 ALPN,服务端无从选 h2,connect.rs 的 selected_alpn 不可能等于 b"h2",
// 必走 Raw 隧道,H2 bridge 分支被从根绕开;force_h1=false 时退回 profile.alpn(=今日行为)。
pub fn connect_config_force_h1(
    profile: &TlsProfile,
    force_h1: bool,
) -> Result<ConnectConfiguration, BoringCtxError> {
    if force_h1 {
        let http11 = [HTTP_1_1_ALPN.to_owned()];
        connect_config_with_alpn(profile, Some(&http11))
    } else {
        connect_config_with_alpn(profile, None)
    }
}

fn connect_config_with_alpn(
    profile: &TlsProfile,
    alpn_override: Option<&[String]>,
) -> Result<ConnectConfiguration, BoringCtxError> {
    let connector = build_connector_with_alpn(profile, alpn_override)?;
    let config = connector.configure().map_err(BoringCtxError::from)?;
    if profile.extensions.contains(&65037) {
        config.set_enable_ech_grease(true);
    }
    Ok(config)
}

// 强制 H1 时唯一广告的 ALPN 协议标识。
const HTTP_1_1_ALPN: &str = "http/1.1";

pub async fn validate_expected_ja4_before_connect(
    profile: &TlsProfile,
    target_host: &str,
) -> Result<(), BoringCtxError> {
    if !crate::ja4::profile_has_expectation(profile) {
        return Ok(());
    }
    let raw = capture_client_hello_record(profile, target_host).await?;
    let actual = crate::ja4::Ja4Fingerprint::from_tls_client_hello_record(&raw)?;
    crate::ja4::verify_profile_expectation(profile, &actual)?;
    Ok(())
}

pub async fn capture_client_hello_record(
    profile: &TlsProfile,
    target_host: &str,
) -> Result<Vec<u8>, BoringCtxError> {
    let (client, server) = tokio::io::duplex(16 * 1024);
    let capture = tokio::spawn(async move { read_first_tls_record(server).await });
    let config = connect_config(profile)?;
    let target_host = target_host.to_owned();
    let handshake = tokio::spawn(async move {
        let _ = timeout(
            Duration::from_secs(1),
            tokio_boring::connect(config, target_host.as_str(), client),
        )
        .await;
    });
    let raw = capture
        .await
        .map_err(|error| BoringCtxError::ClientHelloCapture(error.to_string()))?
        .map_err(|error| BoringCtxError::ClientHelloCapture(error.to_string()))?;
    let _ = handshake.await;
    Ok(raw)
}

#[cfg(test)]
pub fn ja3_from_profile(profile: &TlsProfile) -> String {
    [
        ja3_legacy_version(&profile.supported_versions).to_string(),
        join_u16(&profile.cipher_suites),
        join_u16(&profile.extensions),
        join_u16(&profile.supported_groups),
        join_u8(&profile.ec_point_formats),
    ]
    .join(",")
}

// 标准 JA3 首字段取 ClientHello 的 legacy_version(record 版本),
// TLS1.3 通过 supported_versions 扩展协商而非 record 版本,因此 0x0304 不会出现在 record。
// 对 [772, 771] 这种 TLS1.3 hello,record 版本固定为 0x0303 = 771。
#[cfg(test)]
fn ja3_legacy_version(versions: &[u16]) -> u16 {
    versions
        .iter()
        .copied()
        .filter(|value| !is_grease(*value) && *value != 0x0304)
        .max()
        .unwrap_or(0)
}

#[derive(Debug, Error)]
pub enum BoringCtxError {
    #[error("boring API error: {0}")]
    Boring(String),
    #[error("unknown TLS version code: {0}")]
    UnknownTlsVersion(u16),
    #[error("ALPN protocol is empty")]
    EmptyAlpnProtocol,
    #[error("ALPN protocol too long: {0} bytes")]
    AlpnProtocolTooLong(usize),
    #[error("ClientHello profile ec_point format is too large for u8: {0}")]
    EcPointFormatTooLarge(u16),
    #[error("ClientHello capture error: {0}")]
    ClientHelloCapture(String),
    #[error(transparent)]
    Ja4(#[from] crate::ja4::Ja4Error),
}

impl From<boring::error::ErrorStack> for BoringCtxError {
    fn from(error: boring::error::ErrorStack) -> Self {
        Self::Boring(error.to_string())
    }
}

fn apply_protocol_bounds(
    builder: &mut boring::ssl::SslConnectorBuilder,
    versions: &[u16],
) -> Result<(), BoringCtxError> {
    let min = versions
        .iter()
        .copied()
        .filter(|value| !is_grease(*value))
        .min()
        .map(ssl_version_from_code)
        .transpose()?;
    let max = versions
        .iter()
        .copied()
        .filter(|value| !is_grease(*value))
        .max()
        .map(ssl_version_from_code)
        .transpose()?;
    builder
        .set_min_proto_version(min)
        .map_err(BoringCtxError::from)?;
    builder
        .set_max_proto_version(max)
        .map_err(BoringCtxError::from)?;
    Ok(())
}

fn ssl_version_from_code(code: u16) -> Result<SslVersion, BoringCtxError> {
    match code {
        0x0301 => Ok(SslVersion::TLS1),
        0x0302 => Ok(SslVersion::TLS1_1),
        0x0303 => Ok(SslVersion::TLS1_2),
        0x0304 => Ok(SslVersion::TLS1_3),
        _ => Err(BoringCtxError::UnknownTlsVersion(code)),
    }
}

fn serialize_alpn(protocols: &[String]) -> Result<Vec<u8>, BoringCtxError> {
    let mut out = Vec::new();
    for protocol in protocols {
        let bytes = protocol.as_bytes();
        if bytes.is_empty() {
            return Err(BoringCtxError::EmptyAlpnProtocol);
        }
        if bytes.len() > u8::MAX as usize {
            return Err(BoringCtxError::AlpnProtocolTooLong(bytes.len()));
        }
        out.push(bytes.len() as u8);
        out.extend_from_slice(bytes);
    }
    Ok(out)
}

fn client_hello_ec_points_as_u8(values: &[u16]) -> Result<Vec<u8>, BoringCtxError> {
    values
        .iter()
        .copied()
        .map(|value| u8::try_from(value).map_err(|_| BoringCtxError::EcPointFormatTooLarge(value)))
        .collect()
}

fn client_hello_profile_ciphers(profile: &TlsProfile) -> Vec<u16> {
    if profile.client_hello_profile.ciphers.is_empty() {
        return Vec::new();
    }
    let mut out = Vec::new();
    for cipher in profile
        .tls13_cipher_order
        .iter()
        .chain(profile.client_hello_profile.ciphers.iter())
        .copied()
    {
        if !out.contains(&cipher) {
            out.push(cipher);
        }
    }
    out
}

async fn read_first_tls_record<R>(mut stream: R) -> std::io::Result<Vec<u8>>
where
    R: AsyncRead + Unpin,
{
    let mut header = [0u8; 5];
    stream.read_exact(&mut header).await?;
    let record_len = u16::from_be_bytes([header[3], header[4]]) as usize;
    let mut body = vec![0u8; record_len];
    stream.read_exact(&mut body).await?;
    let mut raw = header.to_vec();
    raw.extend_from_slice(&body);
    Ok(raw)
}

#[cfg(test)]
fn join_u16(values: &[u16]) -> String {
    values
        .iter()
        .copied()
        .filter(|value| !is_grease(*value))
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join("-")
}

#[cfg(test)]
fn join_u8(values: &[u8]) -> String {
    values
        .iter()
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join("-")
}

fn is_grease(value: u16) -> bool {
    value & 0x0f0f == 0x0a0a && (value >> 8) == (value & 0x00ff)
}

#[cfg(test)]
mod tests {
    use tokio::{
        io::{AsyncReadExt, DuplexStream},
        time::{Duration, timeout},
    };

    #[test]
    fn profile_ja3_changes_when_cipher_order_is_damaged() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();
        let good = super::ja3_from_profile(profile);
        let mut damaged = profile.clone();
        damaged.cipher_suites.reverse();
        let bad = super::ja3_from_profile(&damaged);

        assert_eq!(good, profile.expected_ja3);
        assert_ne!(bad, profile.expected_ja3);
    }

    // 三家新 profile 的 JA4 自洽:profile 里存的 ja4_a/b/c 必须等于 sidecar 基线握手实际
    // emit 的 ClientHello 算出的值——即 validate_expected_ja4_before_connect 必过。这咬住
    // "上线前存值 == 真实线缆指纹",存错(如漏 ALPN 段、用错哈希)会直接 fail-closed 断出口。
    // 自证:对每家再克隆一份把 ja4_a 改一个字符,validate 必转红(证明本测试判别有效)。
    #[tokio::test]
    async fn new_profiles_ja4_expectation_matches_wire_and_mutation_fails() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        for (id, host) in [
            ("openai-codex-cli-v1", "chatgpt.com"),
            ("gemini-cli-v1", "cloudcode-pa.googleapis.com"),
            ("kiro-cli-v1", "q.us-east-1.amazonaws.com"),
        ] {
            let profile = profiles.get(id).unwrap();
            // 存值 == 实际 emit:validate 必过。
            super::validate_expected_ja4_before_connect(profile, host)
                .await
                .unwrap_or_else(|error| panic!("{id} ja4 期望与真实线缆不符: {error}"));

            // 自证判别:把 ja4_a 尾字符改坏,validate 必报 ja4_a 不匹配。
            let mut mutated = profile.clone();
            let mut a = mutated.ja4_a.take().unwrap();
            a.pop();
            a.push('z');
            mutated.ja4_a = Some(a);
            let err = super::validate_expected_ja4_before_connect(&mutated, host)
                .await
                .expect_err(&format!("{id} 变异 ja4_a 后 validate 必须转红"));
            assert!(
                err.to_string().contains("ja4_a"),
                "{id} 变异应命中 ja4_a 段: {err}"
            );
        }
    }

    #[tokio::test]
    async fn boring_wire_ja3_matches_profile_and_changes_when_cipher_order_is_damaged() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();

        let good = capture_wire_ja3(profile.clone()).await;
        let mut damaged = profile.clone();
        damaged.cipher_suites.reverse();
        damaged.client_hello_profile.ciphers.reverse();
        damaged.cipher_list = damaged
            .cipher_list
            .split(':')
            .rev()
            .collect::<Vec<_>>()
            .join(":");
        damaged.tls13_cipher_order.reverse();
        let bad = capture_wire_ja3(damaged).await;

        // boring 不会为本 profile 合成 padding 扩展(21),因此 boring 线缆 JA3 是真 Claude
        // 权威 expected_ja3 去掉扩展段尾部 "-21" 后的子集;sidecar 是温和近似而非逐字节复刻。
        let expected_wire_ja3 = profile.expected_ja3.replace("-21,", ",");
        assert_eq!(good, expected_wire_ja3);
        assert_ne!(bad, expected_wire_ja3);
        assert_ne!(bad, good);
    }

    #[tokio::test]
    async fn boring_extension_order_profile_controls_wire_order() {
        let profile = anthropic_profile();

        let good = capture_wire_client_hello(&profile).await;
        let good_order = good.extensions_without_grease_or_padding();

        // boring 不会为本 profile 合成 padding 扩展(21),故线缆顺序 = extension_order 去掉 21。
        let expected_order: Vec<u16> = profile
            .extension_order
            .iter()
            .copied()
            .filter(|value| *value != 21)
            .collect();
        assert_eq!(good_order, expected_order);
        assert!(
            !good_order.is_empty(),
            "fixture must emit a controlled extension order"
        );

        // 把 supported_versions 扩展(43)挪到队首,证明 extension_order 真正控制线缆顺序。
        let mut damaged = profile.clone();
        damaged.extension_order.retain(|value| *value != 43);
        damaged.extension_order.insert(0, 43);
        let damaged_wire = capture_wire_client_hello(&damaged).await;
        let damaged_order = damaged_wire.extensions_without_grease_or_padding();

        assert_eq!(damaged_order.first(), Some(&43));
        assert_ne!(damaged_order, good_order);
    }

    #[tokio::test]
    async fn boring_tls13_cipher_order_profile_controls_wire_cipher_prefix() {
        let profile = anthropic_profile();

        let good = capture_wire_client_hello(&profile).await;
        let tls13_len = profile.tls13_cipher_order.len();
        assert_eq!(
            &good.ciphers[..tls13_len],
            profile.tls13_cipher_order.as_slice()
        );

        let mut damaged = profile.clone();
        damaged.tls13_cipher_order.reverse();
        let damaged_wire = capture_wire_client_hello(&damaged).await;

        assert_eq!(
            &damaged_wire.ciphers[..tls13_len],
            damaged.tls13_cipher_order.as_slice()
        );
        assert_ne!(damaged_wire.ciphers, good.ciphers);
    }

    #[tokio::test]
    async fn boring_client_hello_profile_controls_raw_ciphers_groups_and_ec_points() {
        let profile = anthropic_profile();

        let good = capture_wire_client_hello(&profile).await;
        assert_eq!(good.ciphers, expected_profile_ciphers(&profile));
        assert_eq!(good.supported_groups, profile.client_hello_profile.groups);
        assert_eq!(
            good.ec_point_formats,
            u16_values_as_u8(&profile.client_hello_profile.ec_points)
        );
        // 真 Claude 只广告单一未压缩点格式 [0]。
        assert_eq!(good.ec_point_formats, [0]);

        // client_hello_profile.groups 真正控制线缆 supported_groups:reverse 后线缆必须随之变。
        let mut damaged = profile.clone();
        damaged.client_hello_profile.groups.reverse();
        let damaged_wire = capture_wire_client_hello(&damaged).await;

        assert_eq!(
            damaged_wire.supported_groups,
            damaged.client_hello_profile.groups
        );
        assert_ne!(damaged_wire.supported_groups, good.supported_groups);
    }

    #[tokio::test]
    async fn boring_signature_algorithms_profile_controls_wire_bytes() {
        let profile = anthropic_profile();

        let good = capture_wire_client_hello(&profile).await;

        assert_eq!(profile.signature_algorithms.len(), 9);
        assert_eq!(good.signature_algorithms, profile.signature_algorithms);

        // signature_algorithms 顺序是指纹的一部分(JA4 c 段不排序);reverse 后线缆字节必须随之变。
        let mut damaged = profile.clone();
        damaged.signature_algorithms.reverse();
        let damaged_wire = capture_wire_client_hello(&damaged).await;

        assert_eq!(
            damaged_wire.signature_algorithms,
            damaged.signature_algorithms
        );
        assert_ne!(damaged_wire.signature_algorithms, good.signature_algorithms);
    }

    // 抓的缺陷:force_h1=true 必须把线缆 ALPN 收窄为仅 http/1.1(不含 h2),否则服务端仍可能
    // 选 h2,connect.rs 的 selected_alpn 等于 b"h2" 走 H2 bridge,force_h1 旋钮形同虚设。
    // 注:生产内置 anthropic profile 本身 alpn=["http/1.1"](真 Claude Code 只走 H1),
    // 故这里构造一个广告 [h2, http/1.1] 的 fixture profile,才能区分 force_h1 收窄前后,
    // 同时也覆盖将来若有 h2 档案接入 sidecar 的场景。
    // 自证:同一 fixture 在 force_h1=false 时线缆 ALPN 含 h2,force_h1=true 时只剩 http/1.1,
    // 两者必须不同,证明 override 真正落到线缆。
    #[tokio::test]
    async fn boring_force_h1_narrows_wire_alpn_to_http11_only() {
        let mut profile = anthropic_profile();
        profile.alpn = vec!["h2".to_owned(), "http/1.1".to_owned()];
        // 前提自检:fixture 确实广告 h2,否则本测试没有区分力。
        assert!(
            profile.alpn.iter().any(|p| p == "h2"),
            "fixture profile 必须广告 h2,才能区分 force_h1 收窄前后"
        );

        let forced = capture_wire_client_hello_force_h1(&profile, true).await;
        assert_eq!(
            forced.alpn_protocols,
            vec!["http/1.1".to_owned()],
            "force_h1=true 时线缆 ALPN 必须只含 http/1.1"
        );
        assert!(
            !forced.alpn_protocols.iter().any(|p| p == "h2"),
            "force_h1=true 时线缆 ALPN 不得包含 h2"
        );

        let unforced = capture_wire_client_hello_force_h1(&profile, false).await;
        assert!(
            unforced.alpn_protocols.iter().any(|p| p == "h2"),
            "force_h1=false 时必须保持 profile.alpn 含 h2(今日行为),实际={:?}",
            unforced.alpn_protocols
        );
        assert_ne!(
            forced.alpn_protocols, unforced.alpn_protocols,
            "force_h1 开关前后线缆 ALPN 必须不同,证明 override 真正落到线缆"
        );
    }

    #[tokio::test]
    async fn empty_boring_setter_fields_keep_boring_default_extension_path() {
        let mut profile = anthropic_profile();
        let explicit_order = profile.extension_order.clone();
        profile.extension_order.clear();
        profile.tls13_cipher_order.clear();
        profile.client_hello_profile = crate::profile::ClientHelloProfile::default();

        let wire = capture_wire_client_hello(&profile).await;
        let default_order = wire.extensions_without_grease_or_padding();

        assert!(!default_order.contains(&22));
        assert_ne!(default_order, explicit_order);
    }

    #[tokio::test]
    async fn boring_wire_ja4_matches_profile_and_rejects_chrome_profile_fixture() {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        let profile = profiles.get("anthropic-cli-mimicry-v1").unwrap();

        let raw = super::capture_client_hello_record(profile, "api.anthropic.com")
            .await
            .unwrap();
        let ja4 = crate::ja4::Ja4Fingerprint::from_tls_client_hello_record(&raw).unwrap();

        crate::ja4::verify_profile_expectation(profile, &ja4).unwrap();

        let mut chrome = profile.clone();
        chrome.ja4_a = Some("t13d1516h2".to_owned());
        chrome.ja4_b = Some("8daaf6152771".to_owned());
        chrome.ja4_c = Some("02713d6af862".to_owned());

        let err = crate::ja4::verify_profile_expectation(&chrome, &ja4).unwrap_err();
        assert!(
            err.to_string().contains("ja4_"),
            "Chrome profile fixture must not validate Anthropic CLI wire JA4: {err}"
        );
    }

    async fn capture_wire_ja3(profile: crate::profile::TlsProfile) -> String {
        let (client, server) = tokio::io::duplex(16 * 1024);
        let capture = tokio::spawn(async move { read_first_tls_record(server).await });
        let config = super::connect_config(&profile).unwrap();
        let _ = timeout(
            Duration::from_secs(1),
            tokio_boring::connect(config, "api.anthropic.com", client),
        )
        .await;
        let raw = capture.await.unwrap();
        parse_wire_ja3(&raw).unwrap()
    }

    async fn capture_wire_client_hello(profile: &crate::profile::TlsProfile) -> WireClientHello {
        let raw = super::capture_client_hello_record(profile, "api.anthropic.com")
            .await
            .unwrap();
        parse_wire_client_hello(&raw).unwrap()
    }

    // 用 force_h1 开关构造连接配置并抓取线缆 ClientHello,供 force_h1 ALPN 断言用。
    async fn capture_wire_client_hello_force_h1(
        profile: &crate::profile::TlsProfile,
        force_h1: bool,
    ) -> WireClientHello {
        let (client, server) = tokio::io::duplex(16 * 1024);
        let capture = tokio::spawn(async move { read_first_tls_record(server).await });
        let config = super::connect_config_force_h1(profile, force_h1).unwrap();
        let _ = timeout(
            Duration::from_secs(1),
            tokio_boring::connect(config, "api.anthropic.com", client),
        )
        .await;
        let raw = capture.await.unwrap();
        parse_wire_client_hello(&raw).unwrap()
    }

    fn anthropic_profile() -> crate::profile::TlsProfile {
        let profiles =
            crate::profile::ProfileStore::from_toml(crate::profile::BUILTIN_PROFILES_TOML).unwrap();
        profiles.get("anthropic-cli-mimicry-v1").unwrap().clone()
    }

    fn u16_values_as_u8(values: &[u16]) -> Vec<u8> {
        values
            .iter()
            .copied()
            .map(|value| u8::try_from(value).unwrap())
            .collect()
    }

    fn expected_profile_ciphers(profile: &crate::profile::TlsProfile) -> Vec<u16> {
        profile
            .tls13_cipher_order
            .iter()
            .chain(profile.client_hello_profile.ciphers.iter())
            .copied()
            .collect()
    }

    async fn read_first_tls_record(mut stream: DuplexStream) -> Vec<u8> {
        let mut header = [0u8; 5];
        if stream.read_exact(&mut header).await.is_err() {
            return Vec::new();
        }
        let record_len = u16::from_be_bytes([header[3], header[4]]) as usize;
        let mut body = vec![0u8; record_len];
        if stream.read_exact(&mut body).await.is_err() {
            return Vec::new();
        }
        let mut raw = header.to_vec();
        raw.extend_from_slice(&body);
        raw
    }

    fn parse_wire_ja3(raw: &[u8]) -> Result<String, &'static str> {
        let hello = parse_wire_client_hello(raw)?;
        // 标准 JA3 首字段用 ClientHello 的 record(legacy)版本;TLS1.3 hello 固定 0x0303 = 771。
        Ok([
            hello.legacy_version.to_string(),
            super::join_u16(&hello.ciphers),
            join_huakai_ja3_extensions(&hello.extensions),
            super::join_u16(&hello.supported_groups),
            super::join_u8(&hello.ec_point_formats),
        ]
        .join(","))
    }

    fn parse_wire_client_hello(raw: &[u8]) -> Result<WireClientHello, &'static str> {
        if raw.len() < 5 || raw[0] != 0x16 {
            return Err("not a TLS handshake record");
        }
        let record_len = u16::from_be_bytes([raw[3], raw[4]]) as usize;
        if raw.len() < 5 + record_len {
            return Err("truncated TLS record");
        }
        let mut record = WireReader::new(&raw[5..5 + record_len]);
        if record.read_u8()? != 0x01 {
            return Err("not a ClientHello");
        }
        let handshake_len = record.read_u24()?;
        let body = record.take(handshake_len)?;
        let mut reader = WireReader::new(body);
        let legacy_version = reader.read_u16()?;
        reader.skip(32)?;
        let session_id_len = reader.read_u8()? as usize;
        reader.skip(session_id_len)?;

        let cipher_len = reader.read_u16()? as usize;
        if !cipher_len.is_multiple_of(2) {
            return Err("invalid cipher list length");
        }
        let cipher_end = reader.position() + cipher_len;
        let mut ciphers = Vec::new();
        while reader.position() < cipher_end {
            ciphers.push(reader.read_u16()?);
        }

        let compression_len = reader.read_u8()? as usize;
        reader.skip(compression_len)?;

        let mut extensions = Vec::new();
        let mut groups = Vec::new();
        let mut ec_points = Vec::new();
        let mut signature_algorithms = Vec::new();
        let mut supported_versions = Vec::new();
        let mut alpn_protocols = Vec::new();
        if reader.remaining() > 0 {
            let extensions_len = reader.read_u16()? as usize;
            let extensions_end = reader.position() + extensions_len;
            while reader.position() < extensions_end {
                let ext_type = reader.read_u16()?;
                let ext_len = reader.read_u16()? as usize;
                let data = reader.take(ext_len)?;
                extensions.push(ext_type);
                match ext_type {
                    10 => groups = parse_u16_vector(data)?,
                    11 => ec_points = parse_u8_vector(data)?,
                    13 => signature_algorithms = parse_u16_vector(data)?,
                    16 => alpn_protocols = parse_alpn_protocols(data)?,
                    43 => supported_versions = parse_supported_versions(data)?,
                    _ => {}
                }
            }
        }
        Ok(WireClientHello {
            legacy_version,
            ciphers,
            extensions,
            supported_groups: groups,
            ec_point_formats: ec_points,
            signature_algorithms,
            supported_versions,
            alpn_protocols,
        })
    }

    #[derive(Debug, PartialEq, Eq)]
    struct WireClientHello {
        legacy_version: u16,
        ciphers: Vec<u16>,
        extensions: Vec<u16>,
        supported_groups: Vec<u16>,
        ec_point_formats: Vec<u8>,
        signature_algorithms: Vec<u16>,
        supported_versions: Vec<u16>,
        alpn_protocols: Vec<String>,
    }

    impl WireClientHello {
        fn extensions_without_grease_or_padding(&self) -> Vec<u16> {
            self.extensions
                .iter()
                .copied()
                .filter(|value| !super::is_grease(*value) && *value != 21)
                .collect()
        }
    }

    fn join_huakai_ja3_extensions(values: &[u16]) -> String {
        values
            .iter()
            .copied()
            .filter(|value| !super::is_grease(*value) && *value != 21)
            .map(|value| value.to_string())
            .collect::<Vec<_>>()
            .join("-")
    }

    fn parse_u16_vector(data: &[u8]) -> Result<Vec<u16>, &'static str> {
        let mut reader = WireReader::new(data);
        let len = reader.read_u16()? as usize;
        if !len.is_multiple_of(2) || reader.remaining() < len {
            return Err("invalid u16 vector");
        }
        let end = reader.position() + len;
        let mut out = Vec::new();
        while reader.position() < end {
            out.push(reader.read_u16()?);
        }
        Ok(out)
    }

    fn parse_u8_vector(data: &[u8]) -> Result<Vec<u8>, &'static str> {
        let mut reader = WireReader::new(data);
        let len = reader.read_u8()? as usize;
        if reader.remaining() < len {
            return Err("invalid u8 vector");
        }
        Ok(reader.take(len)?.to_vec())
    }

    // 解析 ALPN 扩展(type 16):2 字节协议名列表总长,后接若干 (1 字节长度 + 协议字节)。
    fn parse_alpn_protocols(data: &[u8]) -> Result<Vec<String>, &'static str> {
        let mut reader = WireReader::new(data);
        let list_len = reader.read_u16()? as usize;
        if reader.remaining() < list_len {
            return Err("invalid alpn list");
        }
        let end = reader.position() + list_len;
        let mut out = Vec::new();
        while reader.position() < end {
            let name_len = reader.read_u8()? as usize;
            let name = reader.take(name_len)?;
            out.push(String::from_utf8_lossy(name).into_owned());
        }
        Ok(out)
    }

    fn parse_supported_versions(data: &[u8]) -> Result<Vec<u16>, &'static str> {
        let mut reader = WireReader::new(data);
        let len = reader.read_u8()? as usize;
        if !len.is_multiple_of(2) || reader.remaining() < len {
            return Err("invalid supported_versions");
        }
        let end = reader.position() + len;
        let mut out = Vec::new();
        while reader.position() < end {
            out.push(reader.read_u16()?);
        }
        Ok(out)
    }

    struct WireReader<'a> {
        data: &'a [u8],
        offset: usize,
    }

    impl<'a> WireReader<'a> {
        fn new(data: &'a [u8]) -> Self {
            Self { data, offset: 0 }
        }

        fn position(&self) -> usize {
            self.offset
        }

        fn remaining(&self) -> usize {
            self.data.len().saturating_sub(self.offset)
        }

        fn read_u8(&mut self) -> Result<u8, &'static str> {
            Ok(self.take(1)?[0])
        }

        fn read_u16(&mut self) -> Result<u16, &'static str> {
            let bytes = self.take(2)?;
            Ok(u16::from_be_bytes([bytes[0], bytes[1]]))
        }

        fn read_u24(&mut self) -> Result<usize, &'static str> {
            let bytes = self.take(3)?;
            Ok(((bytes[0] as usize) << 16) | ((bytes[1] as usize) << 8) | bytes[2] as usize)
        }

        fn skip(&mut self, len: usize) -> Result<(), &'static str> {
            self.take(len).map(|_| ())
        }

        fn take(&mut self, len: usize) -> Result<&'a [u8], &'static str> {
            if self.remaining() < len {
                return Err("truncated wire data");
            }
            let start = self.offset;
            self.offset += len;
            Ok(&self.data[start..self.offset])
        }
    }
}
