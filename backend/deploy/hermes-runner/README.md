# HUAKAI Hermes Runner

## Dev JWT keys

`backend/docker-compose.dev.yml` mounts `dev-keys/dev_jwt_public.pem` so the
runner can start in JWT-only mode. The matching private key is intentionally not
committed.

For local gateway-to-runner calls, generate a disposable matching keypair:

```bash
scripts/dev/generate-hermes-jwt-keys.sh
```

Then run the gateway from `backend/` with the env values in `.env.dev.example`.
The script writes `dev_jwt_private.pem` with mode `0400` and refreshes
`dev_jwt_public.pem`; do not use these keys outside local development.

## Internal route isolation

The gateway requires HMAC proof on `/internal/runner/bootstrap`,
`/internal/runner/refresh`, and `/internal/keys`. Production deployments should
also keep those routes on a private listener, loopback-only listener, private
network, or equivalent ingress rule so application-layer HMAC is not the only
control.
