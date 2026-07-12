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
# 给单次对话工具调用到网关的一次往返设上界。诊断读很快;卡死的网关不能让对话无限期停滞。
TOOL_EXECUTE_TIMEOUT_SECONDS = 15

_DONE = object()


@dataclass(frozen=True)
class ChatPayload:
    messages: list[dict[str, Any]]
    internal_base_url: str
    internal_token: str = field(repr=False)
    conversation_id: int | None = None
    model: str | None = None
    # tool_catalog 是网关注入的、运维助手可在对话中调用的工具列表(name + description +
    # input_schema)。默认只含只读诊断工具;当 Phase B 提议 KNOB 打开时,还会包含"可提议的"
    # B 级 mutating 工具——这些条目带 mutating=true(及 requires_confirmation)标志。runner
    # 不授权任何工具:它只转发模型选中的工具调用给网关(网关授权 + 执行 + 审计);对带 mutating
    # 标志的工具,它以 mode=propose 转发(只做 dry-run、返回 needs_confirmation 供运营者确认),
    # 自己绝不执行、绝不自动确认。
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
    # WAVE H3b:仅当所加载 agent 的 run_conversation 接受这些参数时(感知签名),才注入"只读"工具目录
    # 与工具执行器,使不支持工具的 agent 不受影响、供应链面也不被扩大。执行器只是把模型选中的工具调用
    # 经 internal token 转发给网关——它自己不做任何授权;授权(只读过滤 + RBAC + 租户隔离)与审计都在网关。
    _inject_tool_kwargs(method, kwargs, payload)

    if inspect.iscoroutinefunction(method):
        return await method(**kwargs)
    return await _run_sync_method(method, kwargs)


def _inject_tool_kwargs(method: Callable[..., Any], kwargs: dict[str, Any], payload: ChatPayload) -> None:
    """仅当 (a) 网关注入了非空目录 且 (b) run_conversation 声明了对应参数(或 **kwargs)时,才把
    tool_catalog + tool_executor 加进 kwargs。安全失败:若签名无法内省,则什么也不注入,而不是冒着
    TypeError 中断对话的风险。"""
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
    """返回一个 (tool_name, args) -> 已脱敏结果 dict 的可调用对象,供 agent 在模型选中工具时调用。
    它带着会话 internal_token 作为凭据,POST 到网关的内部 tool-execute 端点。网关是唯一权威:校验
    token、解析绑定的 operator、按角色下限 + 租户隔离执行、审计、只返回脱敏摘要。runner 从不接触密钥、
    从不授权。

    Phase B:对目录里被标记 mutating 的工具(可提议的 B 级 mutating 工具),executor 以 mode=propose
    转发——网关只做 dry-run 解析、返回 needs_confirmation + correlation_id 供运营者经独立路径确认。runner
    没有运营者确认凭据,故绝不直接执行、也绝不自动确认;它只把 needs_confirmation 结果原样回灌给模型,由
    模型转达运营者。只读工具仍以空 mode 转发(走原只读路径)。"""
    url = _internal_tool_execute_url(payload.internal_base_url)
    token = payload.internal_token
    # 从网关注入的目录里挑出带 mutating 标志的工具名(Phase B 提议 KNOB 关时目录里没有这类工具,
    # 该集合为空 => 所有调用都以空 mode 走只读路径,行为与提议接入前一致)。
    mutating_tools = {t["name"] for t in payload.tool_catalog if t.get("mutating") is True}

    def execute(tool_name: str, args: dict[str, Any] | None = None) -> dict[str, Any]:
        mode = "propose" if tool_name in mutating_tools else ""
        return _post_tool_execute(url, token, tool_name, args or {}, mode)

    return execute


def _internal_tool_execute_url(internal_base_url: str) -> str:
    """从 internal LLM base URL 推导网关的 tool-execute 端点。base URL 指向网关的内部监听器(例如
    http://host:8080/internal/v1/openai);工具端点是"同一 origin"上的固定同级路径,从而继承该监听器
    的网络隔离。"""
    from urllib.parse import urlsplit, urlunsplit

    parts = urlsplit(internal_base_url)
    return urlunsplit((parts.scheme, parts.netloc, "/internal/hermes/tool-execute", "", ""))


def _post_tool_execute(url: str, token: str, tool_name: str, args: dict[str, Any], mode: str = "") -> dict[str, Any]:
    """同步 POST 一次工具调用到网关并返回解析后的结果。网络/解析失败与非 2xx 响应都被转成结构化
    error dict(绝不含原始密钥),使 agent 能自行决定如何继续而非崩掉对话。

    mode 为空(默认)走只读路径;mode="propose"(Phase B,针对 mutating 工具)让网关做 dry-run 解析、
    返回 needs_confirmation + correlation_id。mode 仅在非空时写入请求体,故只读调用的请求体逐字节不变。"""
    import json as _json
    import urllib.error
    import urllib.request

    body_obj: dict[str, Any] = {"tool_name": tool_name, "args": args}
    if mode:
        body_obj["mode"] = mode
    body = _json.dumps(body_obj).encode("utf-8")
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
        # 绝不把 token 或堆栈信息泄露到对话面。
        return {"status": "error", "error_class": "tool_execute_failed"}


def _tool_execute_http_error(exc: Any) -> dict[str, Any]:
    """把非 2xx 的网关响应映射成结构化 error dict。网关的 error body 是个小的 {"error": "<code>"} 枚举
    (不含 PII),可安全暴露。"""
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
    """把网关注入的工具目录归一化成不可变的 {name, description, input_schema} dict 元组。任何畸形条目
    都被丢弃,以免坏条目崩掉对话;缺失 => 空目录(无工具循环)。runner 信任网关侧的过滤——它自己没有
    registry,不重新判定工具的 mutating 性,只转发模型选中的,由网关再授权。

    Phase B:若条目带 mutating=true(可提议的 B 级 mutating 工具,网关 ProposableCatalog 注入),则保留
    mutating(及 requires_confirmation)标志,使 executor 知道要以 mode=propose 转发、模型知道要向运营者
    渲染确认步骤。标志仅在显式为 true 时保留 => 只读条目的形状逐字节不变。"""
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
        item: dict[str, Any] = {
            "name": name.strip(),
            "description": str(entry.get("description") or ""),
            "input_schema": schema if isinstance(schema, dict) else {},
        }
        if entry.get("mutating") is True:
            item["mutating"] = True
        if entry.get("requires_confirmation") is True:
            item["requires_confirmation"] = True
        catalog.append(item)
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
