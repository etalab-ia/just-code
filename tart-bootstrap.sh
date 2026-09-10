#!/usr/bin/env sh
# OpenCode bootstrap script inside Tart macOS guest VM
set -eu

PORT="${1:-4096}"
USERNAME="${2:-opencode}"
TART_MTU="${3-1280}"

# Include macOS network tools in the non-login guest agent environment.
export PATH="$PATH:/usr/sbin:/sbin"
if [ "$TART_MTU" != auto ]; then
    case "$TART_MTU" in
        1[234][0-9][0-9]|1500)
            if [ "$TART_MTU" -lt 1280 ]; then
                echo "TART_MTU must be auto or an integer from 1280 to 1500." >&2
                exit 1
            fi
            ;;
        *)
            echo "TART_MTU must be auto or an integer from 1280 to 1500." >&2
            exit 1
            ;;
    esac
    interface=$(route -n get default | awk '$1 == "interface:" { print $2; exit }')
    if [ -z "$interface" ]; then
        echo "Cannot determine the guest default network interface for TART_MTU." >&2
        exit 1
    fi
    if ! sudo -n ifconfig "$interface" mtu "$TART_MTU"; then
        echo "Cannot apply TART_MTU=$TART_MTU to $interface (passwordless sudo required)." >&2
        exit 1
    fi
    echo "Guest network: $interface MTU=$TART_MTU"
fi

# Read secrets from stdin: server password on line 1, API key on line 2.
if IFS= read -r PASSWORD; then
    : # password provided on stdin (an empty line means no auth)
else
    PASSWORD="albert-dev-pass"
fi
IFS= read -r ALBERT_API_KEY || ALBERT_API_KEY=

# Check if opencode is installed
if ! command -v opencode >/dev/null 2>&1; then
    echo "Installing OpenCode inside macOS VM..."
    if command -v brew >/dev/null 2>&1; then
        brew install anomalyco/tap/opencode || npm install -g opencode-ai || curl -fsSL https://opencode.ai/install | bash
    else
        curl -fsSL https://opencode.ai/install | bash
    fi
fi

# The curl installer drops the binary in ~/.opencode/bin without touching PATH
if ! command -v opencode >/dev/null 2>&1 && [ -x "$HOME/.opencode/bin/opencode" ]; then
    export PATH="$HOME/.opencode/bin:$PATH"
fi
if ! command -v opencode >/dev/null 2>&1; then
    echo "opencode is not available on PATH inside the VM." >&2
    exit 1
fi

# Ensure git safe directory
git config --global user.name "Albert Code Agent" || true
git config --global user.email "albert-code@noreply.etalab.gouv.fr" || true
git config --global --add safe.directory '*' || true

# Prepare workspace
WORKSPACE_DIR="/Volumes/My Shared Files/workspace"
if [ ! -d "$WORKSPACE_DIR" ]; then
    WORKSPACE_DIR="$HOME/workspace"
    mkdir -p "$WORKSPACE_DIR"
fi

export OPENCODE_SERVER_PASSWORD="$PASSWORD"
export OPENCODE_SERVER_USERNAME="$USERNAME"
export ALBERT_API_KEY="$ALBERT_API_KEY"

# shellcheck disable=SC2016
export OPENCODE_CONFIG_CONTENT='{"$schema":"https://opencode.ai/config.json","provider":{"albert":{"npm":"@ai-sdk/openai-compatible","name":"Albert API (État)","options":{"baseURL":"https://albert.api.etalab.gouv.fr/v1","apiKey":"{env:ALBERT_API_KEY}"},"models":{"deepseek-v4-flash":{"name":"DeepSeek V4 Flash (Albert)","limit":{"context":131072,"output":65536}}}}},"model":"albert/deepseek-v4-flash","small_model":"albert/deepseek-v4-flash","permission":{"edit":"allow","external_directory":"allow","bash":{".*":"allow","git push.*(--force|-f | --force-with-lease)":"deny","sudo .*":"deny"},"webfetch":"allow","websearch":"allow","skill":"allow","task":"allow"}}'

cd "$WORKSPACE_DIR"
exec opencode serve --hostname 0.0.0.0 --port "$PORT"
