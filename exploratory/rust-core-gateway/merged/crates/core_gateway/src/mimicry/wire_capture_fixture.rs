//! HUAKAI 测试夹具: 本地 TCP listener 抓 ClientHello 原始字节。
//!
//! 用于 R-2-B-4 byte-level wire match 验证。server 端不完成 TLS 握手,
//! 只读取 client 发出的第一条 TLS record, 再按公开 TLS ClientHello
//! 字节结构解析。这里不读 rustls / openssl / boring / tls-parser source。

use std::{net::SocketAddr, time::Duration};

use tokio::{
    io::{AsyncRead, AsyncReadExt, DuplexStream},
    net::TcpListener,
};

const CAPTURE_TIMEOUT: Duration = Duration::from_secs(5);
const MAX_TLS_RECORD_LEN: usize = 16 * 1024 + 2048;

pub type CaptureHandle = tokio::task::JoinHandle<Vec<u8>>;

pub struct CapturedClientHello {
    pub raw_bytes: Vec<u8>,
    pub parsed: ClientHelloFields,
}

pub struct ClientHelloFields {
    /// ClientHello body 内的 legacy_version。
    pub tls_version: u16,
    /// HUAKAI profile sample 使用的 JA3 version: 有 supported_versions 时取首个非 GREASE 值。
    pub ja3_version: u16,
    pub cipher_suites: Vec<u16>,
    pub extensions: Vec<u16>,
    pub supported_groups: Vec<u16>,
    pub ec_point_formats: Vec<u8>,
    pub supported_versions: Vec<u16>,
    pub sni_hostname: Option<String>,
    /// W11-F §14b S1-3 (Codex review 2026-05-27): payload of TLS ext 27
    /// (compress_certificate, RFC 8879). Parsed list of IANA algorithm
    /// IDs the client advertises as accept-able for server cert
    /// compression. Empty when ext 27 not present. Without this field
    /// the byte-level wire test could only assert ext-presence — a
    /// mutation that replaced the advertised algorithm list with an
    /// empty list would still emit ext 27 with valid framing and pass
    /// the JA3 hash check.
    pub cert_compression_algorithms: Vec<u16>,
    /// W11-F §14b S1-3 (Codex review 2026-05-27): payload of TLS ext
    /// 17513 (ALPS legacy codepoint per RFC 9329 draft) and/or 17613
    /// (new codepoint). Parsed list of ALPN-style protocol names the
    /// client advertises ALPS settings for. Empty when neither ext is
    /// present. Without this field the byte-level wire test could only
    /// assert ext-presence — a mutation that replaced `b"h2"` with an
    /// empty payload (or wrong protocol name) would still emit a
    /// well-framed ext 17513 and pass the JA3 hash check.
    pub alps_protocols: Vec<String>,
}

pub async fn spawn_capture_listener() -> (SocketAddr, CaptureHandle) {
    try_spawn_capture_listener().await.unwrap()
}

pub async fn try_spawn_capture_listener() -> std::io::Result<(SocketAddr, CaptureHandle)> {
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let addr = listener.local_addr()?;
    let handle = tokio::spawn(async move {
        let (mut sock, _) = listener.accept().await.unwrap();
        read_first_tls_record(&mut sock).await.unwrap_or_default()
    });
    Ok((addr, handle))
}

pub fn spawn_capture_duplex() -> (DuplexStream, CaptureHandle) {
    let (client, mut server) = tokio::io::duplex(8192);
    let handle =
        tokio::spawn(async move { read_first_tls_record(&mut server).await.unwrap_or_default() });
    (client, handle)
}

pub fn parse_client_hello(raw: &[u8]) -> Result<ClientHelloFields, &'static str> {
    if raw.len() < 5 || raw[0] != 0x16 {
        return Err("not a TLS handshake record");
    }

    let record_len = u16::from_be_bytes([raw[3], raw[4]]) as usize;
    if record_len > MAX_TLS_RECORD_LEN {
        return Err("TLS record too large");
    }
    if raw.len() < 5 + record_len {
        return Err("truncated TLS record");
    }

    let record = &raw[5..5 + record_len];
    let mut record_reader = WireReader::new(record);
    let handshake_type = record_reader.read_u8()?;
    if handshake_type != 0x01 {
        return Err("not a ClientHello handshake");
    }
    let handshake_len = record_reader.read_u24()?;
    let body = record_reader.take(handshake_len)?;

    let mut reader = WireReader::new(body);
    let tls_version = reader.read_u16()?;
    reader.skip(32)?;

    let session_id_len = reader.read_u8()? as usize;
    reader.skip(session_id_len)?;

    let cipher_suites_len = reader.read_u16()? as usize;
    if cipher_suites_len % 2 != 0 {
        return Err("invalid cipher suite list length");
    }
    let cipher_suites_end = reader.position() + cipher_suites_len;
    let mut cipher_suites = Vec::with_capacity(cipher_suites_len / 2);
    while reader.position() < cipher_suites_end {
        cipher_suites.push(reader.read_u16()?);
    }

    let compression_methods_len = reader.read_u8()? as usize;
    reader.skip(compression_methods_len)?;

    let mut extensions = Vec::new();
    let mut supported_groups = Vec::new();
    let mut ec_point_formats = Vec::new();
    let mut supported_versions = Vec::new();
    let mut sni_hostname = None;
    let mut cert_compression_algorithms = Vec::new();
    let mut alps_protocols = Vec::new();

    if reader.remaining() == 0 {
        return Ok(ClientHelloFields {
            tls_version,
            ja3_version: tls_version,
            cipher_suites,
            extensions,
            supported_groups,
            ec_point_formats,
            supported_versions,
            sni_hostname,
            cert_compression_algorithms,
            alps_protocols,
        });
    }

    let extensions_len = reader.read_u16()? as usize;
    if reader.remaining() < extensions_len {
        return Err("truncated extensions block");
    }
    let extensions_end = reader.position() + extensions_len;

    while reader.position() < extensions_end {
        let extension_type = reader.read_u16()?;
        let extension_data_len = reader.read_u16()? as usize;
        let extension_data = reader.take(extension_data_len)?;
        extensions.push(extension_type);

        match extension_type {
            0 => sni_hostname = parse_sni_hostname(extension_data).or(sni_hostname),
            10 => supported_groups = parse_supported_groups(extension_data)?,
            11 => ec_point_formats = parse_ec_point_formats(extension_data)?,
            27 => cert_compression_algorithms = parse_cert_compression_algorithms(extension_data)?,
            43 => supported_versions = parse_supported_versions(extension_data)?,
            // 17513 = ALPS legacy codepoint (Chrome), 17613 = standard codepoint.
            // Both share the same wire format (proto_list u16-prefixed, each
            // entry u8-prefixed bytes).
            17513 | 17613 => {
                let parsed = parse_alps_protocols(extension_data)?;
                // If both old and new codepoint appear (unusual), merge.
                alps_protocols.extend(parsed);
            }
            _ => {}
        }
    }

    if reader.position() != extensions_end {
        return Err("invalid extensions block length");
    }

    let ja3_version = supported_versions
        .iter()
        .copied()
        .find(|value| !crate::mimicry::ja3_wire::is_grease(*value))
        .unwrap_or(tls_version);

    Ok(ClientHelloFields {
        tls_version,
        ja3_version,
        cipher_suites,
        extensions,
        supported_groups,
        ec_point_formats,
        supported_versions,
        sni_hostname,
        cert_compression_algorithms,
        alps_protocols,
    })
}

async fn read_first_tls_record<S>(sock: &mut S) -> Result<Vec<u8>, std::io::Error>
where
    S: AsyncRead + Unpin,
{
    let mut raw = vec![0u8; 4096];
    let first_read = match tokio::time::timeout(CAPTURE_TIMEOUT, sock.read(&mut raw)).await {
        Ok(Ok(read)) => read,
        Ok(Err(error)) => return Err(error),
        Err(_) => return Ok(Vec::new()),
    };
    raw.truncate(first_read);

    if raw.len() < 5 {
        return Ok(raw);
    }

    let record_len = u16::from_be_bytes([raw[3], raw[4]]) as usize;
    let wanted_len = 5 + record_len.min(MAX_TLS_RECORD_LEN);
    while raw.len() < wanted_len {
        let mut chunk = vec![0u8; wanted_len - raw.len()];
        let read = match tokio::time::timeout(CAPTURE_TIMEOUT, sock.read(&mut chunk)).await {
            Ok(Ok(0)) | Err(_) => break,
            Ok(Ok(read)) => read,
            Ok(Err(error)) => return Err(error),
        };
        chunk.truncate(read);
        raw.extend_from_slice(&chunk);
    }

    Ok(raw)
}

fn parse_sni_hostname(data: &[u8]) -> Option<String> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u16().ok()? as usize;
    if reader.remaining() < list_len {
        return None;
    }
    let list_end = reader.position() + list_len;

    while reader.position() < list_end {
        let name_type = reader.read_u8().ok()?;
        let name_len = reader.read_u16().ok()? as usize;
        let name = reader.take(name_len).ok()?;
        if name_type == 0 {
            return std::str::from_utf8(name).ok().map(str::to_owned);
        }
    }

    None
}

fn parse_supported_groups(data: &[u8]) -> Result<Vec<u16>, &'static str> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u16()? as usize;
    if list_len % 2 != 0 || reader.remaining() < list_len {
        return Err("invalid supported_groups length");
    }
    let list_end = reader.position() + list_len;
    let mut groups = Vec::with_capacity(list_len / 2);
    while reader.position() < list_end {
        groups.push(reader.read_u16()?);
    }
    Ok(groups)
}

fn parse_ec_point_formats(data: &[u8]) -> Result<Vec<u8>, &'static str> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u8()? as usize;
    if reader.remaining() < list_len {
        return Err("invalid ec_point_formats length");
    }
    Ok(reader.take(list_len)?.to_vec())
}

fn parse_supported_versions(data: &[u8]) -> Result<Vec<u16>, &'static str> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u8()? as usize;
    if list_len % 2 != 0 || reader.remaining() < list_len {
        return Err("invalid supported_versions length");
    }
    let list_end = reader.position() + list_len;
    let mut versions = Vec::with_capacity(list_len / 2);
    while reader.position() < list_end {
        versions.push(reader.read_u16()?);
    }
    Ok(versions)
}

/// W11-F §14b S1-3 (Codex review 2026-05-27): parse the body of TLS ext 27
/// (compress_certificate, RFC 8879). Wire shape:
///
///   uint8  algorithms_list_len_in_bytes
///   uint16 algorithm_id [ ]
///
/// Returns the algorithm IDs in the order they appeared on the wire.
fn parse_cert_compression_algorithms(data: &[u8]) -> Result<Vec<u16>, &'static str> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u8()? as usize;
    if list_len % 2 != 0 || reader.remaining() < list_len {
        return Err("invalid cert_compression algorithms length");
    }
    let list_end = reader.position() + list_len;
    let mut algorithms = Vec::with_capacity(list_len / 2);
    while reader.position() < list_end {
        algorithms.push(reader.read_u16()?);
    }
    Ok(algorithms)
}

/// W11-F §14b S1-3 (Codex review 2026-05-27): parse the body of TLS ext
/// 17513 (ALPS legacy / Chrome codepoint) or 17613 (standard codepoint).
/// Wire shape (per BoringSSL ext_alps_add_clienthello in extensions.cc):
///
///   uint16 proto_list_len_in_bytes
///   {
///     uint8  proto_name_len
///     opaque proto_name [proto_name_len]
///   } [ ]
///
/// Returns the protocol names (utf-8 decoded) in capture order. ALPS only
/// transports ALPN-style ASCII names ("h2", "http/1.1", etc.), so utf-8
/// validation is sufficient; invalid utf-8 is rejected.
fn parse_alps_protocols(data: &[u8]) -> Result<Vec<String>, &'static str> {
    let mut reader = WireReader::new(data);
    let list_len = reader.read_u16()? as usize;
    if reader.remaining() < list_len {
        return Err("invalid alps proto_list length");
    }
    let list_end = reader.position() + list_len;
    let mut protocols = Vec::new();
    while reader.position() < list_end {
        let name_len = reader.read_u8()? as usize;
        let name_bytes = reader.take(name_len)?;
        let name = std::str::from_utf8(name_bytes)
            .map_err(|_| "alps protocol name is not valid utf-8")?;
        protocols.push(name.to_owned());
    }
    if reader.position() != list_end {
        return Err("invalid alps proto_list internal layout");
    }
    Ok(protocols)
}

struct WireReader<'a> {
    bytes: &'a [u8],
    offset: usize,
}

impl<'a> WireReader<'a> {
    fn new(bytes: &'a [u8]) -> Self {
        Self { bytes, offset: 0 }
    }

    fn position(&self) -> usize {
        self.offset
    }

    fn remaining(&self) -> usize {
        self.bytes.len().saturating_sub(self.offset)
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
        let end = self.offset.checked_add(len).ok_or("offset overflow")?;
        if end > self.bytes.len() {
            return Err("truncated ClientHello");
        }
        let out = &self.bytes[self.offset..end];
        self.offset = end;
        Ok(out)
    }
}
