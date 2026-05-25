import os
from pathlib import Path
from typing import Mapping

import jwt


ALG_EDDSA = "EdDSA"
DEFAULT_ISSUER = "huakai-gateway"
DEFAULT_AUDIENCE = "hermes-runner"
ISSUER_ENV = "HUAKAI_HERMES_JWT_ISSUER"
AUDIENCE_ENV = "HUAKAI_HERMES_JWT_AUDIENCE"
MAX_TTL_SECONDS = 15 * 60


class JWTVerificationError(ValueError):
    pass


def verify_token(
    token: str,
    public_key_pem: str,
    *,
    audience: str | None = None,
    issuer: str | None = None,
) -> dict:
    try:
        header = jwt.get_unverified_header(token)
    except Exception as exc:
        raise JWTVerificationError("invalid jwt header") from exc
    if header.get("alg") != ALG_EDDSA:
        raise JWTVerificationError("unsupported jwt alg")
    kid = str(header.get("kid", "")).strip()
    if not kid:
        raise JWTVerificationError("missing jwt kid")

    try:
        claims = jwt.decode(
            token,
            public_key_pem,
            algorithms=[ALG_EDDSA],
            audience=_expected_audience(audience),
            issuer=_expected_issuer(issuer),
            options={"require": ["iss", "aud", "sub", "iat", "nbf", "exp"]},
        )
    except Exception as exc:
        raise JWTVerificationError("jwt verification failed") from exc
    if int(claims["exp"]) - int(claims["iat"]) > MAX_TTL_SECONDS:
        raise JWTVerificationError("jwt ttl exceeds policy")
    claims["kid"] = kid
    return claims


class PublicKeyCache:
    def __init__(self, keys: Mapping[str, str] | None = None):
        self._keys = {str(k).strip(): str(v) for k, v in (keys or {}).items() if str(k).strip()}

    def add_key(self, kid: str, public_key_pem: str) -> None:
        kid = str(kid).strip()
        if not kid:
            raise JWTVerificationError("missing jwt kid")
        self._keys[kid] = public_key_pem

    def get(self, kid: str) -> str:
        kid = str(kid).strip()
        try:
            return self._keys[kid]
        except KeyError as exc:
            raise JWTVerificationError("unknown jwt kid") from exc

    def verify(self, token: str) -> dict:
        try:
            header = jwt.get_unverified_header(token)
        except Exception as exc:
            raise JWTVerificationError("invalid jwt header") from exc
        return verify_token(token, self.get(header.get("kid", "")))


def _expected_issuer(issuer: str | None = None) -> str:
    return str(issuer or os.environ.get(ISSUER_ENV, "")).strip() or DEFAULT_ISSUER


def _expected_audience(audience: str | None = None) -> str:
    return str(audience or os.environ.get(AUDIENCE_ENV, "")).strip() or DEFAULT_AUDIENCE


def load_public_key_cache_from_env() -> PublicKeyCache:
    cache = PublicKeyCache()
    single_path = os.environ.get("HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH", "").strip()
    single_kid = os.environ.get("HUAKAI_HERMES_JWT_KID", "").strip()
    if single_path and single_kid:
        cache.add_key(single_kid, Path(single_path).read_text(encoding="utf-8"))

    keys_dir = os.environ.get("HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR", "").strip()
    if keys_dir:
        for path in sorted(Path(keys_dir).glob("*.pem")):
            cache.add_key(path.stem, path.read_text(encoding="utf-8"))
    return cache
