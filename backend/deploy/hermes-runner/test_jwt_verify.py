import base64
import json
import os
from pathlib import Path
import time
import unittest

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ed25519

from jwt_verify import PublicKeyCache, JWTVerificationError, verify_token


class JWTVerifyTests(unittest.TestCase):
    def setUp(self):
        self.private_key = ed25519.Ed25519PrivateKey.generate()
        self.public_pem = self.private_key.public_key().public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        ).decode("utf-8")

    def test_verify_token_accepts_eddsa_and_rejects_mutations(self):
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

        claims = verify_token(token, self.public_pem)
        self.assertEqual(claims["sub"], "7:42")
        self.assertEqual(claims["kid"], "kid-a")

        bad_alg = _replace_header(token, {"alg": "none", "typ": "JWT", "kid": "kid-a"})
        with self.assertRaises(JWTVerificationError):
            verify_token(bad_alg, self.public_pem)

        parts = token.split(".")
        sig = bytearray(_b64decode(parts[2]))
        sig[0] ^= 0x80
        parts[2] = _b64encode(bytes(sig))
        with self.assertRaises(JWTVerificationError):
            verify_token(".".join(parts), self.public_pem)

    def test_expired_token_and_unknown_kid_are_rejected(self):
        now = int(time.time())
        expired = jwt.encode(
            {
                "iss": "huakai-gateway",
                "aud": "hermes-runner",
                "sub": "7:42",
                "iat": now - 1200,
                "nbf": now - 1200,
                "exp": now - 600,
            },
            self.private_key,
            algorithm="EdDSA",
            headers={"kid": "kid-a"},
        )
        with self.assertRaises(JWTVerificationError):
            verify_token(expired, self.public_pem)

        cache = PublicKeyCache({"kid-b": self.public_pem})
        with self.assertRaises(JWTVerificationError):
            cache.verify(expired)

    def test_public_key_cache_verify_uses_custom_issuer_audience_env(self):
        self._set_env("HUAKAI_HERMES_JWT_ISSUER", "custom-hermes-gateway")
        self._set_env("HUAKAI_HERMES_JWT_AUDIENCE", "custom-hermes-runner")
        now = int(time.time())
        custom_token = jwt.encode(
            {
                "iss": "custom-hermes-gateway",
                "aud": "custom-hermes-runner",
                "sub": "7:42",
                "iat": now,
                "nbf": now,
                "exp": now + 900,
            },
            self.private_key,
            algorithm="EdDSA",
            headers={"kid": "kid-a"},
        )
        cache = PublicKeyCache({"kid-a": self.public_pem})

        claims = cache.verify(custom_token)
        self.assertEqual(claims["sub"], "7:42")

        default_token = jwt.encode(
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
        with self.assertRaises(JWTVerificationError):
            cache.verify(default_token)

    def test_dockerfile_copies_jwt_verify_module(self):
        dockerfile = Path(__file__).with_name("Dockerfile").read_text(encoding="utf-8")
        copy_sources = []
        for line in dockerfile.splitlines():
            parts = line.strip().split()
            if parts and parts[0] == "COPY" and len(parts) >= 3:
                copy_sources.extend(parts[1:-1])

        self.assertIn("jwt_verify.py", copy_sources)

    def test_dockerfile_copies_chat_runtime_modules(self):
        dockerfile = Path(__file__).with_name("Dockerfile").read_text(encoding="utf-8")
        copy_sources = []
        for line in dockerfile.splitlines():
            parts = line.strip().split()
            if parts and parts[0] == "COPY" and len(parts) >= 3:
                copy_sources.extend(parts[1:-1])

        self.assertIn("hermes_chat.py", copy_sources)
        self.assertIn("sse_events.py", copy_sources)

    def _set_env(self, name, value):
        old = os.environ.get(name)
        os.environ[name] = value
        self.addCleanup(_restore_env, name, old)


def _replace_header(token, header):
    parts = token.split(".")
    parts[0] = _b64encode(json.dumps(header, separators=(",", ":")).encode("utf-8"))
    return ".".join(parts)


def _b64decode(value):
    padding = "=" * (-len(value) % 4)
    return base64.urlsafe_b64decode(value + padding)


def _b64encode(value):
    return base64.urlsafe_b64encode(value).decode("ascii").rstrip("=")


def _restore_env(name, old):
    if old is None:
        os.environ.pop(name, None)
    else:
        os.environ[name] = old


if __name__ == "__main__":
    unittest.main()
