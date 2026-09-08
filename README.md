# OpenCode Docker experiment

Experiment 1 for Albert Code's remote-execution track: run the **OpenCode backend inside an isolated Linux container** and drive it from the **native OpenCode TUI** on your machine.

```text
host terminal (opencode attach) ──> container :4096 (opencode serve)
                                        ├── /workspace  = your project (bind-mounted)
                                        ├── tools       = bash, git, node, python
                                        └── inference   = Albert API (deepseek-v4-flash)
```

## Prerequisites

- Docker or Colima running
- `just` (`brew install just`)
- OpenCode CLI on the host (`npm install -g opencode-ai`)
- `ALBERT_API_KEY` in your environment (or in `.env`, which is gitignored)

## Usage

```bash
just          # list all commands
just start    # start the sandbox and attach the OpenCode TUI
just stop     # stop the sandbox
just check    # health + Albert provider sanity check
just logs     # follow backend logs
just shell    # shell inside the container
just restart  # rebuild image and restart (destructive)
just clean    # remove container and image
```

`just start` mounts the project directory set via `PROJECT_DIR` (defaults to `./workspace`):

```bash
export PROJECT_DIR="$HOME/Code/my-repo"
just start
```

Agent-started dev servers on ports **3000-3010** are reachable from the host browser (`http://localhost:3000` etc.).

## Sample prompts to try

Once attached, these exercise the main experiment dimensions:

1. **"What's in this workspace? Describe the project structure."** — basic inference and file tools.
2. **"What OS, kernel, and architecture are you running on?"** — confirms the agent is executing inside the Linux container, not on your Mac.
3. **"Build a simple snake game served by a Node server on port 3000, then start it."** — writes files, spawns a dev server; open `http://localhost:3000` on your host to verify preview reachability.
4. **"Create a small python script that prints the Fibonacci sequence and run it."** — polyglot toolchain inside the sandbox.
5. **"Make a change to one file, then revert it."** — edit tools and diff review.
6. **Detach (Ctrl+C / exit) and run `just start` again** — check session continuity and reconnect behavior.
7. **"Run your dev server log to /tmp/server.log and show me the last lines."** — scratch-space behavior outside the project directory.

## Design notes

- **No bind-mounted config file.** The Albert provider config is passed inline via `OPENCODE_CONFIG_CONTENT` in `docker-compose.yml`. A single-file bind mount would appear inside the container as an undeletable read-only mountpoint and confuses agents.
- **The only host mount is the project directory.** Agent edits land directly in your host checkout. This mirrors today's Lima behavior; the remote-authoritative model (clone in sandbox, deliver via branch/PR) is a later experiment.
- **Permissive in-sandbox permissions.** The container is the containment boundary, so `edit`, `bash`, and `external_directory` are allowed inside it.
- **Secrets.** `.env` is gitignored; `ALBERT_API_KEY` is read from the environment and never written to the image.
