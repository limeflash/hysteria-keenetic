#!/bin/sh
# NDM hook: notifies hysteria-manager when a managed OpkgTun interface changes state.
# Keenetic calls this script with env vars: $id, $system_name, $layer, $level.

[ -z "$system_name" ] && exit 0

case "$system_name" in
    OpkgTun*)
        PORT="${HM_PORT:-2230}"
        curl -s -o /dev/null -m 2 \
            "http://127.0.0.1:${PORT}/api/hook/iface-changed?id=${id}&system_name=${system_name}&layer=${layer}&level=${level}" \
            2>/dev/null &
        ;;
esac
