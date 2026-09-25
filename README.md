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

`just-code` sans argument utilise **Microsandbox en isolation `full`** : l'agent, sa TUI et ses identifiants tournent dans la microVM, dont l'espace de travail est scellé (aucun fichier de l'hôte n'y est monté). `--tart` et `--agent-vm` restent disponibles explicitement, par flag ou via `RUNTIME`.

La commande prépare l'espace de travail de l'invité, démarre l'agent et attache la TUI OpenCode native.

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

### Configurer la machine (setup)

`just-code setup` prépare la machine en une passe guidée : diagnostic
(plateforme, virtualisation, disque — lecture seule) → identifiant Albert
(masqué, validé contre le catalogue ; rejet et indisponibilité réseau
distincts) → identifiant GitHub optionnel → identité git → modèle par
défaut → revue → application (réglages globaux + runtime managé).

```bash
just-code setup            # assistant interactif
just-code setup doctor     # diagnostic lecture seule, aucune écriture
just-code setup --fallback # utiliser le magasin fichier consentit
just-code setup --no-color # sortie terminal simple
```

Règles du flux :

- **Aucune saisie avant un bloqueur fatal.** La virtualisation et le disque
  sont vérifiés avant toute invite : la clé n'est jamais demandée sur une
  machine qui ne peut pas exécuter le runtime.
- **Reprise.** Un setup interrompu reprend là où il s'est arrêté (journal
  d'état hôte) — les identifiants déjà stockés ne sont pas redemandés.
- **Rejet ≠ réseau.** Une clé refusée (401/403) propose de la ressaisir ;
  un endpoint injoignable stocke la clé avec un avertissement (la
  validation se rattrapera avec `just-code models`). Aucun appel
  d'inférence n'est effectué pour valider.
- **GitHub sans pénalité.** Ignorer l'identifiant GitHub ne bloque rien ;
  il n'est activé pour aucun projet (l'approbation par projet vient du
  chantier GitHub invité).
- **Magasin indisponible = choix explicite.** Un Secret Service absent
  propose le repli fichier consentit (`--fallback`), jamais une création
  silencieuse.

**Un fichier `.env` n'est pas lu.** just-code lit l'environnement du processus : exporte les variables dans ton shell (ou dans le gestionnaire de secrets de ton choix), et passe les réglages ponctuels par des flags.

```bash
export ALBERT_API_KEY=ta-clé          # ou : just-code auth add albert
just-code                             # Microsandbox, isolation full, racine du projet
```

C'est volontaire : un `.env` non versionné dans le répertoire courant rendait le lancement dépendant d'un fichier invisible (et de son contenu en secrets), et deux invocations du même binaire pouvaient se comporter différemment. Si tu as un `.env` d'une installation précédente, just-code te le signale au lancement ; pour voir ce qu'il contient et où chaque valeur va :

```bash
just-code config import-env .env      # prévisualisation seule : rien n'est écrit
just-code auth add albert             # la clé va dans le magasin d'identifiants
```

L'application automatique d'un import n'existe pas encore : la prévisualisation liste les valeurs reconnues et le fichier d'origine n'est jamais modifié.

Un `.env.example` reste dans le dépôt comme **gabarit documentaire** : il liste les variables et leurs défauts, il n'est jamais lu.

### Variables disponibles

| Variable | Requise | Valeur par défaut | Rôle |
| --- | --- | --- | --- |
| `ALBERT_API_KEY` | oui | aucune | Clé utilisée par le provider Albert API. Ne la commite jamais. |
| `RUNTIME` | non | `microsandbox` | Runtime préféré : `microsandbox`, `tart` ou `agent-vm`. Un flag explicite reste prioritaire. Microsandbox est le défaut sur toutes les plateformes depuis P12, parce que c'est le runtime à espace de travail scellé. |
| `ISOLATION` | non | `full` | Frontière d'exécution de l'agent : `full` (tout l'agent, TUI et identifiants compris, tourne dans l'invité) ou `backend` (le serveur tourne dans le sandbox et le TUI s'y attache depuis l'hôte). Le défaut est `full` depuis P12 : le parcours à zéro option est celui où l'agent reste dans le sandbox. Un flag explicite reste prioritaire. |
| `WORKSPACE_DIR` | non | racine du projet | Répertoire de travail du projet. En Microsandbox c'est la **source** du contenu transféré, filtré, vers l'invité (voir [Sécurité du workspace](#sécurité-du-workspace)) ; pour Tart et agent-vm c'est le répertoire **monté** sur `/workspace`. Un chemin relatif est résolu depuis le répertoire de lancement. Un `WORKSPACE_DIR` explicite est toujours honoré tel quel. |
| `PROJECT_DIR` | non | — | Ancien nom de `WORKSPACE_DIR`, encore accepté avec un avertissement. Ne pas utiliser dans une nouvelle configuration. |
| `OPENCODE_SERVER_USERNAME` | non | `opencode` | Nom d'utilisateur de l'authentification HTTP du backend. |
| `OPENCODE_SERVER_PASSWORD` | non | `albert-dev-pass` | Mot de passe HTTP du backend. Une valeur explicitement vide (`OPENCODE_SERVER_PASSWORD=`) désactive l'authentification. |
| `JUST_CODE_START_TIMEOUT` | non | `300` | Délai maximal, en secondes entières positives, pour attendre que le backend soit prêt avant d'attacher le TUI. |
| `JUST_CODE_CPUS` | non | `2` | Nombre de processeurs alloués à l'invité Microsandbox (1 à 255 : le SDK transporte cette valeur sur un octet, une valeur supérieure est refusée plutôt que repliée). Prioritaire sur le manifeste du projet. |
| `JUST_CODE_MEMORY_MB` | non | `4096` | Mémoire allouée à l'invité Microsandbox, en Mio. Prioritaire sur le manifeste du projet. |
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

- **Le `.env` n'est plus lu** (P12). Le lancement dépendait d'un fichier non versionné du répertoire courant — exactement le genre de fichier qui porte des secrets et que l'espace scellé existe pour tenir hors de l'invité — et deux invocations du même binaire pouvaient se comporter différemment. Les variables **exportées** sont lues ; un `.env` présent est signalé avec la commande qui l'adopte (`just-code config import-env <chemin>`).
- `--microsandbox`, `--tart` ou `--agent-vm` prime sur `RUNTIME`.
- `--isolation backend|full` prime sur `ISOLATION` ; un flag explicite reste utilisable même si `ISOLATION` contient une valeur invalide.
- Sans `WORKSPACE_DIR`, le **répertoire de travail est la racine du projet**. Pour Tart et agent-vm, cela change ce qui est monté : le montage devient le projet lui-même au lieu de `./workspace`, et donc la porte de sécurité de ces runtimes (qui refusent un workspace contenant un `.env`, puisqu'ils le montent) porte désormais sur le projet. En Microsandbox il n'y a pas de montage : changer la source prend effet au `workspace sync` suivant, sans recréation.
- Le **dimensionnement de l'invité** (`JUST_CODE_CPUS`, `JUST_CODE_MEMORY_MB`) est figé à la création du sandbox : le runtime ne redimensionne pas un invité en cours. Une valeur enregistrée dans le manifeste du projet qui change est donc signalée comme nécessitant une recréation, jamais ignorée en silence.
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
just-code auth add [albert|github|context7]   # saisie masquée interactive (ou --stdin pour un pipe)
just-code auth status                         # état du magasin, jamais les valeurs
just-code auth remove [albert|github|context7] # révocation puis suppression
```

Le secret ne passe jamais par la ligne de commande (argv) : la saisie interactive est masquée, `--stdin` lit une ligne sur l'entrée standard. Sur les hôtes sans magasin natif (Linux headless sans Secret Service), le repli `--fallback` écrit un fichier JSON `0600` dans le répertoire de configuration — **jamais créé implicitement** : sans consentement explicite (`auth add --fallback`), toute écriture échoue. Un magasin natif indisponible ou verrouillé est une erreur explicite, pas un repli silencieux vers ce fichier en clair.

Au démarrage, la clé Albert est résolue dans l'ordre : `credentialRef` (env `JUST_CODE_CREDENTIAL_REF` > manifeste projet > réglages utilisateur), puis la variable d'environnement `ALBERT_API_KEY`, puis l'identifiant `albert` du magasin. Les identifiants restent référencés par nom (`credentialRef`) dans la configuration gérée ; la valeur ne figure jamais dans `settings.json`, `project.json` ni les exports.

Un magasin natif indisponible ou verrouillé est une erreur explicite au démarrage, jamais un repli silencieux vers le fichier : ce fichier peut détenir un identifiant périmé ou différent précisément quand l'attendu ne peut pas être vérifié. Seul un « introuvable » (aucune entrée) poursuit vers le repli consentit.

La rotation est détectée sans jamais toucher la valeur : `auth add` incrémente un compteur par identifiant (état hôte, non secret), et la réconciliation compare ce compteur — remplacer une clé sur une instance saine planifie un rafraîchissement au lieu du no-op.

### Liaisons d'identifiants (bindings)

Sur Microsandbox, les identifiants sont injectés par le proxy de secrets : la valeur brute n'est jamais persistée (ni dans la base du runtime, ni dans l'environnement invité) — la liaison est une référence à une variable d'environnement hôte, re-résolue à chaque application et à chaque démarrage. La liaison `albert` est obligatoire ; `github` et `context7` sont **optionnelles et approuvées projet par projet** :

```bash
just-code bindings list              # liaisons connues, approbations, état du magasin
just-code bindings approve github    # autorise la liaison github pour ce projet
just-code bindings revoke github     # retire l'approbation et révoque l'instance du projet
```

L'approbation est un enregistrement local à l'hôte (`~/.local/state/just-code/instances/<instance>/bindings.json`) : elle n'est jamais versionnée et ne voyage pas avec un clone. Les hôtes autorisés sont disjoints entre liaisons, donc une liaison ne peut pas être substituée vers la destination d'une autre.

### Révocation

`just-code auth remove` révoque l'accès avant de supprimer l'entrée du magasin : sur les instances Microsandbox en cours d'exécution, la liaison proxy est supprimée à chaud (l'invité garde un placeholder inerte jusqu'au prochain redémarrage — un avertissement le signale) ; sur les instances arrêtées, la référence persistée est retirée pour le prochain démarrage. La révocation raisonne par **entrée de magasin**, pas par nom de liaison : un `credentialRef` peut alimenter la liaison Albert depuis une entrée nommée autrement, et c'est la liaison invitée effectivement alimentée qui est retirée (l'instantané le consigne sous la forme `entrée@magasin#liaison`). Supprimer l'entrée d'un magasin ne touche pas les instances liées à l'autre magasin ; une instance dont la clé venait de l'environnement (variable `ALBERT_API_KEY`) est ignorée, cette variable n'appartenant pas à just-code ; une source non confirmable est révoquée puis signalée. Sur Tart et agent-vm, la clé est lisible en clair dans l'invité : la suppression est **refusée** tant qu'une telle instance tourne — y compris lorsque l'entrée supprimée n'est pas nommée `albert` mais alimente la liaison Albert — car retirer la copie du magasin ne révoquerait rien.

### Configuration OpenCode composée et confiance locale (P10)

La configuration OpenCode effective est composée au lancement : l'asset embarqué (provider Albert, permissions) fusionné avec la **couche gérée** (`OPENCODE_CONFIG_CONTENT`), qui ne porte que les champs managés — aujourd'hui `model` et `small_model`. OpenCode fusionne le contenu inline **en dernier** (contrat D-001), donc la couche gérée gagne champ par champ contre la config projet et la config utilisateur, sans jamais réécrire un fichier JSONC utilisateur.

La sélection du modèle suit la précédence `JUST_CODE_MODEL` > manifeste projet (`model`) > réglages utilisateur (`defaultModel`) > valeur intégrée. `just-code models` liste les modèles text-generation du catalogue Albert (validés, limites de contexte incluses, jamais fabriquées) ; un échec réseau retombe sur le **dernier-known-good** mis en cache — une erreur réseau n'efface jamais le modèle en service, et une sélection absente du catalogue est signalée plutôt que silencieusement conservée.

Les conflits de champs managés sont signalés **en diff** avant lancement : si la config projet définit `model` différemment, les deux valeurs sont affichées et la valeur gérée gagne (l'inverse serait un écrasement invisible).

**Confiance locale :** les entrées de projet qui exécutent du code au chargement d'OpenCode — plugins déclarés, plugins auto-découverts (`.opencode/plugin/*.js|ts` — ils s'exécutent **sans déclaration**), commandes MCP locales — exigent une approbation locale avant le premier `start` du projet :

```bash
just-code trust status   # ce que le projet déclare, ce qui est approuvé, ce qui a changé
just-code trust approve  # approuve le contenu actuel de chaque entrée
```

L'approbation est liée au **contenu** (hachage par fichier) : un fichier modifié est de nouveau non approuvé, un nouveau plugin apparaissant nécessite sa propre approbation. L'enregistrement vit dans l'état hôte (`~/.local/state/just-code/projects/<projet>/opencode-trust.json`), jamais dans le dépôt — un clone n'apporte pas sa confiance avec lui. Les liens symboliques sont refusés à l'approbation (un chemin repointable n'est pas un contenu épinglé).

| Runtime | Injection protégée | Portée |
|---|---|---|
| Microsandbox | Oui, en `backend` **et** en `full` | La clé n'existe que côté hôte ; l'invité reçoit le placeholder `$MSB_ALBERT_API_KEY`, remplacé par le proxy réseau uniquement vers `albert.api.etalab.gouv.fr` |
| Tart | Non | La clé est lisible dans l'invité, en `backend` comme en `full` |
| agent-vm | Non | Idem |

Sur Microsandbox, le secret est déclaré via l'API de secrets du runtime et la substitution réseau se fait à la frontière : passer en `full` place le TUI dans la microVM sans exposer la clé pour autant. Un sandbox créé par une version antérieure, qui persistait la clé en clair dans l'environnement invité, est refusé au démarrage avec la commande de recréation à lancer ; l'ancienne valeur ne peut pas être remplacée sur place. Si l'environnement invité ne peut pas être relu, le démarrage est refusé de la même façon : sans cette lecture, rien ne distingue un sandbox sain d'un sandbox qui expose encore la clé.

Sur Tart et agent-vm, les secrets ne passent jamais par la ligne de commande : en mode `full` ils sont transmis sur l'entrée standard (Tart) ou par un fichier `0600` copié dans l'invité (agent-vm), et le TUI les lit depuis ce fichier. Le fichier porte aussi la configuration du provider Albert, sans quoi le TUI n'aurait pas de modèle à utiliser. Ces deux runtimes n'ont pas de proxy audité : la clé y est en clair dans l'invité, quel que soit le niveau d'isolation. Le démarrage l'exige donc explicitement : sans `--acknowledge-guest-credentials`, `start` est refusé ; avec, l'avertissement reste affiché à chaque démarrage. Réservez-les aux travaux qui n'ont pas besoin de la clé, ou traitez l'invité comme portant un identifiant vivant.

Le niveau d'isolation est fixé à la création du sandbox Microsandbox. Le demander différent sur un sandbox existant est refusé, avec la commande de recréation à lancer ; `just-code restart --<runtime>` recrée l'environnement dans le mode demandé.

Par défaut, `./workspace` est monté comme projet. Pour pointer sur un vrai dépôt :

```bash
export WORKSPACE_DIR="$HOME/Code/mon-projet"
just-code --microsandbox
```

`PROJECT_DIR` reste accepté mais est déprécié (un avertissement le signale).

### Sécurité du workspace

**En mode Microsandbox, le checkout hôte n'est pas monté dans l'invité.** L'invité possède son propre espace de travail, un volume interne au sandbox : aucun fichier de l'hôte — `.env`, secret non commité, fichier ignoré — n'est lisible depuis l'agent. Le contenu ne traverse la frontière que par un **transfert filtré**, jamais par un montage.

#### Transfert hôte → invité (filtre refus par défaut)

Chaque synchronisation — provisionnement initial et chaque rafraîchissement — résout un ensemble de fichiers concret, visible avant qu'un octet ne traverse. Sont **exclus par défaut** :

- les fichiers `.env` / `.env.*` (hors `.env.example` / `.env.sample`) ;
- les fichiers signalés par [gitleaks](https://github.com/gitleaks/gitleaks), si l'outil est installé sur l'hôte (sinon l'avertissement signale que la détection n'a pas tourné ; le filtre par nom, lui, s'applique toujours) ;
- les fichiers ignorés par Git (c'est là que vivent les `.env` locaux et l'état dérivé) ;
- les symlinks (un lien peut résoudre hors de l'ensemble résolu) ;
- la métadonnée `.git` (le dépôt de l'invité est créé **dans** l'invité).

Le filtre est **structurellement inévitable** : le transfert écrit un payload unique construit depuis l'ensemble résolu, pas une copie d'arborescence. Un fichier refusé ne peut donc pas se retrouver dans l'invité.

```
just-code workspace status          # ce qui traverserait, ce qui est refusé et pourquoi
just-code workspace allow <chemin>  # réintégrer explicitement un fichier précis
just-code workspace deny <chemin>   # revenir au refus par défaut
just-code workspace sync [--force]  # rafraîchir l'espace invité
```

Il n'y a **pas** d'option « tout inclure » : une réintégration est une décision par fichier, enregistrée dans l'état hôte.

#### Retour des changements

Les modifications faites dans l'invité ne reviennent jamais en écriture directe dans ton checkout :

```
just-code workspace export [--out <fichier>]
```

produit un patch (fichiers suivis et nouveaux) écrit dans l'état hôte, à relire puis appliquer avec `git apply`. Avec le grant GitHub activé, la livraison par branche/PR prend le relais (P13).

**Non destructif :** `workspace sync` refuse de rafraîchir si l'invité contient du travail non commité ; il nomme les fichiers et n'écrase qu'avec `--force`.

#### Instances héritées et scan d'hygiène

Une instance créée avant ce modèle porte un **montage lié** de l'hôte : son invité peut lire ton checkout. Elle n'est jamais convertie sur place — `just-code` refuse de la démarrer et indique la recréation explicite (`just-code clean --microsandbox` puis `start`).

Le scan de démarrage n'est plus une frontière de sécurité : il reste un **conseil d'hygiène** qui signale les fichiers ressemblant à des secrets dans le checkout. Sa détection est réutilisée à la frontière de transfert, où le refus est effectif.

**Tart et agent-vm montent toujours le workspace** : le modèle scellé ne s'y applique pas, et le scan y garde son rôle bloquant. Pour un projet qui doit exposer le checkout à l'invité, l'un de ces runtimes reste le choix explicite.

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
