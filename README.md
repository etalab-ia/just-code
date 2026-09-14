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

Première brique d'expérimentation pour la piste « exécution distante » d'[Albert Code](https://github.com/etalab-ia/albert-code) : l'agent tourne dans un environnement Linux (ou macOS via Tart) avec les modèles souverains d'[Albert API](https://albert.api.etalab.gouv.fr), pendant que tu gardes ton interface habituelle. Tu peux comparer une microVM Microsandbox ou une VM macOS Tart sans changer de workflow.

```text
terminal hôte (opencode attach) ──> sandbox sélectionné :4096 (opencode serve)
                                            ├── /workspace  = ton projet (bind-mount)
                                            ├── outils      = bash, git, node, python
                                            └── inférence   = Albert API (deepseek-v4-flash)
```

Ce n'est **pas un produit** : c'est un terrain de jeu pour mesurer l'UX (latence, reconnexion, persistance de session, previews web) et comparer les frontières d'isolation.

## Prérequis

- [Go](https://go.dev) >= 1.22 et une chaîne C native (`gcc`, Xcode Command Line Tools ou MinGW) pour compiler le CLI
- OpenCode CLI sur l'hôte (`npm install -g opencode-ai`)
- Une **clé Albert API** dans l'environnement (ou dans `.env`, ignoré par git)
- L'un des runtimes disponibles :
  - [Microsandbox](https://github.com/superradcompany/microsandbox) sur un Mac Apple Silicon, Linux (KVM) ou Windows arm64/x64 (Windows Hypervisor Platform / WHP) ; l'exécutable `msb` n'a pas besoin d'être installé ;
  - [Tart](https://github.com/openai/tart) (`brew install openai/tools/tart`) sur un Mac Apple Silicon pour les environnements de dev macOS (notamment Xcode / iOS).

Construction du CLI :

```bash
go build -o just-code ./cmd/just-code
go test ./...
```

`just-code` embarque le SDK Go Microsandbox. Au premier `start` ou `doctor`, il télécharge automatiquement la version correspondante du runtime sous `$MSB_HOME` si cette variable est définie, sinon sous `~/.microsandbox/`. Ce chemin est géré directement par le SDK : il n'a pas besoin d'être ajouté au `PATH`.

```bash
just-code doctor --microsandbox
```

Le téléchargement ne modifie pas la configuration de l'hôte. Sous Linux, KVM doit être accessible. Sous Windows, active **Windows Hypervisor Platform** dans les fonctionnalités Windows puis redémarre si elle ne l'est pas déjà.

## Configuration

Crée ta configuration locale depuis l'exemple :

```bash
cp .env.example .env
```

Renseigne `ALBERT_API_KEY` dans `.env`. Sans autre configuration, chaque commande qui cible un runtime exige `--microsandbox` ou `--tart`. Pour conserver une préférence locale, décommente aussi l'une de ces lignes :

```dotenv
RUNTIME=microsandbox
# ou
RUNTIME=tart
```

Un flag explicite reste prioritaire sur `RUNTIME` : `just-code --microsandbox` utilise toujours Microsandbox, même si `.env` préfère Tart. Il n'existe aucun runtime par défaut intégré.

### Réseau Tart et VPN

`TART_MTU=1280` est la valeur par défaut pour le réseau invité Tart. Elle limite les blocages TLS observés avec certains VPN sur le Mac hôte, sans garantir la compatibilité avec tous les VPN. Le bootstrap applique cette valeur à l'interface de route IPv4 par défaut avant toute installation de logiciel ; seul le réseau de la VM est modifié. Une MTU réduite peut légèrement diminuer les performances réseau.

Dans `.env`, `TART_MTU` accepte un entier de **1280 à 1500**, ou **`auto`** pour ne pas modifier la MTU. `auto` ne restaure pas une valeur précédemment appliquée ; utiliser `1500` pour revenir à la valeur habituelle. L'application nécessite `sudo` sans mot de passe dans l'invité (disponible dans l'image de base utilisée). Les erreurs sont consignées dans `just-code logs --tart`.

Après modification, exécuter **`just-code stop` puis `just-code code --tart`** pour réappliquer le réglage tout en conservant les logiciels installés. Un backend déjà sain n'est pas reconfiguré par `just-code start`. Ne pas utiliser `just-code restart` pour cela : cette commande recrée la VM.

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
just-code clean --tart           # supprime le sandbox et son état local
just-code doctor --tart          # vérifie l'installation du runtime
just-code version                # identifie le binaire (version, commit, plateforme)
just-code help                   # liste les commandes
```

Lancer `just-code` sans commande démarre le backend sélectionné et attache le TUI natif OpenCode. Les commandes et les flags de runtime peuvent être donnés dans n'importe quel ordre (`just-code --microsandbox start` et `just-code start --microsandbox` sont équivalents). La commande `code` n'existe pas : la taper renvoie une erreur explicite.

Les runtimes publient les mêmes ports et ne doivent pas tourner simultanément. Si un autre runtime est déjà actif, `just-code` (attachement), `just-code start` et `just-code restart` proposent de l'arrêter avant de continuer. Quand tu quittes le TUI OpenCode, l'attachement propose aussi d'arrêter le backend ; répondre non le laisse disponible pour une reconnexion. `just-code stop` détecte l'état réel et ignore volontairement `RUNTIME`.

Par défaut, `./workspace` est monté comme projet. Pour pointer sur un vrai dépôt :

```bash
export WORKSPACE_DIR="$HOME/Code/mon-projet"
just-code code --microsandbox
```

`PROJECT_DIR` reste accepté mais est déprécié (un avertissement le signale).

**Les montages sont figés à la création.** Microsandbox et Tart fixent le volume au moment de la création du sandbox ou de la VM. Changer `WORKSPACE_DIR` sur un sandbox Microsandbox existant n'a donc aucun effet : `just-code` détecte l'écart et prévient. Pour l'appliquer, il faut recréer avec `just-code restart --microsandbox` (destructif). Tart ne permet pas cette détection ; le changement de répertoire y est donc uniquement documenté.

Les serveurs de dev lancés par l'agent sur les ports **3000-3010** sont accessibles depuis le navigateur de l'hôte : `http://localhost:3000`, etc. pour Microsandbox. Avec Tart, la VM macOS est une machine à part entière sur le réseau NAT : les previews et le TUI OpenCode utilisent l'adresse de la VM, par exemple `open "http://$(tart ip opencode-tahoe-base-latest):3000"`.

## Prompts d'exemple

Une fois attaché, ces prompts exercent les dimensions clés de l'expérience :

1. **« Qu'y a-t-il dans ce workspace ? Décris la structure du projet. »** — inférence et outils fichiers de base.
2. **« Sur quel OS, noyau et architecture tournes-tu ? »** — confirme que l'agent s'exécute dans le sandbox Linux, pas directement sur ton Mac.
3. **« Code un petit jeu snake servi par un serveur Node sur le port 3000, puis lance-le. »** — écriture de fichiers + serveur de dev ; ouvre `http://localhost:3000` pour vérifier la preview.
4. **« Écris un script python qui affiche la suite de Fibonacci et exécute-le. »** — toolchain polyglotte dans le sandbox.
5. **« Modifie un fichier, puis annule ta modification. »** — outils d'édition et revue de diff.
6. **Détache-toi (Ctrl+C / quitte), conserve le runtime, puis relance `just-code --microsandbox` ou `just-code --tart`** — continuité de session et reconnexion.
7. **« Envoie les logs de ton serveur de dev dans /tmp/server.log et montre-moi les dernières lignes. »** — comportement du scratch space hors du répertoire projet.

## Dépannage

### Le backend ne devient jamais healthy

Une VM Microsandbox survit à un redémarrage, mais son entrée de conteneur (`/.msb/scripts/start`, qui lance `opencode serve`) ne s'exécute qu'à la **création**. Une VM qui revient au démarrage est donc `running` sans aucun processus OpenCode : « VM démarrée » n'est pas « backend prêt ». C'est la cause la plus fréquente d'un backend qui ne devient jamais healthy.

`just-code` en tient compte et se répare : si le sandbox tourne mais que le backend ne répond pas, l'entrée est relancée dans la microVM ; s'il est arrêté, il est démarré puis l'entrée est relancée (un simple redémarrage de la VM ne rejoue pas l'entrée). Le même correctif s'applique à Tart, où le processus OpenCode obsolète est tué avant relance.

Si malgré cela le backend ne répond pas, `just-code code` attend en affichant l'avancement (300 s par défaut, réglable via `JUST_CODE_START_TIMEOUT` en secondes). À l'expiration, l'erreur précise le dernier résultat observé, ce qui distingue les causes :

- `HTTP 401: unauthorized` — le mot de passe attendu par le backend diffère de `OPENCODE_SERVER_PASSWORD`. Un sandbox créé lors d'un run précédent conserve l'ancien mot de passe. Utiliser `just-code restart --<runtime>` (destructif) ou `just-code stop` puis `just-code code --<runtime>`.
- `connection refused` — le backend n'écoute pas ; `just-code logs --<runtime>` montre la sortie du bootstrap invité.
- `HTTP 200: ...` sans `healthy` — l'application démarre encore ; attendre quelques secondes.

Le premier démarrage d'un sandbox Microsandbox installe ~384 Mio de paquets dans la microVM et peut dépasser largement une minute ; les démarrages suivants sont rapides (l'installation est marquée dans `/var/lib/just-code/toolchain-ready`).

Le réflexe le plus simple reste `just-code stop` suivi d'un relancement de `just-code code --<runtime>`.

### Conteneur Docker d'une version précédente

Les versions antérieures à la suppression du runtime Docker laissaient un conteneur `albert-opencode-sandbox` (`restart: unless-stopped`) qui publiait les ports 4096 et 3000-3010, les mêmes que les runtimes actuels. Au premier `start` / `attach`, et sur `just-code stop`, just-code détecte ce conteneur et le supprime automatiquement en l'annonçant ; s'il ne peut pas le supprimer, il s'arrête en le signalant plutôt que de laisser un conflit de ports inexpliqué. Aucun `docker` installé n'est nécessaire : l'absence de Docker est ignorée.

## Portage Go

Le CLI est un binaire Go unique (`cmd/just-code`) qui remplace entièrement le `justfile`. Il orchestre les deux runtimes via une bibliothèque testée (`internal/justcode`) et le SDK Go Microsandbox, sans dépendre d'une commande `msb` externe.

**Binaire autonome.** La configuration OpenCode et le script de démarrage Microsandbox vivent dans `assets/` et sont embarqués dans le binaire via `go:embed`. Ils sont transmis directement au SDK sans fichier de configuration temporaire. Le runtime natif téléchargé reste séparé sous `~/.microsandbox/` (ou `$MSB_HOME`).

**Bootstrap Tart en Go.** Le bootstrap invité Tart (clampage MTU, installation d'OpenCode, `exec opencode serve`) est du code Go dans le même binaire, exposé sous la sous-commande interne `__guest-bootstrap`. La VM macOS étant elle aussi en arm64, le CLI copie son propre binaire dans le partage en lecture seule, puis le copie sur le disque local de l'invité (l'exécution directe depuis le partage virtiofs n'est pas fiable) avant de l'exécuter.

Les trois régressions shell de l'ancien `justfile` sont corrigées et couvertes par `go test` :

- **Délai de santé à horloge murale** : la boucle attend une échéance mesurée en temps réel (300 s par défaut, `JUST_CODE_START_TIMEOUT` en secondes), avec un timeout de 5 s par requête (une connexion bloquée ne contourne plus la limite).
- **Mot de passe vide préservé** : `OPENCODE_SERVER_PASSWORD=""` signifie « pas d'authentification », au lieu de retomber silencieusement sur `albert-dev-pass`.
- **Surface des flags** : `--microsandbox` et `--tart` sont reconnus (flag explicite prioritaire sur `RUNTIME`), et les messages d'erreur reflètent exactement cette surface.

Défauts découverts ensuite et corrigés dans le même esprit :

- **Auto-réparation du backend Microsandbox** : découverte et cycle de vie via le SDK, relance de l'entrée de conteneur quand la VM tourne sans backend, avec nouvelle tentative bornée pendant que l'agent invité démarre.
- **Clé API confinée sur l'hôte** : la valeur est remise directement au SDK et n'entre jamais dans l'environnement invité ; le proxy ne la substitue que pour `albert.api.etalab.gouv.fr`. Elle est rafraîchie lors du redémarrage d'un sandbox existant.
- **Montages figés à la création** : `WORKSPACE_DIR` remplace `PROJECT_DIR` (déprécié, encore honoré), et Microsandbox avertit quand le montage détecté diffère de la configuration.
- **Pré-vol `opencode`** : le CLI de l'hôte est vérifié avant de démarrer un runtime, avec la commande d'installation.
- **Validation différée de `JUST_CODE_START_TIMEOUT`** : une valeur malformée bloque la commande d'attachement, pas `stop`, `clean`, `logs`, `doctor` ni `check`.

Contrats comportementaux reproduits : secrets absents des arguments de processus ; isolation par préfixe `opencode-` ; endpoints de santé ; clampage de MTU (1280-1500 ou `auto`) validé sur l'hôte avant le boot de la VM ; relance du backend (SIGTERM puis SIGKILL après 10 sondes) ; détection des runtimes actifs et résolution de conflits.

Le SDK embarque une bibliothèque FFI propre à chaque plateforme et nécessite CGO. Les binaires de publication sont donc construits sur des runners natifs pour macOS arm64, Linux amd64/arm64 et Windows amd64/arm64. macOS Intel n'est pas publié : le SDK Microsandbox 0.6.18 ne fournit pas de bibliothèque FFI pour `darwin/amd64`.

```bash
go test -race ./...   # vert
go vet ./...          # propre

# Compilation sur la plateforme cible (CGO activé)
CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o just-code ./cmd/just-code
```

## Intégration continue et publication

Deux workflows GitHub Actions accompagnent le CLI :

- **`ci.yml`** (sur chaque PR, et sur `main`) : vérification du formatage (`gofmt`), `go vet`, `go test -race`, puis tests et compilation CGO sur chaque plateforme native prise en charge.
- **`release-please.yml`** (sur `main`) : [release-please](https://github.com/googleapis/release-please) maintient une PR de release à partir des Conventional Commits (`feat:` = minor, `fix:` = patch). Fusionner cette PR écrit le `CHANGELOG.md`, crée le tag et la release GitHub, puis construit les cinq binaires (`darwin-arm64`, `linux-arm64`, `linux-x64`, `windows-x64.exe`, `windows-arm64.exe`) sur leurs runners natifs, génère `SHA256SUMS` et les attache à la release.

Le tag de release est la source de vérité de la version : il est injecté dans le binaire via `-ldflags "-X main.version=<tag>"`, donc `just-code version` affiche exactement la version publiée.

Pour publier : fusionner la PR de release-please. C'est tout. Le pipeline d'artefacts peut aussi être exercé sans couper une release : `workflow_dispatch` avec un tag existant reconstruit et réattache les assets.

Vérification après téléchargement (les sommes sont générées depuis `dist/`, donc les chemins correspondent aux fichiers publiés) :

```bash
curl -fsSLO https://github.com/etalab-ia/just-code/releases/latest/download/just-code-darwin-arm64
curl -fsSLO https://github.com/etalab-ia/just-code/releases/latest/download/SHA256SUMS
shasum -a 256 -c SHA256SUMS --ignore-missing
```

Les binaires macOS ne sont ni signés ni notariés, et portent une signature ad-hoc (requise pour exécuter un binaire non signé sur Apple Silicon). `curl` ne pose pas l'attribut `com.apple.quarantine`, donc Gatekeeper ne bloque pas ce chemin d'installation ; un téléchargement via navigateur reste à débloquer avec `xattr -d com.apple.quarantine <binaire>`. La notarisation complète exigerait une adhésion Apple Developer et un certificat Developer ID.

## Développement

Le dépôt embarque des hooks pre-commit. Prérequis : [pre-commit](https://pre-commit.com/#install) et [Go](https://go.dev/dl/) sur le `PATH` (pre-commit compile gitleaks depuis la version épinglée au premier commit ; gitleaks lui-même n'a pas besoin d'être installé). Après un clone :

```bash
pre-commit install
```

Le hook [gitleaks](https://github.com/gitleaks/gitleaks) scanne les changements stagés à chaque commit et bloque l'ajout de secrets (clés API, mots de passe). La version est épinglée dans `.pre-commit-config.yaml`. Le premier commit après l'installation est lent (compilation de gitleaks) ; les suivants sont quasi instantanés. Pour le passer ponctuellement : `SKIP=gitleaks git commit ...` (les hooks locaux restent contournables ; ne le fais que si tu sais pourquoi).

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

---

just code · département IA dans l'État (IAE), DINUM · Licence MIT
