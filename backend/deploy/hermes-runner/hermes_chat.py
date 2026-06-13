import asyncio
import concurrent.futures
import contextvars
import hashlib
import hmac
import inspect
import os
import queue as thread_queue
import time
from contextlib import suppress
from dataclasses import dataclass, field
from typing import Any, AsyncIterator, Callable, Iterable

from fastapi import HTTPException, Request

try:
    from sse_starlette.sse import EventSourceResponse
except ImportError:
    from sse_starlette import EventSourceResponse

from sse_events import encode_event, encode_keepalive


HEADER_TENANT = "X-Hermes-Tenant"
HEADER_USER = "X-Hermes-User"
HEARTBEAT_SECONDS = 20
HERMES_HOME_ROOT = "/var/lib/huakai/hermes"
INTERNAL_BASE_URL_ENV = "HUAKAI_HERMES_INTERNAL_LLM_BASE_URL"
INTERNAL_TOKEN_SECRET_ENV = "HUAKAI_HERMES_INTERNAL_TOKEN_SECRET"
INTERNAL_TOKEN_MAX_TTL_SECONDS = 5 * 60
# Bounds one conversational tool call's round-trip to the gateway. Diagnostic
# reads are fast; a hung gateway must not stall the chat indefinitely.
TOOL_EXECUTE_TIMEOUT_SECONDS = 15

_DONE = object()


@dataclass(frozen=True)
class ChatPayload:
    messages: list[dict[str, Any]]
    internal_base_url: str
    internal_token: str = field(repr=False)
    conversation_id: int | None = None
    model: str | None = None
    # tool_catalog is the gateway-injected list of READ-ONLY diagnostic tools the
    # ops assistant may call mid-conversation (name + description + input_schema).
    # It NEVER contains a mutating tool — the gateway filters to read-only before
    # injection. The runner does not authorize tools; it only forwards the model's
    # chosen tool call to the gateway, which authorizes + executes + audits.
    tool_catalog: tuple[dict[str, Any], ...] = ()


class _CallbackAck:
    def __await__(self):
        if False:
            yield None
        return None


async def chat_response(
    request: Request,
    *,
    tenant_header: str = HEADER_TENANT,
    user_header: str = HEADER_USER,
):
    tenant_id = _positive_header_int(request, tenant_header)
    user_id = _positive_header_int(request, user_header)
    payload = _parse_payload(await _read_json(request))
    _verify_internal_token(payload.internal_token, tenant_id=tenant_id, user_id=user_id)
    return event_source_response(iter_chat_sse(payload, tenant_id=tenant_id, user_id=user_id))


def event_source_response(events: Iterable[bytes]):
    return EventSourceResponse(
        events,
        ping=HEARTBEAT_SECONDS,
        ping_message_factory=encode_keepalive,
        sep="\n",
    )


async def iter_chat_sse(
    payload: ChatPayload | dict[str, Any],
    *,
    tenant_id: int,
    user_id: int,
    agent_cls: type | None = None,
    constants_module: Any | None = None,
) -> AsyncIterator[bytes]:
    chat_payload = _parse_payload(payload) if isinstance(payload, dict) else payload
    _verify_internal_token(chat_payload.internal_token, tenant_id=tenant_id, user_id=user_id)
    queue: thread_queue.Queue[bytes | object] = thread_queue.Queue()
    task = asyncio.create_task(
        _run_agent_to_queue(
            chat_payload,
            tenant_id=tenant_id,
            user_id=user_id,
            queue=queue,
            agent_cls=agent_cls,
            constants_module=constants_module,
        )
    )

    try:
        if chat_payload.conversation_id is not None:
            yield encode_event("conversation", {"id": chat_payload.conversation_id})

        while True:
            try:
                item = queue.get_nowait()
            except thread_queue.Empty:
                await asyncio.sleep(0.01)
                continue
            if item is _DONE:
                break
            yield item
    finally:
        if not task.done():
            task.cancel()
            with suppress(asyncio.CancelledError):
                await task


async def _run_agent_to_queue(
    payload: ChatPayload,
    *,
    tenant_id: int,
    user_id: int,
    queue: thread_queue.Queue[bytes | object],
    agent_cls: type | None,
    constants_module: Any | None,
) -> None:
    constants = constants_module
    if constants is None:
        agent_cls, constants = _load_hermes_modules(agent_cls)
    elif agent_cls is None:
        agent_cls, _ = _load_hermes_modules(agent_cls)

    home_token = constants.set_hermes_home_override(_hermes_home(tenant_id, user_id))
    try:
        agent = agent_cls(**_agent_init_kwargs(payload))
        callback = _StreamCallback(queue)
        result = await _invoke_run_conversation(agent, payload, callback)
        failure_message = _agent_failure_message(result, payload.internal_token)
        if failure_message is not None:
            queue.put(encode_event("error", {"code": "agent_failed", "message": failure_message}))
            return
        queue.put(
            encode_event(
                "done",
                {
                    "finish_reason": _finish_reason(result),
                    "total_tokens": _total_tokens(result),
                },
            )
        )
    except Exception:
        queue.put(encode_event("error", {"code": "agent_error", "message": "hermes agent failed"}))
    finally:
        _reset_hermes_home(constants, home_token)
        queue.put(_DONE)


class _StreamCallback:
    def __init__(self, queue: thread_queue.Queue[bytes | object]):
        self._queue = queue

    def __call__(self, chunk: Any):
        for frame in _frames_from_chunk(chunk):
            self._queue.put(frame)
        return _CallbackAck()


async def _invoke_run_conversation(agent: Any, payload: ChatPayload, callback: Callable[[Any], Any]) -> Any:
    method = agent.run_conversation
    kwargs = _conversation_kwargs(payload, callback)
    # WAVE H3b: inject the READ-ONLY tool catalog + a tool executor ONLY when the
    # loaded agent's run_conversation accepts them (signature-aware), so an agent
    # that does not support tools is unaffected and the supply-chain surface is not
    # widened. The executor merely FORWARDS the model's chosen tool call to the
    # gateway over the internal token — it performs NO authorization itself; the
    # gateway authorizes (read-only filter + RBAC + tenant scope) + audits.
    _inject_tool_kwargs(method, kwargs, payload)

    if inspect.iscoroutinefunction(method):
        return await method(**kwargs)
    return await _run_sync_method(method, kwargs)


def _inject_tool_kwargs(method: Callable[..., Any], kwargs: dict[str, Any], payload: ChatPayload) -> None:
    """Add tool_catalog + tool_executor to kwargs iff (a) a non-empty catalog was
    injected by the gateway AND (b) run_conversation declares the matching
    parameter (or **kwargs). Fail-safe: if the signature cannot be introspected,
    inject nothing rather than risk a TypeError that aborts the chat."""
    if not payload.tool_catalog:
        return
    try:
        params = inspect.signature(method).parameters
    except (TypeError, ValueError):
        return
    accepts_var_kw = any(p.kind == inspect.Parameter.VAR_KEYWORD for p in params.values())
    if "tool_catalog" in params or accepts_var_kw:
        kwargs["tool_catalog"] = [dict(t) for t in payload.tool_catalog]
    if "tool_executor" in params or accepts_var_kw:
        kwargs["tool_executor"] = _build_tool_executor(payload)


def _build_tool_executor(payload: ChatPayload) -> Callable[[str, dict[str, Any]], dict[str, Any]]:
    """Return a callable (tool_name, args) -> sanitized result dict that the agent
    invokes when the model picks a tool. It POSTs to the gateway's internal
    read-only tool-execute endpoint with the session internal_token as proof.
    The gateway is the sole authority: it verifies the token, resolves the bound
    operator, REJECTS any mutating tool, runs the read-only tool with the
    operator's role floor + tenant scope, audits the call, and returns only the
    sanitized summary. The runner never sees a secret and never authorizes."""
    url = _internal_tool_execute_url(payload.internal_base_url)
    token = payload.internal_token

    def execute(tool_name: str, args: dict[str, Any] | None = None) -> dict[str, Any]:
        return _post_tool_execute(url, token, tool_name, args or {})

    return execute


def _internal_tool_execute_url(internal_base_url: str) -> str:
    """Derive the gateway's tool-execute endpoint from the internal LLM base URL.
    The base URL points at the gateway's internal listener (e.g.
    http://host:8080/internal/v1/openai); the tool endpoint is a sibling fixed
    path on the SAME origin so it inherits the listener's network isolation."""
    from urllib.parse import urlsplit, urlunsplit

    parts = urlsplit(internal_base_url)
    return urlunsplit((parts.scheme, parts.netloc, "/internal/hermes/tool-execute", "", ""))


def _post_tool_execute(url: str, token: str, tool_name: str, args: dict[str, Any]) -> dict[str, Any]:
    """Synchronously POST one tool call to the gateway and return the parsed
    result. Network/parse failures and non-2xx responses are surfaced as a
    structured error dict (never a raw secret) so the agent can decide how to
    continue rather than crashing the conversation."""
    import json as _json
    import urllib.error
    import urllib.request

    body = _json.dumps({"tool_name": tool_name, "args": args}).encode("utf-8")
    req = urllib.request.Request(url, data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=TOOL_EXECUTE_TIMEOUT_SECONDS) as resp:
            payload = resp.read()
        parsed = _json.loads(payload.decode("utf-8"))
        if isinstance(parsed, dict):
            return parsed
        return {"status": "error", "error_class": "malformed_result"}
    except urllib.error.HTTPError as exc:
        return _tool_execute_http_error(exc)
    except Exception:
        # Never leak the token or a stack trace into the conversation surface.
        return {"status": "error", "error_class": "tool_execute_failed"}


def _tool_execute_http_error(exc: Any) -> dict[str, Any]:
    """Map a non-2xx gateway response to a structured error dict. The gateway's
    error body is a small {"error": "<code>"} enum (no PII), safe to surface."""
    code = "tool_execute_failed"
    try:
        detail = json_loads_safe(exc.read())
        if isinstance(detail, dict):
            err = detail.get("error") or detail.get("error_class")
            if isinstance(err, str) and err:
                code = err
    except Exception:
        pass
    return {"status": "error", "error_class": code}


def json_loads_safe(raw: Any) -> Any:
    import json as _json

    try:
        if isinstance(raw, (bytes, bytearray)):
            raw = raw.decode("utf-8", errors="replace")
        return _json.loads(raw)
    except Exception:
        return None


async def _run_sync_method(method: Callable[..., Any], kwargs: dict[str, Any]) -> Any:
    context = contextvars.copy_context()
    loop = asyncio.get_running_loop()
    with concurrent.futures.ThreadPoolExecutor(max_workers=1, thread_name_prefix="hermes-agent-run") as executor:
        return await loop.run_in_executor(executor, context.run, lambda: method(**kwargs))


def _agent_init_kwargs(payload: ChatPayload) -> dict[str, Any]:
    kwargs: dict[str, Any] = {
        "base_url": payload.internal_base_url,
        "api_key": payload.internal_token,
    }
    if payload.model:
        kwargs["model"] = payload.model
    return kwargs


def _conversation_kwargs(payload: ChatPayload, callback: Callable[[Any], Any]) -> dict[str, Any]:
    user_index = _last_user_message_index(payload.messages)
    user_message = _message_text(payload.messages[user_index].get("content"))
    system_message = _system_message(payload.messages)
    history = [
        dict(message)
        for index, message in enumerate(payload.messages)
        if index != user_index and message.get("role") != "system"
    ]
    return {
        "user_message": user_message,
        "system_message": system_message,
        "conversation_history": history or None,
        "stream_callback": callback,
    }


def _last_user_message_index(messages: list[dict[str, Any]]) -> int:
    for index in range(len(messages) - 1, -1, -1):
        message = messages[index]
        if isinstance(message, dict) and message.get("role") == "user":
            return index
    raise ValueError("messages must include a user message")


def _system_message(messages: list[dict[str, Any]]) -> str | None:
    parts = [
        _message_text(message.get("content"))
        for message in messages
        if isinstance(message, dict) and message.get("role") == "system"
    ]
    parts = [part for part in parts if part]
    return "\n\n".join(parts) or None


def _message_text(content: Any) -> str:
    if content is None:
        return ""
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        parts: list[str] = []
        for item in content:
            if isinstance(item, str):
                parts.append(item)
            elif isinstance(item, dict) and isinstance(item.get("text"), str):
                parts.append(item["text"])
        return "\n".join(parts)
    return str(content)


def _frames_from_chunk(chunk: Any) -> list[bytes]:
    if chunk is None:
        return []
    if isinstance(chunk, bytes):
        chunk = chunk.decode("utf-8", errors="replace")
    if isinstance(chunk, str):
        return [encode_event("token", {"delta": chunk})] if chunk else []
    if isinstance(chunk, dict):
        event = str(chunk.get("event") or chunk.get("type") or "").strip()
        if event == "status":
            return [encode_event("status", _status_data(chunk))]
        if event == "token":
            delta = str(chunk.get("delta") or chunk.get("text") or chunk.get("content") or "")
            return [encode_event("token", {"delta": delta})] if delta else []
        if "phase" in chunk:
            return [encode_event("status", _status_data(chunk))]
        delta = str(chunk.get("delta") or chunk.get("text") or chunk.get("content") or "")
        return [encode_event("token", {"delta": delta})] if delta else []

    delta = getattr(chunk, "delta", None) or getattr(chunk, "text", None) or getattr(chunk, "content", None)
    return [encode_event("token", {"delta": str(delta)})] if delta else []


def _status_data(chunk: dict[str, Any]) -> dict[str, str]:
    phase = str(chunk.get("phase") or "response")
    detail = str(chunk.get("detail") or chunk.get("message") or "")
    return {"phase": phase, "detail": detail}


async def _read_json(request: Request) -> Any:
    try:
        return await request.json()
    except Exception as exc:
        raise HTTPException(status_code=400, detail="invalid json") from exc


def _parse_payload(value: Any) -> ChatPayload:
    if not isinstance(value, dict):
        raise HTTPException(status_code=400, detail="invalid request")
    messages = value.get("messages")
    if not isinstance(messages, list) or not messages:
        raise HTTPException(status_code=400, detail="messages required")
    base_url = _internal_base_url()
    token = _required_string(value, "internal_token")
    conversation_id = _optional_positive_int(value.get("conversation_id"))
    model = value.get("model")
    if model is not None:
        model = str(model).strip() or None
    return ChatPayload(
        conversation_id=conversation_id,
        messages=messages,
        model=model,
        internal_base_url=base_url,
        internal_token=token,
        tool_catalog=_parse_tool_catalog(value.get("tool_catalog")),
    )


def _parse_tool_catalog(raw: Any) -> tuple[dict[str, Any], ...]:
    """Normalize the gateway-injected tool catalog into an immutable tuple of
    {name, description, input_schema} dicts. Anything malformed is dropped so a
    bad entry can never crash the conversation; absent => empty catalog (no tool
    loop). The runner trusts the gateway's read-only filter — it does not re-check
    mutating-ness here (it has no registry), it only forwards what the model picks
    and the gateway re-authorizes."""
    if not isinstance(raw, list):
        return ()
    catalog: list[dict[str, Any]] = []
    for entry in raw:
        if not isinstance(entry, dict):
            continue
        name = entry.get("name")
        if not isinstance(name, str) or not name.strip():
            continue
        schema = entry.get("input_schema")
        catalog.append(
            {
                "name": name.strip(),
                "description": str(entry.get("description") or ""),
                "input_schema": schema if isinstance(schema, dict) else {},
            }
        )
    return tuple(catalog)


def _required_string(value: dict[str, Any], name: str) -> str:
    raw = value.get(name)
    if not isinstance(raw, str) or not raw.strip():
        raise HTTPException(status_code=400, detail=f"{name} required")
    return raw.strip()


def _internal_base_url() -> str:
    value = os.environ.get(INTERNAL_BASE_URL_ENV, "").strip()
    if not value:
        raise HTTPException(status_code=500, detail="internal llm base url not configured")
    return value


def _internal_token_secret() -> bytes:
    value = os.environ.get(INTERNAL_TOKEN_SECRET_ENV, "").strip()
    if not value:
        raise HTTPException(status_code=500, detail="internal token secret not configured")
    return value.encode("utf-8")


def _verify_internal_token(
    token: str,
    *,
    tenant_id: int,
    user_id: int,
    now: Callable[[], float] = time.time,
) -> None:
    parts = token.split("|")
    if len(parts) != 5:
        raise _internal_token_unauthorized()
    token_tenant, token_user, request_id, exp_raw, signature = parts
    if token_tenant != str(tenant_id) or token_user != str(user_id):
        raise _internal_token_unauthorized()
    if not request_id.strip() or not signature.strip():
        raise _internal_token_unauthorized()
    try:
        exp = int(exp_raw)
    except ValueError as exc:
        raise _internal_token_unauthorized() from exc

    current = int(now())
    if exp < current or exp > current + INTERNAL_TOKEN_MAX_TTL_SECONDS:
        raise _internal_token_unauthorized()

    canonical = f"{token_tenant}|{token_user}|{request_id}|{exp_raw}"
    expected = hmac.new(_internal_token_secret(), canonical.encode("utf-8"), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(signature, expected):
        raise _internal_token_unauthorized()


def _internal_token_unauthorized() -> HTTPException:
    return HTTPException(status_code=401, detail="unauthorized")


def _optional_positive_int(raw: Any) -> int | None:
    if raw is None or raw == 0 or raw == "":
        return None
    try:
        value = int(raw)
    except (TypeError, ValueError) as exc:
        raise HTTPException(status_code=400, detail="conversation_id must be positive int") from exc
    if value <= 0:
        raise HTTPException(status_code=400, detail="conversation_id must be positive int")
    return value


def _positive_header_int(request: Request, header: str) -> int:
    raw = request.headers.get(header, "").strip()
    try:
        value = int(raw)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail=f"{header} must be positive int") from exc
    if value <= 0:
        raise HTTPException(status_code=400, detail=f"{header} must be positive int")
    return value


def _hermes_home(tenant_id: int, user_id: int) -> str:
    return f"{HERMES_HOME_ROOT}/tenants/{tenant_id}/users/{user_id}"


def _reset_hermes_home(constants: Any, token: Any) -> None:
    reset = getattr(constants, "reset_hermes_home_override", None)
    if callable(reset):
        reset(token)
        return
    token_var = getattr(token, "var", None)
    if token_var is not None:
        token_var.reset(token)


def _agent_failure_message(result: Any, internal_token: str) -> str | None:
    completed = _value_from_result(result, "completed", None)
    failed = _value_from_result(result, "failed", None)
    if completed is not False and failed is not True:
        return None
    raw = (
        _value_from_result(result, "error", None)
        or _value_from_result(result, "message", None)
        or _value_from_result(result, "detail", None)
        or "hermes agent failed"
    )
    message = str(raw).strip() or "hermes agent failed"
    if internal_token:
        message = message.replace(internal_token, "[redacted]")
    return message[:500]


def _finish_reason(result: Any) -> str:
    value = _value_from_result(result, "finish_reason", "stop")
    if value in {"stop", "error", "length"}:
        return value
    return "stop"


def _total_tokens(result: Any) -> int:
    value = _value_from_result(result, "total_tokens", 0)
    try:
        total = int(value)
    except (TypeError, ValueError):
        return 0
    return max(total, 0)


def _value_from_result(result: Any, name: str, default: Any) -> Any:
    if isinstance(result, dict):
        return result.get(name, default)
    return getattr(result, name, default)


def _load_hermes_modules(agent_cls: type | None):
    try:
        from hermes_agent import AIAgent, hermes_constants
    except ImportError:
        from run_agent import AIAgent
        import hermes_constants

    return agent_cls or AIAgent, hermes_constants
