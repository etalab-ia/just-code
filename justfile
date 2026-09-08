# Albert Code / OpenCode Docker experiment
# Requires: just, docker (or colima), opencode CLI on the host, ALBERT_API_KEY in env

set dotenv-load := true

# Project directory mounted into the container as /workspace
project_dir := env_var_or_default("PROJECT_DIR", justfile_directory() / "workspace")
password := env_var_or_default("OPENCODE_SERVER_PASSWORD", "albert-dev-pass")
username := env_var_or_default("OPENCODE_SERVER_USERNAME", "opencode")
port := "4096"

# Show this help
default: help

# List available commands
help:
    @just --list

# Start the sandbox and attach the native OpenCode TUI
code: up
    #!/usr/bin/env sh
    until curl -s -u {{username}}:{{password}} http://localhost:{{port}}/global/health 2>/dev/null | grep -q healthy; do sleep 0.5; done
    exec opencode attach http://localhost:{{port}} --username {{username}} --password {{password}}

# Stop the sandbox (kills agent-spawned processes inside)
stop:
    @echo "Stopping sandbox..."
    @docker compose down --timeout 3 2>&1 | grep -v '^$' || true
    @echo "Stopped."

# Start the backend container only, without attaching the TUI
up:
    @PROJECT_DIR="{{project_dir}}" ALBERT_API_KEY="$ALBERT_API_KEY" docker compose up -d --quiet-pull 2>&1 | grep -v '^$' || true

# Build the sandbox image
build:
    @ALBERT_API_KEY="$ALBERT_API_KEY" docker compose build --quiet

# Rebuild from scratch and restart
restart: stop build up

# Follow backend logs
logs:
    docker compose logs -f

# Check backend health and registered Albert provider
check:
    @curl -s -u {{username}}:{{password}} http://localhost:{{port}}/global/health
    @echo
    @curl -s -u {{username}}:{{password}} http://localhost:{{port}}/provider | python3 -c "import json,sys; d=json.load(sys.stdin); p=[x for x in d['all'] if x['id']=='albert']; print('albert provider:', 'registered, default', d['default'].get('albert') if p else 'MISSING')"

# Open a shell inside the sandbox
shell:
    docker exec -it albert-opencode-sandbox bash

# Remove container and image
clean: stop
    @docker rmi just-code-opencode-backend 2>/dev/null || true
    @echo "Image removed."
