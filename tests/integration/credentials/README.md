# Harnais d'intégration identifiants Microsandbox (P02)

Rejoue la matrice de caractérisation du transport des identifiants
(décision `docs/decisions/2026-09-24-microsandbox-credential-transport.md`)
contre un vrai runtime Microsandbox épinglé, sur macOS (libkrun).

## Usage

```sh
# cas non réseau (persistance de la référence source) — sans laboratoire
tests/integration/credentials/run.sh

# cas réseau (substitution, violations, rotation) — opt-in, laboratoire requis
MSB_CREDENTIAL_INTEGRATION_NETWORK=1 tests/integration/credentials/run.sh
```

Le harnais crée une sandbox jetable `p02-cred-harness` (image `alpine`),
exécute les cas T1-T11 du décision record, puis la supprime. Un échec
d'assertion sort non-zéro avec le nom du cas. Un échec de **préparation**
(laboratoire incomplet, `msb` absent, IP LAN indisponible) sort avec le
code 2 et un message distinct : il n'est jamais compté comme un échec de
transport.

## Laboratoire local

Les cas réseau exigent un serveur TLS écho, un serveur DNS et une CA sous
`$MSB_CREDENTIAL_LAB` (défaut `/tmp/p02-lab`) :

- `ca.crt` / `ca.key` : CA d'interception TLS (aussi utilisée en amont) ;
- `srv.crt` / `srv.key` : certificat serveur avec SANs `DNS:p02lab.test`,
  `DNS:evil.test`, `IP:<IP-LAN-hôte>`, `IP:172.16.0.5`, `IP:127.0.0.1` ;
- `server.py` : écho HTTPS sur `0.0.0.0:8443`, journalise chaque requête en
  JSON (`{method, path, headers, body}`) dans `requests.log`, répond 302 vers
  `https://evil.test:8443/redirect-target` sur `/redirect-to-evil` ;
- `dns.py` : DNS UDP sur `0.0.0.0:5354`, répond `p02lab.test` et
  `evil.test` -> IP LAN hôte, REFUSED (rcode 5) pour les autres noms afin que
  le résolveur bascule sur le fallback (1.1.1.1).

L'IP LAN de l'hôte est lue sur `en0`. Le moteur Microsandbox doit résoudre
lui-même les noms autorisés (les entrées `/etc/hosts` invité ne suffisent
pas), d'où le serveur DNS dédié.

## Épinglage

Les versions testées sont épinglées dans `pin.json` (CLI msb, SDK Go, image
invité). Le harnais échoue si la version CLI ne correspond pas. La matrice
doit être reproduite et le décision record mis à jour avant toute montée de
version du runtime.

## Cas non couverts par le harnais

- Le test d'écriture Git HTTPS réel (clone + push sur dépôt privé jetable)
  et les appels Albert réels ont été menés manuellement dans la session
  P02 ; ils exigent des identifiants réels et ne sont pas rejouables en CI.
  Voir le décision record, section Méthode.
- Le canal body (substitution désactivée par défaut) et le HTTP non-TLS ne
  sont pas rejoués : le premier suit le même chemin que query (violation),
  le second est hors du modèle `require_tls_identity`.
