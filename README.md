```
       _            __                    __
      (_)_  _______/ /_   _________  ____/ /__
     / / / / / ___/ __/  / ___/ __ \/ __  / _ \
    / / /_/ (__  ) /_   / /__/ /_/ / /_/ /  __/
 __/ /\__,_/____/\__/   \___/\____/\__,_/\___/
/___/
```

⚠️ **PROJET EXPÉRIMENTAL : bac à sable de R&D pour évaluer l'exécution distante d'agents de code avec Albert API. Rien de stable, tout peut changer.** ⚠️

# just code

**Un backend OpenCode isolé avec Docker ou Microsandbox, piloté par le TUI OpenCode natif de ta machine.**

Première brique d'expérimentation pour la piste « exécution distante » d'[Albert Code](https://github.com/etalab-ia/albert-code) : l'agent tourne dans un environnement Linux avec les modèles souverains d'[Albert API](https://albert.api.etalab.gouv.fr), pendant que tu gardes ton interface habituelle. Tu peux comparer un conteneur Docker avec une microVM Microsandbox sans changer de workflow.

```text
terminal hôte (opencode attach) ──> sandbox sélectionné :4096 (opencode serve)
                                            ├── /workspace  = ton projet (bind-mount)
                                            ├── outils      = bash, git, node, python
                                            └── inférence   = Albert API (deepseek-v4-flash)
```

Ce n'est **pas un produit** : c'est un terrain de jeu pour mesurer l'UX (latence, reconnexion, persistance de session, previews web) et comparer les frontières d'isolation.

## Prérequis

- [`just`](https://just.systems) (`brew install just`)
- OpenCode CLI sur l'hôte (`npm install -g opencode-ai`)
- Une **clé Albert API** dans l'environnement (ou dans `.env`, ignoré par git)
- L'un des runtimes disponibles :
  - Docker ou [Colima](https://github.com/abiosoft/colima) ;
  - [Microsandbox](https://github.com/superradcompany/microsandbox) (`msb` >= 0.6.16) sur un Mac Apple Silicon ou un hôte Linux avec KVM ;
  - [Tart](https://github.com/openai/tart) (`brew install openai/tools/tart`) sur un Mac Apple Silicon pour les environnements de dev macOS (notamment Xcode / iOS).

Installation de Microsandbox :

```bash
curl -fsSL https://install.microsandbox.dev | sh
msb doctor
```

## Configuration

Crée ta configuration locale depuis l'exemple :

```bash
cp .env.example .env
```

Renseigne `ALBERT_API_KEY` dans `.env`. Sans autre configuration, chaque commande qui cible un runtime exige `--docker`, `--microsandbox` ou `--tart`. Pour conserver une préférence locale, décommente aussi l'une de ces lignes :

```dotenv
RUNTIME=docker
# ou
RUNTIME=microsandbox
# ou
RUNTIME=tart
```

Un flag explicite reste prioritaire sur `RUNTIME` : `just code --docker` utilise toujours Docker, même si `.env` préfère Microsandbox ou Tart. Il n'existe aucun runtime par défaut intégré.

### Réseau Tart et VPN

`TART_MTU=1280` est la valeur par défaut pour le réseau invité Tart. Elle limite les blocages TLS observés avec certains VPN sur le Mac hôte, sans garantir la compatibilité avec tous les VPN. Le bootstrap applique cette valeur à l'interface de route IPv4 par défaut avant toute installation de logiciel ; seul le réseau de la VM est modifié. Une MTU réduite peut légèrement diminuer les performances réseau.

Dans `.env`, `TART_MTU` accepte un entier de **1280 à 1500**, ou **`auto`** pour ne pas modifier la MTU. `auto` ne restaure pas une valeur précédemment appliquée ; utiliser `1500` pour revenir à la valeur habituelle. L'application nécessite `sudo` sans mot de passe dans l'invité (disponible dans l'image de base utilisée). Les erreurs sont consignées dans `just logs --tart`.

Après modification, exécuter **`just stop` puis `just code --tart`** pour réappliquer le réglage tout en conservant les logiciels installés. Un backend déjà sain n'est pas reconfiguré par `just start`. Ne pas utiliser `just restart` pour cela : cette commande recrée la VM.

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

Le préfixe `opencode-` identifie les VM gérées par just-code : `just stop` arrête toutes les VM Tart locales portant ce préfixe. Ne pas le réutiliser pour des VM créées en dehors de just-code.

Pour basculer vers Sonoma :

```dotenv
TART_IMAGE=ghcr.io/cirruslabs/macos-sonoma-base:latest
```

Les VM Tahoe et Sonoma coexistent ; changer `TART_IMAGE` cible l'autre VM sans supprimer la précédente. Utiliser `tart list` pour voir les VM disponibles et `tart delete <nom>` pour libérer l'espace.

**Note** : Tahoe est nécessaire pour Xcode 26.3+. Sonoma ne supporte que Xcode 16.2 et versions antérieures.

## Utilisation

```bash
just          # liste les commandes
just code --docker                # démarre Docker et attache le TUI
just code --microsandbox          # démarre Microsandbox et attache le TUI
just code --tart                  # démarre Tart (VM macOS) et attache le TUI
just code                         # utilise RUNTIME défini dans .env
just start --tart                  # démarre un backend sans attacher le TUI
just stop                         # arrête tout runtime just-code actif
just check                        # santé du backend actif + provider Albert
just logs --tart                  # logs d'un runtime explicite
just shell --tart                 # shell dans un runtime explicite
just build --tart                 # télécharge ou met à jour l'image de base
just restart --tart               # recrée le sandbox (destructif)
just clean --tart                 # supprime le sandbox et son état local
just doctor --tart                # vérifie l'installation du runtime
```

Les deux runtimes publient les mêmes ports et ne doivent pas tourner simultanément. Si l'autre runtime est déjà actif, `just code` et `just start` proposent de l'arrêter avant de continuer. Quand tu quittes le TUI OpenCode, `just code` propose aussi d'arrêter le backend ; répondre non le laisse disponible pour une reconnexion. `just stop` détecte l'état réel et ignore volontairement `RUNTIME`.

Par défaut, `./workspace` est monté comme projet. Pour pointer sur un vrai dépôt :

```bash
export PROJECT_DIR="$HOME/Code/mon-projet"
just code --microsandbox
```

Les serveurs de dev lancés par l'agent sur les ports **3000-3010** sont accessibles depuis le navigateur de l'hôte : `http://localhost:3000`, etc. pour Docker et Microsandbox. Avec Tart, la VM macOS est une machine à part entière sur le réseau NAT : les previews et le TUI OpenCode utilisent l'adresse de la VM, par exemple `open "http://$(tart ip opencode-tahoe-base-latest):3000"`.

## Prompts d'exemple

Une fois attaché, ces prompts exercent les dimensions clés de l'expérience :

1. **« Qu'y a-t-il dans ce workspace ? Décris la structure du projet. »** — inférence et outils fichiers de base.
2. **« Sur quel OS, noyau et architecture tournes-tu ? »** — confirme que l'agent s'exécute dans le sandbox Linux, pas directement sur ton Mac.
3. **« Code un petit jeu snake servi par un serveur Node sur le port 3000, puis lance-le. »** — écriture de fichiers + serveur de dev ; ouvre `http://localhost:3000` pour vérifier la preview.
4. **« Écris un script python qui affiche la suite de Fibonacci et exécute-le. »** — toolchain polyglotte dans le sandbox.
5. **« Modifie un fichier, puis annule ta modification. »** — outils d'édition et revue de diff.
6. **Détache-toi (Ctrl+C / quitte), conserve le runtime, puis relance `just code --docker` ou `just code --microsandbox`** — continuité de session et reconnexion.
7. **« Envoie les logs de ton serveur de dev dans /tmp/server.log et montre-moi les dernières lignes. »** — comportement du scratch space hors du répertoire projet.

## Portage Go (banc d'essai)

Le cycle de vie du runtime Tart est également porté en Go dans `internal/justcode`, avec un CLI autonome `cmd/just-code`. C'est un banc d'essai pour comparer Go à une implémentation Bun/TypeScript sur le durcissement : le code shell inline du `justfile` est remplacé par une bibliothèque testée, sans dépendance externe.

```bash
go build -o just-code ./cmd/just-code
go test ./...

# Compilation croisée native (sans CGO)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o just-code-darwin-arm64 ./cmd/just-code
```

Les binaires statiques font ~7 Mo (Linux amd64 et macOS arm64/amd64), sans runtime embarqué, contre ~60-80 Mo pour un binaire `bun build --compile` qui embarque JavaScriptCore.

Ce premier port couvre uniquement le runtime Tart. Les trois régressions shell du `justfile` y sont corrigées et testées :

- **Délai de santé à horloge murale** : la boucle attend une échéance de 120 s mesurée en temps réel, avec un `--max-time` de 5 s par requête (une connexion bloquée ne contourne plus la limite).
- **Mot de passe vide préservé** : `OPENCODE_SERVER_PASSWORD=""` signifie « pas d'authentification », au lieu de retomber silencieusement sur `albert-dev-pass`.
- **Surface des flags** : `--docker`, `--microsandbox` et `--tart` sont reconnus (flag explicite prioritaire sur `RUNTIME`), et les messages d'erreur reflètent exactement cette surface.

Les contrats comportementaux du runtime Tart sont reproduits : secrets (`ALBERT_API_KEY` et mot de passe) transmis sur l'entrée standard, jamais dans les arguments ; isolation par préfixe `opencode-` ; endpoints de santé ; clampage de MTU (1280-1500 ou `auto`), validé sur l'hôte avant le boot de la VM ; et relance du backend (SIGTERM puis SIGKILL après 10 sondes). Docker et Microsandbox restent dans le `justfile` pour l'instant.

## Choix de conception

- **Trois runtimes, aucun défaut intégré.** Les commandes ciblent `--docker`, `--microsandbox` ou `--tart`, avec une préférence `RUNTIME` facultative pour les usages répétés. Le flag explicite est toujours prioritaire. Le répertoire projet et le port OpenCode (4096) restent identiques.
- **Docker et Microsandbox préservés.** Les environnements conteneurisés et microVM Linux d'origine restent inchangés. Ils exposent les previews sur `localhost` (ports 3000-3010).
- **VM macOS avec Tart pour Xcode/iOS.** Tart permet d'exécuter l'agent OpenCode directement dans un système macOS invité, donnant accès aux outils de compilation Xcode (`xcodebuild`, `swift`, simulateurs). Par défaut, l'image `ghcr.io/cirruslabs/macos-tahoe-base:latest` est clonée dans une VM locale nommée `opencode-tahoe-base-latest` (surchargeable via `TART_IMAGE`). L'utilisateur ou le développeur peut ensuite y installer les outils Xcode nécessaires. La VM étant une machine invitée macOS sur le réseau NAT, le TUI et les previews sont joints par son adresse (`tart ip opencode-tahoe-base-latest`) plutôt que par `localhost`. `ALBERT_API_KEY` est transmise sur l'entrée standard du processus de bootstrap, jamais dans la liste des arguments ; seul `tart-bootstrap.sh` (copie dédiée en lecture seule) est partagé avec la VM, pas le dépôt ni `.env`.
- **MicroVM nommée et persistante.** Avec Microsandbox et Tart, `just stop` conserve l'état inscriptible de la VM et les relances ultérieures évitent de repartir de zéro. `just restart` recrée la VM proprement.
- **Pas de démon Docker pour Microsandbox.** La microVM démarre à la demande depuis l'image OCI officielle `ghcr.io/anomalyco/opencode:latest`.
- **Pas de fichier de config OpenCode bind-mounté.** La configuration du provider Albert est passée inline via `OPENCODE_CONFIG_CONTENT` dans le fichier du runtime. Le seul bind-mount est le répertoire projet.
- **Éditions visibles sur l'hôte.** Les modifications de l'agent atterrissent directement dans ton checkout local. Le modèle « remote-authoritative » (clone dans le sandbox, livraison via branche/PR) reste une expérience ultérieure.
- **Permissions permissives dans le sandbox.** Le runtime sélectionné est la frontière de confinement : `edit`, `bash` et `external_directory` sont autorisés à l'intérieur.
- **Secrets.** `.env` est ignoré par git. Avec Docker, `ALBERT_API_KEY` est injectée dans l'environnement du conteneur. Avec Microsandbox, la vraie valeur reste sur l'hôte : seule une valeur de substitution entre dans la microVM et le proxy réseau ne la remplace que pour `albert.api.etalab.gouv.fr`.

---

just code · département IA dans l'État (IAE), DINUM · Licence MIT
