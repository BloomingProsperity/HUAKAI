use serde::{Deserialize, Serialize};
use thiserror::Error;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

pub const PROTOCOL_VERSION: u16 = 4;
pub const OPERATION_CONNECT: &str = "connect";
pub const OPERATION_READY: &str = "ready";

pub const CAPABILITY_BUILTIN_PROFILE: &str = "builtin_profile";
pub const CAPABILITY_INLINE_PROFILE: &str = "inline_profile";
pub const CAPABILITY_HTTP_PROXY: &str = "http_proxy";
pub const CAPABILITY_HTTPS_PROXY: &str = "https_proxy";
pub const CAPABILITY_SOCKS5_PROXY: &str = "socks5_proxy";
pub const CAPABILITY_H2_BRIDGE: &str = "h2_bridge";
pub const CAPABILITY_FORCE_H1: &str = "force_h1";
pub const CAPABILITY_TARGET_IP_PINNING: &str = "target_ip_pinning";
pub const CAPABILITY_PROXY_IP_PINNING: &str = "proxy_ip_pinning";

pub const ERROR_PROTOCOL_UNSUPPORTED: &str = "protocol_unsupported";
pub const ERROR_OPERATION_UNSUPPORTED: &str = "operation_unsupported";
pub const ERROR_PROFILE_UNKNOWN: &str = "profile_unknown";
pub const ERROR_PROFILE_INVALID: &str = "profile_invalid";
pub const ERROR_TARGET_INVALID: &str = "target_invalid";
pub const ERROR_TARGET_POLICY_DENIED: &str = "target_policy_denied";
pub const ERROR_PROXY_INVALID: &str = "proxy_invalid";
pub const ERROR_PROXY_CONNECT: &str = "proxy_connect";
pub const ERROR_UPSTREAM_DNS: &str = "upstream_dns";
pub const ERROR_UPSTREAM_CONNECTION_REFUSED: &str = "upstream_connection_refused";
pub const ERROR_UPSTREAM_NETWORK_UNREACHABLE: &str = "upstream_network_unreachable";
pub const ERROR_UPSTREAM_CONNECT: &str = "upstream_connect";
pub const ERROR_UPSTREAM_TIMEOUT: &str = "upstream_timeout";
pub const ERROR_TLS_HANDSHAKE: &str = "tls_handshake";
pub const ERROR_INTERNAL: &str = "internal";

const MAX_FRAME_LEN: usize = 1024 * 1024;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ControlRequest {
    pub version: u16,
    pub operation: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub target_host: String,
    #[serde(default, skip_serializing_if = "is_zero_u16")]
    pub port: u16,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub profile_id: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub inline_profile: Option<InlineTlsProfile>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub correlation_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub force_h1: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub proxy: Option<ProxySpec>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub proxy_resolved_ips: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub pinned_target_ips: Vec<String>,
}

fn is_zero_u16(value: &u16) -> bool {
    *value == 0
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct InlineTlsProfile {
    pub id: String,
    pub grease_enabled: bool,
    pub cipher_suites: Vec<u16>,
    pub supported_groups: Vec<u16>,
    pub ec_point_formats: Vec<u8>,
    pub signature_algorithms: Vec<u16>,
    pub alpn_protocols: Vec<String>,
    pub tls_supported_versions: Vec<u16>,
    pub key_share_groups: Vec<u16>,
    pub psk_modes: Vec<u8>,
    pub extensions_order: Vec<u16>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProxySpec {
    pub scheme: String,
    pub host: String,
    pub port: u16,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub username: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub password: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ControlAck {
    pub version: u16,
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<ControlError>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub capabilities: Vec<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub profile_ids: Vec<String>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ControlError {
    pub code: String,
    pub message: String,
}

impl ControlAck {
    pub fn ok() -> Self {
        Self {
            version: PROTOCOL_VERSION,
            ok: true,
            error: None,
            capabilities: Vec::new(),
            profile_ids: Vec::new(),
        }
    }

    pub fn ready(profile_ids: Vec<String>) -> Self {
        Self {
            version: PROTOCOL_VERSION,
            ok: true,
            error: None,
            capabilities: vec![
                CAPABILITY_BUILTIN_PROFILE.to_owned(),
                CAPABILITY_INLINE_PROFILE.to_owned(),
                CAPABILITY_HTTP_PROXY.to_owned(),
                CAPABILITY_HTTPS_PROXY.to_owned(),
                CAPABILITY_SOCKS5_PROXY.to_owned(),
                CAPABILITY_H2_BRIDGE.to_owned(),
                CAPABILITY_FORCE_H1.to_owned(),
                CAPABILITY_TARGET_IP_PINNING.to_owned(),
                CAPABILITY_PROXY_IP_PINNING.to_owned(),
            ],
            profile_ids,
        }
    }

    pub fn error(code: impl Into<String>, message: impl Into<String>) -> Self {
        Self {
            version: PROTOCOL_VERSION,
            ok: false,
            error: Some(ControlError {
                code: code.into(),
                message: message.into(),
            }),
            capabilities: Vec::new(),
            profile_ids: Vec::new(),
        }
    }
}

#[derive(Debug, Error)]
pub enum ProtoError {
    #[error("ipc io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("ipc json error: {0}")]
    Json(#[from] serde_json::Error),
    #[error("ipc frame length {actual} exceeds max {max}")]
    FrameTooLarge { actual: usize, max: usize },
}

pub async fn read_frame<R>(reader: &mut R) -> Result<Vec<u8>, ProtoError>
where
    R: AsyncRead + Unpin,
{
    let mut prefix = [0u8; 4];
    reader.read_exact(&mut prefix).await?;
    let len = u32::from_le_bytes(prefix) as usize;
    if len > MAX_FRAME_LEN {
        return Err(ProtoError::FrameTooLarge {
            actual: len,
            max: MAX_FRAME_LEN,
        });
    }
    let mut body = vec![0u8; len];
    reader.read_exact(&mut body).await?;
    Ok(body)
}

pub async fn write_frame<W>(writer: &mut W, body: &[u8]) -> Result<(), ProtoError>
where
    W: AsyncWrite + Unpin,
{
    if body.len() > MAX_FRAME_LEN {
        return Err(ProtoError::FrameTooLarge {
            actual: body.len(),
            max: MAX_FRAME_LEN,
        });
    }
    writer.write_all(&(body.len() as u32).to_le_bytes()).await?;
    writer.write_all(body).await?;
    writer.flush().await?;
    Ok(())
}

pub async fn read_control_request<R>(reader: &mut R) -> Result<ControlRequest, ProtoError>
where
    R: AsyncRead + Unpin,
{
    let body = read_frame(reader).await?;
    Ok(serde_json::from_slice(&body)?)
}

pub async fn write_control_request<W>(
    writer: &mut W,
    request: &ControlRequest,
) -> Result<(), ProtoError>
where
    W: AsyncWrite + Unpin,
{
    let body = serde_json::to_vec(request)?;
    write_frame(writer, &body).await
}

pub async fn read_control_ack<R>(reader: &mut R) -> Result<ControlAck, ProtoError>
where
    R: AsyncRead + Unpin,
{
    let body = read_frame(reader).await?;
    Ok(serde_json::from_slice(&body)?)
}

pub async fn write_control_ack<W>(writer: &mut W, ack: &ControlAck) -> Result<(), ProtoError>
where
    W: AsyncWrite + Unpin,
{
    let body = serde_json::to_vec(ack)?;
    write_frame(writer, &body).await
}

#[cfg(test)]
mod tests {
    use std::io::Cursor;

    fn request() -> super::ControlRequest {
        super::ControlRequest {
            version: super::PROTOCOL_VERSION,
            operation: super::OPERATION_CONNECT.to_owned(),
            target_host: "api.anthropic.com".to_owned(),
            port: 443,
            profile_id: "anthropic-cli-mimicry-v1".to_owned(),
            inline_profile: None,
            correlation_id: Some("corr-1".to_owned()),
            force_h1: None,
            proxy: None,
            proxy_resolved_ips: Vec::new(),
            pinned_target_ips: Vec::new(),
        }
    }

    #[tokio::test]
    async fn frame_uses_little_endian_length_prefix() {
        let mut out = Vec::new();
        super::write_frame(&mut out, b"abc").await.unwrap();
        assert_eq!(&out[..4], &[3, 0, 0, 0]);
        assert_eq!(&out[4..], b"abc");
    }

    #[tokio::test]
    async fn versioned_connect_request_round_trips() {
        let req = request();
        let mut wire = Vec::new();
        super::write_control_request(&mut wire, &req).await.unwrap();
        let decoded = super::read_control_request(&mut Cursor::new(wire))
            .await
            .unwrap();
        assert_eq!(decoded, req);
    }

    #[test]
    fn request_without_version_is_rejected() {
        let raw = r#"{"operation":"ready"}"#;
        let error = serde_json::from_str::<super::ControlRequest>(raw).unwrap_err();
        assert!(error.to_string().contains("version"));
    }

    #[test]
    fn inline_profile_round_trips_without_losing_fields() {
        let mut req = request();
        req.profile_id.clear();
        req.inline_profile = Some(super::InlineTlsProfile {
            id: "tenant-profile".to_owned(),
            grease_enabled: false,
            cipher_suites: vec![4865, 4866, 49195],
            supported_groups: vec![29, 23],
            ec_point_formats: vec![0],
            signature_algorithms: vec![1027, 2052],
            alpn_protocols: vec!["http/1.1".to_owned()],
            tls_supported_versions: vec![772, 771],
            key_share_groups: vec![29],
            psk_modes: vec![1],
            extensions_order: vec![0, 10, 11, 13, 43, 45, 51],
        });
        let encoded = serde_json::to_vec(&req).unwrap();
        let decoded: super::ControlRequest = serde_json::from_slice(&encoded).unwrap();
        assert_eq!(decoded, req);
    }

    #[test]
    fn ready_ack_exposes_version_capabilities_and_profiles() {
        let ack = super::ControlAck::ready(vec!["p1".to_owned(), "p2".to_owned()]);
        assert_eq!(ack.version, super::PROTOCOL_VERSION);
        assert!(ack.ok);
        assert!(
            ack.capabilities
                .iter()
                .any(|value| value == super::CAPABILITY_INLINE_PROFILE)
        );
        assert!(
            ack.capabilities
                .iter()
                .any(|value| value == super::CAPABILITY_FORCE_H1)
        );
        assert!(
            ack.capabilities
                .iter()
                .any(|value| value == super::CAPABILITY_TARGET_IP_PINNING)
        );
        assert_eq!(ack.profile_ids, ["p1", "p2"]);
    }

    #[test]
    fn force_h1_round_trips_and_is_optional() {
        let mut req = request();
        req.force_h1 = Some(true);
        let encoded = serde_json::to_vec(&req).unwrap();
        assert!(String::from_utf8_lossy(&encoded).contains("\"force_h1\":true"));
        let decoded: super::ControlRequest = serde_json::from_slice(&encoded).unwrap();
        assert_eq!(decoded.force_h1, Some(true));

        req.force_h1 = None;
        let encoded = serde_json::to_vec(&req).unwrap();
        assert!(!String::from_utf8_lossy(&encoded).contains("force_h1"));
    }

    #[test]
    fn structured_error_keeps_code_separate_from_message() {
        let ack = super::ControlAck::error(super::ERROR_PROFILE_UNKNOWN, "missing profile");
        let error = ack.error.unwrap();
        assert_eq!(error.code, super::ERROR_PROFILE_UNKNOWN);
        assert_eq!(error.message, "missing profile");
    }
}
