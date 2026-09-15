# Développement

Ce document décrit l'architecture interne du CLI, l'outillage de développement et le pipeline de publication. Pour l'installation et l'utilisation, voir le [README](../README.md).

## Portage Go

Le CLI est un binaire Go unique (`cmd/just-code`) qui remplace entièrement le `justfile`. Il orchestre les deux runtimes via une bibliothèque testée (`internal/justcode`), sans dépendance externe.

**Binaire autonome.** Les ressources de runtime (`microsandbox.yaml`) vivent dans `assets/` et sont embarquées dans le binaire via `go:embed`. Au premier lancement d'une commande qui en a besoin, le CLI les matérialise sous `~/.local/state/just-code/assets/` (écriture atomique). Le binaire peut donc être exécuté depuis n'importe quel répertoire, sans le dépôt.

**Pas de script shell.** Le bootstrap invité Tart (clampage MTU, installation d'OpenCode, `exec opencode serve`) est du code Go dans le même binaire, exposé sous la sous-commande interne `__guest-bootstrap`. La VM macOS étant elle aussi en arm64, le CLI copie son propre binaire dans le partage en lecture seule, puis le copie sur le disque local de l'invité (l'exécution directe depuis le partage virtiofs n'est pas fiable) avant de l'exécuter. Il n'y a donc plus aucun `.sh` dans le projet.

Les trois régressions shell de l'ancien `justfile` sont corrigées et couvertes par `go test` :

- **Délai de santé à horloge murale** : la boucle attend une échéance mesurée en temps réel (300 s par défaut, `JUST_CODE_START_TIMEOUT` en secondes), avec un timeout de 5 s par requête (une connexion bloquée ne contourne plus la limite).
- **Mot de passe vide préservé** : `OPENCODE_SERVER_PASSWORD=""` signifie « pas d'authentification », au lieu de retomber silencieusement sur `albert-dev-pass`.
- **Surface des flags** : `--microsandbox` et `--tart` sont reconnus (flag explicite prioritaire sur `RUNTIME`), et les messages d'erreur reflètent exactement cette surface.

Défauts découverts ensuite et corrigés dans le même esprit :

- **Auto-réparation du backend Microsandbox** : découverte via `msb ls` (les codes de sortie de `msb inspect` ne sont pas fiables), relance de l'entrée de conteneur quand la VM tourne sans backend, avec nouvelle tentative bornée pendant que l'agent invité démarre.
- **Clé API transmise explicitement à `msb`** : `msb` résout le secret `ALBERT_API_KEY` depuis l'environnement de l'hôte ; la variable est donc passée explicitement aux commandes `msb`.
- **Montages figés à la création** : `WORKSPACE_DIR` remplace `PROJECT_DIR` (déprécié, encore honoré), et Microsandbox avertit quand le montage détecté diffère de la configuration.
- **Pré-vol `opencode`** : le CLI de l'hôte est vérifié avant de démarrer un runtime, avec la commande d'installation.
- **Validation différée de `JUST_CODE_START_TIMEOUT`** : une valeur malformée bloque la commande d'attachement, pas `stop`, `clean`, `logs`, `doctor` ni `check`.

Contrats comportementaux reproduits : secrets (`ALBERT_API_KEY` et mot de passe) transmis sur l'entrée standard, jamais dans les arguments ; isolation par préfixe `opencode-` ; endpoints de santé ; clampage de MTU (1280-1500 ou `auto`) validé sur l'hôte avant le boot de la VM ; relance du backend (SIGTERM puis SIGKILL après 10 sondes) ; détection des runtimes actifs et résolution de conflits.

Binaires statiques ~7 Mo (Linux amd64, macOS arm64/amd64), sans runtime embarqué, contre ~60-80 Mo pour un binaire `bun build --compile` qui embarque JavaScriptCore.

```bash
go test -race ./...   # vert
go vet ./...          # propre

# Compilation croisée native (sans CGO)
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o just-code-darwin-arm64 ./cmd/just-code
```

## Hooks pre-commit

Le dépôt embarque des hooks pre-commit. Prérequis : [pre-commit](https://pre-commit.com/#install) et [Go](https://go.dev/dl/) sur le `PATH` (pre-commit compile gitleaks depuis la version épinglée au premier commit ; gitleaks lui-même n'a pas besoin d'être installé). Après un clone :

```bash
pre-commit install
```

Le hook [gitleaks](https://github.com/gitleaks) scanne les changements stagés à chaque commit et bloque l'ajout de secrets (clés API, mots de passe). La version est épinglée dans `.pre-commit-config.yaml`. Le premier commit après l'installation est lent (compilation de gitleaks) ; les suivants sont quasi instantanés. Pour le passer ponctuellement : `SKIP=gitleaks git commit ...` (les hooks locaux restent contournables ; ne le fais que si tu sais pourquoi).

## Intégration continue et publication

Deux workflows GitHub Actions accompagnent le CLI :

- **`ci.yml`** (sur chaque PR, et sur `main`) : vérification du formatage (`gofmt`), `go vet`, `go test -race` et compilation.
- **`release-please.yml`** (sur `main`) : [release-please](https://github.com/googleapis/release-please) maintient une PR de release à partir des Conventional Commits (`feat:` = minor, `fix:` = patch). Fusionner cette PR écrit le `CHANGELOG.md`, crée le tag et la release GitHub, puis construit les six binaires (`darwin-arm64`, `darwin-x64`, `linux-arm64`, `linux-x64`, `windows-x64.exe`, `windows-arm64.exe`), génère `SHA256SUMS` et les attache à la release — dans le même job, car les événements créés avec `GITHUB_TOKEN` ne déclenchent pas d'autres workflows.

Le tag de release est la source de vérité de la version : il est injecté dans le binaire via `-ldflags "-X main.version=<tag>"`, donc `just-code version` affiche exactement la version publiée.

Pour publier : fusionner la PR de release-please. C'est tout. Le pipeline d'artefacts peut aussi être exercé sans couper une release : `workflow_dispatch` avec un tag existant reconstruit et réattache les assets.
