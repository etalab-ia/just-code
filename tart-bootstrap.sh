#!/usr/bin/env sh
# OpenCode bootstrap script inside Tart macOS guest VM
set -eu

PORT="${1:-4096}"
PASSWORD="${2:-albert-dev-pass}"
USERNAME="${3:-opencode}"
ALBERT_API_KEY="${4:-}"

# Check if opencode is installed
if ! command -v opencode >/dev/null 2>&1; then
    echo "Installing OpenCode inside macOS VM..."
    if command -v brew >/dev/null 2>&1; then
        brew install anomalyco/tap/opencode || npm install -g opencode-ai || curl -fsSL https://opencode.ai/install | bash
    else
        curl -fsSL https://opencode.ai/install | bash
    fi
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
