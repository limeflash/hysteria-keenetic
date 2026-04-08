#!/bin/sh

set -e

REPO="limeflash/hysteria-keenetic"
PKG_NAME="hysteria-manager"

info()  { printf "\033[1;32m[+]\033[0m %s\n" "$1"; }
warn()  { printf "\033[1;33m[!]\033[0m %s\n" "$1"; }
error() { printf "\033[1;31m[-]\033[0m %s\n" "$1"; exit 1; }

detect_arch() {
    ARCH=$(opkg print-architecture 2>/dev/null | awk '/_kn/{print $2}' | sed 's/_kn.*//' | head -n1)
    [ -z "$ARCH" ] && error "Не удалось определить архитектуру Entware"
    case "$ARCH" in
        mipsel-3.4|mips-3.4|aarch64-3.10) ;;
        *) error "Архитектура не поддерживается: $ARCH" ;;
    esac
    PKG_ARCH="$ARCH"
}

download_and_install() {
    TMP_DIR="$(mktemp -d)"
    trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

    FILE_NAME="${PKG_NAME}_${PKG_ARCH}.ipk"
    URL="https://github.com/${REPO}/releases/latest/download/${FILE_NAME}"
    info "Скачиваю ${FILE_NAME}"
    curl -fsSL "$URL" -o "${TMP_DIR}/${FILE_NAME}" || error "Не удалось скачать пакет ${URL}"

    info "Устанавливаю пакет"
    opkg install "${TMP_DIR}/${FILE_NAME}" || error "opkg install завершился ошибкой"
}

detect_arch
download_and_install
info "Установка завершена. Панель по умолчанию доступна на http://192.168.1.1:2230"
