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
        };

        let json = serde_json::to_string(&req).unwrap();

        assert!(
            !json.contains("force_h1"),
            "force_h1=None 必须省略该键以保持旧线缆兼容,实际 JSON={json}"
        );
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
