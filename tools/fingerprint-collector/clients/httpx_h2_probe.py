"""HUAKAI fingerprint-collector: httpx h2 probe (cross-validation against undici).

W11-F F-1.g attempt-3 (2026-05-26): driven by Owner's "拓 Python SDK 作交叉验证"
direction. Captures httpx's HTTP/2 SETTINGS + HEADERS to compare against the
undici v7 baseline (already committed in dcee914) and HUAKAI's existing
anthropic-claude-code.json (claims alpn=["http/1.1"], potentially stale).

Hypothesis under test: do BoringSSL-derived stacks (undici, Chrome) produce
similar h2 fingerprints to OpenSSL-derived stacks (Python httpx)? Or do they
diverge — telling us each first-party client needs its own profile?

This probe:
- Connects to the local capture server (127.0.0.1:18099) with self-signed cert
  trust disabled (the server is local, terminating TLS for capture only).
- Forces httpx to use HTTP/2 (verify=False, http2=True).
- Sends one POST shaped like an Anthropic Messages request so user-agent /
  header set matches what an Anthropic-bound Python client realistically sends.
- Prints status + body + the User-Agent that the server saw (for visual diff).

Usage:
    python tools/fingerprint-collector/clients/httpx_h2_probe.py

Prereq:
    pip install 'httpx[http2]'

License: HUAKAI original implementation, MIT.
"""

from __future__ import annotations

import json
import os
import sys

try:
    import httpx
except ImportError:
    print("ERROR: httpx missing. Install: pip install 'httpx[http2]'", file=sys.stderr)
    sys.exit(2)

TARGET = os.environ.get("HUAKAI_PROBE_TARGET", "https://127.0.0.1:18099")
PATH = "/v1/messages"


def main() -> int:
    body = json.dumps(
        {
            "model": "claude-3-haiku-20240307",
            "max_tokens": 8,
            "messages": [{"role": "user", "content": "probe"}],
        }
    ).encode("utf-8")

    # Match what an Anthropic Python SDK call would build: anthropic-version
    # required, x-api-key bearer, content-type json, accept json. SDK uses
    # `python-httpx/<ver>` as default user-agent unless overridden.
    headers = {
        "user-agent": "huakai-httpx-h2-probe/1.0 (httpx)",
        "content-type": "application/json",
        "accept": "application/json",
        "x-api-key": "probe-not-a-real-key",
        "anthropic-version": "2023-06-01",
    }

    # http2=True: force ALPN h2 advertisement; httpx will still negotiate down
    # if server only offers h1.1, but our capture server advertises h2 only.
    # verify=False: bypass self-signed cert check (local capture server).
    with httpx.Client(http2=True, verify=False, timeout=10.0) as client:
        try:
            resp = client.post(TARGET + PATH, headers=headers, content=body)
            print(
                f"[probe] status={resp.status_code} "
                f"http_version={resp.http_version} "
                f"body={resp.text[:80]!r}"
            )
            return 0
        except Exception as exc:
            print(f"[probe] error: {type(exc).__name__}: {exc}", file=sys.stderr)
            return 1


if __name__ == "__main__":
    sys.exit(main())
