import asyncio
import contextvars
import hashlib
import hmac
import os
import sys
import time
import types
import unittest


class _FakeEventSourceResponse:
    def __init__(self, content, **kwargs):
        self.content = content
        self.kwargs = kwargs


class _FakeHTTPException(Exception):
    def __init__(self, status_code, detail):
        super().__init__(detail)
        self.status_code = status_code
        self.detail = detail


class _FakeApp:
    def middleware(self, *_args, **_kwargs):
        return lambda fn: fn

    def get(self, *_args, **_kwargs):
        return lambda fn: fn

    def post(self, *_args, **_kwargs):
        return lambda fn: fn


sys.modules["fastapi"] = types.SimpleNamespace(
    FastAPI=lambda *a, **k: _FakeApp(),
    HTTPException=_FakeHTTPException,
    Request=object,
)
sys.modules["sse_starlette"] = types.SimpleNamespace(EventSourceResponse=_FakeEventSourceResponse)
sys.modules["sse_starlette.sse"] = types.SimpleNamespace(EventSourceResponse=_FakeEventSourceResponse)

import hermes_chat


class _FakeHermesConstants:
    def __init__(self):
        self._home = contextvars.ContextVar("fake_hermes_home", default=None)

    def set_hermes_home_override(self, value):
        return self._home.set(value)

    def reset_hermes_home_override(self, token):
        self._home.reset(token)

    def current_home(self):
        return self._home.get()


class HermesChatTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self._set_env("HUAKAI_HERMES_INTERNAL_LLM_BASE_URL", "https://env.gateway.internal/v1/openai")
        self._set_env("HUAKAI_HERMES_INTERNAL_TOKEN_SECRET", "unit-secret")

    async def test_contextvar_isolation_resets_home_between_requests(self):
        constants = _FakeHermesConstants()
        calls = []
        token_a = _signed_internal_token(7, 42, "req-a")
        token_b = _signed_internal_token(8, 43, "req-b")

        class RecordingAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                calls.append(
                    {
                        "init": {"base_url": base_url, "api_key": api_key, "model": model},
                        "home": constants.current_home(),
                    }
                )

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                calls[-1]["run"] = {
                    "user_message": user_message,
                    "system_message": system_message,
                    "conversation_history": conversation_history,
                    "task_id": task_id,
                    "persist_user_message": persist_user_message,
                }
                stream_callback({"delta": constants.current_home()})
                return {"finish_reason": "stop", "total_tokens": 1}

        await _collect(
            _payload(token_a),
            tenant_id=7,
            user_id=42,
            agent_cls=RecordingAgent,
            constants_module=constants,
        )
        self.assertIsNone(constants.current_home())

        await _collect(
            _payload(token_b),
            tenant_id=8,
            user_id=43,
            agent_cls=RecordingAgent,
            constants_module=constants,
        )
        self.assertIsNone(constants.current_home())

        self.assertEqual(
            [call["home"] for call in calls],
            [
                "/var/lib/huakai/hermes/tenants/7/users/42",
                "/var/lib/huakai/hermes/tenants/8/users/43",
            ],
        )
        self.assertEqual(calls[0]["init"]["api_key"], token_a)
        self.assertEqual(calls[1]["init"]["api_key"], token_b)

    async def test_body_internal_base_url_is_ignored_for_env_base_url(self):
        constants = _FakeHermesConstants()
        calls = []
        token = _signed_internal_token(7, 42, "req-env-base")

        class RecordingAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                calls.append({"base_url": base_url, "api_key": api_key})

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                return {"completed": True, "finish_reason": "stop", "total_tokens": 1}

        frames = await _collect(
            _payload(token, base_url="http://169.254.169.254/latest/meta-data"),
            tenant_id=7,
            user_id=42,
            agent_cls=RecordingAgent,
            constants_module=constants,
        )

        self.assertEqual(calls[0]["base_url"], "https://env.gateway.internal/v1/openai")
        self.assertEqual(calls[0]["api_key"], token)
        self.assertEqual(frames[-1], 'event: done\ndata: {"finish_reason":"stop","total_tokens":1}\n\n')

    async def test_internal_token_with_invalid_signature_is_rejected_before_agent_runs(self):
        constants = _FakeHermesConstants()
        bad_token = _tamper_signature(_signed_internal_token(7, 42, "req-bad-sig"))

        class AgentMustNotRun:
            def __init__(self, base_url=None, api_key=None, model=""):
                raise AssertionError("invalid internal_token reached agent")

        with self.assertRaises(_FakeHTTPException) as err:
            await _collect(
                _payload(bad_token),
                tenant_id=7,
                user_id=42,
                agent_cls=AgentMustNotRun,
                constants_module=constants,
            )

        self.assertEqual(err.exception.status_code, 401)

    async def test_expired_internal_token_is_rejected_before_agent_runs(self):
        constants = _FakeHermesConstants()
        expired_token = _signed_internal_token(7, 42, "req-expired", exp=int(time.time()) - 1)

        class AgentMustNotRun:
            def __init__(self, base_url=None, api_key=None, model=""):
                raise AssertionError("expired internal_token reached agent")

        with self.assertRaises(_FakeHTTPException) as err:
            await _collect(
                _payload(expired_token),
                tenant_id=7,
                user_id=42,
                agent_cls=AgentMustNotRun,
                constants_module=constants,
            )

        self.assertEqual(err.exception.status_code, 401)

    async def test_done_event_is_terminal_sentinel_after_agent_success(self):
        constants = _FakeHermesConstants()

        class DoneAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                pass

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                stream_callback("hello")
                return {"completed": True, "finish_reason": "stop", "total_tokens": 7}

        frames = await _collect(
            _payload(_signed_internal_token(7, 42, "req-done")),
            tenant_id=7,
            user_id=42,
            agent_cls=DoneAgent,
            constants_module=constants,
        )

        self.assertIn('event: token\ndata: {"delta":"hello"}\n\n', frames)
        self.assertEqual(frames[-1], 'event: done\ndata: {"finish_reason":"stop","total_tokens":7}\n\n')

    async def test_failed_agent_result_emits_agent_failed_error_without_done(self):
        constants = _FakeHermesConstants()

        class FailedResultAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                pass

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                return {"completed": False, "error": "tool failed"}

        frames = await _collect(
            _payload(_signed_internal_token(7, 42, "req-agent-failed")),
            tenant_id=7,
            user_id=42,
            agent_cls=FailedResultAgent,
            constants_module=constants,
        )
        joined = "".join(frames)

        self.assertEqual(frames, ['event: error\ndata: {"code":"agent_failed","message":"tool failed"}\n\n'])
        self.assertNotIn("event: done", joined)

    async def test_agent_exception_emits_error_without_done_or_token_leak(self):
        constants = _FakeHermesConstants()
        token = _signed_internal_token(7, 42, "req-exception")

        class RaisingAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                pass

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                raise RuntimeError("boom with secret internal-token")

        frames = await _collect(
            _payload(token),
            tenant_id=7,
            user_id=42,
            agent_cls=RaisingAgent,
            constants_module=constants,
        )
        joined = "".join(frames)

        self.assertIn('event: error\ndata: {"code":"agent_error","message":"hermes agent failed"}\n\n', frames)
        self.assertNotIn("event: done", joined)
        self.assertNotIn(token, joined)
        self.assertIsNone(constants.current_home())

    def test_event_source_response_uses_20s_keepalive_comment(self):
        response = hermes_chat.event_source_response(iter(()))

        self.assertEqual(response.kwargs["ping"], 20)
        self.assertEqual(response.kwargs["sep"], "\n")
        self.assertEqual(response.kwargs["ping_message_factory"](), b": keepalive\n\n")

    async def test_sync_agent_sleep_does_not_starve_keepalive_comment_task(self):
        constants = _FakeHermesConstants()
        timestamps = {}

        class BlockingAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                pass

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                timestamps["started"] = time.monotonic()
                time.sleep(2)
                timestamps["finished"] = time.monotonic()
                stream_callback("released")
                return {"finish_reason": "stop", "total_tokens": 5}

        response = hermes_chat.event_source_response(iter(()))
        heartbeats = []

        async def heartbeat_probe():
            while "finished" not in timestamps:
                await asyncio.sleep(0.05)
                if "started" in timestamps and "finished" not in timestamps:
                    heartbeats.append((time.monotonic(), response.kwargs["ping_message_factory"]()))

        collect_task = asyncio.create_task(
            _collect(
                _payload(_signed_internal_token(7, 42, "req-blocking")),
                tenant_id=7,
                user_id=42,
                agent_cls=BlockingAgent,
                constants_module=constants,
            )
        )
        heartbeat_task = asyncio.create_task(heartbeat_probe())
        frames = await asyncio.wait_for(collect_task, timeout=3)
        await asyncio.wait_for(heartbeat_task, timeout=1)

        self.assertIn('event: token\ndata: {"delta":"released"}\n\n', frames)
        self.assertEqual(frames[-1], 'event: done\ndata: {"finish_reason":"stop","total_tokens":5}\n\n')
        self.assertTrue(
            [frame for tick, frame in heartbeats if tick < timestamps["finished"]],
            "direct sync run_conversation would starve the event loop until after the 2s sleep",
        )
        self.assertTrue(all(frame == b": keepalive\n\n" for _, frame in heartbeats))

    async def test_invokes_hermes_agent_with_v014_conversation_signature(self):
        constants = _FakeHermesConstants()
        calls = []

        class StrictSignatureAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                calls.append({"init": {"base_url": base_url, "api_key": api_key, "model": model}})

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                calls[-1]["run"] = {
                    "user_message": user_message,
                    "system_message": system_message,
                    "conversation_history": conversation_history,
                    "task_id": task_id,
                    "persist_user_message": persist_user_message,
                }
                stream_callback("ok")
                return {"finish_reason": "stop", "total_tokens": 3}

        frames = await _collect(
            {
                "messages": [
                    {"role": "system", "content": "system guard"},
                    {"role": "user", "content": "first"},
                    {"role": "assistant", "content": "answer"},
                    {"role": "user", "content": "final question"},
                ],
                "model": "deep-hermes",
                "conversation_id": 123,
                "internal_base_url": "https://gateway.internal/v1/openai",
                "internal_token": _signed_internal_token(7, 42, "req-signature"),
            },
            tenant_id=7,
            user_id=42,
            agent_cls=StrictSignatureAgent,
            constants_module=constants,
        )

        self.assertEqual(calls[0]["init"]["model"], "deep-hermes")
        self.assertEqual(calls[0]["run"]["user_message"], "final question")
        self.assertEqual(calls[0]["run"]["system_message"], "system guard")
        self.assertEqual(
            calls[0]["run"]["conversation_history"],
            [
                {"role": "user", "content": "first"},
                {"role": "assistant", "content": "answer"},
            ],
        )
        self.assertIsNone(calls[0]["run"]["task_id"])
        self.assertIsNone(calls[0]["run"]["persist_user_message"])
        self.assertIn('event: token\ndata: {"delta":"ok"}\n\n', frames)
        self.assertEqual(frames[-1], 'event: done\ndata: {"finish_reason":"stop","total_tokens":3}\n\n')

    async def test_tool_aware_agent_receives_catalog_and_working_executor(self):
        # 变异:若 runner 不再塑形 tool_call 请求、或不再解析网关的 tool_result,具备工具能力的 agent
        # 就拿不到可用的诊断数据,其回答便无所凭据。
        constants = _FakeHermesConstants()
        seen = {}
        captured_request = {}

        def fake_post(url, token, tool_name, args, mode=""):
            captured_request.update({"url": url, "token": token, "tool_name": tool_name, "args": args, "mode": mode})
            # 代替网关返回的脱敏 tool_result。
            return {"tool_name": tool_name, "status": "ok", "result": {"event_count": 3}}

        original_post = hermes_chat._post_tool_execute
        hermes_chat._post_tool_execute = fake_post
        self.addCleanup(lambda: setattr(hermes_chat, "_post_tool_execute", original_post))

        class ToolAwareAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                pass

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                stream_callback=None,
                tool_catalog=None,
                tool_executor=None,
            ):
                seen["catalog"] = tool_catalog
                seen["result"] = tool_executor("audit_lookup", {"severity": "error"})
                stream_callback("grounded")
                return {"finish_reason": "stop", "total_tokens": 2}

        # 固定 env base URL(runner "始终"从 env 推导 internal base、忽略请求体——这是一道 SSRF 防护),
        # 使本断言里的 tool-execute URL 确定可预期。
        self._set_env("HUAKAI_HERMES_INTERNAL_LLM_BASE_URL", "http://gw.internal:8080/internal/v1/openai")
        token = _signed_internal_token(7, 42, "req-tool")
        payload = _payload(token, base_url="http://attacker.example/internal/v1/openai")
        payload["tool_catalog"] = [
            {"name": "audit_lookup", "description": "read audit events", "input_schema": {"severity": "str"}},
        ]

        frames = await _collect(payload, tenant_id=7, user_id=42, agent_cls=ToolAwareAgent, constants_module=constants)

        self.assertEqual(seen["catalog"], [{"name": "audit_lookup", "description": "read audit events", "input_schema": {"severity": "str"}}])
        # 执行器塑形了请求:name + args 已转发;token 已携带。
        self.assertEqual(captured_request["tool_name"], "audit_lookup")
        self.assertEqual(captured_request["args"], {"severity": "error"})
        self.assertEqual(captured_request["token"], token)
        # 只读工具(目录无 mutating 标志)以空 mode 转发 —— 走只读路径,不触发提议。
        self.assertEqual(captured_request["mode"], "")
        # 且网关 origin + 固定的 tool-execute 路径是从 ENV base URL(而非攻击者可控的请求体)推导出来的,挫败 SSRF。
        self.assertEqual(captured_request["url"], "http://gw.internal:8080/internal/hermes/tool-execute")
        # agent 消费了脱敏后的结果。
        self.assertEqual(seen["result"], {"tool_name": "audit_lookup", "status": "ok", "result": {"event_count": 3}})
        self.assertIn('event: token\ndata: {"delta":"grounded"}\n\n', frames)

    def test_executor_sends_propose_for_mutating_and_relays_needs_confirmation(self):
        # 抓的缺陷(Phase B 提议接线的头牌):被网关目录标记 mutating 的工具,executor 必须以
        # mode=propose 转发(网关只做 dry-run、返回 needs_confirmation),而只读工具仍以空 mode 转发;
        # 且 needs_confirmation 结果要原样回灌给模型(runner 绝不自动确认)。
        # 变异(已验证转红):把 mode 计算改成恒 "" → account_pause 不再走 propose,mode 断言红。
        seen = {}

        def fake_post(url, token, tool_name, args, mode=""):
            seen[tool_name] = mode
            if mode == "propose":
                return {"status": "needs_confirmation", "correlation_id": "hmc_x", "preview": {"to": "paused"}}
            return {"status": "ok", "result": {}}

        original_post = hermes_chat._post_tool_execute
        hermes_chat._post_tool_execute = fake_post
        self.addCleanup(lambda: setattr(hermes_chat, "_post_tool_execute", original_post))

        payload = hermes_chat.ChatPayload(
            messages=[],
            internal_base_url="http://gw.internal:8080/internal/v1/openai",
            internal_token="tok",
            tool_catalog=(
                {"name": "audit_lookup", "description": "d", "input_schema": {}},
                {"name": "account_pause", "description": "d", "input_schema": {}, "mutating": True, "requires_confirmation": True},
            ),
        )
        execute = hermes_chat._build_tool_executor(payload)
        execute("audit_lookup", {})
        prop = execute("account_pause", {"account_id": 5})

        self.assertEqual(seen.get("audit_lookup"), "")          # 只读 → 空 mode(只读路径)
        self.assertEqual(seen.get("account_pause"), "propose")  # mutating → mode=propose
        self.assertEqual(prop.get("status"), "needs_confirmation")  # needs_confirmation 原样回灌
        self.assertEqual(prop.get("correlation_id"), "hmc_x")

    def test_post_tool_execute_writes_mode_only_when_set(self):
        # 抓的缺陷:mode 非空时必须进请求体(网关据此走 propose 分支),空时绝不写 mode 键(只读请求体
        # 与提议接入前逐字节一致)。
        # 变异(已验证转红):去掉 `if mode: body_obj["mode"]=mode` → propose 请求体缺 mode → 断言红。
        import json as _json
        import urllib.request as _ur

        captured = {}

        class _Resp:
            def __enter__(self):
                return self

            def __exit__(self, *a):
                return False

            def read(self):
                return b'{"status":"needs_confirmation","correlation_id":"hmc_x"}'

        def fake_urlopen(req, timeout=None):
            captured["data"] = req.data
            return _Resp()

        original = _ur.urlopen
        _ur.urlopen = fake_urlopen
        self.addCleanup(lambda: setattr(_ur, "urlopen", original))

        out = hermes_chat._post_tool_execute(
            "http://gw.internal:8080/internal/hermes/tool-execute", "tok", "account_pause", {"account_id": 5}, "propose"
        )
        body = _json.loads(captured["data"].decode("utf-8"))
        self.assertEqual(body.get("mode"), "propose")
        self.assertEqual(out, {"status": "needs_confirmation", "correlation_id": "hmc_x"})

        hermes_chat._post_tool_execute(
            "http://gw.internal:8080/internal/hermes/tool-execute", "tok", "audit_lookup", {}, ""
        )
        body_ro = _json.loads(captured["data"].decode("utf-8"))
        self.assertNotIn("mode", body_ro)  # 空 mode 不写键

    def test_parse_tool_catalog_preserves_mutating_flags(self):
        # 抓的缺陷:网关注入的 mutating / requires_confirmation 标志必须被保留(否则 executor 无从判断
        # 该走 propose);只读条目则不应凭空多出这些键。
        # 变异(已验证转红):不保留 mutating 标志 → mut.get("mutating") 为 None → 断言红。
        raw = [
            {"name": "audit_lookup", "description": "d", "input_schema": {}},
            {"name": "account_pause", "description": "d", "input_schema": {}, "mutating": True, "requires_confirmation": True},
        ]
        parsed = hermes_chat._parse_tool_catalog(raw)
        ro = next(t for t in parsed if t["name"] == "audit_lookup")
        mut = next(t for t in parsed if t["name"] == "account_pause")
        self.assertNotIn("mutating", ro)
        self.assertNotIn("requires_confirmation", ro)
        self.assertTrue(mut.get("mutating"))
        self.assertTrue(mut.get("requires_confirmation"))

    async def test_strict_signature_agent_does_not_receive_tool_kwargs(self):
        # 变异:若 tool-kwarg 注入不感知签名,一个未声明 tool_catalog/tool_executor 的已加载 agent
        # 就会因 TypeError 崩溃(或 runner 会扩大其供应链契约)。即便目录存在,它也必须完全不受影响。
        constants = _FakeHermesConstants()
        observed = {}

        class StrictSignatureAgent:
            def __init__(self, base_url=None, api_key=None, model=""):
                pass

            def run_conversation(
                self,
                user_message,
                system_message=None,
                conversation_history=None,
                task_id=None,
                stream_callback=None,
                persist_user_message=None,
            ):
                observed["ran"] = True
                stream_callback("ok")
                return {"finish_reason": "stop", "total_tokens": 1}

        payload = _payload(_signed_internal_token(7, 42, "req-strict"))
        payload["tool_catalog"] = [{"name": "audit_lookup", "description": "x", "input_schema": {}}]

        frames = await _collect(payload, tenant_id=7, user_id=42, agent_cls=StrictSignatureAgent, constants_module=constants)

        # 它成功运行(无 TypeError),且从未被传入 tool kwargs。
        self.assertTrue(observed.get("ran"))
        self.assertEqual(frames[-1], 'event: done\ndata: {"finish_reason":"stop","total_tokens":1}\n\n')

    def test_tool_execute_url_is_sibling_path_on_same_origin(self):
        # 变异:错误的推导(例如往 /v1/openai 路径上追加)会把工具调用指向不存在的路由,使每个工具都 404。
        self.assertEqual(
            hermes_chat._internal_tool_execute_url("http://127.0.0.1:8080/internal/v1/openai"),
            "http://127.0.0.1:8080/internal/hermes/tool-execute",
        )
        self.assertEqual(
            hermes_chat._internal_tool_execute_url("https://gw.example:9443/internal/v1/openai/"),
            "https://gw.example:9443/internal/hermes/tool-execute",
        )

    def test_parse_tool_catalog_drops_malformed_entries(self):
        # 变异:畸形的目录条目必须被丢弃,而不能崩掉 payload 解析——坏的网关条目绝不能破坏对话。
        catalog = hermes_chat._parse_tool_catalog(
            [
                {"name": "audit_lookup", "description": "ok", "input_schema": {"a": "b"}},
                {"name": "", "description": "blank name dropped"},
                "not-a-dict",
                {"description": "no name dropped"},
                {"name": "log_analyze"},  # 缺 description/schema => 取默认值
            ]
        )
        self.assertEqual(
            catalog,
            (
                {"name": "audit_lookup", "description": "ok", "input_schema": {"a": "b"}},
                {"name": "log_analyze", "description": "", "input_schema": {}},
            ),
        )

    def _set_env(self, name, value):
        old = os.environ.get(name)
        os.environ[name] = value
        self.addCleanup(_restore_env, name, old)


async def _collect(payload, *, tenant_id, user_id, agent_cls, constants_module):
    frames = []
    async for frame in hermes_chat.iter_chat_sse(
        payload,
        tenant_id=tenant_id,
        user_id=user_id,
        agent_cls=agent_cls,
        constants_module=constants_module,
    ):
        frames.append(frame.decode("utf-8"))
    return frames


def _payload(token, *, base_url="https://gateway.internal/v1/openai"):
    return {
        "messages": [{"role": "user", "content": "hello"}],
        "model": "test-model",
        "internal_base_url": base_url,
        "internal_token": token,
    }


def _signed_internal_token(tenant_id, user_id, request_id, *, exp=None, secret="unit-secret"):
    if exp is None:
        exp = int(time.time()) + 120
    canonical = f"{tenant_id}|{user_id}|{request_id}|{exp}"
    signature = hmac.new(secret.encode("utf-8"), canonical.encode("utf-8"), hashlib.sha256).hexdigest()
    return f"{canonical}|{signature}"


def _tamper_signature(token):
    replacement = "0" if token[-1] != "0" else "1"
    return token[:-1] + replacement


def _restore_env(name, old):
    if old is None:
        os.environ.pop(name, None)
    else:
        os.environ[name] = old


if __name__ == "__main__":
    unittest.main()
