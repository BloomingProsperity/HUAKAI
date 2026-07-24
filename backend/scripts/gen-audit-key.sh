#!/usr/bin/env bash
# 一键生成 production 审计签名所需的 ed25519 私钥(PKCS#8 PEM),降低首次上线摩擦。
#
# 用法:
#   bash backend/scripts/gen-audit-key.sh            # 默认生成到 secrets/audit_key.pem
#   bash backend/scripts/gen-audit-key.sh /path/key.pem
#
# 生成后按提示把 HUAKAI_AUDIT_PRIVATE_KEY_PATH 指向该私钥(容器部署经 volumes 挂载,
# 详见 docs/deploy/生产部署与升级.md）。切勿把私钥提交进仓库（.gitignore 已忽略 secrets/）。
set -euo pipefail

OUT="${1:-secrets/audit_key.pem}"
DIR="$(dirname "$OUT")"

if ! command -v openssl >/dev/null 2>&1; then
  echo "错误:未找到 openssl,请先安装 openssl 再运行本脚本。" >&2
  exit 1
fi

if [ -e "$OUT" ]; then
  echo "错误:$OUT 已存在,拒绝覆盖(避免误删在用私钥)。如确需重建请先手动移走旧文件。" >&2
  exit 1
fi

mkdir -p "$DIR"
# 仅属主可读写,防止私钥被同机其他用户读取。
( umask 077 && openssl genpkey -algorithm ed25519 -out "$OUT" )
chmod 600 "$OUT" 2>/dev/null || true

echo "✓ 已生成审计私钥:$OUT"
echo
echo "下一步：在 .env / shell 设置（容器部署见 docs/deploy/生产部署与升级.md）："
echo "  裸二进制:HUAKAI_AUDIT_PRIVATE_KEY_PATH=$OUT"
echo "  容器部署:HUAKAI_AUDIT_PRIVATE_KEY_HOST=$OUT(经 volumes 挂到容器内 HUAKAI_AUDIT_PRIVATE_KEY_PATH)"
echo "  同时确保 HUAKAI_AUDIT_LEDGER_BACKEND=postgres(production 强制持久化账本)。"
