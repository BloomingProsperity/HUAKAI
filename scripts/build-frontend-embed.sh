#!/usr/bin/env bash
# 构建前端 SPA 并拷进 go:embed 落点,供 `go build -tags embed` 把单页应用打进网关二进制。
#
# 背景:backend/internal/webui 已有 embed 脚手架(embed_on.go 的 //go:embed all:dist),
# 且网关已挂载 webui.Handler(middleware.go);但 webui/dist 里只有占位 index.html,
# 真正的 React 产物需要本脚本在构建期生成并覆盖进去。仓库 .gitignore 忽略 dist 下的
# 生成资产(只提交占位 index.html),故本脚本产物是构建期临时态,不应提交。
#
# 用法:在仓库根目录执行 `scripts/build-frontend-embed.sh`,随后即可 `go build -tags embed`。
set -euo pipefail

# 解析仓库根目录(脚本位于 <root>/scripts/ 下)。
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

FRONTEND_DIR="${ROOT_DIR}/frontend"
DIST_SRC="${FRONTEND_DIR}/dist"
EMBED_DST="${ROOT_DIR}/backend/internal/webui/dist"

echo "[embed] 安装前端依赖并构建 (vite build)…"
cd "${FRONTEND_DIR}"
if [ -f package-lock.json ]; then
  npm ci
else
  npm install
fi
npm run build

if [ ! -f "${DIST_SRC}/index.html" ]; then
  echo "[embed] 错误:未找到 ${DIST_SRC}/index.html,前端构建可能失败" >&2
  exit 1
fi

echo "[embed] 拷贝前端产物到 go:embed 落点 ${EMBED_DST}…"
# 清空旧产物(含占位 index.html),整体替换为真实构建结果。
rm -rf "${EMBED_DST}"
mkdir -p "${EMBED_DST}"
cp -R "${DIST_SRC}/." "${EMBED_DST}/"

echo "[embed] 完成。现在可执行:cd backend && go build -tags embed ./cmd/gateway"
