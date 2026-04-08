#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${ROOT_DIR}/dist"
VERSION="${VERSION:-0.1.1}"
GO_BIN="${GO_BIN:-go}"
HYSTERIA_BIN_DIR="${HYSTERIA_BIN_DIR:-${DIST_DIR}/hysteria-client}"

declare -A GOARCH_MAP=(
  ["mipsel-3.4"]="mipsle"
  ["mips-3.4"]="mips"
  ["aarch64-3.10"]="arm64"
)

mkdir -p "${DIST_DIR}" "${HYSTERIA_BIN_DIR}"

build_manager() {
  local entware_arch="$1"
  local goarch="$2"
  local gomips=""
  local out_dir="${DIST_DIR}/package-${entware_arch}"
  local data_dir="${out_dir}/data"
  local control_dir="${out_dir}/control"

  rm -rf "${out_dir}"
  mkdir -p "${data_dir}/opt/bin" \
           "${data_dir}/opt/etc/init.d" \
           "${data_dir}/opt/etc/hysteria-manager/profiles" \
           "${data_dir}/opt/var/log/hysteria-manager" \
           "${control_dir}"

  if [[ "${goarch}" == "mips" || "${goarch}" == "mipsle" ]]; then
    gomips="softfloat"
    GOOS=linux GOARCH="${goarch}" GOMIPS="${gomips}" "${GO_BIN}" build -o "${data_dir}/opt/bin/hysteria-manager" "${ROOT_DIR}"
  else
    GOOS=linux GOARCH="${goarch}" "${GO_BIN}" build -o "${data_dir}/opt/bin/hysteria-manager" "${ROOT_DIR}"
  fi

  cp "${ROOT_DIR}/packaging/init.d/S99hysteria-manager" "${data_dir}/opt/etc/init.d/S99hysteria-manager"
  cp "${ROOT_DIR}/packaging/control/postinst" "${control_dir}/postinst"
  cp "${ROOT_DIR}/packaging/control/prerm" "${control_dir}/prerm"
  sed \
    -e "s/@VERSION@/${VERSION}/g" \
    -e "s/@ARCH@/${entware_arch}/g" \
    "${ROOT_DIR}/packaging/control/control.template" > "${control_dir}/control"

  if [[ -f "${HYSTERIA_BIN_DIR}/hysteria-${entware_arch}" ]]; then
    cp "${HYSTERIA_BIN_DIR}/hysteria-${entware_arch}" "${data_dir}/opt/bin/hysteria"
  fi

  (
    cd "${out_dir}"
    echo "2.0" > debian-binary
    tar --numeric-owner --owner=0 --group=0 -czf control.tar.gz -C control .
    tar --numeric-owner --owner=0 --group=0 -czf data.tar.gz -C data .
    tar --numeric-owner --owner=0 --group=0 -czf "${DIST_DIR}/hysteria-manager_${entware_arch}.ipk" ./debian-binary ./control.tar.gz ./data.tar.gz
  )
}

for entware_arch in "${!GOARCH_MAP[@]}"; do
  build_manager "${entware_arch}" "${GOARCH_MAP[$entware_arch]}"
done

echo "Built release artifacts into ${DIST_DIR}"
