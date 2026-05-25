#![cfg(feature = "mimicry-http2-fork")]

use std::collections::BTreeMap;

use core_gateway::mimicry::{
    BuiltinProfile,
    http2_adapter::{HttpTwoAdapterError, HttpTwoMimicryAdapter, capture_first_request_frames},
    load_builtin_profile,
};
use http::{Request, Version};
use tokio::sync::oneshot;

#[test]
fn http2_adapter_rejects_builtin_codex_until_h2_capture_is_backfilled() {
    let profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");

    let error = HttpTwoMimicryAdapter::new_with_profile(&profile)
        .expect_err("内置 codex 模板尚未有真实 h2 SETTINGS/pseudo-header 抓包");

    assert!(
        matches!(error, HttpTwoAdapterError::MissingProfileField { .. }),
        "缺少真实 h2 字段时必须显式失败，实际: {error}"
    );
}

#[tokio::test]
async fn http2_adapter_encodes_initial_settings_in_profile_order() {
    let profile = codex_profile_with_h2_order();
    let adapter = HttpTwoMimicryAdapter::new_with_profile(&profile).expect("adapter 应构造成功");

    let exchange = adapter
        .encode_request_exchange(codex_request())
        .await
        .expect("内存 H2 capture 应成功");
    let pairs = parse_settings_id_value_pairs(&exchange.initial_settings_frame);
    let expected_ids: Vec<u16> = profile.h2_settings_order.iter().cloned().collect();
    let expected_values: Vec<(u16, u32)> = profile
        .h2_settings_order
        .iter()
        .map(|id| {
            (
                *id,
                *profile
                    .h2_settings_values
                    .get(id)
                    .expect("测试 profile 每个 SETTINGS id 都应有值"),
            )
        })
        .collect();

    eprintln!(
        "L2-A6 settings bytes sample: {}",
        hex_prefix(&exchange.initial_settings_frame, 64)
    );
    assert_eq!(
        pairs.iter().map(|(id, _)| *id).collect::<Vec<_>>(),
        expected_ids
    );
    assert_eq!(pairs, expected_values);
}

#[tokio::test]
async fn http2_adapter_encodes_request_pseudo_headers_in_profile_order() {
    let profile = codex_profile_with_h2_order();
    let adapter = HttpTwoMimicryAdapter::new_with_profile(&profile).expect("adapter 应构造成功");

    let exchange = adapter
        .encode_request_exchange(codex_request())
        .await
        .expect("内存 H2 capture 应成功");
    eprintln!(
        "L2-A6 headers bytes sample: {}",
        hex_prefix(&exchange.request_headers_frame, 96)
    );
    let actual_order = parse_pseudo_header_order(&exchange.request_headers_frame);

    assert_eq!(actual_order, profile.h2_pseudo_header_order);
}

/// W11-F F-1.b (Owner-approved synthesis 2026-05-25): loopback TCP capture
/// proves `HttpTwoMimicryAdapter::drive_request` works over real
/// `tokio::net::TcpStream`, not just in-memory `DuplexStream`. Spawns an
/// in-process minimal H2 peer using `tokio::net::TcpListener` + the new
/// `capture_first_request_frames` helper, accepts the fork client's
/// TcpStream, and asserts the same SETTINGS + HEADERS order assertions as
/// the in-memory tests above — but with bytes that traversed real loopback
/// TCP.
///
/// `oneshot` channel ensures the test goes red if the listener is never
/// hit (e.g. a future mutation silently falling back to in-memory duplex);
/// without that proof, this test could degenerate into the same path as
/// the in-memory tests and stop discriminating.
///
/// Mutation discriminators (CLAUDE.md #14):
/// - If `drive_request` ever stops invoking `send_request.send_request(...)`,
///   the HEADERS frame never flushes onto the TCP socket and the server
///   `capture_first_request_frames` task times out reading frames → test red.
/// - If `apply_settings` stops calling `builder.settings_order(...)`, the
///   captured SETTINGS order reverts to the fork default and the
///   `expected_ids` assertion goes red.
/// - If `apply_pseudo_order` stops calling `headers_pseudo_order`, the
///   HEADERS HPACK pseudo-header sequence diverges from the profile and
///   `actual_pseudo_order` assertion goes red.
/// - If the listener-hit `oneshot` is ever swapped to fire BEFORE
///   `listener.accept().await` returns, this test no longer proves the
///   client connected — keep that ordering.
#[tokio::test]
async fn loopback_tcp_capture_matches_profile_order() {
    let profile = codex_profile_with_h2_order();
    let adapter = HttpTwoMimicryAdapter::new_with_profile(&profile).expect("adapter 应构造成功");

    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("loopback listener bind");
    let server_addr = listener.local_addr().expect("listener has local_addr");

    let (listener_hit_tx, listener_hit_rx) = oneshot::channel::<()>();
    let server_task = tokio::spawn(async move {
        let (peer_stream, _client_addr) = listener.accept().await.expect("loopback accept");
        // Send the listener-hit signal AFTER accept resolves so the test
        // can prove the client actually connected over real TCP.
        let _ = listener_hit_tx.send(());
        capture_first_request_frames(peer_stream).await
    });

    let tcp_stream = tokio::net::TcpStream::connect(server_addr)
        .await
        .expect("loopback connect");
    let connection_task = adapter
        .drive_request(tcp_stream, codex_request())
        .await
        .expect("drive_request 应成功 over TCP");

    // Confirm the listener was actually hit; if drive_request silently fell
    // back to a non-TCP path, this await would time out.
    tokio::time::timeout(std::time::Duration::from_secs(3), listener_hit_rx)
        .await
        .expect("listener-hit signal must fire within 3s")
        .expect("oneshot receive");

    let exchange = server_task
        .await
        .expect("server task joined")
        .expect("capture over loopback should succeed");
    connection_task.abort();
    let _ = connection_task.await;

    // Same byte-level assertions as the in-memory test, but on bytes that
    // crossed real loopback TCP.
    let pairs = parse_settings_id_value_pairs(&exchange.initial_settings_frame);
    let expected_ids: Vec<u16> = profile.h2_settings_order.iter().cloned().collect();
    let expected_values: Vec<(u16, u32)> = profile
        .h2_settings_order
        .iter()
        .map(|id| {
            (
                *id,
                *profile
                    .h2_settings_values
                    .get(id)
                    .expect("测试 profile 每个 SETTINGS id 都应有值"),
            )
        })
        .collect();
    eprintln!(
        "F-1.b loopback TCP SETTINGS bytes: {}",
        hex_prefix(&exchange.initial_settings_frame, 64)
    );
    assert_eq!(
        pairs.iter().map(|(id, _)| *id).collect::<Vec<_>>(),
        expected_ids,
        "loopback TCP SETTINGS id order must match profile (F-1.b)"
    );
    assert_eq!(
        pairs, expected_values,
        "loopback TCP SETTINGS values must match profile (F-1.b)"
    );

    let actual_pseudo_order = parse_pseudo_header_order(&exchange.request_headers_frame);
    eprintln!(
        "F-1.b loopback TCP HEADERS bytes: {}",
        hex_prefix(&exchange.request_headers_frame, 96)
    );
    assert_eq!(
        actual_pseudo_order, profile.h2_pseudo_header_order,
        "loopback TCP HEADERS pseudo-header order must match profile (F-1.b)"
    );
}

fn codex_profile_with_h2_order() -> core_gateway::mimicry::FingerprintProfile {
    let mut profile = load_builtin_profile(BuiltinProfile::CodexCli).expect("codex profile 应加载");
    let settings_order = vec![4, 1, 6, 5, 2, 3];
    let settings_values = BTreeMap::from([
        (4, 65_535),
        (1, 4_096),
        (6, 262_144),
        (5, 16_384),
        (2, 0),
        (3, 100),
    ]);
    let pseudo_header_order = vec![
        ":method".to_owned(),
        ":authority".to_owned(),
        ":scheme".to_owned(),
        ":path".to_owned(),
    ];

    profile.h2_settings_frame.available = true;
    profile.h2_settings_frame.raw_order = settings_order.clone();
    profile.h2_settings_frame.values = settings_values.clone();
    profile.h2_settings_frame.source =
        Some("L2-A6 synthetic encoder-level test profile".to_owned());
    profile.h2_settings_order = settings_order;
    profile.h2_settings_values = settings_values;
    profile.h2_pseudo_header_capture.available = true;
    profile.h2_pseudo_header_capture.order = pseudo_header_order.clone();
    profile.h2_pseudo_header_capture.source =
        Some("L2-A6 synthetic encoder-level test profile".to_owned());
    profile.h2_pseudo_header_order = pseudo_header_order;
    profile
}

fn codex_request() -> Request<()> {
    Request::builder()
        .method("POST")
        .uri("https://chatgpt.com/backend-api/codex/responses")
        .version(Version::HTTP_2)
        .header(
            "user-agent",
            "codex_cli_rs/0.128.0 (<OS> <OS_VERSION>; <ARCH>)",
        )
        .body(())
        .expect("test request 应可构造")
}

fn parse_settings_id_value_pairs(frame: &[u8]) -> Vec<(u16, u32)> {
    assert_eq!(frame_type(frame), 0x04, "必须是 SETTINGS frame");
    let payload = frame_payload(frame);
    assert_eq!(payload.len() % 6, 0, "SETTINGS payload 必须是 6-byte entry");

    payload
        .chunks_exact(6)
        .map(|chunk| {
            (
                u16::from_be_bytes([chunk[0], chunk[1]]),
                u32::from_be_bytes([chunk[2], chunk[3], chunk[4], chunk[5]]),
            )
        })
        .collect()
}

fn parse_pseudo_header_order(frame: &[u8]) -> Vec<String> {
    assert_eq!(frame_type(frame), 0x01, "必须是 HEADERS frame");
    let flags = frame[4];
    let mut payload = frame_payload(frame);
    if flags & 0x08 != 0 {
        let pad_len = payload[0] as usize;
        payload = &payload[1..payload.len() - pad_len];
    }
    if flags & 0x20 != 0 {
        payload = &payload[5..];
    }

    pseudo_names_from_hpack(payload)
}

fn pseudo_names_from_hpack(block: &[u8]) -> Vec<String> {
    let mut offset = 0;
    let mut names = Vec::new();

    while offset < block.len() {
        let byte = block[offset];
        if byte & 0x80 != 0 {
            let index = decode_prefixed_integer(block, &mut offset, 7);
            if let Some(name) = static_pseudo_name(index) {
                names.push(name.to_owned());
            } else if !names.is_empty() {
                break;
            }
            continue;
        }

        if byte & 0x20 != 0 {
            let _ = decode_prefixed_integer(block, &mut offset, 5);
            continue;
        }

        let prefix_bits = if byte & 0x40 != 0 { 6 } else { 4 };
        let name_index = decode_prefixed_integer(block, &mut offset, prefix_bits);
        if name_index == 0 {
            if !names.is_empty() {
                break;
            }
            skip_hpack_string(block, &mut offset);
        } else if let Some(name) = static_pseudo_name(name_index) {
            names.push(name.to_owned());
        } else if !names.is_empty() {
            break;
        }
        skip_hpack_string(block, &mut offset);
    }

    names
}

fn static_pseudo_name(index: usize) -> Option<&'static str> {
    match index {
        1 => Some(":authority"),
        2 | 3 => Some(":method"),
        4 | 5 => Some(":path"),
        6 | 7 => Some(":scheme"),
        8 => Some(":status"),
        _ => None,
    }
}

fn decode_prefixed_integer(block: &[u8], offset: &mut usize, prefix_bits: u8) -> usize {
    assert!(*offset < block.len(), "HPACK integer offset 越界");
    let mask = (1u8 << prefix_bits) - 1;
    let mut value = (block[*offset] & mask) as usize;
    *offset += 1;
    if value < mask as usize {
        return value;
    }

    let mut shift = 0;
    loop {
        assert!(*offset < block.len(), "HPACK continuation 越界");
        let byte = block[*offset];
        *offset += 1;
        value += ((byte & 0x7f) as usize) << shift;
        if byte & 0x80 == 0 {
            return value;
        }
        shift += 7;
    }
}

fn skip_hpack_string(block: &[u8], offset: &mut usize) {
    let len = decode_prefixed_integer(block, offset, 7);
    assert!(
        *offset + len <= block.len(),
        "HPACK string length 超过 header block"
    );
    *offset += len;
}

fn frame_payload(frame: &[u8]) -> &[u8] {
    assert!(frame.len() >= 9, "HTTP/2 frame header 至少 9 bytes");
    let len = ((frame[0] as usize) << 16) | ((frame[1] as usize) << 8) | frame[2] as usize;
    assert_eq!(frame.len(), 9 + len, "frame length 与 payload 不一致");
    &frame[9..]
}

fn frame_type(frame: &[u8]) -> u8 {
    assert!(frame.len() >= 4, "HTTP/2 frame header 至少 4 bytes");
    frame[3]
}

fn hex_prefix(bytes: &[u8], max_len: usize) -> String {
    bytes
        .iter()
        .take(max_len)
        .map(|byte| format!("{byte:02x}"))
        .collect::<Vec<_>>()
        .join(" ")
}
