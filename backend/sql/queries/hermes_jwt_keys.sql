-- Hermes Runner JWT 公钥查询。

-- name: InsertJWTKey :one
INSERT INTO hermes_jwt_keys (kid, alg, public_key_pem, valid_until)
VALUES (
    sqlc.arg(kid)::text,
    sqlc.arg(alg)::text,
    sqlc.arg(public_key_pem)::text,
    sqlc.narg(valid_until)::timestamptz
)
RETURNING kid, alg, public_key_pem, valid_from, valid_until, revoked_at, created_at;

-- name: GetActiveJWTKeys :many
SELECT kid, alg, public_key_pem, valid_from, valid_until, revoked_at, created_at
FROM hermes_jwt_keys
WHERE valid_from <= NOW()
  AND (valid_until IS NULL OR valid_until > NOW())
  AND revoked_at IS NULL
ORDER BY valid_from DESC, kid ASC;

-- name: GetJWTKeyByKid :one
SELECT kid, alg, public_key_pem, valid_from, valid_until, revoked_at, created_at
FROM hermes_jwt_keys
WHERE kid = sqlc.arg(kid)::text;

-- name: RevokeJWTKey :execrows
UPDATE hermes_jwt_keys
SET revoked_at = NOW()
WHERE kid = sqlc.arg(kid)::text
  AND revoked_at IS NULL
RETURNING kid;
