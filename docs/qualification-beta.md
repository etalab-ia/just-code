# Qualification bêta P21

Ce protocole complète les workflows GitHub Actions. Les runners hébergés
qualifient les compilations natives, les tests unitaires, les installateurs,
le contrat OpenCode hôte et les fixtures. Ils ne constituent pas une preuve
de démarrage d'une microVM sur l'hyperviseur natif.

## Prérequis

- Utiliser un checkout de la révision candidate, avec son tag/version noté.
- Utiliser une machine physique ou un hôte de virtualisation dédié, pas une
  VM imbriquée non qualifiée.
- Installer OpenCode et Microsandbox conformément au README. Pour Microsandbox,
  `just-code start --microsandbox` installe le runtime géré si nécessaire ;
  `just-code doctor --microsandbox` est en lecture seule et ne l'installe pas.
- Pour un test réel de transport Albert, utiliser une clé de test révocable.
  Ne jamais mettre une clé dans le rapport ou les artefacts de CI.
- Prévoir un checkout jetable contenant un dépôt Git synthétique pour les
  parcours qui créent ou modifient un projet.

## Parcours sur chaque hôte

1. Noter OS, architecture, matériel ou hyperviseur natif, version du CLI,
   version du runtime et date. Lancer `just-code version`.
2. Vérifier le diagnostic sans effet de bord : définir `$MSB_HOME` vers un
   répertoire temporaire vide et retirer `MSB_PATH` et
   `MSB_LIBKRUNFW_PATH`, puis lancer
   `just-code doctor --microsandbox`. La commande doit échouer en indiquant
   que le runtime manque, sans créer de fichier. Répéter après installation
   et confirmer que le runtime est diagnostiqué. Lancer aussi
   `just-code setup doctor` et `just-code setup doctor --json`; les deux
   rapports doivent être cohérents et ne contenir aucune valeur secrète.
   Conserver localement le JSON comme pièce d'évidence; le relire et le
   redacter avant de le joindre à un ticket ou à un compte rendu.
3. Les workflows GitHub Actions fournissent les preuves de build et des tests
   unitaires par architecture. Ne lancer `go test ./...` que depuis un
   checkout contributeur avec Go et les outils C requis; son absence sur un
   poste d'atelier ne bloque pas le test du binaire installé.
4. Avec un projet Git jetable, tester le parcours utilisateur vide :
   `just-code init`, puis lancer `just-code --microsandbox --isolation full`.
   Après démarrage, exécuter `just-code check --isolation full`. Vérifier que
   le TUI tourne dans l'invité et que `/workspace` est invité, pas un montage
   du checkout hôte.
5. Créer un fichier-canari non secret dans le checkout hôte et un fichier
   `.env` factice contenant `ALBERT_API_KEY=host-canary-not-a-secret`. Après
   synchronisation ou actualisation, vérifier depuis l'invité que le canari
   filtré n'est pas lisible. Ne pas utiliser une vraie valeur de secret.
6. Modifier un fichier dans l'invité, lancer `just-code workspace export` pour
   produire un diff, revoir ce diff, puis appliquer uniquement les changements
   retenus. Confirmer qu'aucune écriture directe n'a touché le checkout avant
   l'application explicite.
7. Tester `just-code stop`, le redémarrage non destructif et la reconnexion.
   Confirmer que l'état annoncé comme persistant l'est, sans recréation
   implicite.
8. Répéter dans un projet existant avec des fichiers de configuration et du
   texte utilisateur préexistants. Pour le parcours importé, utiliser
   `just-code import albert-code` d'abord en aperçu, puis appliquer seulement
   après revue. Vérifier que l'import répété est stable et que les réglages
   inconnus/non gérés restent intacts.
9. Cloner un dépôt d'équipe jetable qui contient une configuration
   just-code versionnée. Vérifier `just-code check`, un `just-code update`
   sans changement attendu, puis le démarrage et l'export de changements dans
   une branche de test. Ne pas utiliser un dépôt contenant des secrets ou du
   contenu de production.
10. Sur macOS arm64 uniquement, exécuter les parcours réseau de
    `tests/integration/credentials/README.md` dans le laboratoire dédié et avec
    une clé de test révocable. Distinguer un échec de préparation du laboratoire
    d'un échec d'assertion. Le laboratoire documenté dépend des interfaces et
    commandes macOS : ne pas présenter ce résultat comme preuve de transport
    sur Linux ou Windows.
11. Pour les runtimes explicitement revendiqués, exécuter leurs diagnostics et
    un cycle start/stop/reprise séparément : Tart sur macOS arm64 ; agent-vm sur
    macOS/Linux avec Lima. Aucun résultat de ces runtimes ne qualifie
    Microsandbox.

## Protocoles par plateforme

Exécuter les étapes communes 1 à 11 sur chaque hôte, puis les contrôles
spécifiques ci-dessous. Une cible sans runtime disponible ou avec un
prérequis d'hyperviseur absent est « non qualifiée », pas un échec de
just-code : noter l'écart dans la matrice.

### macOS arm64

Prérequis : Apple Silicon, Hypervisor.framework, OpenCode, just-code.

1. Microsandbox : installer par `just-code start --microsandbox`, vérifier
   `just-code doctor --microsandbox`, lancer le parcours en isolation `full`,
   puis `just-code stop`.
2. Tart : installer Tart explicitement, vérifier `just-code doctor --tart`,
   lancer `just-code start --tart --isolation backend`, vérifier la connexion
   du TUI hôte et l'accès au endpoint local, puis `just-code stop --tart`.
3. agent-vm : installer Lima, vérifier `just-code doctor --agent-vm`, lancer
   `just-code start --agent-vm --isolation backend`, vérifier que le template
   est construit une seule fois et que le redémarrage conserve l'état, puis
   `just-code stop --agent-vm`.
4. Noter si l'hôte est une machine de l'utilisateur, une machine CI dédiée,
   ou un Mac distant géré, car la confiance n'est pas équivalente.

### Linux amd64

Prérequis : `/dev/kvm` ouvert par l'utilisateur, OpenCode, just-code.

1. Vérifier `test -r /dev/kvm -a -w /dev/kvm` ; le résultat et la commande
   doivent apparaître dans le rapport.
2. Microsandbox : installer par `just-code start --microsandbox`, vérifier
   `just-code doctor --microsandbox`, lancer le parcours en isolation `full`,
   puis `just-code stop`.
3. agent-vm : installer Lima explicitement, vérifier `just-code doctor
   --agent-vm`, lancer `just-code start --agent-vm --isolation backend`, puis
   `just-code stop --agent-vm`. Ne pas déduire le succès Microsandbox du
   succès Lima.
4. Si l'hôte est une VM cloud, ne qualifier Microsandbox que lorsque KVM est
   réellement disponible dans l'invité ; sinon la preuve est invalide.

### Linux arm64

Prérequis et parcours identiques à Linux amd64, mais l'architecture native
doit être confirmée (`uname -m` = `aarch64`). Ne pas accepter un test amd64
émulation comme preuve arm64.

### Windows amd64

Prérequis : Windows 10 ou 11 x64, Windows Hypervisor Platform activée,
OpenCode, just-code.

1. Vérifier la fonctionnalité WHP dans les fonctionnalités Windows, puis
   redémarrer si elle vient d'être activée.
2. Exécuter le protocole commun dans PowerShell, y compris le parcours
   d'installation avec `install.ps1`.
3. Microsandbox : installer par `just-code start --microsandbox`, vérifier
   `just-code doctor --microsandbox`, lancer le parcours en isolation `full`,
   puis `just-code stop`.
4. Ne pas remplacer WHP par WSL2 ou Hyper-V embarqué sans preuve : le backend
   annoncé est WHP.

### Windows arm64

Prérequis : Windows 10 ou 11 arm64, WHP, OpenCode, just-code.

1. Confirmer l'architecture native (`$env:PROCESSOR_ARCHITECTURE` = `ARM64`).
2. Exécuter le protocole commun dans PowerShell, y compris `install.ps1`.
3. Microsandbox : installer par `just-code start --microsandbox`, vérifier
   `just-code doctor --microsandbox`, lancer le parcours en isolation `full`,
   puis `just-code stop`.
4. Les runtimes Tart et agent-vm ne sont pas qualifiés sur Windows arm64 ;
   ne pas marquer cette combinaison comme supportée.

## Matrice de preuve

Compléter une ligne par combinaison réellement testée. « Non testé » et
« bloqué par l'hôte » ne valent pas « réussi ».

| OS / architecture | Hôte / contexte | Runtime / backend natif | Version CLI / runtime | `setup doctor` | Doctor runtime | Parcours P21 | Résultat / lien vers preuve |
|---|---|---|---|---|---|---|---|
| macOS arm64 | | Microsandbox / Apple Virtualization | | | | | |
| macOS arm64 | | Tart / Apple Virtualization | | | | | |
| macOS arm64 | | agent-vm / Lima | | | | | |
| Linux amd64 | | Microsandbox / KVM | | | | | |
| Linux arm64 | | Microsandbox / KVM | | | | | |
| Linux amd64 ou arm64 | | agent-vm / Lima | | | | | |
| Windows amd64 | | Microsandbox / WHP | | | | | |
| Windows arm64 | | Microsandbox / WHP | | | | | |

Pour chaque résultat, conserver les commandes, le code de sortie et les
assertions pertinentes. Masquer les clés, mots de passe, jetons, identifiants
de comptes et chemins personnels avant de partager une sortie. Ne pas joindre
les journaux bruts d'un laboratoire TLS : ils peuvent contenir les valeurs de
test envoyées aux serveurs.

## Interprétation

- CI verte prouve les cas exécutés par ses jobs, pas la disponibilité d'un
  hyperviseur ni le fonctionnement d'une microVM.
- Un échec avant le démarrage du runtime est un échec de préparation, pas une
  preuve sur le comportement invité.
- Une combinaison sans preuve reste « non qualifiée » ; ne pas l'annoncer
  comme supportée sur la seule base d'une compilation native.
- Toute violation du filtre de transfert, tout montage hôte présent dans un
  chemin Microsandbox scellé, toute mutation par `doctor` ou toute fuite de
  secret bloque la qualification jusqu'à correction et nouvelle exécution.
