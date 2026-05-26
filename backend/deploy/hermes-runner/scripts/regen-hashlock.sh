#!/usr/bin/env bash
# 重新生成 hermes-runner 的完整传递哈希锁。
#
# 用途: 把 requirements.in (直接依赖) 经 pip-compile 解析成
# requirements.txt (含全部传递依赖 + sha256 哈希),供 Docker
# 阶段以 pip install --require-hashes 强校验。
#
# 运行环境要求:
#   - Python 3.12 (与 Dockerfile FROM python:3.12-slim 对齐)
#   - 可达 PyPI 网络
#   - pip-tools (脚本会自动 pip install --user)
#
# 沙箱不可跑: Claude 工作沙箱无 PyPI 出站,也无 pip-tools 模块。
# 必须在 Owner 本机 / CI runner 执行。
#
# 使用:
#   cd backend/deploy/hermes-runner
#   ./scripts/regen-hashlock.sh
#
# 完成后人工 diff requirements.txt,确认所有包都带 --hash=sha256:...
# 行,然后 git add + commit。

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNNER_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
IN_FILE="${RUNNER_DIR}/requirements.in"
OUT_FILE="${RUNNER_DIR}/requirements.txt"

if [[ ! -f "${IN_FILE}" ]]; then
    echo "error: ${IN_FILE} not found" >&2
    exit 1
fi

PYTHON_BIN="${PYTHON_BIN:-python3.12}"
if ! command -v "${PYTHON_BIN}" >/dev/null 2>&1; then
    echo "error: ${PYTHON_BIN} not on PATH; install Python 3.12 or set PYTHON_BIN" >&2
    exit 1
fi

PY_VERSION="$("${PYTHON_BIN}" -c 'import sys; print(".".join(map(str, sys.version_info[:2])))')"
if [[ "${PY_VERSION}" != "3.12" ]]; then
    echo "error: ${PYTHON_BIN} reports ${PY_VERSION}, need 3.12 to match Dockerfile" >&2
    exit 1
fi

echo "==> using ${PYTHON_BIN} (Python ${PY_VERSION})"

if ! "${PYTHON_BIN}" -m piptools --version >/dev/null 2>&1; then
    echo "==> installing pip-tools into user site"
    "${PYTHON_BIN}" -m pip install --user --upgrade pip-tools
fi

echo "==> regenerating ${OUT_FILE}"
"${PYTHON_BIN}" -m piptools compile \
    --generate-hashes \
    --resolver=backtracking \
    --output-file="${OUT_FILE}" \
    "${IN_FILE}"

echo
echo "==> done. inspect changes:"
echo "    git diff ${OUT_FILE#${RUNNER_DIR}/}"
echo
echo "==> sanity counts:"
grep -c '^[a-zA-Z0-9]' "${OUT_FILE}" | awk '{print "  pinned package lines: " $1}'
grep -c '\-\-hash=sha256:' "${OUT_FILE}" | awk '{print "  --hash lines:         " $1}'
