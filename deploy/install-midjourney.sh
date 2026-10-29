#!/usr/bin/env bash
# Install the optional Discord bridge beside a native yingzo-api systemd service.
# Can be downloaded and run separately for releases whose main installer did not
# install companion services. Discord user tokens remain in the gateway DB.
set -euo pipefail

MJ_INSTALL_DIR="${MJ_INSTALL_DIR:-/opt/yingzo-api}"
MJ_CONFIG_DIR="${MJ_CONFIG_DIR:-/etc/yingzo-api}"
MJ_SYSTEMD_DIR="${MJ_SYSTEMD_DIR:-/etc/systemd/system}"
MJ_SERVICE_USER="${MJ_SERVICE_USER:-yingzo-api}"
MJ_GATEWAY_SERVICE="${MJ_GATEWAY_SERVICE:-yingzo-api}"
MJ_BRIDGE_SERVICE=yingzo-midjourney-discord
MJ_GITHUB_REPO="${GITHUB_REPO:-Zluo003/yingzo-api}"
MJ_NODE_VERSION="${MJ_NODE_VERSION:-v24.22.2}"
MJ_BRIDGE_PORT="${MJ_BRIDGE_PORT:-}"
MJ_WORK_DIR=""
MJ_STAGE=""
MJ_ROLLBACK_PENDING=false
MJ_SOURCE_SWAPPED=false
MJ_BRIDGE_WAS_ACTIVE=false
MJ_BRIDGE_WAS_ENABLED=false

mj_cleanup() {
    local status=$?
    if [[ "$MJ_ROLLBACK_PENDING" == true ]]; then
        mj_restore_previous || echo 'Automatic recovery failed; inspect the Discord service before retrying.' >&2
    fi
    [[ -z "$MJ_STAGE" ]] || rm -rf -- "$MJ_STAGE"
    [[ -z "$MJ_WORK_DIR" ]] || rm -rf -- "$MJ_WORK_DIR"
    return "$status"
}

mj_help() {
    cat <<'HELP'
Usage: sudo bash install-midjourney.sh [--version v0.3.68] [--source /path/to/midjourney-discord]

Installs the Discord service for an existing native yingzo-api installation.
Without --source, downloads the companion files from the installed gateway's
release and verifies its checksum. Installs a private Node.js 24 runtime if
needed, preserves the generated shared secret, configures systemd, and restarts
an already-running gateway only after the bridge health check succeeds.

Data: /opt/yingzo-api/data/midjourney-discord
Shared configuration: /etc/yingzo-api/midjourney.env (root only)
Service: yingzo-midjourney-discord.service, listening on loopback only
Port: preserves the configured port, otherwise tries 8081 then 18081-18100.
Set MJ_BRIDGE_PORT explicitly to use a specific port (must be available).
HELP
}

mj_download() {
    curl -q --fail --location --silent --show-error --retry 2 \
        --connect-timeout 10 --max-time 180 "$1" -o "$2"
}

mj_verify_checksum() {
    local expected actual
    expected=$(awk -v filename="$(basename "$1")" '$2 == filename {print $1}' "$2")
    actual=$(sha256sum "$1" | awk '{print $1}')
    if [[ ! "$expected" =~ ^[0-9a-fA-F]{64}$ ]] || [[ "$expected" != "$actual" ]]; then
        echo "Checksum verification failed: $(basename "$1")" >&2
        return 1
    fi
}

mj_fetch_source() {
    local version="$1" release_arch archive release_url
    case "$(uname -m)" in
        x86_64) release_arch=amd64 ;;
        aarch64|arm64) release_arch=arm64 ;;
        *) echo 'Only Linux amd64 and arm64 are supported.' >&2; return 1 ;;
    esac
    [[ "$version" == v* ]] || version="v$version"
    [[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid gateway release version.' >&2; return 1; }
    archive="yingzo-api_${version#v}_linux_${release_arch}.tar.gz"
    release_url="https://github.com/${MJ_GITHUB_REPO}/releases/download/${version}"
    echo "Downloading Discord companion files from $version..."
    mj_download "$release_url/$archive" "$MJ_WORK_DIR/$archive"
    mj_download "$release_url/checksums.txt" "$MJ_WORK_DIR/checksums.txt"
    mj_verify_checksum "$MJ_WORK_DIR/$archive" "$MJ_WORK_DIR/checksums.txt"
    tar -xzf "$MJ_WORK_DIR/$archive" -C "$MJ_WORK_DIR" midjourney-discord
    MJ_SOURCE_DIR="$MJ_WORK_DIR/midjourney-discord"
}

mj_prepare_node() {
    local node_major node_arch node_archive node_parent candidate
    MJ_NODE_BIN=""
    for candidate in "$MJ_INSTALL_DIR/midjourney-node/bin/node" /usr/bin/node /usr/local/bin/node; do
        [[ -x "$candidate" ]] || continue
        candidate=$(readlink -f "$candidate")
        # ProtectHome=true hides root/nvm and other home-local installations.
        case "$candidate" in /root/*|/home/*) continue ;; esac
        node_major=$("$candidate" -p 'Number(process.versions.node.split(".")[0])')
        node_parent=$(cd "$(dirname "$candidate")/.." && pwd)
        if [[ "$node_major" =~ ^[0-9]+$ ]] && (( node_major >= 24 )) && [[ -f "$node_parent/lib/node_modules/npm/bin/npm-cli.js" ]]; then
            MJ_NODE_BIN="$candidate"
            break
        fi
    done
    if [[ -z "$MJ_NODE_BIN" ]]; then
        [[ "$MJ_NODE_VERSION" =~ ^v24\.[0-9]+\.[0-9]+$ ]] || { echo 'MJ_NODE_VERSION must be an explicit Node 24 release.' >&2; return 1; }
        case "$(uname -m)" in x86_64) node_arch=x64 ;; aarch64|arm64) node_arch=arm64 ;; *) return 1 ;; esac
        node_archive="node-${MJ_NODE_VERSION}-linux-${node_arch}.tar.xz"
        echo "Installing isolated Node.js $MJ_NODE_VERSION runtime..."
        mj_download "https://nodejs.org/dist/${MJ_NODE_VERSION}/$node_archive" "$MJ_WORK_DIR/$node_archive"
        mj_download "https://nodejs.org/dist/${MJ_NODE_VERSION}/SHASUMS256.txt" "$MJ_WORK_DIR/SHASUMS256.txt"
        mj_verify_checksum "$MJ_WORK_DIR/$node_archive" "$MJ_WORK_DIR/SHASUMS256.txt"
        mkdir -p "$MJ_WORK_DIR/node"
        tar -xJf "$MJ_WORK_DIR/$node_archive" -C "$MJ_WORK_DIR/node" --strip-components=1
        install -d -m 0755 "$MJ_INSTALL_DIR/midjourney-node"
        cp -R "$MJ_WORK_DIR/node/." "$MJ_INSTALL_DIR/midjourney-node/"
        MJ_NODE_BIN="$MJ_INSTALL_DIR/midjourney-node/bin/node"
    fi
    # Use the npm shipped with the selected Node runtime. Invoke npm and the
    # repository-owned SDK patch explicitly so install scripts never run as root.
    node_parent=$(cd "$(dirname "$MJ_NODE_BIN")/.." && pwd)
    MJ_NPM_CLI="$node_parent/lib/node_modules/npm/bin/npm-cli.js"
    if [[ ! -f "$MJ_NPM_CLI" ]]; then
        echo 'The selected Node.js runtime is missing npm.' >&2
        return 1
    fi
    mj_as_service_user "$MJ_NODE_BIN" -e 'require("node:sqlite").DatabaseSync' >/dev/null
}

mj_prepare_source() {
    local source_dir="$1" stage="$2" required
    for required in package.json package-lock.json src/server.js src/bridge.js src/client.js scripts/patch-sdk.js; do
        [[ -f "$source_dir/$required" ]] || { echo "Missing Discord companion file: $required" >&2; return 1; }
    done
    install -d -m 0755 "$stage/src" "$stage/scripts"
    install -m 0644 "$source_dir/package.json" "$source_dir/package-lock.json" "$stage/"
    install -m 0644 "$source_dir/src/"*.js "$stage/src/"
    install -m 0644 "$source_dir/scripts/patch-sdk.js" "$stage/scripts/patch-sdk.js"
}

mj_valid_port() {
    [[ "$1" =~ ^[1-9][0-9]{3,4}$ ]] && (( $1 >= 1024 && $1 <= 65535 ))
}

mj_port_available() {
    mj_as_service_user "$MJ_NODE_BIN" -e '
const server = require("node:net").createServer();
server.once("error", error => {
    if (error.code !== "EADDRINUSE") console.error("Cannot check local port:", error.code);
    process.exit(error.code === "EADDRINUSE" ? 1 : 2);
});
server.listen(Number(process.argv[1]), "127.0.0.1", () => server.close());
' "$1"
}

mj_choose_port() {
    local configured_url="" configured_port="" candidate status
    local candidates=()
    if [[ -f "$MJ_CONFIG_DIR/midjourney.env" ]]; then
        configured_url=$(sed -n 's/^MIDJOURNEY_BRIDGE_URL=//p' "$MJ_CONFIG_DIR/midjourney.env")
        if [[ "$configured_url" =~ ^http://127\.0\.0\.1:([0-9]+)$ ]]; then
            configured_port="${BASH_REMATCH[1]}"
        fi
    fi
    if [[ -n "$MJ_BRIDGE_PORT" ]]; then
        if ! mj_valid_port "$MJ_BRIDGE_PORT"; then echo 'Invalid bridge port.' >&2; return 1; fi
        candidates=("$MJ_BRIDGE_PORT")
    else
        if mj_valid_port "$configured_port"; then candidates+=("$configured_port"); fi
        candidates+=(8081 {18081..18100})
    fi
    for candidate in "${candidates[@]}"; do
        # A running installation already owns its port. Keep it during upgrades
        # instead of treating the existing MJ listener as a port conflict.
        if [[ "$MJ_BRIDGE_WAS_ACTIVE" == true && "$candidate" == "$configured_port" ]]; then
            MJ_BRIDGE_PORT="$candidate"
            return 0
        fi
        if mj_port_available "$candidate"; then
            MJ_BRIDGE_PORT="$candidate"
            echo "Using Discord service port 127.0.0.1:$candidate."
            return 0
        else
            status=$?
            if (( status != 1 )); then return "$status"; fi
        fi
    done
    echo 'No available Discord service port. Set MJ_BRIDGE_PORT to a free port and retry.' >&2
    return 1
}

mj_write_configuration() {
    local env_file="$MJ_CONFIG_DIR/midjourney.env" shared_secret env_tmp
    if ! mj_valid_port "$MJ_BRIDGE_PORT"; then
        echo 'Invalid bridge port.' >&2
        return 1
    fi
    install -d -m 0755 "$MJ_CONFIG_DIR"
    if [[ -f "$env_file" ]]; then
        shared_secret=$(sed -n 's/^MIDJOURNEY_BRIDGE_SECRET=//p' "$env_file")
        if [[ ! "$shared_secret" =~ ^[A-Za-z0-9_+=/.-]{32,}$ ]]; then
            echo "Existing shared secret is invalid; correct $env_file rather than silently rotating it." >&2
            return 1
        fi
    else
        shared_secret=$(openssl rand -hex 32)
    fi
    env_tmp=$(mktemp "$MJ_CONFIG_DIR/.midjourney.env.XXXXXX")
    chmod 0600 "$env_tmp"
    printf 'MIDJOURNEY_BRIDGE_URL=http://127.0.0.1:%s\nMIDJOURNEY_BRIDGE_SECRET=%s\n' "$MJ_BRIDGE_PORT" "$shared_secret" > "$env_tmp"
    chown root:root "$env_tmp"
    mv -f "$env_tmp" "$env_file"

    install -d -m 0755 "$MJ_SYSTEMD_DIR/$MJ_GATEWAY_SERVICE.service.d"
    cat > "$MJ_SYSTEMD_DIR/$MJ_GATEWAY_SERVICE.service.d/midjourney.conf" <<UNIT
[Unit]
Wants=$MJ_BRIDGE_SERVICE.service
After=$MJ_BRIDGE_SERVICE.service

[Service]
EnvironmentFile=$env_file
UNIT
    cat > "$MJ_SYSTEMD_DIR/$MJ_BRIDGE_SERVICE.service" <<UNIT
[Unit]
Description=Yingzo Midjourney Discord bridge
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$MJ_SERVICE_USER
Group=$MJ_SERVICE_USER
WorkingDirectory=$MJ_INSTALL_DIR/midjourney-discord
ExecStart="$MJ_NODE_BIN" "$MJ_INSTALL_DIR/midjourney-discord/src/server.js"
EnvironmentFile=$env_file
Environment=HOST=127.0.0.1
Environment=PORT=$MJ_BRIDGE_PORT
Environment="MIDJOURNEY_DATA_FILE=$MJ_INSTALL_DIR/data/midjourney-discord/tasks.sqlite"
Restart=on-failure
RestartSec=5
TimeoutStopSec=30
UMask=0077
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths="$MJ_INSTALL_DIR/data/midjourney-discord"

[Install]
WantedBy=multi-user.target
UNIT
    chmod 0644 "$MJ_SYSTEMD_DIR/$MJ_GATEWAY_SERVICE.service.d/midjourney.conf" "$MJ_SYSTEMD_DIR/$MJ_BRIDGE_SERVICE.service"
}

mj_as_service_user() {
    runuser -u "$MJ_SERVICE_USER" -- "$@"
}

mj_save_configuration() {
    local file index=0
    for file in "$MJ_CONFIG_DIR/midjourney.env" "$MJ_SYSTEMD_DIR/$MJ_BRIDGE_SERVICE.service" "$MJ_SYSTEMD_DIR/$MJ_GATEWAY_SERVICE.service.d/midjourney.conf"; do
        if [[ -f "$file" ]]; then cp -p "$file" "$MJ_WORK_DIR/config-$index"; fi
        index=$((index + 1))
    done
    if systemctl is-active --quiet "$MJ_BRIDGE_SERVICE" 2>/dev/null; then MJ_BRIDGE_WAS_ACTIVE=true; fi
    if systemctl is-enabled --quiet "$MJ_BRIDGE_SERVICE" 2>/dev/null; then MJ_BRIDGE_WAS_ENABLED=true; fi
}

mj_restore_previous() {
    local file index=0
    systemctl stop "$MJ_BRIDGE_SERVICE" || true
    if [[ "$MJ_SOURCE_SWAPPED" == true ]]; then
        rm -rf -- "$MJ_INSTALL_DIR/midjourney-discord"
        if [[ -d "$MJ_INSTALL_DIR/midjourney-discord.previous" ]]; then
            mv "$MJ_INSTALL_DIR/midjourney-discord.previous" "$MJ_INSTALL_DIR/midjourney-discord"
        fi
    fi
    # Disable while the unit still exists, then restore its previous files.
    if [[ "$MJ_BRIDGE_WAS_ENABLED" != true ]]; then systemctl disable "$MJ_BRIDGE_SERVICE" || true; fi
    for file in "$MJ_CONFIG_DIR/midjourney.env" "$MJ_SYSTEMD_DIR/$MJ_BRIDGE_SERVICE.service" "$MJ_SYSTEMD_DIR/$MJ_GATEWAY_SERVICE.service.d/midjourney.conf"; do
        if [[ -f "$MJ_WORK_DIR/config-$index" ]]; then
            cp -p "$MJ_WORK_DIR/config-$index" "$file"
        else
            rm -f "$file"
        fi
        index=$((index + 1))
    done
    systemctl daemon-reload
    if [[ "$MJ_BRIDGE_WAS_ACTIVE" == true ]]; then systemctl restart "$MJ_BRIDGE_SERVICE"; fi
}

mj_main() {
    local source_arg="" version="" previous gateway_active=false healthy=false attempt value command
    while (( $# )); do
        case "$1" in
            --source|--version)
                [[ $# -ge 2 && -n "$2" ]] || { mj_help; return 1; }
                if [[ "$1" == --source ]]; then source_arg="$2"; else version="$2"; fi
                shift 2 ;;
            --help|-h) mj_help; return 0 ;;
            *) mj_help; return 1 ;;
        esac
    done
    [[ "$(uname -s)" == Linux && "$(id -u)" == 0 ]] || { echo 'Run this script with sudo on the gateway Linux server.' >&2; return 1; }
    for value in "$MJ_INSTALL_DIR" "$MJ_CONFIG_DIR" "$MJ_SYSTEMD_DIR"; do
        [[ "$value" =~ ^/[A-Za-z0-9_./\ -]+$ ]] || { echo 'Installation paths must be absolute and contain only letters, digits, spaces, /, ., _, or -.' >&2; return 1; }
    done
    [[ "$MJ_SERVICE_USER" =~ ^[a-z_][a-z0-9_-]*$ && "$MJ_GATEWAY_SERVICE" =~ ^[A-Za-z0-9_-]+$ ]] || { echo 'Invalid service user or gateway service name.' >&2; return 1; }
    [[ -x "$MJ_INSTALL_DIR/yingzo-api" ]] || { echo 'Install the native yingzo-api gateway first.' >&2; return 1; }
    for command in curl sha256sum tar xz openssl runuser systemctl flock; do
        command -v "$command" >/dev/null 2>&1 || { echo "Required command is missing: $command" >&2; return 1; }
    done
    id "$MJ_SERVICE_USER" >/dev/null
    install -d -m 0755 "$MJ_CONFIG_DIR"
    exec 9>"$MJ_CONFIG_DIR/.midjourney-install.lock"
    flock -n 9 || { echo 'Another Discord service installation is in progress.' >&2; return 1; }
    MJ_WORK_DIR=$(mktemp -d)
    trap mj_cleanup EXIT
    if [[ -n "$source_arg" ]]; then
        MJ_SOURCE_DIR="$source_arg"
        if [[ -d "$MJ_SOURCE_DIR/midjourney-discord" ]]; then MJ_SOURCE_DIR="$MJ_SOURCE_DIR/midjourney-discord"; fi
        MJ_SOURCE_DIR=$(cd "$MJ_SOURCE_DIR" && pwd)
    else
        if [[ -z "$version" ]]; then
            version=$("$MJ_INSTALL_DIR/yingzo-api" --version | grep -oE 'v?[0-9]+\.[0-9]+\.[0-9]+' | head -1)
        fi
        mj_fetch_source "$version"
    fi
    mj_prepare_node
    MJ_STAGE=$(mktemp -d "$MJ_INSTALL_DIR/.midjourney-stage.XXXXXX")
    mj_prepare_source "$MJ_SOURCE_DIR" "$MJ_STAGE"
    chown -R "$MJ_SERVICE_USER:$MJ_SERVICE_USER" "$MJ_STAGE"
    echo 'Installing locked npm dependencies as the gateway service user...'
    (
        cd "$MJ_STAGE"
        mj_as_service_user "$MJ_NODE_BIN" "$MJ_NPM_CLI" ci --omit=dev --ignore-scripts --no-audit --no-fund --cache "$MJ_STAGE/.npm-cache" || exit 1
        mj_as_service_user "$MJ_NODE_BIN" scripts/patch-sdk.js
    ) || return 1
    rm -rf -- "$MJ_STAGE/.npm-cache"
    if systemctl is-active --quiet "$MJ_GATEWAY_SERVICE"; then gateway_active=true; fi
    mj_save_configuration
    mj_choose_port
    MJ_ROLLBACK_PENDING=true
    mj_write_configuration
    install -d -m 0700 -o "$MJ_SERVICE_USER" -g "$MJ_SERVICE_USER" "$MJ_INSTALL_DIR/data/midjourney-discord"
    previous="$MJ_INSTALL_DIR/midjourney-discord.previous"
    # Keep the prior companion source/dependencies for rollback; runtime state
    # and the generated shared secret live outside these replaceable files.
    rm -rf -- "$previous"
    if [[ -d "$MJ_INSTALL_DIR/midjourney-discord" ]]; then mv "$MJ_INSTALL_DIR/midjourney-discord" "$previous"; fi
    MJ_SOURCE_SWAPPED=true
    mv "$MJ_STAGE" "$MJ_INSTALL_DIR/midjourney-discord"
    systemctl daemon-reload
    systemctl enable "$MJ_BRIDGE_SERVICE"
    systemctl restart "$MJ_BRIDGE_SERVICE"
    for (( attempt=0; attempt<30; attempt++ )); do
        if systemctl is-active --quiet "$MJ_BRIDGE_SERVICE" && [[ "$(curl -q --noproxy '*' --fail --silent --max-time 2 "http://127.0.0.1:$MJ_BRIDGE_PORT/health")" == '{"ok":true}' ]]; then
            healthy=true
            break
        fi
        sleep 1
    done
    if [[ "$healthy" != true ]]; then
        echo "Discord service health check failed. Gateway was not restarted. Inspect: journalctl -u $MJ_BRIDGE_SERVICE -n 50" >&2
        return 1
    fi
    MJ_ROLLBACK_PENDING=false
    if [[ "$gateway_active" == true ]]; then systemctl restart "$MJ_GATEWAY_SERVICE"; fi
    echo "Discord service ready at 127.0.0.1:$MJ_BRIDGE_PORT. Shared secret is stored in $MJ_CONFIG_DIR/midjourney.env (not printed)."
    echo 'In the admin panel, run the MJ account connection test again.'
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    mj_main "$@"
fi
