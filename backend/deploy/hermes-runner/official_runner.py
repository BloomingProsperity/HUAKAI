import asyncio
import json
import os
import re
import shutil
import signal
import tempfile
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any, AsyncIterator, Awaitable, Callable
from urllib.parse import urlsplit, urlunsplit

from fastapi import HTTPException, Request

try:
    from sse_starlette.sse import EventSourceResponse
except ImportError:
    from sse_starlette import EventSourceResponse

from sse_events import encode_event, encode_keepalive


HEADER_TENANT = "X-Hermes-Tenant"
HEADER_USER = "X-Hermes-User"
HEARTBEAT_SECONDS = 20
MCP_URL_ENV = "HUAKAI_HERMES_MCP_URL"
WORK_ROOT_ENV = "HUAKAI_HERMES_WORK_ROOT"
DEFAULT_WORK_ROOT = "/run/huakai-hermes"
DEFAULT_HERMES_BINARY = "hermes"
EXPECTED_HERMES_VERSION = "0.19.0"
INTERNAL_TOKEN_SAFETY_SECONDS = 30.0


class RunnerFailure(Exception):
    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


@dataclass(frozen=True)
class ChatPayload:
    messages: list[dict[str, Any]]
    model_base_url: str
    model_api_key: str
    mcp_token: str
    internal_token_expires_at: int
    model: str
    context_window: int | None = None
    conversation_id: int | None = None


@dataclass(frozen=True)
class RunnerConfig:
    mcp_url: str
    egress_proxy_url: str
    work_root: Path
    hermes_binary: str
    max_concurrency: int
    queue_timeout_seconds: float
    process_timeout_seconds: float
    terminate_grace_seconds: float
    max_prompt_chars: int
    max_stdout_bytes: int
    max_stderr_bytes: int
    max_turns: int
    mcp_timeout_seconds: int

    @classmethod
    def from_env(cls) -> "RunnerConfig":
        mcp_url = _validated_mcp_url(os.environ.get(MCP_URL_ENV, ""))
        work_root = Path(os.environ.get(WORK_ROOT_ENV, DEFAULT_WORK_ROOT)).expanduser()
        if not work_root.is_absolute():
            raise RunnerFailure("invalid_work_root")
        binary = os.environ.get("HUAKAI_HERMES_BINARY", DEFAULT_HERMES_BINARY).strip()
        if not binary:
            raise RunnerFailure("invalid_hermes_binary")
        return cls(
            mcp_url=mcp_url,
            egress_proxy_url=_validated_egress_proxy_url(
                os.environ.get("HUAKAI_HERMES_EGRESS_PROXY_URL", "")
            ),
            work_root=work_root,
            hermes_binary=binary,
            max_concurrency=_bounded_int("HUAKAI_HERMES_MAX_CONCURRENCY", 1, 1, 32),
            queue_timeout_seconds=_bounded_float(
                "HUAKAI_HERMES_QUEUE_TIMEOUT_SECONDS", 10.0, 0.1, 120.0
            ),
            process_timeout_seconds=_bounded_float(
                "HUAKAI_HERMES_PROCESS_TIMEOUT_SECONDS", 240.0, 1.0, 285.0
            ),
            terminate_grace_seconds=_bounded_float(
                "HUAKAI_HERMES_TERMINATE_GRACE_SECONDS", 3.0, 0.1, 15.0
            ),
            max_prompt_chars=_bounded_int(
                "HUAKAI_HERMES_MAX_PROMPT_CHARS", 131_072, 1_024, 1_048_576
            ),
            max_stdout_bytes=_bounded_int(
                "HUAKAI_HERMES_MAX_STDOUT_BYTES", 1_048_576, 4_096, 8_388_608
            ),
            max_stderr_bytes=_bounded_int(
                "HUAKAI_HERMES_MAX_STDERR_BYTES", 65_536, 4_096, 1_048_576
            ),
            max_turns=_bounded_int("HUAKAI_HERMES_MAX_TURNS", 20, 1, 60),
            mcp_timeout_seconds=_bounded_int(
                "HUAKAI_HERMES_MCP_TIMEOUT_SECONDS", 20, 1, 120
            ),
        )


@dataclass(frozen=True)
class CommandSpec:
    argv: tuple[str, ...]
    cwd: Path
    env: dict[str, str]
    timeout_seconds: float
    terminate_grace_seconds: float
    max_stdout_bytes: int
    max_stderr_bytes: int


@dataclass(frozen=True)
class ProcessResult:
    returncode: int
    stdout: bytes
    stderr: bytes
    stdout_truncated: bool = False
    stderr_truncated: bool = False


@dataclass(frozen=True)
class AgentResult:
    text: str
    total_tokens: int


@dataclass(frozen=True)
class UsageReport:
    total_tokens: int
    completed: bool | None
    failed: bool


ProcessExecutor = Callable[[CommandSpec], Awaitable[ProcessResult]]


class OfficialHermesRunner:
    def __init__(
        self,
        config: RunnerConfig,
        *,
        executor: ProcessExecutor | None = None,
        clock: Callable[[], float] | None = None,
    ):
        self.config = config
        self._executor = executor or execute_process
        self._clock = clock or time.time
        self._semaphore = asyncio.Semaphore(config.max_concurrency)

    async def run(self, payload: ChatPayload) -> AgentResult:
        try:
            await asyncio.wait_for(
                self._semaphore.acquire(), timeout=self.config.queue_timeout_seconds
            )
        except TimeoutError as exc:
            raise RunnerFailure("runner_busy") from exc
        try:
            return await self._run_isolated(payload)
        finally:
            self._semaphore.release()

    async def _run_isolated(self, payload: ChatPayload) -> AgentResult:
        self.config.work_root.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chmod(self.config.work_root, 0o700)
        run_root = Path(tempfile.mkdtemp(prefix="request-", dir=self.config.work_root))
        os.chmod(run_root, 0o700)
        try:
            hermes_home = run_root / "hermes-home"
            home = run_root / "home"
            work = run_root / "work"
            runtime = run_root / "runtime"
            for directory in (hermes_home, home, work, runtime):
                directory.mkdir(mode=0o700)

            prompt = _build_prompt(payload.messages)
            if len(prompt) > self.config.max_prompt_chars:
                raise RunnerFailure("prompt_too_large")

            config_path = hermes_home / "config.yaml"
            usage_path = run_root / "usage.json"
            _write_private_json(
                config_path,
                _hermes_config(self.config, payload, work),
            )
            command = CommandSpec(
                argv=(
                    self.config.hermes_binary,
                    "--model",
                    payload.model,
                    "--provider",
                    "custom",
                    "--toolsets",
                    "huakai",
                    "--ignore-rules",
                    "--usage-file",
                    str(usage_path),
                    "-z",
                    prompt,
                ),
                cwd=work,
                env=_child_environment(
                    hermes_home=hermes_home,
                    home=home,
                    runtime=runtime,
                    internal_host=urlsplit(self.config.mcp_url).hostname or "",
                    egress_proxy_url=self.config.egress_proxy_url,
                ),
                timeout_seconds=_process_timeout_for_token(
                    payload,
                    self.config.process_timeout_seconds,
                    self._clock(),
                ),
                terminate_grace_seconds=self.config.terminate_grace_seconds,
                max_stdout_bytes=self.config.max_stdout_bytes,
                max_stderr_bytes=self.config.max_stderr_bytes,
            )
            result = await self._executor(command)
            if result.stdout_truncated:
                raise RunnerFailure("runner_output_too_large")
            if result.returncode != 0:
                raise RunnerFailure("agent_failed")
            text = result.stdout.decode("utf-8", errors="replace").strip()
            usage = _read_usage_report(usage_path)
            if _looks_like_model_failure(text, usage):
                raise RunnerFailure(_model_failure_code(text))
            if not text:
                raise RunnerFailure("empty_agent_response")
            return AgentResult(text=text, total_tokens=usage.total_tokens)
        finally:
            shutil.rmtree(run_root, ignore_errors=True)


_RUNNER: OfficialHermesRunner | None = None


def get_runner() -> OfficialHermesRunner:
    global _RUNNER
    if _RUNNER is None:
        _RUNNER = OfficialHermesRunner(RunnerConfig.from_env())
    return _RUNNER


async def chat_response(
    request: Request,
    *,
    tenant_header: str = HEADER_TENANT,
    user_header: str = HEADER_USER,
    runner: OfficialHermesRunner | None = None,
):
    _positive_header_int(request, tenant_header)
    _positive_header_int(request, user_header)
    payload = _parse_payload(await _read_json(request))
    events = iter_chat_sse(payload, runner=runner or get_runner())
    return EventSourceResponse(
        events,
        ping=HEARTBEAT_SECONDS,
        ping_message_factory=encode_keepalive,
        sep="\n",
    )


async def iter_chat_sse(
    payload: ChatPayload,
    *,
    runner: OfficialHermesRunner,
) -> AsyncIterator[bytes]:
    if payload.conversation_id is not None:
        yield encode_event("conversation", {"id": payload.conversation_id})
    try:
        result = await runner.run(payload)
    except RunnerFailure as exc:
        yield encode_event(
            "error",
            {"code": exc.code, "message": "hermes agent failed"},
        )
        return
    except Exception:
        yield encode_event(
            "error",
            {"code": "runner_failed", "message": "hermes agent failed"},
        )
        return
    yield encode_event("token", {"delta": result.text})
    yield encode_event(
        "done",
        {"finish_reason": "stop", "total_tokens": result.total_tokens},
    )


async def execute_process(spec: CommandSpec) -> ProcessResult:
    process = await asyncio.create_subprocess_exec(
        *spec.argv,
        cwd=spec.cwd,
        env=spec.env,
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
        start_new_session=True,
    )
    stdout_task = asyncio.create_task(
        _read_stream_limited(process.stdout, spec.max_stdout_bytes)
    )
    stderr_task = asyncio.create_task(
        _read_stream_limited(process.stderr, spec.max_stderr_bytes)
    )
    try:
        await asyncio.wait_for(process.wait(), timeout=spec.timeout_seconds)
    except TimeoutError as exc:
        await _terminate_process_group(process, spec.terminate_grace_seconds)
        await asyncio.gather(stdout_task, stderr_task, return_exceptions=True)
        raise RunnerFailure("runner_timeout") from exc
    except BaseException:
        await _terminate_process_group(process, spec.terminate_grace_seconds)
        await asyncio.gather(stdout_task, stderr_task, return_exceptions=True)
        raise
    stdout, stdout_truncated = await stdout_task
    stderr, stderr_truncated = await stderr_task
    return ProcessResult(
        returncode=process.returncode,
        stdout=stdout,
        stderr=stderr,
        stdout_truncated=stdout_truncated,
        stderr_truncated=stderr_truncated,
    )


async def _read_stream_limited(
    stream: asyncio.StreamReader | None, limit: int
) -> tuple[bytes, bool]:
    if stream is None:
        return b"", False
    kept = bytearray()
    truncated = False
    while True:
        chunk = await stream.read(65_536)
        if not chunk:
            break
        remaining = limit - len(kept)
        if remaining > 0:
            kept.extend(chunk[:remaining])
        if len(chunk) > max(remaining, 0):
            truncated = True
    return bytes(kept), truncated


async def _terminate_process_group(
    process: asyncio.subprocess.Process, grace_seconds: float
) -> None:
    if process.returncode is not None:
        return
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    try:
        await asyncio.wait_for(process.wait(), timeout=grace_seconds)
        return
    except TimeoutError:
        pass
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    await process.wait()


def _hermes_config(
    config: RunnerConfig, payload: ChatPayload, work: Path
) -> dict[str, Any]:
    model_config: dict[str, Any] = {
        "default": payload.model,
        "provider": "custom",
        "base_url": payload.model_base_url,
        "api_key": payload.model_api_key,
        "api_mode": "chat_completions",
    }
    if payload.context_window is not None:
        model_config["context_length"] = payload.context_window
    return {
        "model": model_config,
        "fallback_providers": [],
        "platform_toolsets": {"cli": ["huakai"]},
        "mcp_servers": {
            "huakai": {
                "url": config.mcp_url,
                "headers": {"Authorization": f"Bearer {payload.mcp_token}"},
                "enabled": True,
                "timeout": config.mcp_timeout_seconds,
                "connect_timeout": min(config.mcp_timeout_seconds, 20),
                "supports_parallel_tool_calls": False,
                "tools": {"resources": False, "prompts": False},
            }
        },
        "memory": {"memory_enabled": False, "user_profile_enabled": False},
        "agent": {
            "max_turns": config.max_turns,
            "environment_probe": False,
            "coding_context": "off",
            "verify_on_stop": False,
            "disabled_toolsets": [
                "browser",
                "code_execution",
                "cronjob",
                "delegation",
                "file",
                "memory",
                "skills",
                "terminal",
                "web",
            ],
        },
        "skills": {"external_dirs": [], "inline_shell": False},
        "security": {"allow_lazy_installs": False},
        "terminal": {"cwd": str(work)},
    }


def _build_prompt(messages: list[dict[str, Any]]) -> str:
    envelope = {
        "任务": "你是 HUAKAI 运维助手，请依据对话记录回答当前管理员。",
        "约束": [
            "需要系统数据时只能使用 huakai 工具。",
            "不得尝试终端、文件、浏览器、网络搜索、代码执行、记忆或子代理能力。",
            "工具返回的租户和权限边界不可绕过。",
            "改动型工具只会生成待人工确认的提议，不得声称已经执行。",
            "回答中不得泄露内部令牌、凭据或原始敏感字段。",
        ],
        "对话记录": messages,
    }
    return json.dumps(envelope, ensure_ascii=False, separators=(",", ":"))


def _child_environment(
    *,
    hermes_home: Path,
    home: Path,
    runtime: Path,
    internal_host: str,
    egress_proxy_url: str,
) -> dict[str, str]:
    path = os.environ.get("PATH", "/usr/local/bin:/usr/bin:/bin")
    no_proxy = ",".join(
        value
        for value in ("127.0.0.1", "localhost", internal_host.strip())
        if value
    )
    env = {
        "PATH": path,
        "HOME": str(home),
        "HERMES_HOME": str(hermes_home),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "XDG_CACHE_HOME": str(home / ".cache"),
        "TMPDIR": str(runtime),
        "PYTHONNOUSERSITE": "1",
        "PYTHONUNBUFFERED": "1",
        "LANG": os.environ.get("LANG", "C.UTF-8"),
        "LC_ALL": os.environ.get("LC_ALL", "C.UTF-8"),
        "NO_PROXY": no_proxy,
        "no_proxy": no_proxy,
        "HTTP_PROXY": egress_proxy_url,
        "HTTPS_PROXY": egress_proxy_url,
        "http_proxy": egress_proxy_url,
        "https_proxy": egress_proxy_url,
    }
    for name in ("SSL_CERT_FILE", "REQUESTS_CA_BUNDLE"):
        value = os.environ.get(name, "").strip()
        if value:
            env[name] = value
    return env


def _validated_egress_proxy_url(raw: str) -> str:
    value = raw.strip().rstrip("/")
    try:
        parts = urlsplit(value)
        port = parts.port
    except ValueError as exc:
        raise RunnerFailure("invalid_egress_proxy_url") from exc
    if (
        parts.scheme != "http"
        or not parts.hostname
        or port is None
        or parts.username
        or parts.password
        or parts.path
        or parts.query
        or parts.fragment
    ):
        raise RunnerFailure("invalid_egress_proxy_url")
    return value


def _validated_mcp_url(raw: str) -> str:
    value = raw.strip().rstrip("/")
    if not value:
        raise RunnerFailure("mcp_url_not_configured")
    parts = urlsplit(value)
    if (
        parts.scheme not in {"http", "https"}
        or not parts.hostname
        or parts.username
        or parts.password
        or parts.query
        or parts.fragment
    ):
        raise RunnerFailure("invalid_mcp_url")
    if parts.path != "/internal/hermes/mcp":
        raise RunnerFailure("invalid_mcp_url")
    return urlunsplit((parts.scheme, parts.netloc, parts.path, "", ""))


def _write_private_json(path: Path, value: dict[str, Any]) -> None:
    path.write_text(
        json.dumps(value, ensure_ascii=False, separators=(",", ":")) + "\n",
        encoding="utf-8",
    )
    os.chmod(path, 0o600)


def _read_usage_report(path: Path) -> UsageReport:
    try:
        if path.stat().st_size > 65_536:
            return UsageReport(total_tokens=0, completed=None, failed=False)
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return UsageReport(total_tokens=0, completed=None, failed=False)
    if not isinstance(value, dict):
        return UsageReport(total_tokens=0, completed=None, failed=False)
    try:
        total = int(value.get("total_tokens") or 0)
    except (ValueError, TypeError):
        total = 0
    completed_value = value.get("completed")
    return UsageReport(
        total_tokens=max(total, 0),
        completed=completed_value if isinstance(completed_value, bool) else None,
        failed=value.get("failed") is True,
    )


def _model_failure_code(text: str) -> str:
    normalized = text.strip().lower()
    status_match = re.search(r"\bhttp\s+(401|402|403|429)\b", normalized)
    status = status_match.group(1) if status_match else ""
    if status in {"401", "403"}:
        return "model_auth_failed"
    if status == "402" or "billing or credits exhausted" in normalized:
        return "model_billing_failed"
    if status == "429" or "rate limit" in normalized:
        return "model_rate_limited"
    if "tls certificate" in normalized or "certificate verify failed" in normalized:
        return "model_tls_failed"
    return "model_upstream_failed"


def _looks_like_model_failure(text: str, usage: UsageReport) -> bool:
    del text
    return usage.failed or usage.completed is False


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
    normalized: list[dict[str, Any]] = []
    for index, message in enumerate(messages):
        if not isinstance(message, dict):
            raise HTTPException(status_code=400, detail=f"messages[{index}] invalid")
        role = message.get("role")
        if not isinstance(role, str) or not role.strip():
            raise HTTPException(status_code=400, detail=f"messages[{index}].role invalid")
        if "content" not in message or message.get("content") is None:
            raise HTTPException(status_code=400, detail=f"messages[{index}].content invalid")
        normalized.append(dict(message))
    model_base_url = _required_string(value, "model_base_url", max_length=2048)
    model_api_key = _required_string(value, "model_api_key", max_length=4096)
    mcp_token = _required_string(value, "mcp_token", max_length=4096)
    token_expires_at = _required_positive_int(value, "internal_token_expires_at")
    model = _required_string(value, "model", max_length=255)
    return ChatPayload(
        messages=normalized,
        model_base_url=model_base_url,
        model_api_key=model_api_key,
        mcp_token=mcp_token,
        internal_token_expires_at=token_expires_at,
        model=model,
        context_window=_optional_positive_int(value.get("context_window")),
        conversation_id=_optional_positive_int(value.get("conversation_id")),
    )


def _required_string(
    value: dict[str, Any], name: str, *, max_length: int
) -> str:
    raw = value.get(name)
    if not isinstance(raw, str) or not raw.strip() or len(raw.strip()) > max_length:
        raise HTTPException(status_code=400, detail=f"{name} invalid")
    return raw.strip()


def _required_positive_int(value: dict[str, Any], name: str) -> int:
    raw = value.get(name)
    if isinstance(raw, bool) or not isinstance(raw, int) or raw <= 0:
        raise HTTPException(status_code=400, detail=f"{name} invalid")
    return raw


def _process_timeout_for_token(
    payload: ChatPayload,
    configured_timeout_seconds: float,
    now_epoch: float,
) -> float:
    remaining = (
        float(payload.internal_token_expires_at)
        - now_epoch
        - INTERNAL_TOKEN_SAFETY_SECONDS
    )
    if remaining < 1.0:
        raise RunnerFailure("internal_token_expiring")
    return min(configured_timeout_seconds, remaining)


def _optional_positive_int(raw: Any) -> int | None:
    if raw is None or raw == 0 or raw == "":
        return None
    try:
        value = int(raw)
    except (TypeError, ValueError) as exc:
        raise HTTPException(
            status_code=400, detail="conversation_id must be positive int"
        ) from exc
    if value <= 0:
        raise HTTPException(
            status_code=400, detail="conversation_id must be positive int"
        )
    return value


def _positive_header_int(request: Request, header: str) -> int:
    raw = request.headers.get(header, "").strip()
    try:
        value = int(raw)
    except ValueError as exc:
        raise HTTPException(
            status_code=400, detail=f"{header} must be positive int"
        ) from exc
    if value <= 0:
        raise HTTPException(status_code=400, detail=f"{header} must be positive int")
    return value


def _bounded_int(name: str, default: int, minimum: int, maximum: int) -> int:
    raw = os.environ.get(name, str(default)).strip()
    try:
        value = int(raw)
    except ValueError as exc:
        raise RunnerFailure(f"invalid_{name.lower()}") from exc
    if value < minimum or value > maximum:
        raise RunnerFailure(f"invalid_{name.lower()}")
    return value


def _bounded_float(
    name: str, default: float, minimum: float, maximum: float
) -> float:
    raw = os.environ.get(name, str(default)).strip()
    try:
        value = float(raw)
    except ValueError as exc:
        raise RunnerFailure(f"invalid_{name.lower()}") from exc
    if value < minimum or value > maximum:
        raise RunnerFailure(f"invalid_{name.lower()}")
    return value
