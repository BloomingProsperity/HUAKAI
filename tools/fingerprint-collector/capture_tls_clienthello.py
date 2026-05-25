"""mitmproxy addon: capture TLS ClientHello fingerprint data from CLI tools.

W11-F F-2.5 (2026-05-25): real-upstream capture for byte-level diff against
the profile JSON templates under ``tools/fingerprint-collector/templates/``.

Usage
-----
::

    mitmdump -p 18099 -s tools/fingerprint-collector/capture_tls_clienthello.py

Then in another shell, point HTTPS_PROXY at the proxy and run one request::

    HTTPS_PROXY=http://127.0.0.1:18099 codex chat ...
    HTTPS_PROXY=http://127.0.0.1:18099 gemini ...

Each ClientHello observed is written as one JSON object per line to
``tools/fingerprint-collector/captures/clienthello-<unix-ts>.jsonl``.

Scope
-----
The addon ONLY inspects the unencrypted ClientHello (first TLS handshake
frame). Application data after the handshake is decrypted by mitmproxy
because the CA cert is trusted by the CLI's TLS stack, but this addon does
not touch / log / forward decrypted payloads. The CA install is needed only
so the handshake completes — without it, the CLI would close the connection
on cert validation failure before any subsequent request flows.
"""

import json
import time
from pathlib import Path

from mitmproxy import ctx


def _safe(value):
    """Best-effort JSON-safe coercion (preserve ints, list-of-ints, hex bytes)."""
    if value is None:
        return None
    if isinstance(value, (bytes, bytearray)):
        return value.hex()
    if isinstance(value, (list, tuple)):
        return [_safe(v) for v in value]
    if isinstance(value, dict):
        return {str(k): _safe(v) for k, v in value.items()}
    if isinstance(value, (bool, int, float, str)):
        return value
    return repr(value)


class TLSClientHelloCapture:
    def __init__(self):
        capture_dir = Path("tools/fingerprint-collector/captures")
        capture_dir.mkdir(parents=True, exist_ok=True)
        self.out_path = capture_dir / f"clienthello-{int(time.time())}.jsonl"
        ctx.log.info(f"[F-2.5 capture] writing JSONL to {self.out_path}")

    def tls_clienthello(self, data):
        ch = data.client_hello

        peer = None
        try:
            peer = str(data.context.client.peername)
        except Exception:
            peer = None

        record = {
            "ts": time.time(),
            "peer": peer,
        }

        # mitmproxy 12.x ClientHello attributes — defensive introspection so
        # this addon survives minor API drift across mitmproxy versions.
        scalar_attrs = (
            "sni",
            "alpn_protocols",
            "cipher_suites",
            "extensions",
            "signature_algorithms",
            "supported_groups",
            "elliptic_curves",
            "ec_point_formats",
            "session_id",
            "version",
            "legacy_version",
            "extensions_present",
            "psk_key_exchange_modes",
            "supported_versions",
        )
        for attr in scalar_attrs:
            if hasattr(ch, attr):
                try:
                    record[attr] = _safe(getattr(ch, attr))
                except Exception as exc:
                    record[f"{attr}_error"] = repr(exc)

        # Try to extract raw ClientHello bytes — the byte-for-byte source of
        # truth for diffing against profile templates.
        for attr in ("raw_bytes", "bytes", "data", "client_hello_bytes"):
            if hasattr(ch, attr):
                try:
                    raw = getattr(ch, attr)
                    if callable(raw):
                        raw = raw()
                    if isinstance(raw, (bytes, bytearray)):
                        record["raw_hex"] = raw.hex()
                        record["raw_len"] = len(raw)
                        break
                except Exception as exc:
                    record[f"raw_{attr}_error"] = repr(exc)

        try:
            cipher_count = len(record.get("cipher_suites") or [])
            ext_count = len(record.get("extensions") or [])
            ctx.log.info(
                f"[F-2.5 capture] {peer} SNI={record.get('sni')} "
                f"ALPN={record.get('alpn_protocols')} "
                f"ciphers={cipher_count} extensions={ext_count}"
            )
        except Exception:
            ctx.log.info(f"[F-2.5 capture] {peer} (logging summary failed)")

        try:
            with self.out_path.open("a", encoding="utf-8") as fh:
                fh.write(json.dumps(record, ensure_ascii=False) + "\n")
        except Exception as exc:
            ctx.log.error(f"[F-2.5 capture] write failed: {exc!r}")


addons = [TLSClientHelloCapture()]
