#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="${WORK_DIR:-${ROOT_DIR}/build/hysteria-src}"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/dist/hysteria-client}"
TARGET="${1:-mipsel-3.4}"

declare -A GOARCH_MAP=(
  ["mipsel-3.4"]="mipsle"
  ["mips-3.4"]="mips"
  ["aarch64-3.10"]="arm64"
)

declare -A GOMIPS_MAP=(
  ["mipsel-3.4"]="softfloat"
  ["mips-3.4"]="softfloat"
  ["aarch64-3.10"]=""
)

GOARCH_VALUE="${GOARCH_MAP[$TARGET]:-}"
if [[ -z "${GOARCH_VALUE}" ]]; then
  echo "Unsupported target: ${TARGET}" >&2
  exit 1
fi

rm -rf "${WORK_DIR}"
mkdir -p "${WORK_DIR}" "${OUT_DIR}"
git clone --depth 1 https://github.com/apernet/hysteria "${WORK_DIR}"

pushd "${WORK_DIR}" >/dev/null
if [[ -n "${GOMIPS_MAP[$TARGET]}" ]]; then
  GOOS=linux GOARCH="${GOARCH_VALUE}" GOMIPS="${GOMIPS_MAP[$TARGET]}" python3 hyperbole.py build -r
else
  GOOS=linux GOARCH="${GOARCH_VALUE}" python3 hyperbole.py build -r
fi

cp build/hysteria-* "${OUT_DIR}/hysteria-${TARGET}"
popd >/dev/null

echo "Built hysteria client for ${TARGET}: ${OUT_DIR}/hysteria-${TARGET}"
