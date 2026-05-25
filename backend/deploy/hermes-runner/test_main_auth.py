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


def _request(token, tenant, user):
    return types.SimpleNamespace(
        headers={
            "Authorization": f"Bearer {token}",
            main.HEADER_TENANT: tenant,
            main.HEADER_USER: user,
        },
        state=types.SimpleNamespace(),
    )


if __name__ == "__main__":
    unittest.main()
