# Qualification Linux amd64 — preuves conservées

Exécution du protocole `docs/qualification-beta.md` (étapes communes 1 à 9,
parcours secrets 1 à 7, matrice terminal/annulation, ligne installateur) sur
un hôte Linux amd64 jetable :

- Hôte : instance Scaleway DEV1-S (2 vCPU, 1,9 Gio, Ubuntu 22.04, fr-par-1),
  KVM imbriqué réel (`/dev/kvm` accessible en lecture/écriture, indicateur
  `svm` présent). La ligne Linux amd64 du protocole exige ce point : une
  microVM démarrée sans KVM réel ne vaut pas preuve.
- Révision qualifiée : `db9b8fcd2d270f32f8b5848b1a6586a1e15bc42d`
  (`just-code v0.10.0`, binaire compilé sur place en Go 1.27.2 ; la ligne
  installateur utilise le binaire officiel de la release, SHA256 vérifié).
- Date : 2026-10-16. Chaque fichier porte une ligne `META` avec l'horodatage
  et, pour les étapes qui s'y prêtent, la révision qualifiée.
  Note de traçabilité : les lignes `META` des fichiers `06` à `24` ont été
  complétées avec le champ `source` après l'exécution (même journée, même
  révision, vérifiée dans `03-setup-doctor.txt` et `23b-installer.txt`), en
  réponse à la revue de la PR ; les contenus d'exécution sont inchangés.

## Lecture des fichiers

| Fichier | Étape du protocole | Résultat |
| --- | --- | --- |
| `00-host-inventory.txt` | 1 (inventaire, KVM, versions) | conforme |
| `01-doctor-missing-runtime.txt` | 2 (doctor sans runtime, sans effet de bord) | conforme |
| `02-auth-add-albert.txt` | prérequis identifiant Albert (stdin, `--fallback`) | conforme |
| `03-setup-doctor.txt` | 2 (`setup doctor` texte et JSON, aucune valeur secrète) | conforme |
| `04-init.txt` | 4 (`init` non interactif ; refus sans `--root` puis succès) | conforme |
| `05-first-start.txt` | 4 (premier démarrage microVM : 33,8 s tout compris) | conforme |
| `06-check.txt` | 4 (`check --isolation full`) | conforme |
| `07-workspace-filter.txt` | 5 (filtre de transfert : dotenv, ignorés, gitleaks) | conforme |
| `08-export-sync.txt` | 6 (export → revue → application → `sync --force`) | conforme |
| `09-lifecycle.txt` | 7 (stop, redémarrage 5,7 s, état conservé) | conforme |
| `10-doctor-canary.txt` | 2 reprise + 5 (doctor post-install, canari `ALBERT_API_KEY=host-canary-not-a-secret` invisible depuis l'invité) | conforme |
| `11-tui-in-guest.txt`, `11-tui-launch.typescript` | 4 (le TUI tourne dans l'invité : processus `opencode` observé dans la microVM pendant l'attache) | conforme |
| `12-import-preview.txt`, `12b-import-no-init.txt`, `13-import-apply.txt` | 8 (projet préexistant, `import albert-code` aperçu/application/répétition, réglages non gérés intacts) | conforme |
| `14-team-repo.txt` | 9 (clone d'équipe : check, `update` sans changement, démarrage, export sur branche de test) | conforme |
| `15-secrets-import.txt` | secrets 1 (aperçu masqué, doublons signalés, refus non-TTY sans `--yes`, sources inchangées, `--fallback` requis et appliqué) | conforme, voir écarts ci-dessous |
| `16-bindings.txt`, `16b-bindings.txt` | secrets 2 et 3 (approbations proxy et lisible-invité, classes de valeurs livrées/refusées) | conforme |
| `17-delivery.txt`, `17b-delivery-after-storefix.txt` | incident : démarrage impossible avec un index importé en `--fallback` | **défaut** (#199) |
| `18-native-delivery.txt` | secrets 2 et 3 avec magasin natif : marqueur seul dans `.env`, valeurs réelles 0600 dans `classes.env`, **substitution réelle vers httpbin.org** depuis l'invité | conforme |
| `19-negative-substitution.txt` | secrets 2 (contrôle négatif : requête portant un marqueur vers un hôte non approuvé coupée par le proxy) | conforme |
| `20-lifecycle.txt`, `20b-cross-profile.txt` | secrets 4 à 6 (refus de `--clear`, rotation, révocation conformes ; inter-profils : échec explicite sur création mais tolérance sur démarrage no-op) | **partiel** (#204) |
| `21-locked-store.txt` | secrets 7 (magasin indisponible : démarrage échoue explicitement, liaisons préservées) | conforme |
| `22-terminal-matrix.txt`, `22b-terminal-matrix.txt` | terminal/annulation (40 et 200 colonnes en PTY vérifié, Ctrl-C, idempotence ; `22b` est l'exécution qualifiante, `22` est la première tentative conservée pour la traçabilité) | conforme |
| `23b-installer.txt` | installateur officiel pipe `curl \| sh`, binaire `v0.10.0`, SHA256 vérifié | conforme |
| `24-agent-vm*.txt`, `24-agent-vm-*.log` | 11 (agent-vm / Lima : doctor, construction du modèle, démarrage, arrêt) | **partiel, limité par l'hôte** |

## Écarts constatés pendant l'exécution

Chacun fait l'objet d'une issue ; ce résumé ne remplace pas leur description.

1. **#199 — Secrets importés avec `--fallback` irrésolubles au démarrage** : sur un
   hôte Linux sans session D-Bus, l'import exige `--fallback` (le magasin
   natif est verrouillé), mais `start` résout toujours le magasin natif et
   échoue. Contournement utilisé pour la suite du protocole :
   `dbus-run-session` + `gnome-keyring-daemon` déverrouillé, réimport dans le
   magasin natif.
2. **#200 — Image agent-vm par défaut invalide** : `template:debian-13` n'existe pas
   sous Lima 1.2.1 (syntaxe `template://` requise, `debian-13` déplacé sous
   `experimental/`). Contournement : `AGENT_VM_IMAGE=template://debian-12`.
3. **#201 — `start --agent-vm` ignore l'identifiant Albert du magasin de repli** :
   la résolution ne consulte que le magasin natif. Contournement : variable
   `ALBERT_API_KEY` dans l'environnement.
4. **#201 — Dimensionnement agent-vm par défaut trop grand pour un petit hôte** :
   4 Gio / 2 vCPU échoue (`Cannot allocate memory`) sur une machine de
   1,9 Gio. Contournement : `AGENT_VM_MEMORY_GB=1 AGENT_VM_CPUS=1`.
5. **#202 — Qualité des messages** : `go --version` sondé (exit 2) au lieu de
   `go version` ; erreur de réconciliation affichant `(<nil>)` au lieu de la
   cause ; `secret-service` annoncé « accessible » sur une sonde en lecture
   alors que les écritures échouent ; `--fallback` absent de la ligne d'usage
   de `secrets bindings`.
6. **#204 — Démarrage no-op tolérant une liaison hors profil actif** : sur une
   instance déjà en cours, `start` avec un profil actif différent des liaisons
   réussit (exit 0, avertissement seul) en conservant les secrets de l'ancien
   profil ; seul le chemin de création échoue explicitement.

## agent-vm (Lima) sur cet hôte

Le parcours agent-vm aboutit partiellement, pour des raisons de capacité de
l'hôte et non du produit :

- `doctor --agent-vm` : conforme (Lima 1.2.1 détecté ; la racine sous
  `/root` est refusée par Lima — le parcours s'exécute sous un utilisateur
  non privilégié, ce qui est une exigence de Lima, pas de just-code) ;
- construction du modèle de base : réussie avec les contournements #200
  (image) et #201 (dimensionnement : 1 Gio / 1 vCPU sur cet hôte de 1,9 Gio) ;
- démarrage de la VM gérée : réussi (5 min 31 s au premier démarrage,
  modèle inclus) ; le serveur OpenCode répond sur `/global/health` après
  ~40 s (HTTP 401, authentification requise) ;
- tenue de charge : sur 1 vCPU / 1 Gio, le serveur devient ensuite muet
  (délais dépassés sur `/provider` puis `/global/health`). `just-code check`
  ne peut pas le confirmer dans ce dimensionnement ;
- arrêt / redémarrage / arrêt : conformes, état Lima propre.

La ligne reste donc « partiel, limité par l'hôte » : la preuve de santé
interactive du backend demande un hôte plus grand (le dimensionnement par
défaut du modèle, 4 Gio / 2 vCPU, ne rentre pas dans 1,9 Gio).

## Magasin d'identifiants

Les transcripts du magasin Secret Service réel (deux exécutions
`--- PASS`, plus une exécution hors session documentant le comportement
« collection verrouillée ») vivent dans
`tests/integration/credentials/evidence/`, conformément à la convention de ce
répertoire.
