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

- [Go](https://go.dev) >= 1.22 et une chaîne C native : Xcode Command Line Tools (macOS), `gcc` (Linux), MinGW-w64 x64 ou LLVM-MinGW (Windows). Sous Windows arm64, la chaîne doit cibler `aarch64-w64-mingw32`.
- OpenCode CLI sur l'hôte (`npm install -g opencode-ai`)
- Une **clé Albert API** dans l'environnement (ou dans `.env`, ignoré par git)
- L'un des runtimes disponibles :
  - [Microsandbox](https://github.com/superradcompany/microsandbox) sur un Mac Apple Silicon, Linux (KVM) ou Windows arm64/x64 (Windows Hypervisor Platform / WHP) ; l'exécutable `msb` n'a pas besoin d'être installé ;
  - [Tart](https://github.com/openai/tart) (`brew install openai/tools/tart`) sur un Mac Apple Silicon pour les environnements de dev macOS (notamment Xcode / iOS).

`just-code` embarque le SDK Go Microsandbox. Au premier `start` ou `doctor`, il télécharge automatiquement la version correspondante du runtime sous `$MSB_HOME` si cette variable est définie, sinon sous `~/.microsandbox/`. Ce chemin est géré directement par le SDK : il n'a pas besoin d'être ajouté au `PATH`.

```bash
just-code doctor --microsandbox
```

Le téléchargement ne modifie pas la configuration de l'hôte. Sous Linux, KVM doit être accessible. Sous Windows, active **Windows Hypervisor Platform** dans les fonctionnalités Windows puis redémarre si elle ne l'est pas déjà.

### Depuis les sources (contributeurs)

Prérequis : [Go](https://go.dev) >= 1.22 et une chaîne C native (Xcode Command Line Tools sur macOS, `gcc` sur Linux, MinGW-w64 x64 ou LLVM-MinGW sur Windows ; sous Windows arm64, la chaîne doit cibler `aarch64-w64-mingw32`).

```bash
go build -o just-code ./cmd/just-code
go test ./...
```

## Démarrage rapide

Le CLI lit un fichier `.env` dans le répertoire de travail (puis à côté de l'exécutable). Seule `ALBERT_API_KEY` est requise ; tous les autres réglages ont une valeur par défaut.

```bash
echo "ALBERT_API_KEY=ta-clé" > .env
just-code --microsandbox
```

Depuis un checkout des sources, tu peux partir du modèle complet à la place :

```bash
cp .env.example .env
# Renseigne ALBERT_API_KEY dans .env
just-code --microsandbox
```

Sans autre configuration, chaque commande qui cible un runtime exige `--microsandbox` ou `--tart`. La première commande démarre le backend, attend qu'il soit prêt, puis attache le TUI OpenCode natif.

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

Le CLI lit un fichier `.env` dans le répertoire de travail (puis à côté de l'exécutable) ; les variables déjà exportées dans l'environnement priment sur le fichier. Seule `ALBERT_API_KEY` est requise. Depuis un checkout des sources, le modèle complet est disponible :

```bash
cp .env.example .env
```

Pour conserver une préférence de runtime locale, ajoute aussi l'une de ces lignes dans `.env` :

```dotenv
RUNTIME=microsandbox
# ou
RUNTIME=tart
```

Un flag explicite reste prioritaire sur `RUNTIME` : `just-code --microsandbox` utilise toujours Microsandbox, même si `.env` préfère Tart. Il n'existe aucun runtime par défaut intégré.

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

## Utilisation

```bash
just-code                        # démarre (RUNTIME) et attache le TUI
just-code --tart                 # démarre Tart et attache le TUI
just-code --microsandbox         # démarre Microsandbox et attache le TUI
just-code start --tart           # démarre un backend sans attacher le TUI
just-code stop                   # arrête tout runtime just-code actif
just-code check                  # santé du backend actif + provider Albert
just-code logs --tart            # logs d'un runtime explicite
just-code shell --tart           # shell dans un runtime explicite
just-code restart --tart         # recrée le sandbox (destructif)
just-code clean --tart            # supprime le sandbox et son état local
just-code doctor --tart           # vérifie l'installation du runtime
just-code version                # identifie le binaire (version, commit, plateforme)
just-code help                   # liste les commandes
```

Lancer `just-code` sans commande démarre le backend sélectionné et attache le TUI natif OpenCode. Les commandes et les flags de runtime peuvent être donnés dans n'importe quel ordre (`just-code --microsandbox start` et `just-code start --microsandbox` sont équivalents). La commande `code` n'existe pas : la taper renvoie une erreur explicite.

Les runtimes publient les mêmes ports et ne doivent pas tourner simultanément. Si un autre runtime est déjà actif, `just-code` (attachement), `just-code start` et `just-code restart` proposent de l'arrêter avant de continuer. Quand tu quittes le TUI OpenCode, l'attachement propose aussi d'arrêter le backend ; répondre non le laisse disponible pour une reconnexion. `just-code stop` détecte l'état réel et ignore volontairement `RUNTIME`.

Par défaut, `./workspace` est monté comme projet. Pour pointer sur un vrai dépôt :

```bash
export WORKSPACE_DIR="$HOME/Code/mon-projet"
just-code --microsandbox
```

`PROJECT_DIR` reste accepté mais est déprécié (un avertissement le signale).

**Les montages sont figés à la création.** Microsandbox et Tart fixent le volume au moment de la création du sandbox ou de la VM. Changer `WORKSPACE_DIR` sur un sandbox Microsandbox existant n'a donc aucun effet : `just-code` détecte l'écart et prévient. Pour l'appliquer, il faut recréer avec `just-code restart --microsandbox` (destructif). Tart ne permet pas cette détection ; le changement de répertoire y est donc uniquement documenté.

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

## Contribuer

L'architecture du portage Go, les hooks pre-commit (gitleaks) et le pipeline d'intégration continue et de publication (release-please) sont documentés dans [docs/development.md](docs/development.md).

---

just code · département IA dans l'État (IAE), DINUM · Licence MIT
