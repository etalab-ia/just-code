set -eu
if [ ! -f /var/lib/just-code/toolchain-ready ]; then
  apk add --no-cache bash build-base ca-certificates curl git nodejs npm procps python3 py3-pip
  mkdir -p /var/lib/just-code
  touch /var/lib/just-code/toolchain-ready
fi
git config --global user.name "Albert Code Agent"
git config --global user.email "albert-code@noreply.etalab.gouv.fr"
git config --global --add safe.directory '*'
exec opencode serve --hostname 0.0.0.0 --port 4096
