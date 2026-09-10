# Albert Code / OpenCode sandbox experiment
# Requires: just, opencode CLI, ALBERT_API_KEY, and Docker, Microsandbox, or Tart

set dotenv-load

# Optional runtime preference; an explicit command argument takes precedence
runtime_preference := env_var_or_default("RUNTIME", "")
preferred_runtime_flag := if runtime_preference == "" { "" } else { "--" + runtime_preference }

# Project directory mounted into the sandbox as /workspace
project_dir := env_var_or_default("PROJECT_DIR", justfile_directory() / "workspace")
msb_config := justfile_directory() / "microsandbox.yaml"
msb_image := "ghcr.io/anomalyco/opencode:latest"
msb_sandbox := "albert-opencode-sandbox"
tart_image := env_var_or_default("TART_IMAGE", "ghcr.io/cirruslabs/macos-tahoe-base:latest")
# Derive a stable VM name from the image reference; the opencode- prefix marks
# just-code-managed VMs that `just stop` owns (e.g. opencode-tahoe-base-latest).
tart_vm := "opencode-" + replace(replace(trim_start_matches(file_name(tart_image), "macos-"), ":", "-"), "@sha256", "-sha256")
tart_mtu := env_var_or_default("TART_MTU", "1280")
password := env_var_or_default("OPENCODE_SERVER_PASSWORD", "albert-dev-pass")
username := env_var_or_default("OPENCODE_SERVER_USERNAME", "opencode")
port := "4096"

# Show this help
default: help

# List available commands
help:
    @echo "Runtime: pass --docker, --microsandbox, or --tart."
    @echo "Set RUNTIME=docker|microsandbox|tart in .env to omit the flag."
    @echo
    @just --list

# Start a sandbox and attach the native OpenCode TUI
code runtime_flag=preferred_runtime_flag: (start runtime_flag)
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

    case "$runtime" in
        tart)
            endpoint="http://$(tart ip --wait 60 "{{ tart_vm }}"):{{ port }}"
            ;;
        *)
            endpoint="http://localhost:{{ port }}"
            ;;
    esac

    until curl -s -u "{{ username }}:{{ password }}" "$endpoint/global/health" 2>/dev/null | grep -q healthy; do sleep 0.5; done
    opencode attach "$endpoint" --username "{{ username }}" --password "{{ password }}"

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

# Start the selected backend without attaching the TUI
start runtime_flag=preferred_runtime_flag:
    @just _prepare-runtime {{ quote(runtime_flag) }}
    @just _dispatch start {{ quote(runtime_flag) }}

# Build or pull the selected runtime image
build runtime_flag=preferred_runtime_flag:
    @just _dispatch build {{ quote(runtime_flag) }}

# Recreate the selected sandbox
restart runtime_flag=preferred_runtime_flag:
    @just _prepare-runtime {{ quote(runtime_flag) }}
    @just _dispatch restart {{ quote(runtime_flag) }}

# Follow logs for the selected runtime
logs runtime_flag=preferred_runtime_flag:
    @just _dispatch logs {{ quote(runtime_flag) }}

# Check the single running backend and registered Albert provider
check:
    #!/usr/bin/env sh
    set -eu
    runtime=$(just _single-running-runtime)
    case "$runtime" in
        tart)
            vm=$(tart list 2>/dev/null | awk '$1 == "local" && $2 ~ /^opencode-/ && $NF == "running" { print $2 }')
            set -- $vm
            case "$#" in
                0) echo "No just-code Tart VM is running." >&2; exit 1 ;;
                1) endpoint="http://$(tart ip --wait 60 "$1"):{{ port }}" ;;
                *) echo "Multiple Tart VMs are running; run 'just stop' first." >&2; exit 1 ;;
            esac
            ;;
        *)
            endpoint="http://localhost:{{ port }}"
            ;;
    esac
    curl -s -u "{{ username }}:{{ password }}" "$endpoint/global/health"
    echo
    curl -s -u "{{ username }}:{{ password }}" "$endpoint/provider" | python3 -c "import json,sys; d=json.load(sys.stdin); p=[x for x in d['all'] if x['id']=='albert']; print('albert provider:', 'registered, default', d['default'].get('albert') if p else 'MISSING')"

# Open a shell inside the selected runtime
shell runtime_flag=preferred_runtime_flag:
    @just _dispatch shell {{ quote(runtime_flag) }}

# Remove the selected sandbox and its image or writable state
clean runtime_flag=preferred_runtime_flag:
    @just _dispatch clean {{ quote(runtime_flag) }}

# Check the selected runtime installation
doctor runtime_flag=preferred_runtime_flag:
    @just _dispatch doctor {{ quote(runtime_flag) }}

_runtime-name runtime_flag:
    #!/usr/bin/env sh
    case {{ quote(runtime_flag) }} in
        --docker) echo docker ;;
        --microsandbox) echo microsandbox ;;
        --tart) echo tart ;;
        "") echo "Select --docker, --microsandbox, or --tart, or set RUNTIME in .env." >&2; exit 2 ;;
        *) echo "Expected --docker, --microsandbox, or --tart (RUNTIME must be docker, microsandbox, or tart)." >&2; exit 2 ;;
    esac

_dispatch action runtime_flag:
    #!/usr/bin/env sh
    set -eu
    action={{ quote(action) }}
    case "$action" in
        start|build|restart|logs|shell|clean|doctor) ;;
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
    if command -v tart >/dev/null 2>&1 && tart list 2>/dev/null | awk '$1 == "local" && $2 ~ /^opencode-/ && $NF == "running" { print $2 }' | grep -q .; then
        echo tart
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

_docker-start:
    #!/usr/bin/env sh
    set -eu
    : "${ALBERT_API_KEY:?Set ALBERT_API_KEY in the environment or .env}"
    mkdir -p "{{ project_dir }}"
    PROJECT_DIR="{{ project_dir }}" docker compose up -d --quiet-pull

_docker-stop:
    docker compose down --timeout 3

_docker-build:
    docker compose build --quiet

_docker-restart: _docker-stop _docker-build _docker-start

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

_microsandbox-start:
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

_microsandbox-restart: _microsandbox-clean _microsandbox-start

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

_tart-start:
    #!/usr/bin/env sh
    set -eu
    : "${ALBERT_API_KEY:?Set ALBERT_API_KEY in the environment or .env}"
    mkdir -p "{{ project_dir }}"
    mkdir -p "$HOME/.local/state/just-code"

    log_file="$HOME/.local/state/just-code/tart.log"
    stage_dir="$HOME/.local/state/just-code/tart"
    guest_bootstrap="/Volumes/My Shared Files/just-code/tart-bootstrap.sh"

    launch_backend() {
        echo "Launching OpenCode server inside {{ tart_vm }}..."
        printf '%s\n%s\n' "{{ password }}" "$ALBERT_API_KEY" |
            nohup tart exec -i "{{ tart_vm }}" /bin/sh "$guest_bootstrap" \
                "{{ port }}" "{{ username }}" {{ quote(tart_mtu) }} >> "$log_file" 2>&1 &
    }

    stage_bootstrap() {
        mkdir -p "$stage_dir"
        cp "{{ justfile_directory() }}/tart-bootstrap.sh" "$stage_dir/tart-bootstrap.sh"
        chmod 644 "$stage_dir/tart-bootstrap.sh"
    }

    wait_for_agent() {
        i=0
        while [ "$i" -lt 60 ]; do
            if tart exec "{{ tart_vm }}" true 2>/dev/null; then
                return 0
            fi
            sleep 1
            i=$((i + 1))
        done
        echo "Timed out waiting for {{ tart_vm }} guest agent." >&2
        return 1
    }

    if tart list 2>/dev/null | awk '$1 == "local" && $2 == "{{ tart_vm }}" && $NF == "running" { print $2 }' | grep -Fxq "{{ tart_vm }}"; then
        wait_for_agent || exit 1
        vm_ip=$(tart ip --wait 60 "{{ tart_vm }}" 2>/dev/null || true)
        if [ -n "$vm_ip" ] && curl -s -u "{{ username }}:{{ password }}" "http://$vm_ip:{{ port }}/global/health" 2>/dev/null | grep -q healthy; then
            echo "{{ tart_vm }} is running with a healthy OpenCode backend."
            exit 0
        fi
        echo "{{ tart_vm }} is running but OpenCode is not healthy; restarting backend..."
        tart exec "{{ tart_vm }}" pkill -x opencode 2>/dev/null || true
        i=0
        while tart exec "{{ tart_vm }}" pgrep -x opencode >/dev/null 2>&1; do
            i=$((i + 1))
            if [ "$i" -ge 10 ]; then
                echo "opencode ignored SIGTERM; force-killing..." >&2
                tart exec "{{ tart_vm }}" pkill -9 -x opencode 2>/dev/null || true
                break
            fi
            sleep 1
        done
        if tart exec "{{ tart_vm }}" pgrep -x opencode >/dev/null 2>&1; then
            echo "Failed to stop the previous opencode process; refusing to relaunch." >&2
            exit 1
        fi
        stage_bootstrap
        launch_backend
        exit 0
    fi

    if ! tart list 2>/dev/null | awk '$1 == "local" { print $2 }' | grep -Fxq "{{ tart_vm }}"; then
        echo "Cloning {{ tart_image }} to {{ tart_vm }}..."
        tart clone "{{ tart_image }}" "{{ tart_vm }}"
    fi

    # Stage only the bootstrap script in a dedicated read-only share so the
    # guest never sees the checkout, its .env, or other host-only files.
    stage_bootstrap

    echo "Starting {{ tart_vm }} with Tart..."
    nohup tart run --no-graphics \
        --dir="workspace:{{ project_dir }}" \
        --dir="just-code:$stage_dir:ro" \
        "{{ tart_vm }}" > "$log_file" 2>&1 &

    wait_for_agent || exit 1
    launch_backend

_tart-stop:
    #!/usr/bin/env sh
    set -eu
    vms=$(tart list 2>/dev/null | awk '$1 == "local" && $2 ~ /^opencode-/ && $NF == "running" { print $2 }')
    if [ -z "$vms" ]; then
        echo "No just-code Tart VM is running."
        exit 0
    fi
    status=0
    for vm in $vms; do
        echo "Stopping $vm..."
        tart stop "$vm" --timeout 5 || status=$?
    done
    exit "$status"

_tart-build:
    tart pull "{{ tart_image }}"

_tart-restart: _tart-clean _tart-start

_tart-logs:
    tail -f "$HOME/.local/state/just-code/tart.log"

_tart-shell:
    tart exec -it "{{ tart_vm }}" /bin/zsh

_tart-clean:
    #!/usr/bin/env sh
    set -eu
    if tart list 2>/dev/null | awk '$1 == "local" && $2 == "{{ tart_vm }}" && $NF == "running" { print $2 }' | grep -Fxq "{{ tart_vm }}"; then
        tart stop "{{ tart_vm }}" --timeout 5
    fi
    if tart list 2>/dev/null | awk '$1 == "local" { print $2 }' | grep -Fxq "{{ tart_vm }}"; then
        tart delete "{{ tart_vm }}"
    else
        echo "{{ tart_vm }} does not exist."
    fi

_tart-doctor:
    @tart --version
    @echo "Tart runtime is ready."
