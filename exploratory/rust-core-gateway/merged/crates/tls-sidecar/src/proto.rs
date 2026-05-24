use serde::{Deserialize, Serialize};
use thiserror::Error;
use tokio::io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt};

const MAX_FRAME_LEN: usize = 1024 * 1024;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct ControlRequest {
    pub target_host: String,
    pub port: u16,
    pub profile_id: String,
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
        };
        let mut wire = Vec::new();

        super::write_control_request(&mut wire, &req).await.unwrap();

        let decoded = super::read_control_request(&mut Cursor::new(wire))
            .await
            .unwrap();
        assert_eq!(decoded, req);
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
