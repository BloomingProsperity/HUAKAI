use serde::{Deserialize, Serialize};
use thiserror::Error;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

const MAX_FRAME_LEN: usize = 1024 * 1024;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ControlRequest {
    pub target_host: String,
    pub port: u16,
    pub profile_id: String,
    // force_h1 为 Some(true) 时,本次拨号握手只广告 ALPN=http/1.1,从根上消除 h2 升级,
    // 必走 Raw 隧道(对齐 Go uTLS 路 utls_dialer.go 的 ForceH1)。
    // serde(default) + skip_serializing_if 保证向后兼容:老 Go 客户端不发本字段时反序列化为
    // None(=今日行为,由 profile.alpn 决定),None 时序列化也不写出该键(=旧线缆字节)。
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub force_h1: Option<bool>,
    // proxy=Some 时,本次拨号先经该代理建隧道(HTTP CONNECT / SOCKS5),再在隧道之上做
    // BoringSSL 握手——出口 IP 走代理,JA3/JA4 仍是伪装指纹,从而让绑账号级代理的账号也能用
    // sidecar(②-3 解 sidecar×代理硬阻塞)。结构化下发而非原始 URL:password 等敏感段不混进
    // 一个可被整体打印的字符串。serde(default)+skip_serializing_if 保证向后兼容:老 Go 客户端
    // 不发本字段时反序列化为 None(=直连目标,今日行为),None 时序列化也不写出该键(=旧线缆字节)。
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub proxy: Option<ProxySpec>,
}

// ProxySpec 是结构化代理下发载荷。Go 侧把已经过 proxyadmin SSRF 校验的 proxyURL 拆成各字段
// 填入,Rust 侧据此建隧道。scheme 取 http|https|socks5(socks5h 归一为 socks5);username/password
// 仅在带认证时出现(skip_serializing_if 省略空值,避免无认证时写出空串)。
// 不传原始 URL 是刻意为之:password 不会被某个把整个 URL 打进日志的调用方泄露。
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
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
pub struct ControlAck {
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

impl ControlAck {
    pub fn ok() -> Self {
        Self {
            ok: true,
            error: None,
        }
    }

    pub fn error(error: impl Into<String>) -> Self {
        Self {
            ok: false,
            error: Some(error.into()),
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

#[cfg(test)]
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

#[cfg(test)]
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

    use tokio::io::AsyncReadExt;

    #[tokio::test]
    async fn write_frame_uses_little_endian_length_prefix() {
        let mut out = Vec::new();

        super::write_frame(&mut out, b"abc").await.unwrap();

        assert_eq!(&out[..4], &[3, 0, 0, 0]);
        assert_eq!(&out[4..], b"abc");
    }

    #[tokio::test]
    async fn read_frame_rejects_big_endian_prefix_for_small_payload() {
        let mut wire = Cursor::new([0, 0, 0, 1, b'x']);

        let err = super::read_frame(&mut wire).await.unwrap_err();

        assert!(
            err.to_string().contains("frame length"),
            "error should mention frame length, got {err}"
        );
    }

    #[tokio::test]
    async fn control_request_round_trips_as_json_frame() {
        let req = super::ControlRequest {
            target_host: "api.anthropic.com".to_owned(),
            port: 443,
            profile_id: "anthropic-cli-mimicry-v1".to_owned(),
            force_h1: None,
            proxy: None,
        };
        let mut wire = Vec::new();

        super::write_control_request(&mut wire, &req).await.unwrap();

        let decoded = super::read_control_request(&mut Cursor::new(wire))
            .await
            .unwrap();
        assert_eq!(decoded, req);
    }

    // 抓的缺陷:老 Go 客户端发的帧里没有 force_h1 键,若 ControlRequest 去掉 serde(default)
    // 则反序列化会因缺字段报错,握手直接断。本测试用不含 force_h1 的历史 JSON 字节断言能解出
    // None,守护向后兼容(老线缆不会因新字段被拒)。
    #[tokio::test]
    async fn control_request_decodes_legacy_frame_without_force_h1_as_none() {
        let legacy_json =
            br#"{"target_host":"api.anthropic.com","port":443,"profile_id":"anthropic-cli-mimicry-v1"}"#;
        let mut wire = Vec::new();
        super::write_frame(&mut wire, legacy_json).await.unwrap();

        let decoded = super::read_control_request(&mut Cursor::new(wire))
            .await
            .unwrap();

        assert_eq!(decoded.force_h1, None);
        assert_eq!(decoded.target_host, "api.anthropic.com");
    }

    // 抓的缺陷:force_h1=None 时若去掉 skip_serializing_if,序列化会多写 "force_h1":null,
    // 改变了发往老 sidecar 的线缆字节(新增字段)。本测试断言 None 时序列化输出里不含 force_h1 键。
    #[tokio::test]
    async fn control_request_omits_force_h1_key_when_none() {
        let req = super::ControlRequest {
            target_host: "api.anthropic.com".to_owned(),
            port: 443,
            profile_id: "anthropic-cli-mimicry-v1".to_owned(),
            force_h1: None,
            proxy: None,
        };

        let json = serde_json::to_string(&req).unwrap();

        assert!(
            !json.contains("force_h1"),
            "force_h1=None 必须省略该键以保持旧线缆兼容,实际 JSON={json}"
        );
    }

    // 抓的缺陷:proxy=None(无账号级代理=直连目标,今日行为)时,若 ProxySpec 去掉
    // skip_serializing_if,序列化会多写 "proxy":null,改变发往老 sidecar 的线缆字节。
    // 本测试断言 None 时序列化输出里不含 proxy 键,守护向后兼容。
    #[tokio::test]
    async fn control_request_omits_proxy_key_when_none() {
        let req = super::ControlRequest {
            target_host: "api.anthropic.com".to_owned(),
            port: 443,
            profile_id: "anthropic-cli-mimicry-v1".to_owned(),
            force_h1: None,
            proxy: None,
        };

        let json = serde_json::to_string(&req).unwrap();

        assert!(
            !json.contains("proxy"),
            "proxy=None 必须省略该键以保持旧线缆兼容,实际 JSON={json}"
        );
    }

    // 抓的缺陷:老 Go 客户端发的帧里没有 proxy 键,若 ControlRequest 的 proxy 去掉
    // serde(default),反序列化会因缺字段报错,握手直接断。本测试用不含 proxy 的历史 JSON
    // 字节断言能解出 None,守护向后兼容(老线缆不会因新字段被拒)。
    #[tokio::test]
    async fn control_request_decodes_legacy_frame_without_proxy_as_none() {
        let legacy_json =
            br#"{"target_host":"api.anthropic.com","port":443,"profile_id":"anthropic-cli-mimicry-v1"}"#;
        let mut wire = Vec::new();
        super::write_frame(&mut wire, legacy_json).await.unwrap();

        let decoded = super::read_control_request(&mut Cursor::new(wire))
            .await
            .unwrap();

        assert_eq!(decoded.proxy, None);
    }

    // 抓的缺陷:proxy=Some(带认证)时,scheme/host/port/username/password 必须能完整
    // round-trip 回来,否则 Rust 端建隧道时拿不到正确目标/凭据,代理穿透失效。
    // 自证:带认证的 ProxySpec 序列化后再反序列化必须逐字段相等。
    #[tokio::test]
    async fn control_request_round_trips_proxy_spec_with_auth() {
        let req = super::ControlRequest {
            target_host: "api.anthropic.com".to_owned(),
            port: 443,
            profile_id: "anthropic-cli-mimicry-v1".to_owned(),
            force_h1: None,
            proxy: Some(super::ProxySpec {
                scheme: "http".to_owned(),
                host: "proxy.example.com".to_owned(),
                port: 3128,
                username: Some("alice".to_owned()),
                password: Some("s3cr3t".to_owned()),
            }),
        };

        let mut wire = Vec::new();
        super::write_control_request(&mut wire, &req).await.unwrap();
        let decoded = super::read_control_request(&mut Cursor::new(wire))
            .await
            .unwrap();

        assert_eq!(decoded, req);
        let proxy = decoded.proxy.expect("proxy 必须解回 Some");
        assert_eq!(proxy.scheme, "http");
        assert_eq!(proxy.host, "proxy.example.com");
        assert_eq!(proxy.port, 3128);
        assert_eq!(proxy.username.as_deref(), Some("alice"));
        assert_eq!(proxy.password.as_deref(), Some("s3cr3t"));
    }

    // 抓的缺陷:无认证代理(username/password=None)时,若 ProxySpec 去掉 skip_serializing_if,
    // 会多写 "username":null,"password":null;反序列化也应能从缺省解出 None。
    // 本测试断言无认证时 JSON 不含 username/password 键且能 round-trip。
    #[tokio::test]
    async fn proxy_spec_omits_credential_keys_when_none() {
        let spec = super::ProxySpec {
            scheme: "socks5".to_owned(),
            host: "10.0.0.9".to_owned(),
            port: 1080,
            username: None,
            password: None,
        };

        let json = serde_json::to_string(&spec).unwrap();
        assert!(
            !json.contains("username") && !json.contains("password"),
            "无认证代理必须省略 username/password 键,实际 JSON={json}"
        );

        let decoded: super::ProxySpec = serde_json::from_str(&json).unwrap();
        assert_eq!(decoded, spec);
    }

    // 抓的缺陷:force_h1=Some(true) 时该字段必须被序列化进 JSON 并能 round-trip 回来,
    // 否则 Go 端开启强制 H1 后 sidecar 根本收不到这个意图,旋钮失效。
    #[tokio::test]
    async fn control_request_round_trips_force_h1_some_true() {
        let req = super::ControlRequest {
            target_host: "api.anthropic.com".to_owned(),
            port: 443,
            profile_id: "anthropic-cli-mimicry-v1".to_owned(),
            force_h1: Some(true),
            proxy: None,
        };

        let json = serde_json::to_string(&req).unwrap();
        assert!(
            json.contains("\"force_h1\":true"),
            "force_h1=Some(true) 必须显式出现在 JSON 里,实际={json}"
        );

        let mut wire = Vec::new();
        super::write_control_request(&mut wire, &req).await.unwrap();
        let decoded = super::read_control_request(&mut Cursor::new(wire))
            .await
            .unwrap();
        assert_eq!(decoded, req);
        assert_eq!(decoded.force_h1, Some(true));
    }

    #[tokio::test]
    async fn read_frame_consumes_exact_payload_only() {
        let mut wire = Cursor::new([3, 0, 0, 0, b'a', b'b', b'c', b'z']);

        let frame = super::read_frame(&mut wire).await.unwrap();
        let mut tail = Vec::new();
        wire.read_to_end(&mut tail).await.unwrap();

        assert_eq!(frame, b"abc");
        assert_eq!(tail, b"z");
    }
}
