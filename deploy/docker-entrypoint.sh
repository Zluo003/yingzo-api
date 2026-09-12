#!/bin/sh
set -e

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root sub2api user from writing files.
if [ "$(id -u)" = "0" ]; then
    mkdir -p /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R sub2api:sub2api /app/data 2>/dev/null || true
    # 素材目录可能按“同路径”挂在 /app/data 之外（AGENT_ASSETS_HOST_DIR=/data/xxx）：
    # 只把挂载根目录交给 sub2api，不递归——里面的素材都是 sub2api 自己创建的，
    # 这样既免去手工 chown，也不会因为素材库很大而拖慢启动。
    if [ -n "${AGENT_ASSETS_HOST_DIR:-}" ] && [ -d "${AGENT_ASSETS_HOST_DIR}" ]; then
        chown sub2api:sub2api "${AGENT_ASSETS_HOST_DIR}" 2>/dev/null || true
    fi
    # Re-invoke this script as sub2api so the flag-detection below
    # also runs under the correct user.
    exec su-exec sub2api "$0" "$@"
fi

# Compatibility: if the first arg looks like a flag (e.g. --help),
# prepend the default binary so it behaves the same as the old
# ENTRYPOINT ["/app/sub2api"] style.
if [ "${1#-}" != "$1" ]; then
    set -- /app/sub2api "$@"
fi

exec "$@"
