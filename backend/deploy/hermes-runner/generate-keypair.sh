#!/usr/bin/env sh
set -eu

TARGET_DIR="${1:-./dev-keys}"
PRIVATE_KEY="${TARGET_DIR}/private.pem"
PUBLIC_KEY="${TARGET_DIR}/public.pem"

if ! command -v openssl >/dev/null 2>&1; then
  echo "错误：生成 Hermes JWT 密钥需要 openssl" >&2
  exit 1
fi

mkdir -p "$TARGET_DIR"
chmod 700 "$TARGET_DIR"

if [ -e "$PRIVATE_KEY" ] || [ -e "$PUBLIC_KEY" ]; then
  echo "错误：目标密钥已存在，拒绝覆盖；请先完成轮换或选择新目录" >&2
  exit 1
fi

umask 077
PRIVATE_STAGING="${TARGET_DIR}/.private.pem.new.$$"
PUBLIC_STAGING="${TARGET_DIR}/.public.pem.new.$$"
cleanup() {
  rm -f "$PRIVATE_STAGING" "$PUBLIC_STAGING"
}
trap cleanup EXIT HUP INT TERM

openssl genpkey -algorithm ED25519 -out "$PRIVATE_STAGING"
openssl pkey -in "$PRIVATE_STAGING" -pubout -out "$PUBLIC_STAGING"
chmod 400 "$PRIVATE_STAGING"
chmod 444 "$PUBLIC_STAGING"
mv "$PRIVATE_STAGING" "$PRIVATE_KEY"
mv "$PUBLIC_STAGING" "$PUBLIC_KEY"
trap - EXIT HUP INT TERM

echo "Hermes JWT 密钥已生成：${TARGET_DIR}"
