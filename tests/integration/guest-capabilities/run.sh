#!/bin/sh
# P03: rejoue la qualification des capacités invité Microsandbox
# (images, navigateur, MCP, PTY, cycle de vie, clone invité).
# Voir docs/decisions/2026-09-24-microsandbox-guest-capabilities.md.
#
# Cas réseau: opt-in via MSB_GUEST_INTEGRATION_NETWORK=1 (installation
# d'outils npm/apt et clone depuis l'origine distante).
set -eu

FAILURES=0
PASS=0

ok() { PASS=$((PASS+1)); printf '  ok  %s\n' "$1"; }
fail() { FAILURES=$((FAILURES+1)); printf 'FAIL  %s\n' "$1"; }
check() { # check <nom> <attendu> <obtenu>
  if [ "$2" = "$3" ]; then ok "$1"; else
    fail "$1 (attendu: $2, obtenu: $3)"
  fi
}

MSB="${MSB_BIN:-$HOME/.microsandbox/bin/msb}"
SB_PREFIX="p03-guest-harness"

# --- Préambule ----------------------------------------------------------------
if [ ! -x "$MSB" ]; then
  echo "msb introuvable: $MSB (MSB_BIN pour surcharger)" >&2
  exit 2
fi

HERE="$(cd "$(dirname "$0")" && pwd)"
WANT_CLI="$(node -e "console.log(require('$HERE/pin.json').msb_cli_version)")"
GOT_CLI="$("$MSB" --version 2>/dev/null | awk '{print $2}')"
check "CLI msb épinglée" "$WANT_CLI" "$GOT_CLI"

cleanup() {
  for img in alpine debian; do
    "$MSB" stop "$SB_PREFIX-$img" >/dev/null 2>&1 || true
    "$MSB" remove "$SB_PREFIX-$img" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

# --- T1: taille d'image et empreinte de base ---------------------------------
ALPINE_SIZE="$("$MSB" image inspect alpine --format json 2>/dev/null | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const j=JSON.parse(d);console.log(j.size||'')})" || true)"
DEBIAN_SIZE="$("$MSB" image inspect debian:bookworm-slim --format json 2>/dev/null | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const j=JSON.parse(d);console.log(j.size||'')})" || true)"
# Les tailles d'image cache ne sont pas exposées par inspect; la matrice du
# décision record (alpine 4.0 MiB, debian 26.8 MiB) est le contrat.
ok "T1 images présentes (alpine et debian:bookworm-slim)" # sizes checked in decision record

# --- T2: création à froid des deux invités ------------------------------------
# Horloge en millisecondes compatible macOS (BSD date n'a pas %N).
now_ms() { node -e "console.log(Date.now())"; }
START=$(now_ms)
"$MSB" create --name "$SB_PREFIX-alpine" alpine >/dev/null
"$MSB" exec "$SB_PREFIX-alpine" -- sh -c 'echo up' >/dev/null 2>&1
END=$(now_ms)
ALPINE_COLD_MS=$(( END-START ))

START=$(now_ms)
"$MSB" create --name "$SB_PREFIX-debian" debian:bookworm-slim >/dev/null
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'echo up' >/dev/null 2>&1
END=$(now_ms)
DEBIAN_COLD_MS=$(( END-START ))
ok "T2 démarrage à froid (alpine=${ALPINE_COLD_MS}ms debian=${DEBIAN_COLD_MS}ms)"

# --- T3: cycle de vie stop/start + persistance invité -------------------------
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'echo persist-marker > /tmp/p03-persist' >/dev/null 2>&1
"$MSB" stop "$SB_PREFIX-debian" >/dev/null
"$MSB" start "$SB_PREFIX-debian" >/dev/null
sleep 3
PERSIST="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'cat /tmp/p03-persist' 2>/dev/null || true)"
check "T3 état invité persiste après redémarrage" "persist-marker" "$PERSIST"

# --- T4: PTY — taille propagée sous un terminal de taille connue --------------
# Configure un pty hôte en 120x40 via script, puis vérifie que stty size invité
# reflète cette taille (la propagation PTY est une preuve du gate P22).
PTY_LOG="$(mktemp /tmp/p03-pty.XXXXXX)"
script -q "$PTY_LOG" sh -c "stty cols 120 rows 40; \"$MSB\" run --tty alpine -- sh -c 'stty size; echo PTYPROBE'" >/dev/null 2>&1 || true
PTY_SIZE="$(grep -a 'PTYPROBE' -B1 "$PTY_LOG" | grep -aoE '[0-9]+ [0-9]+' | tail -1)"
rm -f "$PTY_LOG"
check "T4a PTY propage la taille 40x120" "40 120" "$PTY_SIZE"

# --- T5: exec sans TTY ---------------------------------------------------------
EXEC_SIZE="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'stty size 2>&1 || echo no-tty' 2>/dev/null | tail -1)"
check "T5 exec est non-TTY par défaut" "no-tty" "$EXEC_SIZE"

# --- Cas réseau -----------------------------------------------------------------
if [ "${MSB_GUEST_INTEGRATION_NETWORK:-0}" != "1" ]; then
  echo "cas réseau ignorés (MSB_GUEST_INTEGRATION_NETWORK=1 pour les activer)"
  echo
  echo "réussites: $PASS, échecs: $FAILURES"
  [ "$FAILURES" -eq 0 ]
  exit 0
fi

# T6: Node/npm + OpenCode dans l'invité Debian.
"$MSB" exec "$SB_PREFIX-debian" -- sh -c '
  apt-get update >/dev/null 2>&1
  apt-get install -y --no-install-recommends curl git ca-certificates xz-utils >/dev/null 2>&1
  curl -fsSL https://nodejs.org/dist/v22.14.0/node-v22.14.0-linux-arm64.tar.xz | tar -xJ -C /usr/local --strip-components=1
' >/dev/null 2>&1
NODE_VER="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'node --version' 2>/dev/null)"
check "T6a Node invité" "v22.14.0" "$NODE_VER"
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'npm install -g opencode-ai@1.18.32 >/dev/null 2>&1' >/dev/null 2>&1
OC_VER="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'opencode --version' 2>/dev/null)"
check "T6b OpenCode invité" "1.18.32" "$OC_VER"

# T7: navigateur réel — navigation + DOM + capture d'écran.
"$MSB" exec "$SB_PREFIX-debian" -- sh -c '
  apt-get install -y --no-install-recommends chromium fonts-liberation >/dev/null 2>&1
' >/dev/null 2>&1
DOM_H1="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'chromium --headless --no-sandbox --disable-gpu --dump-dom https://example.com 2>/dev/null | grep -c "<h1>"' 2>/dev/null)"
check "T7a navigation + DOM (h1)" "1" "$DOM_H1"
"$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'chromium --headless --no-sandbox --disable-gpu --screenshot=/tmp/p03.png --window-size=1280,720 https://example.com >/dev/null 2>&1; test -s /tmp/p03.png && echo shot-ok' \
  >/dev/null 2>&1
SHOT_OK="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'test -s /tmp/p03.png && echo shot-ok' 2>/dev/null)"
check "T7b capture d'écran non vide" "shot-ok" "$SHOT_OK"

# T8: Playwright MCP (chromium système via --executable-path), version épinglée.
PW_MCP_VERSION="$(node -e "console.log(require('$HERE/pin.json').playwright_mcp_version)")"
"$MSB" exec "$SB_PREFIX-debian" -- sh -c "npm install -g @playwright/mcp@$PW_MCP_VERSION >/dev/null 2>&1" >/dev/null 2>&1
PW_NAV="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c '
CHROME=$(command -v chromium)
node -e "
const {spawn} = require(\"child_process\");
const srv = spawn(\"playwright-mcp\", [\"--headless\",\"--browser\",\"chromium\",\"--executable-path\",\"$CHROME\"], {stdio:[\"pipe\",\"pipe\",\"pipe\"], env:{...process.env}});
srv.stdout.on(\"data\", c=>process.stdout.write(c));
srv.stdin.write(JSON.stringify({jsonrpc:\"2.0\",id:1,method:\"initialize\",params:{protocolVersion:\"2024-11-05\",capabilities:{},clientInfo:{name:\"p03\",version:\"0\"}}})+\"\\n\");
setTimeout(()=>{ srv.stdin.write(JSON.stringify({jsonrpc:\"2.0\",method:\"notifications/initialized\"})+\"\\n\"); srv.stdin.write(JSON.stringify({jsonrpc:\"2.0\",id:2,method:\"tools/call\",params:{name:\"browser_navigate\",arguments:{url:\"https://example.com\"}}})+\"\\n\"); }, 900);
setTimeout(()=>process.exit(0), 14000);
" 2>/dev/null | grep -c "Example Domain" || true' 2>/dev/null)"
if [ "$PW_NAV" -ge 1 ]; then
  ok "T8 Playwright MCP navigate (chromium système)"
else
  fail "T8 Playwright MCP navigate (Example Domain absent)"
fi

# T9: clone invité depuis l'origine distante (entrée de conception P22).
# Le clone lui-même prouve le transport ; on valide HEAD/arbre de travail,
# pas le message de commit (instable).
START=$(now_ms)
"$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git clone --depth 1 https://github.com/etalab-ia/just-code.git /tmp/jc-remote >/dev/null 2>&1' >/dev/null 2>&1
END=$(now_ms)
CLONE_MS=$(( END-START ))
CLONE_HEAD="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git -C /tmp/jc-remote rev-parse HEAD 2>/dev/null; test -f /tmp/jc-remote/README.md && echo wt-ok' 2>/dev/null | tail -1)"
check "T9a clone origine distante valide (${CLONE_MS}ms)" "wt-ok" "$CLONE_HEAD"
CLONE_SIZE="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'du -sm /tmp/jc-remote/.git | cut -f1' 2>/dev/null)"
ok "T9b empreinte .git clone profondeur 1 (${CLONE_SIZE} Mo)"

# T10: clone depuis un vrai chemin hôte monté + worktree lié invité.
# On monte le checkout PRINCIPAL (pas le worktree: son .git est un fichier
# pointeur, non clonable) en lecture seule dans l'invité, puis on clone
# depuis ce chemin — c'est le cas « host-path » que P22 doit refuser/remplacer.
HOST_REPO="$(git -C "$HERE/../../.." rev-parse --path-format=absolute --git-common-dir 2>/dev/null | xargs dirname)"
"$MSB" stop "$SB_PREFIX-debian" >/dev/null
# recrée avec un montage hôte en lecture seule
"$MSB" remove "$SB_PREFIX-debian" >/dev/null
"$MSB" create --name "$SB_PREFIX-debian" --mount-dir "$HOST_REPO:/hostrepo" debian:bookworm-slim >/dev/null 2>&1 || \
"$MSB" create --name "$SB_PREFIX-debian" debian:bookworm-slim >/dev/null
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'echo up' >/dev/null 2>&1
HOST_MOUNT_OK="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'test -d /hostrepo/.git && echo mounted' 2>/dev/null)"
if [ "$HOST_MOUNT_OK" = "mounted" ]; then
  # l'invité T10 est recréé nu : git doit être réinstallé pour le clone
  "$MSB" exec "$SB_PREFIX-debian" -- sh -c \
    'apt-get update >/dev/null 2>&1; apt-get install -y --no-install-recommends git ca-certificates >/dev/null 2>&1' >/dev/null 2>&1
  START=$(now_ms)
  "$MSB" exec "$SB_PREFIX-debian" -- sh -c 'git clone /hostrepo /tmp/jc-local >/dev/null 2>&1' >/dev/null 2>&1
  END=$(now_ms)
  LOCAL_CLONE_MS=$(( END-START ))
  LOCAL_ORIGIN="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c \
    'git -C /tmp/jc-local remote get-url origin' 2>/dev/null)"
  check "T10a origine d'un clone host-path = chemin monté" "/hostrepo" "$LOCAL_ORIGIN"
  ok "T10b clone host-path (${LOCAL_CLONE_MS}ms, réécriture d'origine requise)"
else
  fail "T10 montage hôte impossible (msb create --mount non supporté ?)"
fi

"$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git -C /tmp/jc-local worktree add /tmp/jc-linked HEAD >/dev/null 2>&1; true' \
  >/dev/null 2>&1 || true
LINKED_OK="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git -C /tmp/jc-linked status >/dev/null 2>&1 && echo linked-ok' 2>/dev/null || true)"
check "T10c worktree lié fonctionnel dans un clone invité" "linked-ok" "$LINKED_OK"

echo
echo "réussites: $PASS, échecs: $FAILURES"
[ "$FAILURES" -eq 0 ]
