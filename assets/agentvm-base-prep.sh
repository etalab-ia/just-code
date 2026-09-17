#!/bin/sh
# agentvm-base-prep: provisions the just-code agent-vm base template.
#
# Runs inside the base VM as the regular user (piped in by
# `limactl shell <template> sh -s`), invoking sudo for system-level changes.
# Running as the user matters: OpenCode installs under $HOME, which is root's
# home if the script itself runs as root.
#
# just-code builds this template itself so it never has to execute a
# third-party provisioning script that cannot be pinned to a release.
#
# Scope is deliberately minimal: base packages plus the toolchain the backend
# needs. Anything else (Docker, Chromium, other agent CLIs, MCP wiring)
# belongs in a user-maintained template referenced through AGENT_VM_TEMPLATE.
set -eu

export DEBIAN_FRONTEND=noninteractive
SUDO="sudo -n"

# Debian's needrestart can prompt mid-upgrade about restarting services, which
# would hang a non-interactive provisioning run. Same guard agent-vm uses.
# shellcheck disable=SC2016  # Perl syntax, must stay literal.
$SUDO mkdir -p /etc/needrestart/conf.d
$SUDO tee /etc/needrestart/conf.d/no-prompt.conf > /dev/null <<'NRCONF'
$nrconf{restart} = 'a';
NRCONF

# Node floor required by the pi and letta coding agents (see the project
# design notes). Debian 13 ships Node 20, which is below that floor, so Node
# comes from NodeSource rather than apt.
NODE_MAJOR=24

echo "just-code: installing base packages..."
# unzip and tar are required by the OpenCode installer; ca-certificates and
# curl by NodeSource and by the installer's own downloads.
$SUDO apt-get update
$SUDO apt-get install -y --no-install-recommends \
  ca-certificates \
  curl \
  git \
  jq \
  sudo \
  tar \
  unzip \
  xz-utils

# PATH for every shell, independent of the login shell. The OpenCode installer
# only writes ~/.zshrc and ~/.zshenv, which a non-interactive shell (briefly:
# `limactl shell <vm> sh -c ...`) never reads. /etc/profile.d is sourced by
# bash and zsh login shells alike.
$SUDO tee /etc/profile.d/just-code.sh > /dev/null <<'PROFILE'
# Managed by just-code. Provides the toolchain paths for every shell.
if [ -d "$HOME/.opencode/bin" ]; then
  PATH="$HOME/.opencode/bin:$PATH"
fi
if [ -d "$HOME/.local/bin" ]; then
  PATH="$HOME/.local/bin:$PATH"
fi
export PATH
PROFILE
$SUDO chmod 0644 /etc/profile.d/just-code.sh

echo "just-code: installing Node.js ${NODE_MAJOR}..."
# `sudo -E` keeps DEBIAN_FRONTEND set for the setup script's apt calls.
curl -fsSL "https://deb.nodesource.com/setup_${NODE_MAJOR}.x" | $SUDO -E bash -
$SUDO apt-get install -y --no-install-recommends nodejs

echo "just-code: installing OpenCode..."
# --no-modify-path: just-code owns PATH through /etc/profile.d above. Letting
# the installer edit shell rc files would duplicate the entry and tie the
# install to zsh.
curl -fsSL https://opencode.ai/install | bash -s -- --no-modify-path

# Fail loudly here rather than at backend launch: a base template without
# OpenCode is unusable, and the failure would otherwise surface much later as
# an opaque health timeout.
if [ ! -x "$HOME/.opencode/bin/opencode" ]; then
  echo "just-code: OpenCode did not install to \$HOME/.opencode/bin/opencode" >&2
  exit 1
fi

echo "just-code: base template provisioning complete."