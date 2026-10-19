# Qualification live — macOS arm64 (Apple Silicon)

Exécutée le 2026-10-16 sur un **Scaleway Mac mini M4** (`jc-qualif-mac1`,
macOS Tahoe 26.7.1, Apple Virtualization.framework, machine nue sans
hyperviseur, louée à l'heure pour la qualification). Source qualifiée :
`db9b8fcd2d270f32f8b5848b1a6586a1e15bc42d` (v0.10.0). Chaque transcript
porte une ligne `META ... source=<sha>` ; les transcripts du référentiel
d'identifiants portent `revision: <sha>`.

## Résultat

**La plateforme est qualifiée pour le runtime Microsandbox et le trousseau
macOS natif.** Le runtime Tart est **bloqué par l'hôte** (contrainte
environnement, pas défaut produit — voir ci-dessous). agent-vm (Lima)
fonctionne avec l'initialisation lente documentée.

## Fichiers

| Fichier | Étape du protocole | Résultat |
|---|---|---|
| `30-mac-installer.txt` | Installation officielle (`scripts/install.sh` pipe) | v0.10.0 installé |
| `31-mac-auth-doctor.txt` | `auth add` + `setup doctor` | **première tentative refusée** (`AUTH_EXIT: 1`, -25308 : trousseau verrouillée, interaction impossible en headless) ; doctor structurel OK |
| `31b-mac-auth-after-unlock.txt` | `auth status` après `security unlock-keychain` | trousseau dédiée déverrouillée : `albert: stored`, sortie 0 |
| `32-mac-init-start.txt`, `32b-mac-start.txt` | Porte microVM (init, premier `start`, `check`) | microVM démarrée (~9 s), `check` OK |
| `33-mac-common.txt` | Filtre transfert, export→review→apply→sync, cycle stop/start, état | filtres dotenv/gitignore/gitleaks OK ; round trip OK ; redémarrage ~1,4 s |
| `34-mac-secrets.txt`, `34b-mac-delivery.txt`, `34c-mac-substitution.txt` | Secrets : import, classes, livraison, substitution, rotation, révocation, verrou | matrice complète sur trousseau native |
| `37-mac-tui-terminal.txt` | TUI dans l'invité + matrice terminal mode ligne (40/200 cols, tailles vérifiées) | opencode visible dans l'invité ; confirmations correctes |
| `46c-mac-terminal-interactive.txt` + `46c-*.typescript` | matrice terminal **mode interactif huh** (pty `script`, Ctrl-C à 40/200 cols, annulation « n », contrôle positif « y ») | Ctrl-C annule l'invite proprement aux deux largeurs (`user aborted`, sortie 1 — le script(1) macOS ne propage pas le statut, l'exit interne est consigné dans le typescript) ; « n » annule sans rien persister ; le contrôle « y » stocke et prouve que le contrôle de persistance discrimine (46/46b remplacés : exit 127 faute de `timeout`, puis exit externe script(1) non probant). Pas d'invite d'approbation en v0.10.0 (#209) ; un `approve` en échec laisse un état partiel (#210) |
| `38-lab-version.txt`, `39-*.txt` | Laboratoire identifiants réseau (matrice T1-T11) | **14/15** avec pin et dns corrigés — voir #205 |
| `42-agentvm.txt` | agent-vm (Lima) doctor/start/health/stop | VM démarre, OpenCode sain après ~3 min d'initialisation |
| `43-44-import-team.txt` | Import Albert Code + cycle dépôt d'équipe | preview/apply/idempotence/préservation OK |
| `35-tart-doctor.txt`, `tart-start2.log`, `tart-start3.log`, `tart-manual-run.log` | Runtime Tart | **bloqué par l'hôte** : VZ refuse la création de la clé hôte (trousseau login non déverrouillable en headless) — voir #206 |
| `tart-start*.log`, `tart-manual-run.log` | journaux bruts Tart | conservés pour le diagnostic |

## Points notables

- **Trousseau** : sur un hôte headless, le trousseau `login` n'est pas
  déverrouillable (mot de passe inconnu, hôte provisionné). La
  qualification a utilisé un trousseau dédié `qualif.keychain-db`
  (créé, déverrouillé, auto-verrouillage désactivé), défini comme
  trousseau par défaut. Toutes les opérations du produit (auth, secrets,
  matrice du référentiel) passent par ce trousseau natif.
- **MicroVM** : premier démarrage ~9 s (l'affichage tronqué par `tail`
  masque les messages d'installation du runtime ; la preuve du boot est
  `msb list` + `check`).
- **Tart** : image `macos-tahoe-base` clonée (4 min), puis échec de
  démarrage — Virtualization.framework ne peut pas créer la clé hôte de
  la VM sans trousseau de session déverrouillé (`VZErrorDomain -9`).
  Diagnostic produit à améliorer (#206) ; contournement : session GUI.
- **agent-vm (Lima)** : pilote `vz`, aarch64. Le serveur OpenCode met
  **~3 minutes** à écouter après le démarrage de la VM ; un `check`
  immédiat échoue (connexion refusée) puis réussit une fois le serveur
  prêt (`{"healthy":true,"version":"1.19.3"}`). À documenter pour les
  utilisateurs (et éventuellement délai d'attente côté produit).
- **Laboratoire identifiants** : `pin.json` exigeait msb 0.7.3 alors que
  la release embarque 0.7.6 ; avec le pin corrigé, 7/15 échouaient à
  cause du REFUSED AAAA de `lab/dns.py` (busybox wget abandonne) ;
  avec le correctif NODATA, **14/15**. Reste T6 : en 0.7.6 un marqueur
  en query est **livré** (placeholder littéral, sans valeur) au lieu
  d'être bloqué comme violation. Détail et preuves : #205.

## Hôte

Machine distante gérée par Scaleway (Apple Silicon à l'heure). L'accès
SSH est par clé dédiée jetable ; le mot de passe du trousseau `login`
n'est pas connu (provisionnement), ce qui motive le trousseau dédié.

## Défauts et observations associés

- #205 — harnais credentials : pin périmé, AAAA REFUSED, T6 changé
- #206 — Tart : le timeout « guest agent » masque un refus VZ (trousseau)
- #201 — résolution des identifiants native-only (contournée ici en
  déverrouillant le trousseau dans la même session)
