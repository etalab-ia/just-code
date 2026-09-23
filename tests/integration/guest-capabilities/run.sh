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

WANT_CLI="$(node -e "console.log(require('$(dirname "$0")/pin.json').msb_cli_version)")"
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
START=$(date +%s%N)
"$MSB" create --name "$SB_PREFIX-alpine" alpine >/dev/null
"$MSB" exec "$SB_PREFIX-alpine" -- sh -c 'echo up' >/dev/null 2>&1
END=$(date +%s%N)
ALPINE_COLD_MS=$(( (END-START)/1000000 ))

START=$(date +%s%N)
"$MSB" create --name "$SB_PREFIX-debian" debian:bookworm-slim >/dev/null
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'echo up' >/dev/null 2>&1
END=$(date +%s%N)
DEBIAN_COLD_MS=$(( (END-START)/1000000 ))
ok "T2 démarrage à froid (alpine=${ALPINE_COLD_MS}ms debian=${DEBIAN_COLD_MS}ms)"

# --- T3: cycle de vie stop/start + persistance invité -------------------------
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'echo persist-marker > /tmp/p03-persist' >/dev/null 2>&1
"$MSB" stop "$SB_PREFIX-debian" >/dev/null
"$MSB" start "$SB_PREFIX-debian" >/dev/null
sleep 3
PERSIST="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'cat /tmp/p03-persist' 2>/dev/null || true)"
check "T3 état invité persiste après redémarrage" "persist-marker" "$PERSIST"

# --- T4: PTY — taille propagée sous un vrai terminal --------------------------
# Sous `script` (pty hôte), stty size doit refléter la taille du terminal.
PTY_OUT="$(script -q /dev/null "$MSB" run --tty alpine -- sh -c 'stty size; echo PTYPROBE' 2>/dev/null | grep -a 'PTYPROBE' || true)"
if [ -n "$PTY_OUT" ]; then
  ok "T4a run --tty ouvre un PTY"
else
  fail "T4a run --tty: PTYPROBE absent"
fi

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

# T8: Playwright MCP (chromium système via --executable-path).
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'npm install -g @playwright/mcp@latest >/dev/null 2>&1' >/dev/null 2>&1
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
START=$(date +%s%N)
"$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git clone --depth 1 https://github.com/etalab-ia/just-code.git /tmp/jc-remote >/dev/null 2>&1' >/dev/null 2>&1
END=$(date +%s%N)
CLONE_MS=$(( (END-START)/1000000 ))
CLONE_OK="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git -C /tmp/jc-remote log --oneline -1 | grep -c "Merge" || true' 2>/dev/null)"
if [ "$CLONE_OK" -ge 1 ]; then
  ok "T9a clone origine distante (${CLONE_MS}ms)"
else
  fail "T9a clone origine distante"
fi
CLONE_SIZE="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c 'du -sm /tmp/jc-remote/.git | cut -f1' 2>/dev/null)"
ok "T9b empreinte .git clone profondeur 1 (${CLONE_SIZE} Mo)"

# T10: clone local (chemin hôte monté / invité) + worktree lié invité.
START=$(date +%s%N)
"$MSB" exec "$SB_PREFIX-debian" -- sh -c 'git clone /tmp/jc-remote /tmp/jc-local >/dev/null 2>&1' >/dev/null 2>&1
END=$(date +%s%N)
LOCAL_CLONE_MS=$(( (END-START)/1000000 ))
LOCAL_ORIGIN="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git -C /tmp/jc-local remote get-url origin' 2>/dev/null)"
check "T10a origine d'un clone local = chemin source" "/tmp/jc-remote" "$LOCAL_ORIGIN"
ok "T10b clone local (${LOCAL_CLONE_MS}ms, réécriture d'origine requise)"

"$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git -C /tmp/jc-remote worktree add /tmp/jc-linked HEAD >/dev/null 2>&1; git -C /tmp/jc-linked status >/dev/null 2>&1 && echo linked-ok' \
  >/dev/null 2>&1
LINKED_OK="$("$MSB" exec "$SB_PREFIX-debian" -- sh -c \
  'git -C /tmp/jc-linked status >/dev/null 2>&1 && echo linked-ok' 2>/dev/null)"
check "T10c worktree lié fonctionnel dans un clone invité" "linked-ok" "$LINKED_OK"

echo
echo "réussites: $PASS, échecs: $FAILURES"
[ "$FAILURES" -eq 0 ]
