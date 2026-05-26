#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
key_dir="$repo_root/backend/deploy/hermes-runner/dev-keys"
private_key="$key_dir/dev_jwt_private.pem"
public_key="$key_dir/dev_jwt_public.pem"

mkdir -p "$key_dir"

if [[ -e "$private_key" && "${FORCE:-}" != "1" ]]; then
  echo "error: $private_key already exists; set FORCE=1 to replace it" >&2
  exit 1
fi

tmp_private="$(mktemp "$key_dir/.dev_jwt_private.XXXXXX")"
tmp_public="$(mktemp "$key_dir/.dev_jwt_public.XXXXXX")"
cleanup() {
  rm -f "$tmp_private" "$tmp_public"
}
trap cleanup EXIT

openssl genpkey -algorithm Ed25519 -out "$tmp_private"
chmod 0400 "$tmp_private"
openssl pkey -in "$tmp_private" -pubout -out "$tmp_public"
chmod 0444 "$tmp_public"

mv "$tmp_private" "$private_key"
mv "$tmp_public" "$public_key"
trap - EXIT

echo "wrote $private_key"
echo "wrote $public_key"
echo "export HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH=./deploy/hermes-runner/dev-keys/dev_jwt_private.pem"
echo "export HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH=./deploy/hermes-runner/dev-keys/dev_jwt_public.pem"
echo "export HUAKAI_HERMES_JWT_KID=dev-local"
