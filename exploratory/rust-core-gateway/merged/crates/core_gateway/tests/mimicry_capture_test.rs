// D3 burn-the-boats: no fallback to hyper-rustls, fix mimicry path instead

mod common;

use std::{net::SocketAddr, time::Duration};

use common::tls_capture::{CaptureError, spawn_capture_once};
use tokio::{io::AsyncWriteExt, net::TcpStream};

#[tokio::test]
async fn malformed_tls_truncated_record_body_returns_io_error() {
    let mut bytes = vec![22, 0x03, 0x03, 0x00, 0x64];
    bytes.extend([0u8; 50]);

    let err = capture_malformed_tls_bytes(bytes).await;
    assert!(
        matches!(
            err,
            CaptureError::Io {
                context: "reading TLS record body",
                ..
            }
        ),
        "截断 TLS record body 应返回读取 record body 的 Io 错误，实际: {err:?}"
    );
}

#[tokio::test]
async fn malformed_tls_bad_handshake_len_returns_length_overflow() {
    let err = capture_malformed_tls_bytes(tls_record(vec![1, 0x00, 0x00, 0x64])).await;
    assert!(
        matches!(
            err,
            CaptureError::LengthOverflow {
                context: "ClientHello handshake body",
                needed: 100,
                offset: 4,
                remaining: 0,
            }
        ),
        "过大的 handshake_len 应返回 ClientHello handshake body 的 LengthOverflow，实际: {err:?}"
    );
}

#[tokio::test]
async fn malformed_tls_odd_cipher_suites_len_returns_odd_u16_list() {
    let err = capture_malformed_tls_bytes(client_hello_record(
        client_hello_body_with_cipher_suites(&[0x13]),
    ))
    .await;
    assert!(
        matches!(
            err,
            CaptureError::OddU16List {
                context: "ClientHello cipher_suites",
                len: 1,
            }
        ),
        "奇数字节长度的 cipher_suites 应返回 OddU16List，实际: {err:?}"
    );
}

#[tokio::test]
async fn malformed_tls_extension_payload_len_overflow_returns_length_overflow() {
    let mut extension_bytes = Vec::new();
    extension_bytes.extend(0x002bu16.to_be_bytes());
    extension_bytes.extend(5u16.to_be_bytes());
    extension_bytes.push(0xaa);

    let err = capture_malformed_tls_bytes(client_hello_record(client_hello_body_with_extensions(
        &extension_bytes,
    )))
    .await;
    assert!(
        matches!(
            err,
            CaptureError::LengthOverflow {
                context: "extension data",
                needed: 5,
                offset: 4,
                remaining: 1,
            }
        ),
        "extension payload 声明长度超过剩余 bytes 时应返回 LengthOverflow，实际: {err:?}"
    );
}

#[tokio::test]
async fn malformed_tls_supported_groups_nested_len_overflow_returns_length_overflow() {
    let extension_bytes = extension_with_payload(10, &[0x00, 0x04, 0x00, 0x1d]);

    let err = capture_malformed_tls_bytes(client_hello_record(client_hello_body_with_extensions(
        &extension_bytes,
    )))
    .await;
    assert!(
        matches!(
            err,
            CaptureError::LengthOverflow {
                context: "supported_groups",
                needed: 4,
                offset: 2,
                remaining: 2,
            }
        ),
        "supported_groups 内层 list_length 超过 payload 时应返回 LengthOverflow，实际: {err:?}"
    );
}

#[tokio::test]
async fn malformed_tls_ec_point_formats_len_overflow_returns_length_overflow() {
    let extension_bytes = extension_with_payload(11, &[0x03, 0x00]);

    let err = capture_malformed_tls_bytes(client_hello_record(client_hello_body_with_extensions(
        &extension_bytes,
    )))
    .await;
    assert!(
        matches!(
            err,
            CaptureError::LengthOverflow {
                context: "ec_point_formats",
                needed: 3,
                offset: 1,
                remaining: 1,
            }
        ),
        "ec_point_formats 内层 list_length 超过 payload 时应返回 LengthOverflow，实际: {err:?}"
    );
}

#[tokio::test]
async fn malformed_tls_non_client_hello_type_returns_unexpected_handshake_type() {
    let err = capture_malformed_tls_bytes(tls_record(vec![2, 0x00, 0x00, 0x00])).await;
    assert!(
        matches!(err, CaptureError::UnexpectedHandshakeType(2)),
        "非 ClientHello handshake type 应返回 UnexpectedHandshakeType(2)，实际: {err:?}"
    );
}

async fn capture_malformed_tls_bytes(bytes: Vec<u8>) -> CaptureError {
    let bind_addr: SocketAddr = "127.0.0.1:0"
        .parse()
        .expect("本地 ephemeral capture 地址应合法");
    let (addr, capture_task) = spawn_capture_once(bind_addr)
        .await
        .expect("capture listener 应可启动");

    let mut client = TcpStream::connect(addr)
        .await
        .expect("malformed TLS client 应能连接 capture listener");
    client
        .write_all(&bytes)
        .await
        .expect("malformed TLS bytes 应能写入本地 TcpStream");
    client
        .shutdown()
        .await
        .expect("malformed TLS client 应能关闭写半边连接");

    let joined = tokio::time::timeout(Duration::from_secs(5), capture_task)
        .await
        .expect("capture task 应在畸形 TLS 输入后结束");
    assert!(joined.is_ok(), "capture task 不应 panic: {joined:?}");

    match joined.unwrap() {
        Ok(capture) => panic!("畸形 TLS bytes 不应解析成功，实际 capture: {capture:?}"),
        Err(err) => err,
    }
}

fn tls_record(body: Vec<u8>) -> Vec<u8> {
    let body_len = u16::try_from(body.len()).expect("测试 record body 长度应适配 TLS u16");
    let mut record = vec![22, 0x03, 0x03];
    record.extend(body_len.to_be_bytes());
    record.extend(body);
    record
}

fn client_hello_record(body: Vec<u8>) -> Vec<u8> {
    let body_len = body.len();
    assert!(
        body_len <= 0x00ff_ffff,
        "测试 ClientHello body 长度应适配 u24 handshake_len"
    );

    let mut handshake = vec![
        1,
        ((body_len >> 16) & 0xff) as u8,
        ((body_len >> 8) & 0xff) as u8,
        (body_len & 0xff) as u8,
    ];
    handshake.extend(body);
    tls_record(handshake)
}

fn client_hello_body_with_cipher_suites(cipher_suites: &[u8]) -> Vec<u8> {
    let mut body = Vec::new();
    body.extend(0x0303u16.to_be_bytes());
    body.extend([0u8; 32]);
    body.push(0);
    body.extend(
        u16::try_from(cipher_suites.len())
            .expect("测试 cipher_suites 长度应适配 u16")
            .to_be_bytes(),
    );
    body.extend(cipher_suites);
    body
}

fn client_hello_body_with_extensions(extension_bytes: &[u8]) -> Vec<u8> {
    let mut body = client_hello_body_with_cipher_suites(&[0x13, 0x01]);
    body.push(1);
    body.push(0);
    body.extend(
        u16::try_from(extension_bytes.len())
            .expect("测试 extensions 长度应适配 u16")
            .to_be_bytes(),
    );
    body.extend(extension_bytes);
    body
}

fn extension_with_payload(extension_id: u16, payload: &[u8]) -> Vec<u8> {
    let mut extension = Vec::new();
    extension.extend(extension_id.to_be_bytes());
    extension.extend(
        u16::try_from(payload.len())
            .expect("测试 extension payload 长度应适配 u16")
            .to_be_bytes(),
    );
    extension.extend(payload);
    extension
}
