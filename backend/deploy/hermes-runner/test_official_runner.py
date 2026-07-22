import asyncio
import json
import os
from pathlib import Path
import sys
import tempfile
import types
import unittest


class _FakeHTTPException(Exception):
    def __init__(self, status_code, detail):
        super().__init__(detail)
        self.status_code = status_code
        self.detail = detail


class _FakeEventSourceResponse:
    def __init__(self, events, **kwargs):
        self.events = events
        self.kwargs = kwargs


class _FakeApp:
    def middleware(self, *_args, **_kwargs):
        return lambda fn: fn

    def get(self, *_args, **_kwargs):
        return lambda fn: fn

    def post(self, *_args, **_kwargs):
        return lambda fn: fn


sys.modules.setdefault(
    "fastapi",
    types.SimpleNamespace(
        FastAPI=lambda *args, **kwargs: _FakeApp(),
        HTTPException=_FakeHTTPException,
        Request=object,
    ),
)
sys.modules.setdefault("sse_starlette", types.SimpleNamespace(EventSourceResponse=_FakeEventSourceResponse))
sys.modules.setdefault(
    "sse_starlette.sse",
    types.SimpleNamespace(EventSourceResponse=_FakeEventSourceResponse),
)

from fastapi import HTTPException

import official_runner
import official_tool_surface


class OfficialRunnerTests(unittest.TestCase):
    def setUp(self):
        official_runner._RUNNER = None

    def test_parse_payload_requires_official_model_configuration(self):
        payload = official_runner._parse_payload(
            {
                "messages": [{"role": "user", "content": "检查系统"}],
                "model_base_url": "https://model.example.com/v1",
                "model_api_key": "sk-external-test",
                "mcp_token": "v1.mcp.signature",
                "internal_token_expires_at": 1_700_000_300,
                "model": "claude-test",
                "conversation_id": 9,
            }
        )

        self.assertEqual(payload.model, "claude-test")
        self.assertEqual(payload.internal_token_expires_at, 1_700_000_300)
        self.assertIsNone(payload.context_window)
        self.assertEqual(payload.conversation_id, 9)
        self.assertEqual(payload.model_base_url, "https://model.example.com/v1")
        self.assertEqual(payload.model_api_key, "sk-external-test")

        with self.assertRaises(HTTPException):
            official_runner._parse_payload(
                {
                    "messages": [{"role": "user", "content": "检查系统"}],
                    "model_base_url": "https://model.example.com/v1",
                    "model_api_key": "model-key",
                    "mcp_token": "mcp-token",
                    "internal_token_expires_at": 1_700_000_300,
                }
            )

        for invalid_expiry in (None, True, 0, -1, "1700000300"):
            with self.subTest(invalid_expiry=invalid_expiry), self.assertRaises(
                HTTPException
            ):
                official_runner._parse_payload(
                    {
                        "messages": [{"role": "user", "content": "检查系统"}],
                        "model_base_url": "https://model.example.com/v1",
                        "model_api_key": "model-key",
                        "mcp_token": "mcp-token",
                        "internal_token_expires_at": invalid_expiry,
                        "model": "model",
                    }
                )

    def test_mcp_url必须固定为HUAKAI内部入口(self):
        mcp_url = official_runner._validated_mcp_url(
            "http://gateway:8080/internal/hermes/mcp/"
        )

        self.assertEqual(mcp_url, "http://gateway:8080/internal/hermes/mcp")

        invalid = (
            "",
            "file:///internal/hermes/mcp",
            "https://user:pass@gateway/internal/hermes/mcp",
            "https://gateway/v1/hermes/mcp",
            "https://gateway/internal/hermes/mcp?target=other",
        )
        for value in invalid:
            with self.subTest(value=value), self.assertRaises(
                official_runner.RunnerFailure
            ):
                official_runner._validated_mcp_url(value)

    def test_config_exposes_only_huakai_mcp_and_disables_local_capabilities(self):
        config = _config(Path("/var/lib/test"))
        payload = _payload()

        rendered = official_runner._hermes_config(config, payload, Path("/work"))

        self.assertEqual(rendered["platform_toolsets"], {"cli": ["huakai"]})
        self.assertEqual(set(rendered["mcp_servers"]), {"huakai"})
        self.assertEqual(
            rendered["mcp_servers"]["huakai"]["headers"]["Authorization"],
            "Bearer v1.mcp.signature",
        )
        self.assertEqual(rendered["model"]["api_key"], "sk-external-test")
        self.assertFalse(rendered["memory"]["memory_enabled"])
        self.assertFalse(rendered["memory"]["user_profile_enabled"])
        self.assertFalse(rendered["security"]["allow_lazy_installs"])
        self.assertEqual(rendered["agent"]["coding_context"], "off")
        self.assertIn("terminal", rendered["agent"]["disabled_toolsets"])
        self.assertIn("web", rendered["agent"]["disabled_toolsets"])
        self.assertEqual(rendered["fallback_providers"], [])
        self.assertEqual(rendered["model"]["context_length"], 128_000)

    def test_startup_check_reads_official_surface_and_removes_workspace(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            work_root = Path(root) / "runner"
            captured = {}

            async def executor(spec):
                captured["spec"] = spec
                config_path = Path(spec.env["HERMES_HOME"]) / "config.yaml"
                captured["config"] = json.loads(
                    config_path.read_text(encoding="utf-8")
                )
                captured["config_mode"] = config_path.stat().st_mode & 0o777
                return official_runner.ProcessResult(
                    returncode=0,
                    stdout=(
                        "Built-in toolsets (cli):\n"
                        "  disabled terminal\n"
                        "  disabled web\n\n"
                        "MCP servers:\n"
                        "  huakai all tools enabled\n"
                    ).encode("utf-8"),
                    stderr=b"",
                )

            asyncio.run(
                official_tool_surface.verify_official_tool_surface(
                    _config(work_root), executor=executor
                )
            )

            spec = captured["spec"]
            self.assertEqual(
                spec.argv, ("hermes", "tools", "list", "--platform", "cli")
            )
            self.assertEqual(captured["config_mode"], 0o600)
            self.assertEqual(set(captured["config"]["mcp_servers"]), {"huakai"})
            self.assertEqual(list(work_root.iterdir()), [])

    def test_startup_check_rejects_expanded_or_unreadable_surface(self):
        invalid_outputs = (
            (
                "Built-in toolsets (cli):\n"
                "  enabled terminal\n"
                "MCP servers:\n"
                "  huakai all tools enabled\n"
            ),
            (
                "Built-in toolsets (cli):\n"
                "  disabled terminal\n"
                "MCP servers:\n"
                "  huakai all tools enabled\n"
                "  foreign all tools enabled\n"
            ),
            "MCP servers:\n  huakai all tools enabled\n",
        )

        for output in invalid_outputs:
            with self.subTest(output=output):
                self.assertFalse(
                    official_tool_surface.tool_surface_is_restricted(
                        output.encode("utf-8")
                    )
                )
        self.assertFalse(
            official_tool_surface.tool_surface_is_restricted(b"\xff\xfe")
        )

    def test_runner_uses_official_cli_and_removes_request_directory(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            work_root = Path(root) / "runner"
            captured = {}

            async def executor(spec):
                captured["spec"] = spec
                config_path = Path(spec.env["HERMES_HOME"]) / "config.yaml"
                captured["config"] = json.loads(config_path.read_text(encoding="utf-8"))
                captured["config_mode"] = config_path.stat().st_mode & 0o777
                usage_path = Path(spec.argv[spec.argv.index("--usage-file") + 1])
                usage_path.write_text('{"total_tokens":37}', encoding="utf-8")
                return official_runner.ProcessResult(
                    returncode=0,
                    stdout="系统正常".encode("utf-8"),
                    stderr=b"",
                )

            runner = official_runner.OfficialHermesRunner(
                _config(work_root),
                executor=executor,
                clock=lambda: 1_700_000_000.0,
            )
            result = asyncio.run(runner.run(_payload()))

            self.assertEqual(result.text, "系统正常")
            self.assertEqual(result.total_tokens, 37)
            spec = captured["spec"]
            self.assertEqual(spec.argv[0], "hermes")
            self.assertEqual(spec.argv[spec.argv.index("--provider") + 1], "custom")
            self.assertEqual(spec.argv[spec.argv.index("--toolsets") + 1], "huakai")
            self.assertIn("--ignore-rules", spec.argv)
            self.assertNotIn("sk-external-test", spec.argv)
            self.assertEqual(captured["config_mode"], 0o600)
            self.assertEqual(spec.timeout_seconds, 1)
            self.assertEqual(
                captured["config"]["model"]["api_key"], "sk-external-test"
            )
            prompt = json.loads(spec.argv[spec.argv.index("-z") + 1])
            self.assertEqual(prompt["对话记录"], _payload().messages)
            self.assertEqual(list(work_root.iterdir()), [])

    def test_child_environment_drops_parent_credentials_and_uses_guarded_proxy(self):
        old_values = {
            "OPENAI_API_KEY": os.environ.get("OPENAI_API_KEY"),
            "HTTP_PROXY": os.environ.get("HTTP_PROXY"),
        }
        os.environ["OPENAI_API_KEY"] = "must-not-pass"
        os.environ["HTTP_PROXY"] = "http://proxy.invalid"
        try:
            env = official_runner._child_environment(
                hermes_home=Path("/run/h"),
                home=Path("/run/home"),
                runtime=Path("/run/runtime"),
                internal_host="gateway",
                egress_proxy_url="http://hermes-egress:8080",
            )
        finally:
            _restore_env(old_values)

        self.assertNotIn("OPENAI_API_KEY", env)
        self.assertEqual(env["HTTP_PROXY"], "http://hermes-egress:8080")
        self.assertEqual(env["HTTPS_PROXY"], "http://hermes-egress:8080")
        self.assertEqual(env["HERMES_HOME"], "/run/h")
        self.assertIn("gateway", env["NO_PROXY"])
        self.assertEqual(env["TMPDIR"], "/run/runtime")

    def test_runner_failure_never_exposes_stderr_or_token(self):
        async def executor(_spec):
            return official_runner.ProcessResult(
                returncode=1,
                stdout=b"",
                stderr=b"sk-external-test private upstream detail",
            )

        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            runner = official_runner.OfficialHermesRunner(
                _config(Path(root) / "runner"),
                executor=executor,
                clock=lambda: 1_700_000_000.0,
            )
            frames = asyncio.run(_collect_events(_payload(), runner))

        body = b"".join(frames).decode("utf-8")
        self.assertIn('"code":"agent_failed"', body)
        self.assertNotIn("private upstream detail", body)
        self.assertNotIn("sk-external-test", body)
        self.assertNotIn("event: done", body)

    def test_sse_preserves_conversation_and_publishes_final_response(self):
        async def executor(spec):
            usage_path = Path(spec.argv[spec.argv.index("--usage-file") + 1])
            usage_path.write_text('{"total_tokens":12}', encoding="utf-8")
            return official_runner.ProcessResult(0, "诊断完成".encode("utf-8"), b"")

        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            runner = official_runner.OfficialHermesRunner(
                _config(Path(root) / "runner"),
                executor=executor,
                clock=lambda: 1_700_000_000.0,
            )
            frames = asyncio.run(_collect_events(_payload(conversation_id=17), runner))

        body = b"".join(frames).decode("utf-8")
        self.assertIn('event: conversation\ndata: {"id":17}', body)
        self.assertIn('event: token\ndata: {"delta":"诊断完成"}', body)
        self.assertIn(
            'event: done\ndata: {"finish_reason":"stop","total_tokens":12}', body
        )

    def test_oversized_stdout_is_rejected_without_partial_answer(self):
        async def executor(_spec):
            return official_runner.ProcessResult(
                0, b"partial", b"", stdout_truncated=True
            )

        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            runner = official_runner.OfficialHermesRunner(
                _config(Path(root) / "runner"),
                executor=executor,
                clock=lambda: 1_700_000_000.0,
            )
            frames = asyncio.run(_collect_events(_payload(), runner))

        body = b"".join(frames).decode("utf-8")
        self.assertIn("runner_output_too_large", body)
        self.assertNotIn("partial", body)
        self.assertNotIn("event: done", body)

    def test_execute_process_timeout_stops_process_group(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            spec = official_runner.CommandSpec(
                argv=(sys.executable, "-c", "import time; time.sleep(30)"),
                cwd=Path(root),
                env={"PATH": os.environ.get("PATH", "")},
                timeout_seconds=0.05,
                terminate_grace_seconds=0.05,
                max_stdout_bytes=4096,
                max_stderr_bytes=4096,
            )

            with self.assertRaises(official_runner.RunnerFailure) as caught:
                asyncio.run(official_runner.execute_process(spec))

        self.assertEqual(caught.exception.code, "runner_timeout")

    def test_concurrency_queue_timeout_returns_busy(self):
        started = asyncio.Event()
        release = asyncio.Event()

        async def executor(spec):
            started.set()
            await release.wait()
            usage_path = Path(spec.argv[spec.argv.index("--usage-file") + 1])
            usage_path.write_text('{"total_tokens":1}', encoding="utf-8")
            return official_runner.ProcessResult(0, b"ok", b"")

        async def scenario(work_root):
            config = _config(work_root, queue_timeout_seconds=0.02)
            runner = official_runner.OfficialHermesRunner(
                config,
                executor=executor,
                clock=lambda: 1_700_000_000.0,
            )
            first = asyncio.create_task(runner.run(_payload()))
            await started.wait()
            with self.assertRaises(official_runner.RunnerFailure) as caught:
                await runner.run(_payload())
            release.set()
            await first
            return caught.exception.code

        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            code = asyncio.run(scenario(Path(root) / "runner"))

        self.assertEqual(code, "runner_busy")

    def test_runner_caps_process_timeout_to_internal_token_lifetime(self):
        captured = {}

        async def executor(spec):
            captured["timeout"] = spec.timeout_seconds
            usage_path = Path(spec.argv[spec.argv.index("--usage-file") + 1])
            usage_path.write_text('{"total_tokens":1}', encoding="utf-8")
            return official_runner.ProcessResult(0, b"ok", b"")

        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            config = _config(Path(root) / "runner", process_timeout_seconds=240)
            runner = official_runner.OfficialHermesRunner(
                config,
                executor=executor,
                clock=lambda: 1_700_000_100.0,
            )
            asyncio.run(
                runner.run(_payload(internal_token_expires_at=1_700_000_200))
            )

        self.assertEqual(captured["timeout"], 70.0)

    def test_runner_rejects_expiring_token_before_starting_process(self):
        called = False

        async def executor(_spec):
            nonlocal called
            called = True
            return official_runner.ProcessResult(0, b"unexpected", b"")

        with tempfile.TemporaryDirectory(dir=Path.cwd()) as root:
            runner = official_runner.OfficialHermesRunner(
                _config(Path(root) / "runner"),
                executor=executor,
                clock=lambda: 1_700_000_100.0,
            )
            with self.assertRaises(official_runner.RunnerFailure) as caught:
                asyncio.run(
                    runner.run(_payload(internal_token_expires_at=1_700_000_130))
                )

        self.assertEqual(caught.exception.code, "internal_token_expiring")
        self.assertFalse(called)

    def test_chat_response_validates_gateway_identity_headers(self):
        request = _FakeRequest(
            headers={official_runner.HEADER_TENANT: "0", official_runner.HEADER_USER: "2"},
            body={
                "messages": [{"role": "user", "content": "检查"}],
                "model_base_url": "https://model.example.com/v1",
                "model_api_key": "model-key",
                "mcp_token": "mcp-token",
                "internal_token_expires_at": 1_700_000_300,
                "model": "model",
            },
        )

        with self.assertRaises(HTTPException):
            asyncio.run(
                official_runner.chat_response(
                    request,
                    runner=types.SimpleNamespace(run=lambda _payload: None),
                )
            )


class _FakeRequest:
    def __init__(self, *, headers, body):
        self.headers = headers
        self._body = body

    async def json(self):
        return self._body


async def _collect_events(payload, runner):
    return [frame async for frame in official_runner.iter_chat_sse(payload, runner=runner)]


def _payload(conversation_id=None, internal_token_expires_at=1_700_000_300):
    return official_runner.ChatPayload(
        messages=[
            {"role": "system", "content": "只报告真实状态"},
            {"role": "user", "content": "检查系统"},
        ],
        model_base_url="https://model.example.com/v1",
        model_api_key="sk-external-test",
        mcp_token="v1.mcp.signature",
        internal_token_expires_at=internal_token_expires_at,
        model="claude-test",
        context_window=128_000,
        conversation_id=conversation_id,
    )


def _config(
    work_root,
    *,
    queue_timeout_seconds=0.2,
    process_timeout_seconds=1,
):
    return official_runner.RunnerConfig(
        mcp_url="http://gateway:8080/internal/hermes/mcp",
        egress_proxy_url="http://hermes-egress:8080",
        work_root=work_root,
        hermes_binary="hermes",
        max_concurrency=1,
        queue_timeout_seconds=queue_timeout_seconds,
        process_timeout_seconds=process_timeout_seconds,
        terminate_grace_seconds=0.1,
        max_prompt_chars=131_072,
        max_stdout_bytes=1_048_576,
        max_stderr_bytes=65_536,
        max_turns=20,
        mcp_timeout_seconds=20,
    )


def _restore_env(values):
    for name, value in values.items():
        if value is None:
            os.environ.pop(name, None)
        else:
            os.environ[name] = value


if __name__ == "__main__":
    unittest.main()
