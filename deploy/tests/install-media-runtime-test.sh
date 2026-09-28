#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
source <(sed '$d' "$repo_root/deploy/install.sh")
INSTALL_DIR="$work/install with spaces"
mkdir -p "$INSTALL_DIR"
SERVICE_USER=runtime-test-user
export RUNTIME_TEST_LOG="$work/calls"

# Both paths must execute the new binary as the service user, not installer root.
runuser() {
    test "$1" = -u && test "$2" = "$SERVICE_USER" && test "$3" = --
    shift 3
    "$@"
}
cat > "$INSTALL_DIR/yingzo-api" <<'EOF'
#!/bin/sh
case "$1" in
  --help) echo '  -prepare-runtime'; exit 0 ;;
  --prepare-runtime) echo prepared >> "$RUNTIME_TEST_LOG"; exit "${RUNTIME_TEST_EXIT:-0}" ;;
  *) exit 1 ;;
esac
EOF
chmod +x "$INSTALL_DIR/yingzo-api"
prepare_media_runtime
test "$(cat "$RUNTIME_TEST_LOG")" = prepared

export RUNTIME_TEST_EXIT=1
if prepare_media_runtime; then
    echo 'installer ignored runtime preparation failure' >&2
    exit 1
fi
unset RUNTIME_TEST_EXIT

# Old versions remain installable and do not receive unknown flags.
cat > "$INSTALL_DIR/yingzo-api" <<'EOF'
#!/bin/sh
case "$1" in
  --help) echo '  -version'; exit 0 ;;
  *) echo unexpected >> "$RUNTIME_TEST_LOG"; exit 1 ;;
esac
EOF
before=$(cat "$RUNTIME_TEST_LOG")
prepare_media_runtime
test "$(cat "$RUNTIME_TEST_LOG")" = "$before"
echo 'installer media runtime tests passed'
