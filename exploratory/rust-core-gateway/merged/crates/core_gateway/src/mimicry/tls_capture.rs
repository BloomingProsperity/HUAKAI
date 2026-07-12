//! 用于 OpenSSL 拟真 preflight 的本地 TLS ClientHello 捕获工具。
//!
//! 该工具只读取第一条明文 TLS handshake record, 绝不完成完整的 TLS 握手。
//! 它由父模块 feature-gate 控制。

use std::{
    io::Read,
    net::{SocketAddr, TcpStream as StdTcpStream},
    time::Duration,
};

use thiserror::Error;
use tokio::{
    io::AsyncReadExt,
    net::{TcpListener, TcpStream},
    task::JoinHandle,
    time,
};

const TLS_HANDSHAKE_RECORD: u8 = 22;
const TLS_CLIENT_HELLO: u8 = 1;
const EXT_SUPPORTED_GROUPS: u16 = 10;
const EXT_EC_POINT_FORMATS: u16 = 11;
const EXT_SIGNATURE_ALGORITHMS: u16 = 13;
const EXT_ALPN: u16 = 16;
const ACCEPT_TIMEOUT: Duration = Duration::from_secs(10);
const READ_TIMEOUT: Duration = Duration::from_secs(10);

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CapturedClientHello {
    pub legacy_version: u16,
    pub cipher_suites: Vec<u16>,
    pub extensions: Vec<u16>,
    pub supported_groups: Vec<u16>,
    pub signature_algorithms: Vec<u16>,
    pub ec_point_formats: Vec<u8>,
    pub alpn_protocols: Vec<String>,
}

#[derive(Debug, Error)]
pub enum CaptureError {
    #[error("TLS capture I/O failed while {context}: {source}")]
    Io {
        context: &'static str,
        #[source]
        source: std::io::Error,
    },
    #[error("TLS capture timed out while {0}")]
    Timeout(&'static str),
    #[error("expected TLS handshake record, got content type 0x{0:02x}")]
    UnexpectedRecordType(u8),
    #[error("unexpected TLS record legacy_version 0x{0:04x}")]
    UnexpectedRecordVersion(u16),
    #[error("expected ClientHello handshake, got handshake type 0x{0:02x}")]
    UnexpectedHandshakeType(u8),
    #[error(
        "{context} length overflow: need {needed} bytes at offset {offset}, remaining {remaining}"
    )]
    LengthOverflow {
        context: &'static str,
        needed: usize,
        offset: usize,
        remaining: usize,
    },
    #[error("{context} has odd byte length {len}; expected u16 wire list")]
    OddU16List { context: &'static str, len: usize },
    #[error("invalid TLS ClientHello: {0}")]
    Invalid(&'static str),
    #[error("ALPN protocol is not UTF-8: {0}")]
    InvalidAlpnUtf8(#[from] std::string::FromUtf8Error),
}

pub async fn capture_once(addr: SocketAddr) -> Result<CapturedClientHello, CaptureError> {
    let listener = TcpListener::bind(addr)
        .await
        .map_err(|source| CaptureError::Io {
            context: "binding capture listener",
            source,
        })?;
    capture_from_listener(listener).await
}

pub async fn spawn_capture_once(
    addr: SocketAddr,
) -> Result<
    (
        SocketAddr,
        JoinHandle<Result<CapturedClientHello, CaptureError>>,
    ),
    CaptureError,
> {
    let listener = TcpListener::bind(addr)
        .await
        .map_err(|source| CaptureError::Io {
            context: "binding capture listener",
            source,
        })?;
    let local_addr = listener.local_addr().map_err(|source| CaptureError::Io {
        context: "reading capture listener local addr",
        source,
    })?;
    let task = tokio::spawn(async move { capture_from_listener(listener).await });
    Ok((local_addr, task))
}

pub fn capture_from_std_stream(
    stream: &mut StdTcpStream,
) -> Result<CapturedClientHello, CaptureError> {
    let mut header = [0u8; 5];
    stream
        .read_exact(&mut header)
        .map_err(|source| CaptureError::Io {
            context: "reading TLS record header",
            source,
        })?;

    validate_tls_record_header(&header)?;

    let body_len = u16::from_be_bytes([header[3], header[4]]) as usize;
    if body_len == 0 {
        return Err(CaptureError::Invalid("TLS record body length is zero"));
    }
    let mut body = vec![0u8; body_len];
    stream
        .read_exact(&mut body)
        .map_err(|source| CaptureError::Io {
            context: "reading TLS record body",
            source,
        })?;

    parse_tls_record_body(&body)
}

async fn capture_from_listener(listener: TcpListener) -> Result<CapturedClientHello, CaptureError> {
    let (mut stream, _) = time::timeout(ACCEPT_TIMEOUT, listener.accept())
        .await
        .map_err(|_| CaptureError::Timeout("accepting first TLS client"))?
        .map_err(|source| CaptureError::Io {
            context: "accepting first TLS client",
            source,
        })?;

    let mut header = [0u8; 5];
    read_exact_timeout(&mut stream, &mut header, "reading TLS record header").await?;

    validate_tls_record_header(&header)?;

    let body_len = u16::from_be_bytes([header[3], header[4]]) as usize;
    if body_len == 0 {
        return Err(CaptureError::Invalid("TLS record body length is zero"));
    }
    let mut body = vec![0u8; body_len];
    read_exact_timeout(&mut stream, &mut body, "reading TLS record body").await?;

    parse_tls_record_body(&body)
}

async fn read_exact_timeout(
    stream: &mut TcpStream,
    buffer: &mut [u8],
    context: &'static str,
) -> Result<(), CaptureError> {
    time::timeout(READ_TIMEOUT, stream.read_exact(buffer))
        .await
        .map_err(|_| CaptureError::Timeout(context))?
        .map(|_| ())
        .map_err(|source| CaptureError::Io { context, source })
}

fn validate_tls_record_header(header: &[u8; 5]) -> Result<(), CaptureError> {
    if header[0] != TLS_HANDSHAKE_RECORD {
        return Err(CaptureError::UnexpectedRecordType(header[0]));
    }
    let record_version = u16::from_be_bytes([header[1], header[2]]);
    if !(0x0301..=0x0304).contains(&record_version) {
        return Err(CaptureError::UnexpectedRecordVersion(record_version));
    }
    Ok(())
}

fn parse_tls_record_body(record_body: &[u8]) -> Result<CapturedClientHello, CaptureError> {
    let mut cursor = Cursor::new(record_body);
    let handshake_type = cursor.read_u8("handshake type")?;
    if handshake_type != TLS_CLIENT_HELLO {
        return Err(CaptureError::UnexpectedHandshakeType(handshake_type));
    }
    let handshake_len = cursor.read_u24("ClientHello handshake length")?;
    let client_hello = cursor.take(handshake_len, "ClientHello handshake body")?;
    cursor.finish("TLS record trailing data")?;

    parse_client_hello_body(client_hello)
}

fn parse_client_hello_body(body: &[u8]) -> Result<CapturedClientHello, CaptureError> {
    let mut cursor = Cursor::new(body);
    let legacy_version = cursor.read_u16("ClientHello legacy_version")?;
    cursor.take(32, "ClientHello random")?;

    let session_id_len = cursor.read_u8("ClientHello legacy_session_id length")? as usize;
    cursor.take(session_id_len, "ClientHello legacy_session_id")?;

    let cipher_suite_bytes = cursor.take_u16_len("ClientHello cipher_suites")?;
    let cipher_suites = parse_u16_wire_list(cipher_suite_bytes, "ClientHello cipher_suites")?;

    let compression_len = cursor.read_u8("ClientHello compression_methods length")? as usize;
    cursor.take(compression_len, "ClientHello compression_methods")?;

    let mut capture = CapturedClientHello {
        legacy_version,
        cipher_suites,
        extensions: Vec::new(),
        supported_groups: Vec::new(),
        signature_algorithms: Vec::new(),
        ec_point_formats: Vec::new(),
        alpn_protocols: Vec::new(),
    };

    if cursor.remaining() == 0 {
        return Ok(capture);
    }

    let extension_bytes = cursor.take_u16_len("ClientHello extensions")?;
    cursor.finish("ClientHello trailing data")?;
    parse_extensions(extension_bytes, &mut capture)?;
    Ok(capture)
}

fn parse_extensions(
    extension_bytes: &[u8],
    capture: &mut CapturedClientHello,
) -> Result<(), CaptureError> {
    let mut cursor = Cursor::new(extension_bytes);
    while cursor.remaining() > 0 {
        let extension_id = cursor.read_u16("extension id")?;
        let data = cursor.take_u16_len("extension data")?;
        capture.extensions.push(extension_id);

        match extension_id {
            EXT_SUPPORTED_GROUPS => capture
                .supported_groups
                .extend(parse_len_prefixed_u16_list(data, "supported_groups")?),
            EXT_SIGNATURE_ALGORITHMS => capture
                .signature_algorithms
                .extend(parse_len_prefixed_u16_list(data, "signature_algorithms")?),
            EXT_EC_POINT_FORMATS => capture
                .ec_point_formats
                .extend(parse_ec_point_formats(data)?),
            EXT_ALPN => capture.alpn_protocols.extend(parse_alpn_protocols(data)?),
            _ => {}
        }
    }
    Ok(())
}

fn parse_len_prefixed_u16_list(
    bytes: &[u8],
    context: &'static str,
) -> Result<Vec<u16>, CaptureError> {
    let mut cursor = Cursor::new(bytes);
    let list = cursor.take_u16_len(context)?;
    cursor.finish(context)?;
    parse_u16_wire_list(list, context)
}

fn parse_u16_wire_list(bytes: &[u8], context: &'static str) -> Result<Vec<u16>, CaptureError> {
    if !bytes.len().is_multiple_of(2) {
        return Err(CaptureError::OddU16List {
            context,
            len: bytes.len(),
        });
    }

    let mut cursor = Cursor::new(bytes);
    let mut values = Vec::with_capacity(bytes.len() / 2);
    while cursor.remaining() > 0 {
        values.push(cursor.read_u16(context)?);
    }
    Ok(values)
}

fn parse_ec_point_formats(bytes: &[u8]) -> Result<Vec<u8>, CaptureError> {
    let mut cursor = Cursor::new(bytes);
    let formats = cursor.take_u8_len("ec_point_formats")?.to_vec();
    cursor.finish("ec_point_formats")?;
    Ok(formats)
}

fn parse_alpn_protocols(bytes: &[u8]) -> Result<Vec<String>, CaptureError> {
    let mut cursor = Cursor::new(bytes);
    let protocols = cursor.take_u16_len("alpn_protocols")?;
    cursor.finish("alpn_protocols extension")?;

    let mut protocol_cursor = Cursor::new(protocols);
    let mut names = Vec::new();
    while protocol_cursor.remaining() > 0 {
        let name_len = protocol_cursor.read_u8("ALPN protocol length")? as usize;
        if name_len == 0 {
            return Err(CaptureError::Invalid("ALPN protocol length is zero"));
        }
        let name = protocol_cursor
            .take(name_len, "ALPN protocol name")?
            .to_vec();
        names.push(String::from_utf8(name)?);
    }
    Ok(names)
}

struct Cursor<'a> {
    bytes: &'a [u8],
    offset: usize,
}

impl<'a> Cursor<'a> {
    fn new(bytes: &'a [u8]) -> Self {
        Self { bytes, offset: 0 }
    }

    fn remaining(&self) -> usize {
        self.bytes.len().saturating_sub(self.offset)
    }

    fn finish(&self, context: &'static str) -> Result<(), CaptureError> {
        if self.remaining() == 0 {
            Ok(())
        } else {
            Err(CaptureError::Invalid(context))
        }
    }

    fn take(&mut self, len: usize, context: &'static str) -> Result<&'a [u8], CaptureError> {
        if len > self.remaining() {
            return Err(CaptureError::LengthOverflow {
                context,
                needed: len,
                offset: self.offset,
                remaining: self.remaining(),
            });
        }
        let start = self.offset;
        self.offset += len;
        Ok(&self.bytes[start..self.offset])
    }

    fn read_u8(&mut self, context: &'static str) -> Result<u8, CaptureError> {
        Ok(self.take(1, context)?[0])
    }

    fn read_u16(&mut self, context: &'static str) -> Result<u16, CaptureError> {
        let bytes = self.take(2, context)?;
        Ok(u16::from_be_bytes([bytes[0], bytes[1]]))
    }

    fn read_u24(&mut self, context: &'static str) -> Result<usize, CaptureError> {
        let bytes = self.take(3, context)?;
        Ok(((bytes[0] as usize) << 16) | ((bytes[1] as usize) << 8) | bytes[2] as usize)
    }

    fn take_u8_len(&mut self, context: &'static str) -> Result<&'a [u8], CaptureError> {
        let len = self.read_u8(context)? as usize;
        self.take(len, context)
    }

    fn take_u16_len(&mut self, context: &'static str) -> Result<&'a [u8], CaptureError> {
        let len = self.read_u16(context)? as usize;
        self.take(len, context)
    }
}
