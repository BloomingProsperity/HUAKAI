from fastapi import FastAPI, HTTPException, Request
from fastapi.responses import JSONResponse

from jwt_verify import JWTVerificationError, load_public_key_cache_from_env


SERVICE_VERSION = "0.14.0"
HEADER_TENANT = "X-Hermes-Tenant"
HEADER_USER = "X-Hermes-User"

app = FastAPI(title="HUAKAI Hermes Runner", version=SERVICE_VERSION)
JWT_KEYS = load_public_key_cache_from_env()


def _unauthorized() -> JSONResponse:
    return JSONResponse(status_code=401, content={"detail": "unauthorized"})


def _valid_jwt(request: Request) -> bool:
    auth = request.headers.get("Authorization", "")
    if not auth.lower().startswith("bearer "):
        return False
    tenant = request.headers.get(HEADER_TENANT, "").strip()
    user = request.headers.get(HEADER_USER, "").strip()
    if not tenant or not user:
        return False
    try:
        claims = JWT_KEYS.verify(auth[7:].strip())
    except JWTVerificationError:
        return False
    if claims.get("sub") != f"{tenant}:{user}":
        return False
    request.state.jwt_claims = claims
    return True


@app.middleware("http")
async def verify_auth(request: Request, call_next):
    # healthz 是容器和 compose healthcheck 使用的免签探针。
    if request.method == "GET" and request.url.path == "/healthz":
        return await call_next(request)

    if not _valid_jwt(request):
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
async def chat(request: Request):
    from hermes_chat import chat_response

    return await chat_response(request, tenant_header=HEADER_TENANT, user_header=HEADER_USER)


@app.get("/conversations")
async def conversations():
    raise HTTPException(status_code=501, detail="Not Implemented")


@app.get("/conversations/{conversation_id}/messages")
async def conversation_messages(conversation_id: str):
    _ = conversation_id
    raise HTTPException(status_code=501, detail="Not Implemented")
