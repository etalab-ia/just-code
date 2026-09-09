# Albert Code / OpenCode sandbox experiment
# Requires: just, opencode CLI, ALBERT_API_KEY, and Docker or Microsandbox

set dotenv-load

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

# List available commands
help:
    @just --list

# Start a sandbox and attach the native OpenCode TUI
code runtime_flag: (up runtime_flag)
    #!/usr/bin/env sh
    set -eu
    runtime=$(just _runtime-name {{ quote(runtime_flag) }})

    ask_to_stop() {
        status=$?
        trap - EXIT HUP INT TERM

        if just _running-runtimes | grep -Fxq "$runtime"; then
            if [ -t 0 ]; then
                printf 'Stop the %s runtime? [y/N] ' "$runtime"
                read -r reply || reply=
                case "$reply" in
                    y|Y|yes|YES|Yes) just "_$runtime-stop" ;;
                    *) echo "$runtime left running. Run 'just stop' when finished." ;;
                esac
            else
                echo "$runtime left running. Run 'just stop' when finished."
            fi
        fi

        exit "$status"
    }

    trap ask_to_stop EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM

    until curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/global/health" 2>/dev/null | grep -q healthy; do sleep 0.5; done
    opencode attach "http://localhost:{{ port }}" --username "{{ username }}" --password "{{ password }}"

# Stop every currently running just-code sandbox
stop:
    #!/usr/bin/env sh
    set -eu
    running=$(just _running-runtimes)
    if [ -z "$running" ]; then
        echo "No just-code runtime is running."
        exit 0
    fi

    status=0
    for runtime in $running; do
        just "_$runtime-stop" || status=$?
    done
    exit "$status"

# Start an explicitly selected backend without attaching the TUI
up runtime_flag:
    @just _prepare-runtime {{ quote(runtime_flag) }}
    @just _dispatch up {{ quote(runtime_flag) }}

# Build or pull an explicitly selected runtime image
build runtime_flag:
    @just _dispatch build {{ quote(runtime_flag) }}

# Recreate an explicitly selected sandbox
restart runtime_flag:
    @just _prepare-runtime {{ quote(runtime_flag) }}
    @just _dispatch restart {{ quote(runtime_flag) }}

# Follow logs for an explicitly selected runtime
logs runtime_flag:
    @just _dispatch logs {{ quote(runtime_flag) }}

# Check the single running backend and registered Albert provider
check:
    @just _single-running-runtime >/dev/null
    @curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/global/health"
    @echo
    @curl -s -u "{{ username }}:{{ password }}" "http://localhost:{{ port }}/provider" | python3 -c "import json,sys; d=json.load(sys.stdin); p=[x for x in d['all'] if x['id']=='albert']; print('albert provider:', 'registered, default', d['default'].get('albert') if p else 'MISSING')"

# Open a shell inside an explicitly selected runtime
shell runtime_flag:
    @just _dispatch shell {{ quote(runtime_flag) }}

# Remove an explicitly selected sandbox and its image or writable state
clean runtime_flag:
    @just _dispatch clean {{ quote(runtime_flag) }}

# Check an explicitly selected runtime installation
doctor runtime_flag:
    @just _dispatch doctor {{ quote(runtime_flag) }}

_runtime-name runtime_flag:
    #!/usr/bin/env sh
    case {{ quote(runtime_flag) }} in
        --docker) echo docker ;;
        --microsandbox) echo microsandbox ;;
        *) echo "Expected --docker or --microsandbox." >&2; exit 2 ;;
    esac

_dispatch action runtime_flag:
    #!/usr/bin/env sh
    set -eu
    action={{ quote(action) }}
    case "$action" in
        up|build|restart|logs|shell|clean|doctor) ;;
        *) echo "Unsupported runtime action: $action" >&2; exit 2 ;;
    esac
    runtime=$(just _runtime-name {{ quote(runtime_flag) }})
    just "_$runtime-$action"

_running-runtimes:
    #!/usr/bin/env sh
    if command -v docker >/dev/null 2>&1 && docker container top albert-opencode-sandbox >/dev/null 2>&1; then
        echo docker
    fi
    if command -v msb >/dev/null 2>&1 && msb ls --running -q 2>/dev/null | grep -Fxq "{{ msb_sandbox }}"; then
        echo microsandbox
    fi

_single-running-runtime:
    #!/usr/bin/env sh
    set -eu
    running=$(just _running-runtimes)
    set -- $running
    case "$#" in
        0) echo "No just-code runtime is running." >&2; exit 1 ;;
        1) echo "$1" ;;
        *) echo "Multiple just-code runtimes are running; run 'just stop' first." >&2; exit 1 ;;
    esac

_prepare-runtime runtime_flag:
    #!/usr/bin/env sh
    set -eu
    requested=$(just _runtime-name {{ quote(runtime_flag) }})
    conflicts=

    for active in $(just _running-runtimes); do
        if [ "$active" != "$requested" ]; then
            conflicts="$conflicts $active"
        fi
    done

    conflicts=${conflicts# }
    [ -n "$conflicts" ] || exit 0

    if [ ! -t 0 ]; then
        echo "$conflicts is already running. Run 'just stop' before starting $requested." >&2
        exit 1
    fi

    printf '%s is already running. Stop it and start %s? [y/N] ' "$conflicts" "$requested"
    read -r reply || reply=
    case "$reply" in
        y|Y|yes|YES|Yes)
            for active in $conflicts; do
                just "_$active-stop"
            done
            ;;
        *) echo "Keeping $conflicts running."; exit 1 ;;
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
