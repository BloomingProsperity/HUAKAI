import asyncio
import hashlib
import hmac
import os
from pathlib import Path
import subprocess
import tempfile
import time
import types
import unittest
import sys

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

class _FakeApp:
    def middleware(self, *_args, **_kwargs):
        return lambda fn: fn

    def get(self, *_args, **_kwargs):
        return lambda fn: fn

    def post(self, *_args, **_kwargs):
        return lambda fn: fn


class _FakeJSONResponse:
    def __init__(self, *args, **kwargs):
        self.args = args
        self.kwargs = kwargs


sys.modules.setdefault("fastapi", types.SimpleNamespace(FastAPI=lambda *a, **k: _FakeApp(), HTTPException=Exception, Request=object))
sys.modules.setdefault("fastapi.responses", types.SimpleNamespace(JSONResponse=_FakeJSONResponse))

import main
from jwt_verify import PublicKeyCache


class MainAuthTests(unittest.TestCase):
    def setUp(self):
        self.private_key = ed25519.Ed25519PrivateKey.generate()
        self.public_pem = self.private_key.public_key().public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        ).decode("utf-8")
        main.JWT_KEYS = PublicKeyCache({"kid-a": self.public_pem})

    def test_jwt_auth_requires_subject_to_match_tenant_user_headers(self):
        now = int(time.time())
        token = jwt.encode(
            {
                "iss": "huakai-gateway",
                "aud": "hermes-runner",
                "sub": "7:42",
                "iat": now,
                "nbf": now,
                "exp": now + 900,
            },
            self.private_key,
            algorithm="EdDSA",
            headers={"kid": "kid-a"},
        )

        ok_request = _request(token, "7", "42")
        self.assertTrue(main._valid_jwt(ok_request))

        cross_user = _request(token, "7", "43")
        self.assertFalse(main._valid_jwt(cross_user))

    def test_middleware_rejects_legacy_hmac_only_request(self):
        old_secret = os.environ.get("HUAKAI_HERMES_SHARED_SECRET")
        old_mode = os.environ.get("HUAKAI_HERMES_AUTH_MODE")
        os.environ["HUAKAI_HERMES_SHARED_SECRET"] = "runner-secret"
        os.environ["HUAKAI_HERMES_AUTH_MODE"] = "hmac"
        try:
            request = _legacy_hmac_request("runner-secret")

            async def call_next(_request):
                return "called-next"

            response = asyncio.run(main.verify_auth(request, call_next))
        finally:
            _restore_env("HUAKAI_HERMES_SHARED_SECRET", old_secret)
            _restore_env("HUAKAI_HERMES_AUTH_MODE", old_mode)

        self.assertIsInstance(response, _FakeJSONResponse)
        self.assertEqual(response.kwargs["status_code"], 401)

    def test_entrypoint_fails_before_uvicorn_without_jwt_key_material_even_with_legacy_hmac_secret(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as tempdir:
            fake_bin = Path(tempdir) / "bin"
            fake_bin.mkdir()
            _write_fake_uvicorn(fake_bin)
            env = _entrypoint_env(fake_bin)
            env["HUAKAI_HERMES_SHARED_SECRET"] = "runner-secret"

            result = _run_entrypoint(env)

        self.assertEqual(result.returncode, 1)
        self.assertIn(
            "HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR or both "
            "HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH and HUAKAI_HERMES_JWT_KID",
            result.stderr,
        )
        self.assertNotIn("uvicorn-started", result.stdout)

    def test_entrypoint_accepts_single_public_key_path_and_kid(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as tempdir:
            temp = Path(tempdir)
            fake_bin = temp / "bin"
            fake_bin.mkdir()
            _write_fake_uvicorn(fake_bin)
            _write_fake_hermes(fake_bin)
            key_path = temp / "runner.pem"
            key_path.write_text("public-key", encoding="utf-8")
            env = _entrypoint_env(fake_bin)
            env.update(
                {
                    "HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH": str(key_path),
                    "HUAKAI_HERMES_JWT_KID": "kid-a",
                }
            )

            result = _run_entrypoint(env)

        self.assertEqual(result.returncode, 42)
        self.assertIn("uvicorn-started main:app --host 0.0.0.0 --port 8801", result.stdout)

    def test_entrypoint_accepts_public_keys_directory(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as tempdir:
            temp = Path(tempdir)
            fake_bin = temp / "bin"
            fake_bin.mkdir()
            _write_fake_uvicorn(fake_bin)
            _write_fake_hermes(fake_bin)
            keys_dir = temp / "keys"
            keys_dir.mkdir()
            (keys_dir / "kid-a.pem").write_text("public-key", encoding="utf-8")
            env = _entrypoint_env(fake_bin)
            env.update(
                {
                    "HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR": str(keys_dir),
                }
            )

            result = _run_entrypoint(env)

        self.assertEqual(result.returncode, 42)
        self.assertIn("uvicorn-started main:app --host 0.0.0.0 --port 8801", result.stdout)

    def test_entrypoint_rejects_wrong_hermes_package_version(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as tempdir:
            temp = Path(tempdir)
            fake_bin = temp / "bin"
            fake_bin.mkdir()
            _write_fake_uvicorn(fake_bin)
            _write_fake_hermes(fake_bin)
            key_path = temp / "runner.pem"
            key_path.write_text("public-key", encoding="utf-8")
            env = _entrypoint_env(fake_bin)
            _write_fake_distribution(Path(env["PYTHONPATH"]), "0.18.0")
            env.update(
                {
                    "HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH": str(key_path),
                    "HUAKAI_HERMES_JWT_KID": "kid-a",
                }
            )

            result = _run_entrypoint(env)

        self.assertEqual(result.returncode, 1)
        self.assertIn("requires hermes-agent 0.19.0", result.stderr)
        self.assertNotIn("uvicorn-started", result.stdout)

    def test_entrypoint_rejects_expanded_official_tool_surface(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as tempdir:
            temp = Path(tempdir)
            fake_bin = temp / "bin"
            fake_bin.mkdir()
            _write_fake_uvicorn(fake_bin)
            _write_fake_hermes(fake_bin, restricted=False)
            key_path = temp / "runner.pem"
            key_path.write_text("public-key", encoding="utf-8")
            env = _entrypoint_env(fake_bin)
            env.update(
                {
                    "HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH": str(key_path),
                    "HUAKAI_HERMES_JWT_KID": "kid-a",
                }
            )

            result = _run_entrypoint(env)

        self.assertEqual(result.returncode, 1)
        self.assertIn("tool surface is not restricted", result.stderr)
        self.assertNotIn("uvicorn-started", result.stdout)

    def test_key_generator_creates_strict_pair_and_refuses_overwrite(self):
        with tempfile.TemporaryDirectory(dir=Path.cwd()) as tempdir:
            target = Path(tempdir) / "keys"
            script = Path(__file__).with_name("generate-keypair.sh")

            first = subprocess.run(
                ["sh", str(script), str(target)],
                capture_output=True,
                text=True,
                timeout=5,
                check=False,
            )
            second = subprocess.run(
                ["sh", str(script), str(target)],
                capture_output=True,
                text=True,
                timeout=5,
                check=False,
            )

            self.assertEqual(first.returncode, 0, first.stderr)
            self.assertEqual((target / "private.pem").stat().st_mode & 0o777, 0o400)
            self.assertEqual((target / "public.pem").stat().st_mode & 0o777, 0o444)
            self.assertEqual(second.returncode, 1)
            self.assertIn("拒绝覆盖", second.stderr)


def _request(token, tenant, user):
    return types.SimpleNamespace(
        headers={
            "Authorization": f"Bearer {token}",
            main.HEADER_TENANT: tenant,
            main.HEADER_USER: user,
        },
        state=types.SimpleNamespace(),
    )


def _legacy_hmac_request(secret):
    payload_body = b'{"messages":[]}'
    timestamp = str(int(time.time()))
    path = "/chat"
    tenant = "7"
    user = "42"
    canonical = "\n".join([timestamp, "POST", path, "", tenant, user]).encode("utf-8") + b"\n" + payload_body
    signature = hmac.new(secret.encode("utf-8"), canonical, hashlib.sha256).hexdigest()

    class Request:
        method = "POST"
        headers = {
            "X-Hermes-Signature": signature,
            "X-Hermes-Timestamp": timestamp,
            main.HEADER_TENANT: tenant,
            main.HEADER_USER: user,
        }
        scope = {"query_string": b""}
        state = types.SimpleNamespace()

        class URL:
            path = "/chat"

        url = URL()

        async def body(self):
            return payload_body

    return Request()


def _restore_env(name, old_value):
    if old_value is None:
        os.environ.pop(name, None)
    else:
        os.environ[name] = old_value


def _entrypoint_env(fake_bin):
    fake_site = fake_bin.parent / "site"
    _write_fake_distribution(fake_site, "0.19.0")
    _write_fake_runtime_modules(fake_site)
    env = {
        "PATH": f"{fake_bin}{os.pathsep}{os.environ.get('PATH', '')}",
        "PYTHONPATH": str(fake_site),
        "HUAKAI_HERMES_RUNNER_BIND": "0.0.0.0:8801",
        "HUAKAI_HERMES_MCP_URL": "http://gateway:8080/internal/hermes/mcp",
        "HUAKAI_HERMES_EGRESS_PROXY_URL": "http://hermes-egress:8080",
        "HUAKAI_HERMES_WORK_ROOT": str(fake_bin.parent / "runner-work"),
    }
    return env


def _run_entrypoint(env):
    entrypoint = Path(__file__).with_name("entrypoint.sh")
    return subprocess.run(
        ["sh", str(entrypoint)],
        cwd=entrypoint.parent,
        env=env,
        capture_output=True,
        text=True,
        timeout=3,
        check=False,
    )


def _write_fake_uvicorn(fake_bin):
    uvicorn = fake_bin / "uvicorn"
    uvicorn.write_text("#!/usr/bin/env sh\necho \"uvicorn-started $*\"\nexit 42\n", encoding="utf-8")
    uvicorn.chmod(0o755)


def _write_fake_hermes(fake_bin, *, restricted=True):
    hermes = fake_bin / "hermes"
    status = "disabled" if restricted else "enabled"
    hermes.write_text(
        "#!/usr/bin/env sh\n"
        "if [ \"${1:-}\" = \"tools\" ]; then\n"
        "  echo 'Built-in toolsets (cli):'\n"
        f"  echo '  {status} terminal'\n"
        "  echo 'MCP servers:'\n"
        "  echo '  huakai all tools enabled'\n"
        "  exit 0\n"
        "fi\n"
        "echo 'Hermes Agent v0.19.0 (test)'\n",
        encoding="utf-8",
    )
    hermes.chmod(0o755)


def _write_fake_distribution(fake_site, version):
    metadata_dir = fake_site / "hermes_agent-0.19.0.dist-info"
    metadata_dir.mkdir(parents=True, exist_ok=True)
    (metadata_dir / "METADATA").write_text(
        f"Metadata-Version: 2.4\nName: hermes-agent\nVersion: {version}\n",
        encoding="utf-8",
    )


def _write_fake_runtime_modules(fake_site):
    fastapi = fake_site / "fastapi"
    fastapi.mkdir(parents=True, exist_ok=True)
    (fastapi / "__init__.py").write_text(
        "class HTTPException(Exception):\n"
        "    pass\n\n"
        "class Request:\n"
        "    pass\n",
        encoding="utf-8",
    )
    sse = fake_site / "sse_starlette"
    sse.mkdir(parents=True, exist_ok=True)
    response = "class EventSourceResponse:\n    pass\n"
    (sse / "__init__.py").write_text(response, encoding="utf-8")
    (sse / "sse.py").write_text(response, encoding="utf-8")


if __name__ == "__main__":
    unittest.main()
