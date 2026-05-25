import hashlib
import hmac
import os
import time
from typing import Callable

from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse


SERVICE_VERSION = "0.14.0"
SECRET_ENV = "HUAKAI_HERMES_SHARED_SECRET"
HEADER_SIGNATURE = "X-Hermes-Signature"
HEADER_TIMESTAMP = "X-Hermes-Timestamp"
HEADER_TENANT = "X-Hermes-Tenant"
HEADER_USER = "X-Hermes-User"
FRESHNESS_SECONDS = 5 * 60

app = FastAPI(title="HUAKAI Hermes Runner", version=SERVICE_VERSION)


def _unauthorized() -> JSONResponse:
    return JSONResponse(status_code=401, content={"detail": "unauthorized"})


def _shared_secret() -> bytes:
    return os.environ.get(SECRET_ENV, "").strip().encode("utf-8")


def _raw_query(request: Request) -> str:
    raw = request.scope.get("query_string", b"")
    if isinstance(raw, bytes):
        return raw.decode("ascii")
    return str(raw)


def _canonical(
    ts: str,
    method: str,
    path: str,
    raw_query: str,
    tenant: str,
    user: str,
    body: bytes,
) -> bytes:
    # 必须和 Go runner_client.go 的 sign 顺序完全一致。
    prefix = "\n".join([ts, method, path, raw_query, tenant, user]) + "\n"
    return prefix.encode("utf-8") + body


def _valid_timestamp(ts: str, now: Callable[[], float] = time.time) -> bool:
    try:
        signed_at = int(ts)
    except ValueError:
        return False
    return abs(int(now()) - signed_at) <= FRESHNESS_SECONDS


def _valid_signature(request: Request, body: bytes) -> bool:
    secret = _shared_secret()
    if not secret:
        return False

    signature = request.headers.get(HEADER_SIGNATURE, "")
    ts = request.headers.get(HEADER_TIMESTAMP, "")
    tenant = request.headers.get(HEADER_TENANT, "")
    user = request.headers.get(HEADER_USER, "")
    if not signature or not ts or not tenant or not user:
        return False
    if not _valid_timestamp(ts):
        return False

    expected = hmac.new(
        secret,
        _canonical(
            ts=ts,
            method=request.method,
            path=request.url.path,
            raw_query=_raw_query(request),
            tenant=tenant,
            user=user,
            body=body,
        ),
        hashlib.sha256,
    ).hexdigest()
    return hmac.compare_digest(signature, expected)


@app.middleware("http")
async def verify_hmac(request: Request, call_next):
    # healthz 是容器和 compose healthcheck 使用的免签探针。
    if request.method == "GET" and request.url.path == "/healthz":
        return await call_next(request)

    body = await request.body()
    if not _valid_signature(request, body):
        return _unauthorized()
    return await call_next(request)


@app.get("/healthz")
async def healthz():
    return {
        "status": "ok",
        "service": "hermes-runner",
        "version": SERVICE_VERSION,
    }


@app.post("/chat")
async def chat():
    raise HTTPException(status_code=501, detail="Not Implemented")


@app.get("/conversations")
async def conversations():
    raise HTTPException(status_code=501, detail="Not Implemented")


@app.get("/conversations/{conversation_id}/messages")
async def conversation_messages(conversation_id: str):
    _ = conversation_id
    raise HTTPException(status_code=501, detail="Not Implemented")
