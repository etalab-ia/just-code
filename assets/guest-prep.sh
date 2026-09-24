#!/bin/sh
set -eu
if [ ! -f /var/lib/just-code/toolchain-ready ]; then
  apk add --no-cache bash build-base ca-certificates curl git nodejs npm procps python3 py3-pip
  mkdir -p /var/lib/just-code
  touch /var/lib/just-code/toolchain-ready
fi
# The identity configured by the global setup (P11) reaches the guest through
# the sandbox environment; these defaults keep an unconfigured machine usable.
git config --global user.name "${JUST_CODE_GIT_NAME:-Albert Code Agent}"
git config --global user.email "${JUST_CODE_GIT_EMAIL:-albert-code@noreply.etalab.gouv.fr}"
git config --global --add safe.directory '*'
