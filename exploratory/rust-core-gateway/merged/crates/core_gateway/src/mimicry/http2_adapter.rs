//! HTTP/2 fork adapter for SETTINGS / pseudo-header order mimicry.
//!
//! L2-A6 只暴露 feature-gated adapter 和本地 byte capture，不接入 ProxyEngine。

use std::{collections::BTreeSet, time::Duration};

use bytes::Bytes;
use http::Request;
use thiserror::Error;
use tokio::{
    io::{AsyncRead, AsyncReadExt, AsyncWrite, AsyncWriteExt, duplex},
    task::JoinError,
    time::timeout,
};

use super::FingerprintProfile;

const H2_CLIENT_PREFACE: &[u8; 24] = b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n";
const EMPTY_SERVER_SETTINGS: [u8; 9] = [0, 0, 0, 0x04, 0, 0, 0, 0, 0];
const CAPTURE_TIMEOUT: Duration = Duration::from_secs(2);

/// 基于 MIT `http2` fork 的 HTTP/2 指纹 adapter。
#[derive(Clone, Debug)]
pub struct HttpTwoMimicryAdapter {
    builder: http2::client::Builder,
    settings_order: Vec<u16>,
    settings_values: Vec<(u16, u32)>,
    pseudo_header_order: Vec<String>,
}

/// 内存连接中捕获的首个 SETTINGS 与请求 HEADERS frame。
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct HttpTwoEncodedExchange {
    pub client_preface: Vec<u8>,
    pub initial_settings_frame: Vec<u8>,
    pub request_headers_frame: Vec<u8>,
}

#[derive(Debug, Error)]
pub enum HttpTwoAdapterError {
    #[error("missing required HTTP/2 profile field {field}")]
    MissingProfileField { field: &'static str },
    #[error("unsupported HTTP/2 setting id {0}")]
    UnsupportedSetting(u16),
    #[error("unsupported HTTP/2 pseudo-header {0}")]
    UnsupportedPseudoHeader(String),
    #[error("invalid HTTP/2 setting {setting_id}: {reason}")]
    InvalidSettingValue { setting_id: u16, reason: String },
    #[error("HTTP/2 adapter build failed: {0}")]
    BuildFailed(String),
    #[error("HTTP/2 encode failed: {0}")]
    EncodeFailed(String),
    #[error("HTTP/2 capture timed out while {0}")]
    Timeout(&'static str),
    #[error("HTTP/2 capture protocol error: {0}")]
    Protocol(String),
    #[error("HTTP/2 fork error: {0}")]
    Http2(#[from] http2::Error),
    #[error("IO error: {0}")]
    Io(#[from] std::io::Error),
    #[error("capture task join error: {0}")]
    Join(#[from] JoinError),
}

impl HttpTwoMimicryAdapter {
    pub fn new_with_profile(profile: &FingerprintProfile) -> Result<Self, HttpTwoAdapterError> {
        if profile.h2_settings_order.is_empty() {
            return Err(HttpTwoAdapterError::MissingProfileField {
                field: "h2_settings_order",
            });
        }
        if profile.h2_settings_values.is_empty() {
            return Err(HttpTwoAdapterError::MissingProfileField {
                field: "h2_settings_values",
            });
        }
        if profile.h2_pseudo_header_order.is_empty() {
            return Err(HttpTwoAdapterError::MissingProfileField {
                field: "h2_pseudo_header_order",
            });
        }

        let mut seen = BTreeSet::new();
        for setting_id in &profile.h2_settings_order {
            if !seen.insert(*setting_id) {
                return Err(HttpTwoAdapterError::InvalidSettingValue {
                    setting_id: *setting_id,
                    reason: "duplicate setting id in order".to_owned(),
                });
            }
            if !profile.h2_settings_values.contains_key(setting_id) {
                return Err(HttpTwoAdapterError::InvalidSettingValue {
                    setting_id: *setting_id,
                    reason: "order has no matching value".to_owned(),
                });
            }
        }
        for setting_id in profile.h2_settings_values.keys() {
            if !seen.contains(setting_id) {
                return Err(HttpTwoAdapterError::InvalidSettingValue {
                    setting_id: *setting_id,
                    reason: "value has no matching order entry".to_owned(),
                });
            }
        }

        let mut builder = http2::client::Builder::new();
        apply_settings(
            &mut builder,
            &profile.h2_settings_order,
            &profile.h2_settings_values,
        )?;
        apply_pseudo_order(&mut builder, &profile.h2_pseudo_header_order)?;

        Ok(Self {
            builder,
            settings_order: profile.h2_settings_order.clone(),
            settings_values: profile
                .h2_settings_order
                .iter()
                .map(|id| (*id, profile.h2_settings_values[id]))
                .collect(),
            pseudo_header_order: profile.h2_pseudo_header_order.clone(),
        })
    }

    pub fn settings_order(&self) -> &[u16] {
        &self.settings_order
    }

    pub fn settings_values(&self) -> &[(u16, u32)] {
        &self.settings_values
    }

    pub fn pseudo_header_order(&self) -> &[String] {
        &self.pseudo_header_order
    }

    /// 通过内存 duplex 捕获 fork 实际写出的 frame bytes。
    pub async fn encode_request_exchange(
        &self,
        request: Request<()>,
    ) -> Result<HttpTwoEncodedExchange, HttpTwoAdapterError> {
        let (client_io, mut peer_io) = duplex(64 * 1024);
        let (mut send_request, connection) = self.builder.handshake::<_, Bytes>(client_io).await?;
        let connection_task = tokio::spawn(async move { connection.await });

        let mut client_preface = vec![0; H2_CLIENT_PREFACE.len()];
        read_exact_capture(&mut peer_io, &mut client_preface, "reading client preface").await?;
        if client_preface.as_slice() != H2_CLIENT_PREFACE {
            connection_task.abort();
            let _ = connection_task.await;
            return Err(HttpTwoAdapterError::Protocol(
                "client preface did not match HTTP/2 prior-knowledge preface".to_owned(),
            ));
        }

        let initial_settings_frame =
            read_frame(&mut peer_io, "reading client initial SETTINGS").await?;
        if frame_type(&initial_settings_frame) != Some(0x04) {
            connection_task.abort();
            let _ = connection_task.await;
            return Err(HttpTwoAdapterError::Protocol(
                "first frame after preface was not SETTINGS".to_owned(),
            ));
        }

        write_all_capture(
            &mut peer_io,
            &EMPTY_SERVER_SETTINGS,
            "writing peer empty SETTINGS",
        )
        .await?;
        let (_response, _body) = send_request
            .send_request(request, true)
            .map_err(|error| HttpTwoAdapterError::EncodeFailed(error.to_string()))?;

        let mut request_headers_frame = None;
        for _ in 0..8 {
            let frame = read_frame(&mut peer_io, "reading client request frames").await?;
            if frame_type(&frame) == Some(0x01) {
                request_headers_frame = Some(frame);
                break;
            }
        }

        connection_task.abort();
        let _ = connection_task.await;

        let request_headers_frame = request_headers_frame.ok_or_else(|| {
            HttpTwoAdapterError::Protocol(
                "client did not emit HEADERS frame within capture window".to_owned(),
            )
        })?;

        Ok(HttpTwoEncodedExchange {
            client_preface,
            initial_settings_frame,
            request_headers_frame,
        })
    }
}

fn apply_settings(
    builder: &mut http2::client::Builder,
    order: &[u16],
    values: &std::collections::BTreeMap<u16, u32>,
) -> Result<(), HttpTwoAdapterError> {
    let mut order_builder = http2::frame::SettingsOrder::builder();
    for setting_id in order {
        let value = values[setting_id];
        order_builder = order_builder.push(setting_id_from_u16(*setting_id)?);
        match *setting_id {
            0x01 => {
                builder.header_table_size(value);
            }
            0x02 => {
                builder.enable_push(bool_setting(*setting_id, value)?);
            }
            0x03 => {
                builder.max_concurrent_streams(value);
            }
            0x04 => {
                if value > ((1 << 31) - 1) {
                    return Err(HttpTwoAdapterError::InvalidSettingValue {
                        setting_id: *setting_id,
                        reason: "INITIAL_WINDOW_SIZE exceeds protocol maximum".to_owned(),
                    });
                }
                builder.initial_window_size(value);
            }
            0x05 => {
                if !(16_384..=16_777_215).contains(&value) {
                    return Err(HttpTwoAdapterError::InvalidSettingValue {
                        setting_id: *setting_id,
                        reason: "MAX_FRAME_SIZE is outside 16384..=16777215".to_owned(),
                    });
                }
                builder.max_frame_size(value);
            }
            0x06 => {
                builder.max_header_list_size(value);
            }
            0x08 => {
                builder.enable_connect_protocol(bool_setting(*setting_id, value)?);
            }
            0x09 => {
                builder.no_rfc7540_priorities(bool_setting(*setting_id, value)?);
            }
            _ => return Err(HttpTwoAdapterError::UnsupportedSetting(*setting_id)),
        }
    }
    builder.settings_order(order_builder.build());
    Ok(())
}

fn apply_pseudo_order(
    builder: &mut http2::client::Builder,
    order: &[String],
) -> Result<(), HttpTwoAdapterError> {
    let mut seen = BTreeSet::new();
    let mut order_builder = http2::frame::PseudoOrder::builder();
    for name in order {
        if !seen.insert(name.as_str()) {
            return Err(HttpTwoAdapterError::BuildFailed(format!(
                "duplicate pseudo-header {name}"
            )));
        }
        order_builder = order_builder.push(pseudo_id_from_name(name)?);
    }
    builder.headers_pseudo_order(order_builder.build());
    Ok(())
}

fn setting_id_from_u16(id: u16) -> Result<http2::frame::SettingId, HttpTwoAdapterError> {
    match id {
        0x01 | 0x02 | 0x03 | 0x04 | 0x05 | 0x06 | 0x08 | 0x09 => Ok(id.into()),
        _ => Err(HttpTwoAdapterError::UnsupportedSetting(id)),
    }
}

fn pseudo_id_from_name(name: &str) -> Result<http2::frame::PseudoId, HttpTwoAdapterError> {
    match name {
        ":method" | "method" => Ok(http2::frame::PseudoId::Method),
        ":scheme" | "scheme" => Ok(http2::frame::PseudoId::Scheme),
        ":authority" | "authority" => Ok(http2::frame::PseudoId::Authority),
        ":path" | "path" => Ok(http2::frame::PseudoId::Path),
        ":protocol" | "protocol" => Ok(http2::frame::PseudoId::Protocol),
        ":status" | "status" => Ok(http2::frame::PseudoId::Status),
        other => Err(HttpTwoAdapterError::UnsupportedPseudoHeader(
            other.to_owned(),
        )),
    }
}

fn bool_setting(setting_id: u16, value: u32) -> Result<bool, HttpTwoAdapterError> {
    match value {
        0 => Ok(false),
        1 => Ok(true),
        _ => Err(HttpTwoAdapterError::InvalidSettingValue {
            setting_id,
            reason: "boolean SETTINGS value must be 0 or 1".to_owned(),
        }),
    }
}

async fn read_frame<R>(
    reader: &mut R,
    context: &'static str,
) -> Result<Vec<u8>, HttpTwoAdapterError>
where
    R: AsyncRead + Unpin,
{
    let mut header = [0; 9];
    read_exact_capture(reader, &mut header, context).await?;
    let len = ((header[0] as usize) << 16) | ((header[1] as usize) << 8) | header[2] as usize;
    let mut frame = header.to_vec();
    frame.resize(9 + len, 0);
    if len > 0 {
        read_exact_capture(reader, &mut frame[9..], context).await?;
    }
    Ok(frame)
}

async fn read_exact_capture<R>(
    reader: &mut R,
    buf: &mut [u8],
    context: &'static str,
) -> Result<(), HttpTwoAdapterError>
where
    R: AsyncRead + Unpin,
{
    timeout(CAPTURE_TIMEOUT, reader.read_exact(buf))
        .await
        .map_err(|_| HttpTwoAdapterError::Timeout(context))??;
    Ok(())
}

async fn write_all_capture<W>(
    writer: &mut W,
    buf: &[u8],
    context: &'static str,
) -> Result<(), HttpTwoAdapterError>
where
    W: AsyncWrite + Unpin,
{
    timeout(CAPTURE_TIMEOUT, writer.write_all(buf))
        .await
        .map_err(|_| HttpTwoAdapterError::Timeout(context))??;
    Ok(())
}

fn frame_type(frame: &[u8]) -> Option<u8> {
    frame.get(3).copied()
}
