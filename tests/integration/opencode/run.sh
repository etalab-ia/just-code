#!/bin/sh
# P01: rejoue la matrice de caractérisation de la configuration OpenCode.
# Voir docs/decisions/2026-09-23-opencode-configuration-contract.md.
# Cas réseau: opt-in via OPENCODE_INTEGRATION_NETWORK=1 + ALBERT_API_KEY.
set -eu

HERE="$(cd "$(dirname "$0")" && pwd)"
PIN="$(node -e "const p=require('$HERE/pin.json'); console.log(p.npm_package+'@'+p.version)")"
WANT_VERSION="$(node -e "console.log(require('$HERE/pin.json').version)")"

FAILURES=0
PASS=0

ok() { PASS=$((PASS+1)); printf '  ok  %s\n' "$1"; }
fail() { FAILURES=$((FAILURES+1)); printf 'FAIL  %s\n' "$1"; }
check() { # check <nom> <attendu> <obtenu>
  if [ "$2" = "$3" ]; then ok "$1"; else
    fail "$1 (attendu: $2, obtenu: $3)"
  fi
}

# --- Installation épinglée ---------------------------------------------------
LAB="$(mktemp -d /tmp/p01-opencode-XXXXXX)"
trap 'rm -rf "$LAB"' EXIT
export HOME="$LAB/home"
mkdir -p "$HOME"
export XDG_CONFIG_HOME="$HOME/.config"
export OPENCODE_DISABLE_EXTERNAL_SKILLS=1
export OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1

echo "install $PIN (isolé sous $LAB)"
npm install --prefix "$LAB/runner" "$PIN" >/dev/null 2>&1
OC="$LAB/runner/node_modules/.bin/opencode"
GOT="$("$OC" --version)"
check "version épinglée" "$WANT_VERSION" "$GOT"

# --- Projet synthétique ------------------------------------------------------
PROJ="$LAB/proj"
mkdir -p "$PROJ"
cd "$PROJ"

config() { OPENCODE_DISABLE_EXTERNAL_SKILLS=1 "$OC" debug config 2>/dev/null; }
model() { config | node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const j=JSON.parse(d);console.log(j.model||'')})"; }
field() { # field <expr> — extrait un champ JSONC de la config fusionnée
  config | node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const j=JSON.parse(d);console.log($1??'')})"
}

# C1: OPENCODE_CONFIG_CONTENT est consommé (mécanisme officiel, fusion finale)
export OPENCODE_CONFIG_CONTENT='{"model":"envc/model"}'
check "C1 contenu inline consommé" "envc/model" "$(model)"
unset OPENCODE_CONFIG_CONTENT

# C2: fusion champ par champ — le champ non défini par la source forte survit
cat > opencode.json <<'EOF'
{ "model": "proj/model", "small_model": "proj/small" }
EOF
export OPENCODE_CONFIG_CONTENT='{"model":"envc/model"}'
check "C2a inline gagne sur projet (model)" "envc/model" "$(model)"
check "C2b champ projet non touché survit (small_model)" "proj/small" "$(field "j.small_model")"
unset OPENCODE_CONFIG_CONTENT

# C3: config globale — survit champ par champ sous le projet
mkdir -p "$XDG_CONFIG_HOME/opencode"
cat > "$XDG_CONFIG_HOME/opencode/opencode.json" <<'EOF'
{ "small_model": "global/small", "permission": { "bash": { "dangerous.*": "deny" } } }
EOF
# le projet ne définit pas small_model: le champ global doit survivre
cat > opencode.json <<'EOF'
{ "model": "proj/model" }
EOF
check "C3a champ global survit (small_model)" "global/small" "$(field "j.small_model")"
check "C3b permission globale survit" "deny" "$(field "j.permission?.bash?.['dangerous.*']")"
check "C3c projet gagne sur global (model)" "proj/model" "$(model)"
# le projet définit small_model: le projet gagne sur le global
cat > opencode.json <<'EOF'
{ "model": "proj/model", "small_model": "proj/small" }
EOF
check "C3d projet gagne sur global (small_model)" "proj/small" "$(field "j.small_model")"

# C4: OPENCODE_CONFIG n'est PAS une surcharge — le projet gagne
cat > "$LAB/extra.json" <<'EOF'
{ "model": "explicit/model" }
EOF
export OPENCODE_CONFIG="$LAB/extra.json"
check "C4 OPENCODE_CONFIG perd contre la config projet" "proj/model" "$(model)"
unset OPENCODE_CONFIG
rm "$LAB/extra.json"

# C5: JSONC projet découvert, parsé, jamais réécrit
rm opencode.json
cat > opencode.jsonc <<'EOF'
{
  // commentaire JSONC qui doit survivre
  "model": "proj/jsonc-model"
}
EOF
check "C5a opencode.jsonc découvert et parsé" "proj/jsonc-model" "$(model)"
if grep -q 'commentaire JSONC qui doit survivre' opencode.jsonc; then
  ok "C5b JSONC non réécrit (commentaire intact)"
else fail "C5b JSONC réécrit"; fi

# C6: découverte depuis un sous-répertoire
mkdir -p sub
( cd sub && check "C6 découverte depuis sous-répertoire" "proj/jsonc-model" "$(model)" )

# C7: clé inconnue ignorée silencieusement (pas de rejet exploitable)
cat > opencode.jsonc <<'EOF'
{ "model": "proj/jsonc-model", "totally_unknown_key_xyz": true }
EOF
GOT="$(config | grep -c totally_unknown_key_xyz || true)"
check "C7 clé inconnue absente de la fusion" "0" "$GOT"

# C8: plugins auto-découverts exécutent du code (contrat de confiance)
cat > opencode.jsonc <<'EOF'
{ "model": "proj/jsonc-model" }
EOF
mkdir -p .opencode/plugin
cat > .opencode/plugin/auto.js <<EOF
import { writeFileSync } from "node:fs"
export default async () => {
  writeFileSync("$LAB/plugin-exec-proof.txt", "executed")
  return {}
}
EOF
rm -f "$LAB/plugin-exec-proof.txt"
config >/dev/null 2>&1
if [ -f "$LAB/plugin-exec-proof.txt" ]; then
  ok "C8a plugin auto-découvert (.js) exécute du code au chargement"
else fail "C8a plugin auto-découvert n'a pas exécuté"; fi

# C8b: OPENCODE_PURE=1 empêche l'exécution
rm -f "$LAB/plugin-exec-proof.txt"
OPENCODE_PURE=1 "$OC" debug config >/dev/null 2>&1
if [ -f "$LAB/plugin-exec-proof.txt" ]; then
  fail "C8b OPENCODE_PURE=1 n'empêche pas l'exécution"
else ok "C8b OPENCODE_PURE=1 empêche l'exécution"; fi

# C8c: OPENCODE_DISABLE_DEFAULT_PLUGINS=1 ne l'empêche pas
rm -f "$LAB/plugin-exec-proof.txt"
OPENCODE_DISABLE_DEFAULT_PLUGINS=1 "$OC" debug config >/dev/null 2>&1
if [ -f "$LAB/plugin-exec-proof.txt" ]; then
  ok "C8c OPENCODE_DISABLE_DEFAULT_PLUGINS=1 n'empêche pas l'exécution"
else fail "C8c OPENCODE_DISABLE_DEFAULT_PLUGINS=1 a empêché l'exécution (comportement changé)"; fi
rm -rf .opencode/plugin

# C9: plugins déclarés exécutent du code (même preuve, chemin déclaré)
cat > .opencode/declared.mjs <<EOF
import { writeFileSync } from "node:fs"
export default async () => {
  writeFileSync("$LAB/plugin-exec-proof.txt", "executed")
  return {}
}
EOF
cat > opencode.jsonc <<EOF
{ "model": "proj/jsonc-model", "plugin": ["./.opencode/declared.mjs"] }
EOF
rm -f "$LAB/plugin-exec-proof.txt"
config >/dev/null 2>&1
if [ -f "$LAB/plugin-exec-proof.txt" ]; then
  ok "C9 plugin déclaré exécute du code au chargement"
else fail "C9 plugin déclaré n'a pas exécuté"; fi
rm -f .opencode/declared.mjs

# C10: découverte de skills projet et global
cat > opencode.jsonc <<'EOF'
{ "model": "proj/jsonc-model" }
EOF
mkdir -p .opencode/skills/proj-skill
cat > .opencode/skills/proj-skill/SKILL.md <<'EOF'
---
name: proj-skill
description: Use when testing project skill discovery.
---
body
EOF
mkdir -p "$XDG_CONFIG_HOME/opencode/skills/global-skill"
cat > "$XDG_CONFIG_HOME/opencode/skills/global-skill/SKILL.md" <<'EOF'
---
name: global-skill
description: Use when testing global skill discovery.
---
body
EOF
SKILLS="$("$OC" debug skill 2>/dev/null | node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const j=JSON.parse(d);console.log(j.map(s=>s.name).filter(n=>n!=='customize-opencode').sort().join(','))})")"
check "C10 skills projet et global découverts" "global-skill,proj-skill" "$SKILLS"

# C11: OPENCODE_DISABLE_PROJECT_CONFIG ignore la config projet
GOT="$(OPENCODE_DISABLE_PROJECT_CONFIG=1 "$OC" debug config 2>/dev/null | node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const j=JSON.parse(d);console.log(j.model||'')})")"
check "C11 config projet désactivable" "" "$GOT"

# --- Cas réseau (opt-in) ----------------------------------------------------
if [ "${OPENCODE_INTEGRATION_NETWORK:-0}" = "1" ]; then
  if [ -z "${ALBERT_API_KEY:-}" ]; then
    echo "OPENCODE_INTEGRATION_NETWORK=1 mais ALBERT_API_KEY absent; cas réseau ignorés" >&2
  else
    cat > opencode.jsonc <<'EOF'
{
  "provider": {
    "albert": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "Albert API (État)",
      "options": {
        "baseURL": "https://albert.api.etalab.gouv.fr/v1",
        "apiKey": "{env:ALBERT_API_KEY}"
      },
      "models": {
        "deepseek-v4-flash": {
          "name": "DeepSeek V4 Flash (Albert)",
          "limit": { "context": 131072, "output": 65536 }
        }
      }
    }
  },
  "model": "albert/deepseek-v4-flash"
}
EOF
    cat > AGENTS.md <<'EOF'
# Instructions de test P01
Réponds toujours en français.
EOF
    OUT="$(ALBERT_API_KEY="$ALBERT_API_KEY" timeout 120 "$OC" run "réponds uniquement le mot: pret" 2>&1 || true)"
    case "$OUT" in
      *pret*) ok "N1 modèle effectif résolu + appel Albert réel + AGENTS.md chargé" ;;
      *) fail "N1 appel réel Albert (sortie: $(printf '%s' "$OUT" | tail -2))" ;;
    esac
    rm AGENTS.md
  fi
else
  echo "cas réseau ignorés (OPENCODE_INTEGRATION_NETWORK=1 pour les activer)"
fi

echo
echo "réussites: $PASS, échecs: $FAILURES"
[ "$FAILURES" -eq 0 ]
