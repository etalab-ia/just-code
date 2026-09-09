# Albert Code / OpenCode Microsandbox experiment
# Requires: just, msb, opencode CLI on the host, ALBERT_API_KEY in env

set dotenv-load

# Project directory mounted into the microVM as /workspace
project_dir := env_var_or_default("PROJECT_DIR", justfile_directory() / "workspace")
config := justfile_directory() / "microsandbox.yaml"
image := "ghcr.io/anomalyco/opencode:latest"
sandbox := "albert-opencode-sandbox"
password := env_var_or_default("OPENCODE_SERVER_PASSWORD", "albert-dev-pass")
username := env_var_or_default("OPENCODE_SERVER_USERNAME", "opencode")
port := "4096"

# Show this help
default: help

# List available commands
help:
    @just --list

# Start the microVM and attach the native OpenCode TUI
code: up
    #!/usr/bin/env sh
    set -eu
    until curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/global/health" 2>/dev/null | grep -q healthy; do sleep 0.5; done
    exec opencode attach "http://localhost:{{ port }}" --username "{{ username }}" --password "{{ password }}"

# Stop the microVM while preserving its writable root filesystem
stop:
    #!/usr/bin/env sh
    set -eu
    if msb ls --running -q | grep -Fxq "{{ sandbox }}"; then
        echo "Stopping {{ sandbox }}..."
        msb stop --timeout 3 "{{ sandbox }}"
    else
        echo "{{ sandbox }} is not running."
    fi

# Start the OpenCode backend without attaching the TUI
up:
    #!/usr/bin/env sh
    set -eu
    : "${ALBERT_API_KEY:?Set ALBERT_API_KEY in the environment or .env}"
    mkdir -p "{{ project_dir }}"

    if msb ls --running -q | grep -Fxq "{{ sandbox }}"; then
        echo "{{ sandbox }} is already running."
    elif msb inspect "{{ sandbox }}" >/dev/null 2>&1; then
        msb modify "{{ sandbox }}" \
            --env "OPENCODE_SERVER_PASSWORD={{ password }}" \
            --env "OPENCODE_SERVER_USERNAME={{ username }}" \
            --next-start
        msb start "{{ sandbox }}"
    else
        msb run \
            --name "{{ sandbox }}" \
            --detach \
            --conf "{{ config }}" \
            --root-disk "8G" \
            --volume "{{ project_dir }}:/workspace" \
            --env "OPENCODE_SERVER_PASSWORD={{ password }}" \
            --env "OPENCODE_SERVER_USERNAME={{ username }}" \
            "{{ image }}"
    fi

# Pre-pull the OpenCode OCI image
pull:
    msb pull "{{ image }}"

# Recreate the microVM from the current image and configuration
restart: clean up

# Follow backend logs
logs:
    msb logs --follow "{{ sandbox }}"

# Check backend health and registered Albert provider
check:
    @curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/global/health"
    @echo
    @curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/provider" | python3 -c "import json,sys; d=json.load(sys.stdin); p=[x for x in d['all'] if x['id']=='albert']; print('albert provider:', 'registered, default', d['default'].get('albert') if p else 'MISSING')"

# Open a shell inside the microVM
shell:
    msb exec "{{ sandbox }}" -- /bin/bash

# Remove the microVM and its writable state
clean:
    #!/usr/bin/env sh
    set -eu
    if msb inspect "{{ sandbox }}" >/dev/null 2>&1; then
        msb rm --force "{{ sandbox }}"
    else
        echo "{{ sandbox }} does not exist."
    fi

# Check host virtualization support and Microsandbox installation
doctor:
    msb doctor
