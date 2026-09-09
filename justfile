# Albert Code / OpenCode sandbox experiment
# Requires: just, opencode CLI, ALBERT_API_KEY, and the selected runtime

set dotenv-load

# Runtime selected through RUNTIME=docker|microsandbox
runtime := env_var_or_default("RUNTIME", "docker")

# Project directory mounted into the sandbox as /workspace
project_dir := env_var_or_default("PROJECT_DIR", justfile_directory() / "workspace")
msb_config := justfile_directory() / "microsandbox.yaml"
msb_image := "ghcr.io/anomalyco/opencode:latest"
msb_sandbox := "albert-opencode-sandbox"
password := env_var_or_default("OPENCODE_SERVER_PASSWORD", "albert-dev-pass")
username := env_var_or_default("OPENCODE_SERVER_USERNAME", "opencode")
port := "4096"

# Show this help
default: help

# List available commands and the selected runtime
help: _check-runtime
    @just --list
    @echo
    @echo "Selected runtime: {{ runtime }}"

# Start the selected sandbox and attach the native OpenCode TUI
code: up
    #!/usr/bin/env sh
    set -eu
    until curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/global/health" 2>/dev/null | grep -q healthy; do sleep 0.5; done
    exec opencode attach "http://localhost:{{ port }}" --username "{{ username }}" --password "{{ password }}"

# Stop the selected sandbox
stop: _check-runtime
    @just "_{{ runtime }}-stop"

# Start the OpenCode backend without attaching the TUI
up: _check-runtime
    @just "_{{ runtime }}-up"

# Build or pull the selected runtime image
build: _check-runtime
    @just "_{{ runtime }}-build"

# Recreate the selected sandbox from its image and configuration
restart: _check-runtime
    @just "_{{ runtime }}-restart"

# Follow backend logs
logs: _check-runtime
    @just "_{{ runtime }}-logs"

# Check backend health and registered Albert provider
check: _check-runtime
    @curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/global/health"
    @echo
    @curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/provider" | python3 -c "import json,sys; d=json.load(sys.stdin); p=[x for x in d['all'] if x['id']=='albert']; print('albert provider:', 'registered, default', d['default'].get('albert') if p else 'MISSING')"

# Open a shell inside the selected sandbox
shell: _check-runtime
    @just "_{{ runtime }}-shell"

# Remove the selected sandbox and its runtime image or writable state
clean: _check-runtime
    @just "_{{ runtime }}-clean"

# Check the selected runtime installation
doctor: _check-runtime
    @just "_{{ runtime }}-doctor"

_check-runtime:
    #!/usr/bin/env sh
    case "{{ runtime }}" in
        docker|microsandbox) ;;
        *) echo "Unsupported RUNTIME={{ runtime }}; expected docker or microsandbox." >&2; exit 2 ;;
    esac

_docker-up:
    #!/usr/bin/env sh
    set -eu
    : "${ALBERT_API_KEY:?Set ALBERT_API_KEY in the environment or .env}"
    mkdir -p "{{ project_dir }}"
    PROJECT_DIR="{{ project_dir }}" docker compose up -d --quiet-pull

_docker-stop:
    docker compose down --timeout 3

_docker-build:
    docker compose build --quiet

_docker-restart: _docker-stop _docker-build _docker-up

_docker-logs:
    docker compose logs --follow

_docker-shell:
    docker exec -it albert-opencode-sandbox bash

_docker-clean:
    docker compose down --timeout 3 --rmi local

_docker-doctor:
    @docker info >/dev/null
    @docker compose version
    @echo "Docker runtime is ready."

_microsandbox-up:
    #!/usr/bin/env sh
    set -eu
    : "${ALBERT_API_KEY:?Set ALBERT_API_KEY in the environment or .env}"
    mkdir -p "{{ project_dir }}"

    if msb ls --running -q | grep -Fxq "{{ msb_sandbox }}"; then
        echo "{{ msb_sandbox }} is already running."
    elif msb inspect "{{ msb_sandbox }}" >/dev/null 2>&1; then
        msb modify "{{ msb_sandbox }}" \
            --env "OPENCODE_SERVER_PASSWORD={{ password }}" \
            --env "OPENCODE_SERVER_USERNAME={{ username }}" \
            --next-start
        msb start "{{ msb_sandbox }}"
    else
        msb run \
            --name "{{ msb_sandbox }}" \
            --detach \
            --conf "{{ msb_config }}" \
            --root-disk "8G" \
            --volume "{{ project_dir }}:/workspace" \
            --env "OPENCODE_SERVER_PASSWORD={{ password }}" \
            --env "OPENCODE_SERVER_USERNAME={{ username }}" \
            "{{ msb_image }}"
    fi

_microsandbox-stop:
    #!/usr/bin/env sh
    set -eu
    if msb ls --running -q | grep -Fxq "{{ msb_sandbox }}"; then
        echo "Stopping {{ msb_sandbox }}..."
        msb stop --timeout 3 "{{ msb_sandbox }}"
    else
        echo "{{ msb_sandbox }} is not running."
    fi

_microsandbox-build:
    msb pull "{{ msb_image }}"

_microsandbox-restart: _microsandbox-clean _microsandbox-up

_microsandbox-logs:
    msb logs --follow "{{ msb_sandbox }}"

_microsandbox-shell:
    msb exec "{{ msb_sandbox }}" -- /bin/bash

_microsandbox-clean:
    #!/usr/bin/env sh
    set -eu
    if msb inspect "{{ msb_sandbox }}" >/dev/null 2>&1; then
        msb rm --force "{{ msb_sandbox }}"
    else
        echo "{{ msb_sandbox }} does not exist."
    fi

_microsandbox-doctor:
    msb doctor
