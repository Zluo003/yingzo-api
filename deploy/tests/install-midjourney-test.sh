#!/usr/bin/env bash
# Exercise the native installer without root privileges, network or real systemd.
# Variables assigned below are also consumed by the dynamically sourced installer.
# shellcheck disable=SC2034
set -euo pipefail
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
work=$(mktemp -d)
trap 'status=$?; if (( status != 0 )); then cat "$work/output" >&2; fi; rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/downloads"
export NATIVE_TEST_STATE="$work/state" NATIVE_TEST_DOWNLOADS="$work/downloads"
export PATH="$work/bin:$PATH"
export MJ_SERVICE_USER=native-test-user
export MJ_GATEWAY_SERVICE=yingzo-api
export MJ_CONFIG_DIR="$work/config with spaces" MJ_SYSTEMD_DIR="$work/systemd"
export MJ_INSTALL_DIR="$work/install with spaces"
mkdir -p "$NATIVE_TEST_STATE" "$MJ_CONFIG_DIR" "$MJ_SYSTEMD_DIR" "$MJ_INSTALL_DIR/midjourney-node/bin" "$MJ_INSTALL_DIR/midjourney-node/lib/node_modules/npm/bin"
touch "$MJ_INSTALL_DIR/midjourney-node/lib/node_modules/npm/bin/npm-cli.js"
cat > "$work/bin/id" <<'STUB'
#!/bin/bash
if [[ "$1" == -u ]]; then echo 0; fi
STUB
cat > "$work/bin/uname" <<'STUB'
#!/bin/bash
case "$1" in -m) echo x86_64 ;; *) echo Linux ;; esac
STUB
cat > "$work/bin/chown" <<'STUB'
#!/bin/bash
exit 0
STUB
cat > "$work/bin/install" <<'STUB'
#!/bin/bash
args=()
while (( $# )); do
    case "$1" in -o|-g) shift 2 ;; *) args+=("$1"); shift ;; esac
done
/usr/bin/install "${args[@]}"
STUB
cat > "$work/bin/runuser" <<'STUB'
#!/bin/bash
set -eu
[[ "$1" == -u && "$2" == native-test-user && "$3" == -- ]]
shift 3
printf '%s\n' "$*" >> "$NATIVE_TEST_STATE/user-calls"
NATIVE_TEST_AS_SERVICE=1 "$@"
STUB
cat > "$MJ_INSTALL_DIR/midjourney-node/bin/node" <<'STUB'
#!/bin/bash
set -eu
if [[ "$1" == -p ]]; then echo 24; exit 0; fi
[[ "${NATIVE_TEST_AS_SERVICE:-}" == 1 ]]
case "$1" in
    -e) exit 0 ;;
    */npm-cli.js)
        [[ "$*" == *'ci --omit=dev --ignore-scripts --no-audit --no-fund'* ]]
        [[ ! -f "$NATIVE_TEST_STATE/npm-fail" ]]
        mkdir -p node_modules
        ;;
    scripts/patch-sdk.js)
        [[ ! -f "$NATIVE_TEST_STATE/patch-fail" ]]
        touch node_modules/patched
        ;;
    *) exit 90 ;;
esac
STUB
cat > "$work/bin/systemctl" <<'STUB'
#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$NATIVE_TEST_STATE/systemctl-calls"
service="${*: -1}"
case "$1" in
    is-active) [[ -f "$NATIVE_TEST_STATE/$service.active" ]] ;;
    is-enabled) [[ -f "$NATIVE_TEST_STATE/$service.enabled" ]] ;;
    enable) touch "$NATIVE_TEST_STATE/$service.enabled" ;;
    disable) rm -f "$NATIVE_TEST_STATE/$service.enabled" ;;
    stop) rm -f "$NATIVE_TEST_STATE/$service.active" ;;
    restart|start)
        [[ ! -f "$NATIVE_TEST_STATE/restart-fail" ]] || exit 1
        touch "$NATIVE_TEST_STATE/$service.active"
        ;;
    daemon-reload) ;;
    *) exit 91 ;;
esac
STUB
cat > "$work/bin/curl" <<'STUB'
#!/bin/bash
set -eu
url="" output=""
while (( $# )); do
    case "$1" in
        -o) output="$2"; shift 2 ;;
        http*) url="$1"; shift ;;
        *) shift ;;
    esac
done
if [[ "$url" == */health ]]; then
    [[ ! -f "$NATIVE_TEST_STATE/bad-health" ]] || exit 22
    echo '{"ok":true}'
else
    cp "$NATIVE_TEST_DOWNLOADS/${url##*/}" "$output"
fi
STUB
cat > "$work/bin/sleep" <<'STUB'
#!/bin/bash
exit 0
STUB
cat > "$MJ_INSTALL_DIR/yingzo-api" <<'STUB'
#!/bin/bash
echo 'yingzo-api v0.3.68'
STUB
chmod +x "$work/bin/"* "$MJ_INSTALL_DIR/midjourney-node/bin/node" "$MJ_INSTALL_DIR/yingzo-api"

run_install() {
    bash "$repo_root/deploy/install-midjourney.sh" "$@" > "$work/output" 2>&1
}
assert_gateway_untouched() {
    if grep -Eq '^(start|restart|stop) yingzo-api$' "$NATIVE_TEST_STATE/systemctl-calls"; then
        echo 'companion installation unexpectedly changed the gateway' >&2
        exit 1
    fi
}

# First install: npm and SDK patch use the service user; only a healthy bridge
# allows restarting the already-running gateway. Paths may contain spaces.
touch "$NATIVE_TEST_STATE/yingzo-api.active"
run_install --source "$repo_root"
test -f "$MJ_INSTALL_DIR/midjourney-discord/node_modules/patched"
grep -Fq 'scripts/patch-sdk.js' "$NATIVE_TEST_STATE/user-calls"
test "$(stat -c '%a' "$MJ_CONFIG_DIR/midjourney.env")" = 600
secret=$(sed -n 's/^MIDJOURNEY_BRIDGE_SECRET=//p' "$MJ_CONFIG_DIR/midjourney.env")
[[ "$secret" =~ ^[0-9a-f]{64}$ ]]
if grep -Fq "$secret" "$work/output"; then echo 'shared secret was printed' >&2; exit 1; fi
grep -Fxq 'restart yingzo-api' "$NATIVE_TEST_STATE/systemctl-calls"
for unit in "$MJ_SYSTEMD_DIR/yingzo-api.service.d/midjourney.conf" "$MJ_SYSTEMD_DIR/yingzo-midjourney-discord.service"; do
    grep -Fxq "EnvironmentFile=$MJ_CONFIG_DIR/midjourney.env" "$unit"
done
grep -Fxq 'Environment=HOST=127.0.0.1' "$MJ_SYSTEMD_DIR/yingzo-midjourney-discord.service"
grep -Fxq "ReadWritePaths=\"$MJ_INSTALL_DIR/data/midjourney-discord\"" "$MJ_SYSTEMD_DIR/yingzo-midjourney-discord.service"
if command -v systemd-analyze >/dev/null 2>&1; then
    unit_errors=$(SYSTEMD_LOG_LEVEL=err systemd-analyze verify "$MJ_SYSTEMD_DIR/yingzo-midjourney-discord.service" 2>&1)
    if [[ -n "$unit_errors" ]]; then echo "$unit_errors" >&2; exit 1; fi
fi

# Reinstall preserves the secret and task data; a stopped gateway stays stopped.
echo persistent-task > "$MJ_INSTALL_DIR/data/midjourney-discord/tasks.sqlite"
cp "$MJ_CONFIG_DIR/midjourney.env" "$work/original.env"
rm "$NATIVE_TEST_STATE/yingzo-api.active"
: > "$NATIVE_TEST_STATE/systemctl-calls"
run_install --source "$repo_root/midjourney-discord"
cmp "$work/original.env" "$MJ_CONFIG_DIR/midjourney.env"
grep -Fxq persistent-task "$MJ_INSTALL_DIR/data/midjourney-discord/tasks.sqlite"
assert_gateway_untouched

# Health or service startup failure restores source AND configuration, while
# never restarting Go. Override the port so config restoration is observable.
echo previous-version > "$MJ_INSTALL_DIR/midjourney-discord/previous-marker"
cp "$MJ_SYSTEMD_DIR/yingzo-midjourney-discord.service" "$work/original.unit"
for failure in bad-health restart-fail npm-fail patch-fail; do
    touch "$NATIVE_TEST_STATE/$failure" "$NATIVE_TEST_STATE/yingzo-api.active"
    : > "$NATIVE_TEST_STATE/systemctl-calls"
    if MJ_BRIDGE_PORT=8099 run_install --source "$repo_root/midjourney-discord"; then
        echo "installation ignored $failure" >&2; exit 1
    fi
    test -f "$MJ_INSTALL_DIR/midjourney-discord/previous-marker"
    cmp "$work/original.env" "$MJ_CONFIG_DIR/midjourney.env"
    cmp "$work/original.unit" "$MJ_SYSTEMD_DIR/yingzo-midjourney-discord.service"
    test -z "$(find "$MJ_INSTALL_DIR" -maxdepth 1 -name '.midjourney-stage.*' -print)"
    assert_gateway_untouched
    rm "$NATIVE_TEST_STATE/$failure"
done

# Download matching v0.3.68 source from a checksummed release; fail closed on a
# corrupted archive before touching either installed service.
archive=yingzo-api_0.3.68_linux_amd64.tar.gz
tar -czf "$NATIVE_TEST_DOWNLOADS/$archive" -C "$repo_root" midjourney-discord/package.json midjourney-discord/package-lock.json midjourney-discord/src midjourney-discord/scripts
(cd "$NATIVE_TEST_DOWNLOADS" && sha256sum "$archive" > checksums.txt)
run_install --version v0.3.68
test -f "$MJ_INSTALL_DIR/midjourney-discord/node_modules/patched"
echo corrupt >> "$NATIVE_TEST_DOWNLOADS/$archive"
: > "$NATIVE_TEST_STATE/systemctl-calls"
if run_install --version v0.3.68; then echo 'accepted corrupted release' >&2; exit 1; fi
test ! -s "$NATIVE_TEST_STATE/systemctl-calls"

# First-install failure also removes the newly created units/config and disables
# the failed companion, leaving Go's service untouched.
rm -rf "$MJ_CONFIG_DIR" "$MJ_SYSTEMD_DIR" "$MJ_INSTALL_DIR/midjourney-discord"
mkdir -p "$MJ_CONFIG_DIR" "$MJ_SYSTEMD_DIR"
rm -f "$NATIVE_TEST_STATE/yingzo-midjourney-discord.active" "$NATIVE_TEST_STATE/yingzo-midjourney-discord.enabled"
touch "$NATIVE_TEST_STATE/bad-health"
: > "$NATIVE_TEST_STATE/systemctl-calls"
if run_install --source "$repo_root/midjourney-discord"; then echo 'ignored first-install failure' >&2; exit 1; fi
test ! -f "$MJ_CONFIG_DIR/midjourney.env"
test ! -f "$MJ_SYSTEMD_DIR/yingzo-api.service.d/midjourney.conf"
test ! -f "$MJ_SYSTEMD_DIR/yingzo-midjourney-discord.service"
test ! -f "$NATIVE_TEST_STATE/yingzo-midjourney-discord.enabled"
assert_gateway_untouched
rm "$NATIVE_TEST_STATE/bad-health"

# Main installer dispatch: no MJ provisioning until explicitly enabled, then
# update it from the release's separate source directory.
# shellcheck disable=SC1090
source <(sed '$d' "$repo_root/deploy/install.sh")
INSTALL_DIR="$MJ_INSTALL_DIR" CONFIG_DIR="$MJ_CONFIG_DIR" SYSTEMD_DIR="$MJ_SYSTEMD_DIR"
SERVICE_USER="$MJ_SERVICE_USER"
mkdir -p "$INSTALL_DIR/midjourney-discord-source"
cat > "$INSTALL_DIR/install-midjourney.sh" <<'STUB'
#!/bin/bash
printf '%s\n' "$@" > "$NATIVE_TEST_STATE/helper-args"
test "$MJ_INSTALL_DIR" = "$INSTALL_DIR_TEST_EXPECTED"
exit "${NATIVE_TEST_HELPER_EXIT:-0}"
STUB
export INSTALL_DIR_TEST_EXPECTED="$INSTALL_DIR"
prepare_midjourney_runtime
test ! -f "$NATIVE_TEST_STATE/helper-args"
touch "$SYSTEMD_DIR/yingzo-midjourney-discord.service" "$NATIVE_TEST_STATE/yingzo-midjourney-discord.enabled"
prepare_midjourney_runtime
grep -Fxq -- --source "$NATIVE_TEST_STATE/helper-args"
grep -Fxq "$INSTALL_DIR/midjourney-discord-source" "$NATIVE_TEST_STATE/helper-args"
install_midjourney v0.3.68
grep -Fxq -- --version "$NATIVE_TEST_STATE/helper-args"

# Extracting a release replaces only staged sources; live code is retained until
# the companion installer succeeds. Both upgrade entry points recover Go when
# companion provisioning fails instead of leaving the gateway stopped.
mkdir -p "$work/release/deploy" "$work/release/midjourney-discord"
cp "$INSTALL_DIR/install-midjourney.sh" "$work/release/deploy/"
cp "$INSTALL_DIR/yingzo-api" "$work/release/yingzo-api"
echo new-release-source > "$work/release/midjourney-discord/package.json"
mkdir -p "$INSTALL_DIR/midjourney-discord"
echo live-source > "$INSTALL_DIR/midjourney-discord/live-marker"
OS=linux ARCH=amd64 LATEST_VERSION=v0.3.69
for version in 0.3.69 0.3.70; do
    tar -czf "$NATIVE_TEST_DOWNLOADS/yingzo-api_${version}_linux_amd64.tar.gz" -C "$work/release" .
done
(cd "$NATIVE_TEST_DOWNLOADS" && sha256sum yingzo-api_0.3.69_linux_amd64.tar.gz yingzo-api_0.3.70_linux_amd64.tar.gz > checksums.txt)
(download_and_extract > /dev/null)
grep -Fxq new-release-source "$INSTALL_DIR/midjourney-discord-source/package.json"
grep -Fxq live-source "$INSTALL_DIR/midjourney-discord/live-marker"
get_latest_version() { LATEST_VERSION=v0.3.69; }
validate_version() { echo "$1"; }
prepare_media_runtime() { :; }
for operation in upgrade install_version start_service; do
    : > "$NATIVE_TEST_STATE/systemctl-calls"
    if (export NATIVE_TEST_HELPER_EXIT=1; "$operation" v0.3.70 > /dev/null); then
        echo "$operation ignored companion failure" >&2; exit 1
    fi
    grep -Fxq 'start yingzo-api' "$NATIVE_TEST_STATE/systemctl-calls"
done

# Preserve root-only permissions on the shared secret when resetting gateway
# directories, and clean both service units on uninstall while retaining config.
echo retained-secret > "$CONFIG_DIR/midjourney.env"
chmod 0644 "$CONFIG_DIR/midjourney.env"
setup_directories >/dev/null
test "$(stat -c '%a' "$CONFIG_DIR/midjourney.env")" = 600
is_interactive() { return 1; }
userdel() { :; }
FORCE_YES=true PURGE=false
uninstall > "$work/uninstall-output"
test ! -f "$SYSTEMD_DIR/yingzo-midjourney-discord.service"
test ! -f "$SYSTEMD_DIR/yingzo-api.service.d/midjourney.conf"
grep -Fxq retained-secret "$CONFIG_DIR/midjourney.env"
echo 'native Midjourney installer tests passed'
