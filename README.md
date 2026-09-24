```
       _            __                    __
      (_)_  _______/ /_   _________  ____/ /__
     / / / / / ___/ __/  / ___/ __ \/ __  / _ \
    / / /_/ (__  ) /_   / /__/ /_/ / /_/ /  __/
 __/ /\__,_/____/\__/   \___/\____/\__,_/\___/
/___/
```

[![CI](https://github.com/etalab-ia/just-code/actions/workflows/ci.yml/badge.svg)](https://github.com/etalab-ia/just-code/actions/workflows/ci.yml)
[![version](https://img.shields.io/github/v/release/etalab-ia/just-code?label=version&sort=semver)](https://github.com/etalab-ia/just-code/releases/latest)
[![go](https://img.shields.io/github/go-mod/go-version/etalab-ia/just-code?label=go)](https://go.dev/dl/)
[![licence](https://img.shields.io/github/license/etalab-ia/just-code?label=licence)](LICENSE)
![statut](https://img.shields.io/badge/statut-exp%C3%A9rimental-orange)
![plateformes](https://img.shields.io/badge/plateformes-darwin%20%7C%20linux%20%7C%20windows-blue)

⚠️ **PROJET EXPÉRIMENTAL : bac à sable de R&D pour évaluer l'exécution distante d'agents de code avec Albert API. Rien de stable, tout peut changer.** ⚠️

# just code

**Un backend OpenCode isolé dans une microVM Microsandbox ou une VM macOS Tart, piloté par le TUI OpenCode natif de ta machine.**

L'agent tourne dans un environnement Linux (ou macOS via Tart) avec les modèles souverains d'[Albert API](https://albert.api.etalab.gouv.fr), pendant que tu gardes ton interface habituelle. Tu peux comparer une microVM Microsandbox ou une VM macOS Tart sans changer de workflow.

```text
terminal hôte (opencode attach) ──> sandbox sélectionné :4096 (opencode serve)
                                            ├── /workspace  = ton projet (bind-mount)
                                            ├── outils      = bash, git, node, python
                                            └── inférence   = Albert API (deepseek-v4-flash)
```

Ce n'est **pas un produit** : c'est un terrain de jeu pour mesurer l'UX (latence, reconnexion, persistance de session, previews web) et comparer les frontières d'isolation.

## Installation

### Binaire précompilé (recommandé)

Chaque [release](https://github.com/etalab-ia/just-code/releases) publie des binaires pour `darwin-arm64`, `linux-arm64`, `linux-x64`, `windows-x64.exe` et `windows-arm64.exe`, avec un fichier `SHA256SUMS`.

Exemple sur Mac Apple Silicon :

```bash
curl -fsSLO https://github.com/etalab-ia/just-code/releases/latest/download/just-code-darwin-arm64
curl -fsSLO https://github.com/etalab-ia/just-code/releases/latest/download/SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing
chmod +x just-code-darwin-arm64
sudo mv just-code-darwin-arm64 /usr/local/bin/just-code
```

Adapte le nom de l'asset à ta plateforme (`just-code-linux-x64`, `just-code-linux-arm64`, `just-code-windows-x64.exe`, `just-code-windows-arm64.exe`). Sur Linux, remplace `shasum -a 256` par `sha256sum`. macOS Intel n'est pas publié (le SDK Microsandbox ne fournit pas de bibliothèque FFI pour `darwin/amd64`).

Les binaires macOS ne sont ni signés ni notariés, et portent une signature ad-hoc (requise pour exécuter un binaire non signé sur Apple Silicon). `curl` ne pose pas l'attribut `com.apple.quarantine`, donc Gatekeeper ne bloque pas ce chemin d'installation ; un téléchargement via navigateur reste à débloquer avec `xattr -d com.apple.quarantine <binaire>`. La notarisation complète exigerait une adhésion Apple Developer et un certificat Developer ID.

### Dépendances de l'hôte

- OpenCode CLI sur l'hôte (`npm install -g opencode-ai`)
- Une **clé Albert API** dans l'environnement (ou dans `.env`, ignoré par git)
- L'un des runtimes disponibles :
  - [Microsandbox](https://github.com/superradcompany/microsandbox) sur un Mac Apple Silicon, Linux (KVM) ou Windows arm64/x64 (Windows Hypervisor Platform / WHP) ; l'exécutable `msb` n'a pas besoin d'être installé ;
  - [Tart](https://github.com/openai/tart) (`brew install openai/tools/tart`) sur un Mac Apple Silicon pour les environnements de dev macOS (notamment Xcode / iOS) ;
  - [agent-vm](https://github.com/sylvinus/agent-vm) ([Lima](https://lima-vm.io) requis) sur macOS ou Linux : une VM Debian persistante par workspace, clonée depuis un template de base construit par just-code.

`just-code` embarque le SDK Go Microsandbox. Au premier `start` ou `doctor`, il télécharge automatiquement la version correspondante du runtime depuis la [release amont](https://github.com/superradcompany/microsandbox/releases/tag/v0.7.2), une release immuable couverte par une attestation de release GitHub vérifiable, sous `$MSB_HOME` si cette variable est définie, sinon sous `~/.microsandbox/`. L'URL et l'empreinte SHA-256 attendue pour chaque plateforme sont gravées dans le binaire : l'archive est vérifiée avant toute décompression, puis le SDK contrôle encore la présence des fichiers et la version de `msb`. Le chemin géré n'a pas besoin d'être ajouté au `PATH`.

```bash
just-code doctor --microsandbox
```

Pour fournir un runtime installé et vérifié par un autre mécanisme (poste administré, cache interne ou environnement sans accès à GitHub), renseigne ensemble les deux chemins directs :

```bash
MSB_PATH=/chemin/vers/msb \
MSB_LIBKRUNFW_PATH=/chemin/vers/libkrunfw \
just-code doctor --microsandbox
```

`MSB_PATH` doit rapporter exactement `msb 0.7.2`. Dans ce mode manuel, `just-code` ne télécharge aucun artefact et la confiance dans les deux fichiers relève de leur mécanisme de provisionnement.

Le téléchargement ne modifie pas la configuration de l'hôte. Sous Linux, KVM doit être accessible. Sous Windows, active **Windows Hypervisor Platform** dans les fonctionnalités Windows puis redémarre si elle ne l'est pas déjà.

### Depuis les sources (contributeurs)

Prérequis : [Go](https://go.dev) >= 1.22 et une chaîne C native (Xcode Command Line Tools sur macOS, `gcc` sur Linux, MinGW-w64 x64 ou LLVM-MinGW sur Windows ; sous Windows arm64, la chaîne doit cibler `aarch64-w64-mingw32`).

```bash
go build -o just-code ./cmd/just-code
go test ./...
```

## Démarrage rapide

Pour un premier lancement, exporte la clé Albert API dans ton terminal.

macOS / Linux :

```bash
export ALBERT_API_KEY="ta-clé"
just-code --microsandbox
```

Windows (PowerShell) :

```powershell
$env:ALBERT_API_KEY = "ta-clé"
just-code --microsandbox
```

Sur macOS et Linux, sans autre configuration, chaque commande qui cible un runtime exige `--microsandbox`, `--tart` ou `--agent-vm`. Sur Windows, Microsandbox est le seul runtime pris en charge et il est sélectionné par défaut. La commande `just-code` démarre le backend, attend qu'il soit prêt, puis attache le TUI OpenCode natif.

Pour rendre la clé persistante et enregistrer le runtime, le workspace ou d'autres réglages, consulte la section [Configuration](#configuration).

Le premier démarrage d'un sandbox Microsandbox installe ~384 Mio de paquets dans la microVM et peut dépasser largement une minute ; les démarrages suivants sont rapides.

Une fois attaché, la section [Prompts d'exemple](#prompts-dexemple) propose des prompts qui exercent les dimensions clés de l'expérience.

## Prompts d'exemple

Une fois attaché, ces prompts exercent les dimensions clés de l'expérience :

1. **« Qu'y a-t-il dans ce workspace ? Décris la structure du projet. »** — inférence et outils fichiers de base.
2. **« Sur quel OS, noyau et architecture tournes-tu ? »** — confirme que l'agent s'exécute dans le sandbox Linux, pas directement sur ton Mac.
3. **« Code un petit jeu snake servi par un serveur Node sur le port 3000, puis lance-le. »** — écriture de fichiers + serveur de dev ; ouvre `http://localhost:3000` pour vérifier la preview.
4. **« Écris un script python qui affiche la suite de Fibonacci et exécute-le. »** — toolchain polyglotte dans le sandbox.
5. **« Modifie un fichier, puis annule ta modification. »** — outils d'édition et revue de diff.
6. **Détache-toi (Ctrl+C / quitte), conserve le runtime, puis relance `just-code --microsandbox` ou `just-code --tart`** — continuité de session et reconnexion.
7. **« Envoie les logs de ton serveur de dev dans /tmp/server.log et montre-moi les dernières lignes. »** — comportement du scratch space hors du répertoire projet.

## Configuration

Place le fichier `.env` dans le répertoire depuis lequel tu lances `just-code`. Le CLI cherche d'abord à cet endroit, puis à côté de l'exécutable ; pour un binaire installé globalement, utilise le répertoire de lancement plutôt que `/usr/local/bin` ou équivalent.

Configuration courante :

```dotenv
ALBERT_API_KEY=ta-clé
RUNTIME=microsandbox
WORKSPACE_DIR=/chemin/absolu/vers/ton-projet
```

Tu peux omettre tous les réglages sauf `ALBERT_API_KEY` et continuer à choisir le runtime avec `--microsandbox`, `--tart` ou `--agent-vm`.

Vérifie que `.env` est ignoré par Git avant d'y enregistrer ta clé. Si ton projet possède déjà son propre `.env`, tu peux conserver les réglages just-code dans les variables exportées par ton shell afin de ne pas mélanger les deux configurations.

### Variables disponibles

| Variable | Requise | Valeur par défaut | Rôle |
| --- | --- | --- | --- |
| `ALBERT_API_KEY` | oui | aucune | Clé utilisée par le provider Albert API. Ne la commite jamais. |
| `RUNTIME` | non | aucun sur macOS/Linux ; `microsandbox` sur Windows | Runtime préféré : `microsandbox`, `tart` ou `agent-vm`. Un flag explicite reste prioritaire. |
| `ISOLATION` | non | `backend` | Frontière d'exécution de l'agent : `backend` (le serveur tourne dans le sandbox, le TUI s'y attache depuis l'hôte) ou `full` (tout l'agent, TUI compris, tourne dans l'invité). Un flag explicite reste prioritaire. |
| `WORKSPACE_DIR` | non | `./workspace` | Répertoire hôte monté sur `/workspace` dans l'invité. Un chemin relatif est résolu depuis le répertoire de lancement. |
| `PROJECT_DIR` | non | — | Ancien nom de `WORKSPACE_DIR`, encore accepté avec un avertissement. Ne pas utiliser dans une nouvelle configuration. |
| `OPENCODE_SERVER_USERNAME` | non | `opencode` | Nom d'utilisateur de l'authentification HTTP du backend. |
| `OPENCODE_SERVER_PASSWORD` | non | `albert-dev-pass` | Mot de passe HTTP du backend. Une valeur explicitement vide (`OPENCODE_SERVER_PASSWORD=`) désactive l'authentification. |
| `JUST_CODE_START_TIMEOUT` | non | `300` | Délai maximal, en secondes entières positives, pour attendre que le backend soit prêt avant d'attacher le TUI. |
| `MSB_HOME` | non | `~/.microsandbox` | Racine du runtime et de l'état Microsandbox gérés. |
| `MSB_PATH` | non | runtime géré | Chemin direct vers un binaire `msb` fourni manuellement. À définir avec `MSB_LIBKRUNFW_PATH`. |
| `MSB_LIBKRUNFW_PATH` | non | runtime géré | Chemin direct vers la bibliothèque `libkrunfw` fournie manuellement. À définir avec `MSB_PATH`. |
| `TART_IMAGE` | non | `ghcr.io/cirruslabs/macos-tahoe-base:latest` | Image utilisée pour créer la VM Tart. Sans effet sur Microsandbox. |
| `TART_MTU` | non | `1280` | MTU de l'invité Tart : entier de `1280` à `1500`, ou `auto` pour ne pas la modifier. Sans effet sur Microsandbox. |
| `AGENT_VM_TEMPLATE` | non | `agent-vm-base` | Template Lima servant de base à la VM agent-vm. Sous ce nom par défaut, just-code le construit s'il est absent ; tout autre nom désigne un template que vous maintenez, qui n'est jamais construit ni remplacé. Sans effet sur les autres runtimes. |
| `AGENT_VM_VM` | non | `opencode-agent-vm` | Nom de la VM Lima gérée par just-code. Sans effet sur les autres runtimes. |
| `AGENT_VM_IMAGE` | non | `template:debian-13` | Image Lima utilisée pour créer le template de base. Sans effet sur les autres runtimes. |
| `AGENT_VM_DISK_GB` | non | `20` | Taille du disque du template de base, en Go. Lima ne sait agrandir un disque que dans ce sens : prévoir large. Sans effet sur les autres runtimes. |
| `AGENT_VM_MEMORY_GB` | non | `4` | Mémoire du template de base, en Go. Sans effet sur les autres runtimes. |
| `AGENT_VM_CPUS` | non | `2` | Nombre de processeurs du template de base. Sans effet sur les autres runtimes. |

### Priorité et prise d'effet

- Une variable déjà exportée dans l'environnement prime sur toute valeur du fichier `.env`.
- Le `.env` du répertoire de lancement prime sur un éventuel `.env` placé à côté de l'exécutable.
- `--microsandbox`, `--tart` ou `--agent-vm` prime sur `RUNTIME`.
- `--isolation backend|full` prime sur `ISOLATION` ; un flag explicite reste utilisable même si `ISOLATION` contient une valeur invalide.
- Le montage `WORKSPACE_DIR` est figé à la création du sandbox ou de la VM. Le modifier impose `just-code restart --<runtime>`, qui recrée l'environnement.
- Le niveau d'isolation est figé à la création du sandbox Microsandbox : ses scripts de démarrage sont persistés et ne peuvent pas être réécrits. Basculer `ISOLATION` sur un sandbox existant est refusé avec un message ; `just-code restart --microsandbox` le recrée dans le mode demandé.
- Une modification des identifiants HTTP nécessite `just-code stop`, puis un nouveau lancement pour redémarrer le backend avec les nouvelles valeurs.
- Une modification de `TART_MTU` nécessite `just-code stop`, puis `just-code --tart`. Elle ne nécessite pas de recréer la VM.
- Changer `TART_IMAGE` cible une autre VM Tart ; les VM créées depuis des images différentes peuvent coexister.

Les sections suivantes détaillent les réglages propres à Tart. Les montages de workspace et leur recréation sont également expliqués dans [Utilisation](#utilisation).

### Réseau Tart et VPN

`TART_MTU=1280` est la valeur par défaut pour le réseau invité Tart. Elle limite les blocages TLS observés avec certains VPN sur le Mac hôte, sans garantir la compatibilité avec tous les VPN. Le bootstrap applique cette valeur à l'interface de route IPv4 par défaut avant toute installation de logiciel ; seul le réseau de la VM est modifié. Une MTU réduite peut légèrement diminuer les performances réseau.

Dans `.env`, `TART_MTU` accepte un entier de **1280 à 1500**, ou **`auto`** pour ne pas modifier la MTU. `auto` ne restaure pas une valeur précédemment appliquée ; utiliser `1500` pour revenir à la valeur habituelle. L'application nécessite `sudo` sans mot de passe dans l'invité (disponible dans l'image de base utilisée). Les erreurs sont consignées dans `just-code logs --tart`.

Après modification, exécuter **`just-code stop` puis `just-code --tart`** pour réappliquer le réglage tout en conservant les logiciels installés. Un backend déjà sain n'est pas reconfiguré par `just-code start`. Ne pas utiliser `just-code restart` pour cela : cette commande recrée la VM.

Sur macOS, autoriser également le terminal utilisé dans **Réglages Système > Confidentialité et sécurité > Réseau local**. Cette permission couvre l'accès à l'adresse privée de la VM, même si elle tourne sur le même Mac. Sans elle, le TUI peut rester en attente ou signaler une erreur trompeuse d'URL/port.

### Images Tart et nommage des VM

L'image par défaut est **macOS Tahoe** (`:latest`) :

```dotenv
TART_IMAGE=ghcr.io/cirruslabs/macos-tahoe-base:latest
```

Le nom de la VM dérive du nom de fichier de la référence image (`:` et `@sha256` remplacés par `-`, préfixe `opencode-`) :

- **Tahoe** (`:latest`) : `opencode-tahoe-base-latest`
- **Sonoma** (`:latest`) : `opencode-sonoma-base-latest`

Deux images partageant le même nom de fichier (par exemple issues de registres différents) produiraient le même nom de VM. Les images Cirrus Labs de Tahoe et Sonoma ont des noms de fichier distincts et constituent le cas pris en charge.

Le préfixe `opencode-` identifie les VM gérées par just-code : `just-code stop` arrête toutes les VM Tart locales portant ce préfixe. Ne pas le réutiliser pour des VM créées en dehors de just-code.

Pour basculer vers Sonoma :

```dotenv
TART_IMAGE=ghcr.io/cirruslabs/macos-sonoma-base:latest
```

Les VM Tahoe et Sonoma coexistent ; changer `TART_IMAGE` cible l'autre VM sans supprimer la précédente. Utiliser `tart list` pour voir les VM disponibles et `tart delete <nom>` pour libérer l'espace.

**Note** : Tahoe est nécessaire pour Xcode 26.3+. Sonoma ne supporte que Xcode 16.2 et versions antérieures.

### Runtime agent-vm

Le runtime `agent-vm` exécute le backend OpenCode dans une VM Linux Debian persistante pilotée par [Lima](https://lima-vm.io). Contrairement à Microsandbox (microVM éphémère par sandbox), la VM est persistante : les logiciels installés dans l'invité survivent aux arrêts.

Prérequis :

1. [Lima](https://lima-vm.io) (`brew install lima` sur macOS).
2. Rien d'autre : le template de base est construit par just-code lui-même au premier démarrage.

```bash
just-code doctor --agent-vm    # vérifie limactl et signale un template à construire
just-code --agent-vm           # construit le template si besoin, démarre la VM et attache le TUI
```

Au premier `start`, just-code construit un template de base nommé `agent-vm-base` (~5 minutes : paquets de base, Node.js 24, OpenCode), puis le clone en une VM nommée `opencode-agent-vm`, monte `WORKSPACE_DIR` à l'identique dans l'invité, démarre la VM et lance `opencode serve` dedans. Les démarrages suivants réutilisent le template. Le port 4096 est publié sur `127.0.0.1` côté hôte via le transfert de ports Lima ; les serveurs de dev lancés par l'agent sur les ports 3000-3010 sont également accessibles depuis l'hôte (transfert dynamique Lima). Les secrets (clé Albert, authentification HTTP) transitent par un fichier d'environnement poussé dans l'invité, jamais en ligne de commande.

Le provisionnement est volontairement minimal : les paquets de base, Node.js et OpenCode, plus un lien symbolique `opencode` dans `/usr/local/bin` — présent dans le `PATH` par défaut de tout shell, connecté ou non — pour que le backend trouve le binaire sans dépendre des fichiers d'initialisation d'un shell donné. `/etc/profile.d/just-code.sh` complète l'ensemble pour les shells interactifs. just-code ne construit ce template que sous son nom par défaut : tout autre `AGENT_VM_TEMPLATE` désigne un template que vous maintenez, et il n'est ni construit ni remplacé. C'est le point de personnalisation — une équipe peut y préinstaller Docker, Chromium ou d'autres agents et le désigner via `AGENT_VM_TEMPLATE` ; le clonage reste identique.

La construction est refaite si le template est absent **ou** s'il a été laissé inachevé. just-code écrit un marqueur d'achèvement sur l'hôte une fois le provisionnement et l'arrêt terminés : un template présent mais non marqué — le cas d'une construction interrompue, qu'un processus tué ne peut pas annuler lui-même — est reconstruit plutôt que cloné. Un template que vous maintenez n'a pas ce marqueur et est utilisé tel quel. Pour forcer une reconstruction (après un changement de ressources, par exemple), supprimez l'instance : `limactl delete agent-vm-base --force`.

Une opération interrompue est rattrapée : une construction qui échoue supprime le template partiel, pour qu'un nouvel essai reparte d'une base saine plutôt que de cloner une image sans OpenCode.

`just-code stop` arrête la VM (l'état est conservé), `just-code restart --agent-vm` la recrée depuis le template (destructif), `just-code clean --agent-vm` la supprime avec son état local.

## Utilisation

```bash
just-code                        # démarre (RUNTIME) et attache le TUI
just-code --tart                 # démarre Tart et attache le TUI
just-code --microsandbox         # démarre Microsandbox et attache le TUI
just-code --agent-vm             # démarre agent-vm et attache le TUI
just-code start --tart           # démarre un backend sans attacher le TUI
just-code stop                   # arrête l'instance du projet courant
just-code stop --all             # arrête toutes les instances just-code
just-code check                  # santé du backend actif + provider Albert
just-code logs --tart            # logs d'un runtime explicite
just-code shell --tart           # shell dans un runtime explicite
just-code restart --tart         # recrée le sandbox (destructif, demande confirmation)
just-code clean --tart            # supprime le sandbox et son état local
just-code doctor --tart           # vérifie l'installation du runtime
just-code version                # identifie le binaire (version, commit, plateforme)
just-code help                   # liste les commandes
```

Lancer `just-code` sans commande démarre le backend sélectionné et attache le TUI natif OpenCode. Les commandes et les flags de runtime peuvent être donnés dans n'importe quel ordre (`just-code --microsandbox start` et `just-code start --microsandbox` sont équivalents). La commande `code` n'existe pas : la taper renvoie une erreur explicite.

Les environnements sont identifiés par projet (répertoire racine du worktree Git) : chaque projet a sa propre instance `jc-<nom>-<suffixe>`, et les commandes `stop`, `logs`, `shell`, `clean` et `check` ciblent le projet courant. `just-code stop` n'arrête que l'instance du projet courant ; `just-code stop --all` arrête toutes les instances just-code, tous runtimes confondus. Les instances legacy (singleton d'avant l'identification par projet) restent découvrables pour les opérations explicites et ne sont jamais renommées ni supprimées automatiquement.

Les runtimes publient les mêmes ports et ne doivent pas tourner simultanément. Si un autre runtime est déjà actif, `just-code` (attachement), `just-code start` et `just-code restart` proposent de l'arrêter avant de continuer. Quand tu quittes le TUI OpenCode, l'attachement propose aussi d'arrêter le backend ; répondre non le laisse disponible pour une reconnexion. `just-code restart` est destructif : il recrée l'environnement et perd les sessions, les outils installés dans l'invité et les fichiers propres à l'invité ; la commande demande une confirmation interactive (refusée hors TTY, pour CI et scripts).

### Niveaux d'isolation

`--isolation backend|full` (ou `ISOLATION` dans `.env`) choisit où vit l'agent :

```bash
just-code --microsandbox --isolation full    # tout l'agent tourne dans la microVM
just-code --tart --isolation backend         # comportement historique
```

En mode `backend` (défaut), `opencode serve` tourne dans le sandbox et le TUI s'y attache depuis l'hôte : seul le processus serveur est confiné, le TUI et les identifiants de connexion restent côté hôte. En mode `full`, le TUI lui-même tourne dans l'invité et l'hôte n'est qu'un passe-plat terminal ; `just-code check` rapporte alors l'état de la VM au lieu de sonder un endpoint de santé, qui n'existe pas dans ce mode.

```bash
just-code check --isolation full             # état de la VM, pas de health check
```

### Protection des identifiants par runtime

Le niveau d'isolation ne détermine pas à lui seul ce qu'un agent peut lire : c'est le runtime qui décide si la clé Albert est substituée à la frontière réseau ou déposée en clair dans l'invité.

### Stockage global des identifiants (auth)

`just-code auth` stocke les identifiants globaux dans le magasin natif de l'OS — Keychain macOS, Gestionnaire d'identifiants Windows, Secret Service Linux — jamais dans un fichier projet ni un profil shell. Les commandes :

```bash
just-code auth add            # saisie masquée interactive (ou --stdin pour un pipe)
just-code auth status         # état du magasin, jamais les valeurs
just-code auth remove         # suppression
```

Le secret ne passe jamais par la ligne de commande (argv) : la saisie interactive est masquée, `--stdin` lit une ligne sur l'entrée standard. Sur les hôtes sans magasin natif (Linux headless sans Secret Service), le repli `--fallback` écrit un fichier JSON `0600` dans le répertoire de configuration — **jamais créé implicitement** : sans consentement explicite (`auth add --fallback`), toute écriture échoue. Un magasin natif indisponible ou verrouillé est une erreur explicite, pas un repli silencieux vers ce fichier en clair.

Stocker un identifiant ne lui donne aucun accès : la liaison à un sandbox est le chantier P09. `auth remove` refuse de supprimer un identifiant lié à une instance en cours d'exécution tant que la révocation n'est pas appliquée. Les identifiants restent référencés par nom (`credentialRef`) dans la configuration gérée ; la valeur ne figure jamais dans `settings.json`, `project.json` ni les exports.

| Runtime | Injection protégée | Portée |
|---|---|---|
| Microsandbox | Oui, en `backend` **et** en `full` | La clé n'existe que côté hôte ; l'invité reçoit le placeholder `$MSB_ALBERT_API_KEY`, remplacé par le proxy réseau uniquement vers `albert.api.etalab.gouv.fr` |
| Tart | Non | La clé est lisible dans l'invité, en `backend` comme en `full` |
| agent-vm | Non | Idem |

Sur Microsandbox, le secret est déclaré via l'API de secrets du runtime et la substitution réseau se fait à la frontière : passer en `full` place le TUI dans la microVM sans exposer la clé pour autant. Un sandbox créé par une version antérieure, qui persistait la clé en clair dans l'environnement invité, est refusé au démarrage avec la commande de recréation à lancer ; l'ancienne valeur ne peut pas être remplacée sur place. Si l'environnement invité ne peut pas être relu, le démarrage est refusé de la même façon : sans cette lecture, rien ne distingue un sandbox sain d'un sandbox qui expose encore la clé.

Sur Tart et agent-vm, les secrets ne passent jamais par la ligne de commande : en mode `full` ils sont transmis sur l'entrée standard (Tart) ou par un fichier `0600` copié dans l'invité (agent-vm), et le TUI les lit depuis ce fichier. Le fichier porte aussi la configuration du provider Albert, sans quoi le TUI n'aurait pas de modèle à utiliser. Ces deux runtimes n'ont pas de proxy audité : la clé y est en clair dans l'invité, quel que soit le niveau d'isolation, et `just-code` l'annonce au démarrage. Réservez-les aux travaux qui n'ont pas besoin de la clé, ou traitez l'invité comme portant un identifiant vivant.

Le niveau d'isolation est fixé à la création du sandbox Microsandbox. Le demander différent sur un sandbox existant est refusé, avec la commande de recréation à lancer ; `just-code restart --<runtime>` recrée l'environnement dans le mode demandé.

Par défaut, `./workspace` est monté comme projet. Pour pointer sur un vrai dépôt :

```bash
export WORKSPACE_DIR="$HOME/Code/mon-projet"
just-code --microsandbox
```

`PROJECT_DIR` reste accepté mais est déprécié (un avertissement le signale).

### Sécurité du workspace

Le workspace est bind-mounté dans le sandbox : **tout ce qui est lisible dans le workspace est lisible par l'agent**, y compris via des commandes shell (`cat`, `grep`, scripts de build...). Les règles de permission `read` d'OpenCode (qui refusent `*.env` par défaut) ne couvrent que l'outil de lecture, pas le shell.

Avant chaque démarrage, `just-code` scanne donc le workspace et **refuse de démarrer** s'il contient :

- un fichier `.env` ou `.env.*` (hors `.env.example` / `.env.sample`) ;
- un secret détecté par [gitleaks](https://github.com/gitleaks/gitleaks), si l'outil est installé sur l'hôte (sinon un avertissement signale que ce scan n'a pas pu s'exécuter).

Le scan ne suit pas les symlinks : un lien nommé `.env` est bloqué, un lien vers un répertoire hors du workspace n'est pas traversé. La règle : les vrais secrets restent hors du workspace ; un `.env.example` vidé sert de gabarit.

**Limite connue :** le bind-mount est dynamique. Un fichier copié dans le workspace **pendant** que le backend tourne devient immédiatement lisible côté invité, sans qu'un scan au démarrage puisse l'intercepter. Ne copie jamais de secrets dans un workspace exposé à un agent en cours d'exécution ; si cela arrive, `just-code stop`, retire le fichier, puis relance.

**Les montages sont figés à la création.** Microsandbox, Tart et agent-vm fixent le volume au moment de la création du sandbox ou de la VM. Changer `WORKSPACE_DIR` sur un sandbox Microsandbox existant n'a donc aucun effet : `just-code` détecte l'écart et prévient. Pour l'appliquer, il faut recréer avec `just-code restart --microsandbox` (destructif). Tart et agent-vm ne permettent pas cette détection ; le changement de répertoire y est donc uniquement documenté.

Les serveurs de dev lancés par l'agent sur les ports **3000-3010** sont accessibles depuis le navigateur de l'hôte : `http://localhost:3000`, etc. pour Microsandbox. Avec Tart, la VM macOS est une machine à part entière sur le réseau NAT : les previews et le TUI OpenCode utilisent l'adresse de la VM, par exemple `open "http://$(tart ip opencode-tahoe-base-latest):3000"`.

## Dépannage

### Le backend ne devient jamais healthy

Une VM Microsandbox survit à un redémarrage, mais son entrée de conteneur (`/.msb/scripts/start`, qui lance `opencode serve`) ne s'exécute qu'à la **création**. Une VM qui revient au démarrage est donc `running` sans aucun processus OpenCode : « VM démarrée » n'est pas « backend prêt ». C'est la cause la plus fréquente d'un backend qui ne devient jamais healthy.

`just-code` en tient compte et se répare : si le sandbox tourne mais que le backend ne répond pas, l'entrée est relancée dans la microVM ; s'il est arrêté, il est démarré puis l'entrée est relancée (un simple redémarrage de la VM ne rejoue pas l'entrée). Le même correctif s'applique à Tart, où le processus OpenCode obsolète est tué avant relance.

Si malgré cela le backend ne répond pas, l'attachement (`just-code` sans commande) attend en affichant l'avancement (300 s par défaut, réglable via `JUST_CODE_START_TIMEOUT` en secondes). À l'expiration, l'erreur précise le dernier résultat observé, ce qui distingue les causes :

- `HTTP 401: unauthorized` — le mot de passe attendu par le backend diffère de `OPENCODE_SERVER_PASSWORD`. Un sandbox créé lors d'un run précédent conserve l'ancien mot de passe. Utiliser `just-code restart --<runtime>` (destructif) ou `just-code stop` puis `just-code --<runtime>`.
- `connection refused` — le backend n'écoute pas ; `just-code logs --<runtime>` montre la sortie du bootstrap invité.
- `HTTP 200: ...` sans `healthy` — l'application démarre encore ; attendre quelques secondes.

Le premier démarrage d'un sandbox Microsandbox installe ~384 Mio de paquets dans la microVM et peut dépasser largement une minute ; les démarrages suivants sont rapides (l'installation est marquée dans `/var/lib/just-code/toolchain-ready`).

Le réflexe le plus simple reste `just-code stop` suivi d'un relancement de `just-code --<runtime>`.

### Conteneur Docker d'une version précédente

Les versions antérieures à la suppression du runtime Docker laissaient un conteneur `albert-opencode-sandbox` (`restart: unless-stopped`) qui publiait les ports 4096 et 3000-3010, les mêmes que les runtimes actuels. Au premier `start` / `attach`, et sur `just-code stop`, just-code détecte ce conteneur et le supprime automatiquement en l'annonçant ; s'il ne peut pas le supprimer, il s'arrête en le signalant plutôt que de laisser un conflit de ports inexpliqué. Aucun `docker` installé n'est nécessaire : l'absence de Docker est ignorée.

## Choix de conception

- **Deux runtimes, aucun défaut intégré.** Les commandes ciblent `--microsandbox` ou `--tart`, avec une préférence `RUNTIME` facultative pour les usages répétés. Le flag explicite est toujours prioritaire. Le répertoire projet et le port OpenCode (4096) restent identiques.
- **Microsandbox préservé.** La microVM Linux d'origine reste inchangée et expose les previews sur `localhost` (ports 3000-3010).
- **VM macOS avec Tart pour Xcode/iOS.** Tart permet d'exécuter l'agent OpenCode directement dans un système macOS invité, donnant accès aux outils de compilation Xcode (`xcodebuild`, `swift`, simulateurs). Par défaut, l'image `ghcr.io/cirruslabs/macos-tahoe-base:latest` est clonée dans une VM locale nommée `opencode-tahoe-base-latest` (surchargeable via `TART_IMAGE`). L'utilisateur ou le développeur peut ensuite y installer les outils Xcode nécessaires. La VM étant une machine invitée macOS sur le réseau NAT, le TUI et les previews sont joints par son adresse (`tart ip opencode-tahoe-base-latest`) plutôt que par `localhost`. `ALBERT_API_KEY` est transmise sur l'entrée standard du processus de bootstrap, jamais dans la liste des arguments ; seul le binaire du CLI (copie dédiée en lecture seule) est partagé avec la VM, pas le dépôt ni `.env`.
- **MicroVM nommée et persistante.** Avec Microsandbox et Tart, `just-code stop` conserve l'état inscriptible de la VM et les relances ultérieures évitent de repartir de zéro. `just-code restart` recrée la VM proprement.
- **Pas de démon ni de runtime conteneur.** La microVM démarre à la demande depuis l'image OCI officielle `ghcr.io/anomalyco/opencode:latest`.
- **Pas de fichier de config OpenCode bind-mounté.** La configuration du provider Albert est passée inline via `OPENCODE_CONFIG_CONTENT` par le SDK. Le seul bind-mount est le répertoire projet.
- **Éditions visibles sur l'hôte.** Les modifications de l'agent atterrissent directement dans ton checkout local. Le modèle « remote-authoritative » (clone dans le sandbox, livraison via branche/PR) reste une expérience ultérieure.
- **Permissions permissives dans le sandbox.** Le runtime sélectionné est la frontière de confinement : `edit`, `bash` et `external_directory` sont autorisés à l'intérieur.
- **Secrets.** `.env` est ignoré par git. Avec Microsandbox, la vraie valeur reste sur l'hôte : seule une valeur de substitution entre dans la microVM et le proxy réseau ne la remplace que pour `albert.api.etalab.gouv.fr`.
- **Scan du workspace au démarrage.** Le workspace étant intégralement lisible par l'agent, le démarrage est refusé si des fichiers `.env` ou des secrets gitleaks y sont détectés. C'est une frontière au démarrage, pas une garantie continue : un fichier ajouté pendant l'exécution reste lisible via le bind-mount (documenté dans « Sécurité du workspace »).

## Contribuer

L'architecture du portage Go, les hooks pre-commit (gitleaks) et le pipeline d'intégration continue et de publication (release-please) sont documentés dans [docs/development.md](docs/development.md). La résolution typée de la configuration (sources, précédence, schémas des fichiers gérés, transition depuis `.env`) est documentée dans [docs/config.md](docs/config.md).

---

just code · département IA dans l'État (IAE), DINUM · Licence MIT
