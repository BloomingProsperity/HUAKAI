import json
from typing import Any


ALLOWED_EVENT_TYPES = {"conversation", "token", "status", "error", "done"}


def encode_event(event: str, data: dict[str, Any]) -> bytes:
    if event not in ALLOWED_EVENT_TYPES:
        raise ValueError("unsupported SSE event type")
    payload = json.dumps(data, ensure_ascii=False, separators=(",", ":"))
    return f"event: {event}\ndata: {payload}\n\n".encode("utf-8")


def encode_keepalive() -> bytes:
    return b": keepalive\n\n"
