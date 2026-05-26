"""HUAKAI fingerprint-collector: local h2 SETTINGS + HEADERS frame capture server.

W11-F F-1.g attempt-2 (2026-05-26): server-side capture replaces the deleted
mitmproxy 12 addon path (which could not expose SETTINGS frame bytes via any
addon API surface). See docs/process/plans/2026-05-26-f1g-server-side-capture.md.

This server terminates TLS with a self-signed cert + ALPN=h2 and captures the
connecting client's HTTP/2 SETTINGS frame (raw bytes + parsed parameters in
on-wire order) plus HEADERS frame pseudo-header order. It is NOT a proxy and
does NOT forward; it sends a minimal 200 response so the client closes cleanly.

Usage
-----
    python tools/fingerprint-collector/h2_capture_server.py [--port 18099]

Then in another terminal run a client probe:
    node tools/fingerprint-collector/clients/undici_probe.mjs

Each connection appends one JSON object to
tools/fingerprint-collector/captures/h2-server-<unix-ts>.jsonl

Scope
-----
The server is a local listener. It does NOT contact api.anthropic.com or any
external service. It does NOT decrypt any TLS payload other than its own
local connection (which it terminates as the legitimate endpoint). Use ONLY
for capturing the fingerprint of HTTP/2 client libraries running on the
operator's own machine.

License
-------
HUAKAI original implementation, MIT-licensed. Built on the BSD-licensed `h2`
library (https://python-hyper.org/projects/h2/) and BSD-licensed `hpack`.
"""

from __future__ import annotations

import argparse
import json
import socket
import ssl
import subprocess
import sys
import threading
import time
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple

try:
    import h2.config
    import h2.connection
    import h2.events
    from hpack import Decoder as HpackDecoder
except ImportError as exc:
    print(f"ERROR: missing dependency: {exc}", file=sys.stderr)
    print("Install: pip install h2 hpack", file=sys.stderr)
    sys.exit(2)


HERE = Path(__file__).resolve().parent
CERT_DIR = HERE / "tls_cert"
CERT_PATH = CERT_DIR / "server.crt"
KEY_PATH = CERT_DIR / "server.key"
CAPTURE_DIR = HERE / "captures"

HTTP2_PREFACE = b"PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

# SETTINGS parameter names per RFC 7540 §6.5.2.
SETTINGS_NAMES = {
    0x01: "SETTINGS_HEADER_TABLE_SIZE",
    0x02: "SETTINGS_ENABLE_PUSH",
    0x03: "SETTINGS_MAX_CONCURRENT_STREAMS",
    0x04: "SETTINGS_INITIAL_WINDOW_SIZE",
    0x05: "SETTINGS_MAX_FRAME_SIZE",
    0x06: "SETTINGS_MAX_HEADER_LIST_SIZE",
}

FRAME_TYPE_SETTINGS = 0x04
FRAME_TYPE_HEADERS = 0x01
FRAME_HEADER_SIZE = 9


def ensure_self_signed_cert() -> None:
    """Create a self-signed cert + key in CERT_DIR if missing.

    Uses openssl shell-out so we don't add a Python `cryptography` runtime dep.
    """
    CERT_DIR.mkdir(parents=True, exist_ok=True)
    if CERT_PATH.exists() and KEY_PATH.exists():
        return
    print(f"[setup] generating self-signed cert at {CERT_PATH}", file=sys.stderr)
    cmd = [
        "openssl", "req", "-x509", "-newkey", "rsa:2048",
        "-keyout", str(KEY_PATH),
        "-out", str(CERT_PATH),
        "-sha256", "-days", "30", "-nodes",
        "-subj", "/CN=localhost",
        "-addext", "subjectAltName=DNS:localhost,IP:127.0.0.1",
    ]
    try:
        proc = subprocess.run(cmd, capture_output=True, text=True)
    except FileNotFoundError:
        print("ERROR: openssl not on PATH. Install Git-for-Windows or scoop openssl.", file=sys.stderr)
        sys.exit(2)
    if proc.returncode != 0:
        print(f"openssl failed (rc={proc.returncode}):\n{proc.stderr}", file=sys.stderr)
        sys.exit(2)


def parse_settings_frame_payload(payload: bytes) -> List[Dict[str, int]]:
    """Parse a SETTINGS frame payload into ordered [{id, value}, ...].

    SETTINGS payload (RFC 7540 §6.5.1) is a packed array of 6-byte parameters,
    each [2-byte big-endian identifier][4-byte big-endian value]. **Order is
    preserved exactly as sent by the client** — this is the H2 fingerprint axis
    F-1.a requires.

    Raises ValueError if length is not a multiple of 6.
    """
    if len(payload) % 6 != 0:
        raise ValueError(f"SETTINGS payload not multiple of 6 bytes: {len(payload)}")
    params: List[Dict[str, int]] = []
    for off in range(0, len(payload), 6):
        ident = int.from_bytes(payload[off : off + 2], "big")
        value = int.from_bytes(payload[off + 2 : off + 6], "big")
        params.append({"id": ident, "value": value})
    return params


def annotate_settings(params: List[Dict[str, int]]) -> List[Dict[str, Any]]:
    """Decorate raw params with human-readable names."""
    return [
        {
            "id": p["id"],
            "name": SETTINGS_NAMES.get(p["id"], f"UNKNOWN_0x{p['id']:04X}"),
            "value": p["value"],
        }
        for p in params
    ]


def read_exact(sock: ssl.SSLSocket, n: int) -> bytes:
    """Block until exactly n bytes have been read, or raise on EOF."""
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise ConnectionError(f"connection closed after {len(buf)} of {n} bytes")
        buf += chunk
    return buf


def read_frame_header(sock: ssl.SSLSocket) -> Optional[Dict[str, Any]]:
    """Read one 9-byte H2 frame header. Returns parsed dict or None on EOF."""
    try:
        hdr = read_exact(sock, FRAME_HEADER_SIZE)
    except ConnectionError:
        return None
    return {
        "length": int.from_bytes(hdr[0:3], "big"),
        "type": hdr[3],
        "flags": hdr[4],
        "stream_id": int.from_bytes(hdr[5:9], "big") & 0x7FFFFFFF,
        "raw_header_hex": hdr.hex(),
        "raw_header_bytes": hdr,
    }


def extract_header_block(payload: bytes, flags: int) -> bytes:
    """Strip PADDED / PRIORITY framing from a HEADERS frame payload to get the HPACK block."""
    block_start = 0
    block_end = len(payload)
    if flags & 0x08:  # PADDED
        pad_len = payload[0]
        block_start = 1
        block_end = len(payload) - pad_len
    if flags & 0x20:  # PRIORITY
        block_start += 5
    return payload[block_start:block_end]


def extract_frames(buf: bytes) -> Tuple[List[Tuple[bytes, bytes]], bytes]:
    """Parse all complete H2 frames at the start of ``buf``.

    Returns ``(frames, leftover)``:
    - ``frames`` is a list of ``(header_bytes, payload_bytes)`` tuples for every
      complete frame consumed from the front of ``buf``.
    - ``leftover`` is the trailing bytes that do NOT yet constitute a complete
      frame (header too short, or header present but payload incomplete).

    A frame is complete iff the 9-byte header plus its declared length payload
    are both fully present. Caller feeds ``leftover + <next chunk>`` back in on
    the next call so frames split by TCP segmentation reassemble losslessly.
    """
    frames: List[Tuple[bytes, bytes]] = []
    off = 0
    while off + FRAME_HEADER_SIZE <= len(buf):
        hdr = buf[off : off + FRAME_HEADER_SIZE]
        f_len = int.from_bytes(hdr[0:3], "big")
        if off + FRAME_HEADER_SIZE + f_len > len(buf):
            break
        payload = buf[off + FRAME_HEADER_SIZE : off + FRAME_HEADER_SIZE + f_len]
        frames.append((hdr, payload))
        off += FRAME_HEADER_SIZE + f_len
    return frames, buf[off:]


def handle_connection(
    raw_sock: socket.socket,
    peer: Tuple[str, int],
    ssl_context: ssl.SSLContext,
    out_path: Path,
) -> None:
    """One inbound connection: TLS handshake, capture H2 frames, log a JSONL record."""
    record: Dict[str, Any] = {
        "ts": time.time(),
        "peer": f"{peer[0]}:{peer[1]}",
    }
    ssock: Optional[ssl.SSLSocket] = None
    try:
        ssock = ssl_context.wrap_socket(raw_sock, server_side=True)
        record["tls_version"] = ssock.version()
        cipher = ssock.cipher()
        record["tls_cipher"] = cipher[0] if cipher else None
        record["alpn_negotiated"] = ssock.selected_alpn_protocol()

        if record["alpn_negotiated"] != "h2":
            record["error"] = f"ALPN negotiated '{record['alpn_negotiated']}', expected 'h2'"
            return

        # 1) Connection preface (24 bytes, RFC 7540 §3.5).
        preface = read_exact(ssock, len(HTTP2_PREFACE))
        record["preface_matches"] = preface == HTTP2_PREFACE
        record["preface_raw_hex"] = preface.hex()
        if not record["preface_matches"]:
            record["error"] = "client preface mismatch"
            return

        # 2) Client MUST send SETTINGS as the first frame (RFC 7540 §6.5).
        settings_hdr = read_frame_header(ssock)
        if not settings_hdr:
            record["error"] = "EOF before SETTINGS frame"
            return
        if settings_hdr["type"] != FRAME_TYPE_SETTINGS:
            record["error"] = f"expected SETTINGS first, got type=0x{settings_hdr['type']:02X}"
            return
        if settings_hdr["flags"] & 0x01:
            # ACK flag set on the FIRST settings = bogus client.
            record["error"] = "first SETTINGS frame has ACK flag set"
            return

        settings_payload = read_exact(ssock, settings_hdr["length"])
        params = parse_settings_frame_payload(settings_payload)
        record["client_settings_frame"] = {
            "frame_header_hex": settings_hdr["raw_header_hex"],
            "payload_hex": settings_payload.hex(),
            "payload_len": settings_hdr["length"],
            "parameters_in_order": annotate_settings(params),
        }

        # 3) Hand the bytes we already consumed off to h2 so it tracks state.
        conn = h2.connection.H2Connection(
            config=h2.config.H2Configuration(client_side=False, header_encoding="utf-8")
        )
        conn.initiate_connection()
        replay = HTTP2_PREFACE + settings_hdr["raw_header_bytes"] + settings_payload
        conn.receive_data(replay)
        outbound = conn.data_to_send()
        if outbound:
            ssock.sendall(outbound)

        # 4) Drain until we capture a HEADERS frame or the client gives up.
        hpack_decoder = HpackDecoder()
        captured_headers = False
        pending = b""  # accumulates bytes across recv() so split frames reassemble
        deadline = time.time() + 10.0
        ssock.settimeout(5.0)
        while not captured_headers and time.time() < deadline:
            try:
                chunk = ssock.recv(65535)
            except (ssl.SSLError, ConnectionError, socket.timeout):
                break
            if not chunk:
                break

            # Walker keeps its own buffer so TCP segmentation never drops a
            # frame mid-decode. h2's state machine has its own internal
            # buffering, so it gets the raw chunk unchanged.
            pending += chunk
            frames, pending = extract_frames(pending)
            for hdr, payload in frames:
                f_type = hdr[3]
                f_flags = hdr[4]
                if f_type == FRAME_TYPE_HEADERS and not captured_headers:
                    try:
                        block = extract_header_block(payload, f_flags)
                        decoded = hpack_decoder.decode(block, raw=False)
                    except Exception as exc:
                        record["headers_decode_error"] = repr(exc)
                        decoded = []
                    record["client_headers_frame"] = {
                        "frame_header_hex": hdr.hex(),
                        "payload_hex": payload.hex(),
                        "flags_byte": f_flags,
                        "headers_in_order": [
                            {"name": name, "value": value} for name, value in decoded
                        ],
                        "pseudo_header_order": [
                            name for name, _ in decoded if name.startswith(":")
                        ],
                        "regular_header_order": [
                            name for name, _ in decoded if not name.startswith(":")
                        ],
                    }
                    captured_headers = True

            # Feed raw chunk to h2 so it can ACK and respond properly.
            try:
                events = conn.receive_data(chunk)
            except Exception:
                events = []
            for evt in events:
                if isinstance(evt, h2.events.RequestReceived):
                    conn.send_headers(
                        stream_id=evt.stream_id,
                        headers=[(":status", "200"), ("content-length", "2")],
                    )
                    conn.send_data(stream_id=evt.stream_id, data=b"ok", end_stream=True)
            outbound = conn.data_to_send()
            if outbound:
                try:
                    ssock.sendall(outbound)
                except (ssl.SSLError, ConnectionError, socket.timeout):
                    break

        if not captured_headers:
            record["warning"] = "no HEADERS frame captured within 10s deadline"
    except Exception as exc:
        record["error"] = f"{type(exc).__name__}: {exc}"
    finally:
        _write_record(out_path, record)
        if ssock is not None:
            try:
                ssock.close()
            except Exception:
                pass
        try:
            raw_sock.close()
        except Exception:
            pass


def _write_record(out_path: Path, record: Dict[str, Any]) -> None:
    with out_path.open("a", encoding="utf-8") as fh:
        fh.write(json.dumps(record, ensure_ascii=False, default=str) + "\n")
    settings = record.get("client_settings_frame") or {}
    n_params = len(settings.get("parameters_in_order") or [])
    headers = record.get("client_headers_frame") or {}
    n_headers = len(headers.get("headers_in_order") or [])
    err = record.get("error")
    print(
        f"[capture] peer={record.get('peer')} alpn={record.get('alpn_negotiated')} "
        f"settings_params={n_params} headers={n_headers}"
        + (f" ERROR={err}" if err else ""),
        file=sys.stderr,
    )


def main() -> None:
    ap = argparse.ArgumentParser(description="HUAKAI h2 SETTINGS+HEADERS capture server")
    ap.add_argument("--port", type=int, default=18099)
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--max-connections", type=int, default=0,
                    help="exit after N connections handled (0 = run forever)")
    args = ap.parse_args()

    ensure_self_signed_cert()
    CAPTURE_DIR.mkdir(parents=True, exist_ok=True)
    out_path = CAPTURE_DIR / f"h2-server-{int(time.time())}.jsonl"
    print(f"[capture] output: {out_path}", file=sys.stderr)

    ctx = ssl.create_default_context(purpose=ssl.Purpose.CLIENT_AUTH)
    ctx.load_cert_chain(certfile=str(CERT_PATH), keyfile=str(KEY_PATH))
    ctx.set_alpn_protocols(["h2"])

    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        sock.bind((args.host, args.port))
    except OSError as exc:
        print(f"ERROR: bind {args.host}:{args.port} failed: {exc}", file=sys.stderr)
        sys.exit(2)
    sock.listen(8)
    print(f"[capture] listening on https://{args.host}:{args.port} ALPN=h2", file=sys.stderr)
    print("[capture] Ctrl+C to stop", file=sys.stderr)

    handled = 0
    try:
        while True:
            client, peer = sock.accept()
            handled += 1
            t = threading.Thread(
                target=handle_connection,
                args=(client, peer, ctx, out_path),
                daemon=True,
            )
            t.start()
            if args.max_connections and handled >= args.max_connections:
                print(f"[capture] max-connections {args.max_connections} reached; exiting",
                      file=sys.stderr)
                t.join(timeout=15)
                break
    except KeyboardInterrupt:
        print(f"\n[capture] stopped after {handled} connection(s). Output: {out_path}",
              file=sys.stderr)
    finally:
        sock.close()


if __name__ == "__main__":
    main()
