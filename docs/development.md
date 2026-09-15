# Développement

Ce document décrit l'architecture interne du CLI, l'outillage de développement et le pipeline de publication. Pour l'installation et l'utilisation, voir le [README](../README.md).

## Portage Go

Le CLI est un binaire Go unique (`cmd/just-code`) qui remplace entièrement le `justfile`. Il orchestre les deux runtimes via une bibliothèque testée (`internal/justcode`) et le SDK Go Microsandbox, sans dépendre d'une commande `msb` externe.

**Binaire autonome.** La configuration OpenCode et le script de démarrage Microsandbox vivent dans `assets/` et sont embarqués dans le binaire via `go:embed`. Ils sont transmis directement au SDK sans fichier de configuration temporaire. Le runtime natif téléchargé reste séparé sous `~/.microsandbox/` (ou `$MSB_HOME`).

**Pas de script shell.** Le bootstrap invité Tart (clampage MTU, installation d'OpenCode, `exec opencode serve`) est du code Go dans le même binaire, exposé sous la sous-commande interne `__guest-bootstrap`. La VM macOS étant elle aussi en arm64, le CLI copie son propre binaire dans le partage en lecture seule, puis le copie sur le disque local de l'invité (l'exécution directe depuis le partage virtiofs n'est pas fiable) avant de l'exécuter.

Les trois régressions shell de l'ancien `justfile` sont corrigées et couvertes par `go test` :

- **Délai de santé à horloge murale** : la boucle attend une échéance mesurée en temps réel (300 s par défaut, `JUST_CODE_START_TIMEOUT` en secondes), avec un timeout de 5 s par requête (une connexion bloquée ne contourne plus la limite).
- **Mot de passe vide préservé** : `OPENCODE_SERVER_PASSWORD=""` signifie « pas d'authentification », au lieu de retomber silencieusement sur `albert-dev-pass`.
- **Surface des flags** : `--microsandbox` et `--tart` sont reconnus (flag explicite prioritaire sur `RUNTIME`), et les messages d'erreur reflètent exactement cette surface.

Défauts découverts ensuite et corrigés dans le même esprit :

- **Auto-réparation du backend Microsandbox** : découverte et cycle de vie via le SDK, relance de l'entrée de conteneur quand la VM tourne sans backend, avec nouvelle tentative bornée pendant que l'agent invité démarre.
- **Clé API confinée sur l'hôte** : la valeur est remise directement au SDK et n'entre jamais dans l'environnement invité ; le proxy réseau ne la substitue que pour `albert.api.etalab.gouv.fr`. Elle est rafraîchie lors du redémarrage d'un sandbox existant.
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

## Hooks pre-commit

Le dépôt embarque des hooks pre-commit. Prérequis : [pre-commit](https://pre-commit.com/#install) et [Go](https://go.dev/dl/) sur le `PATH` (pre-commit compile gitleaks depuis la version épinglée au premier commit ; gitleaks lui-même n'a pas besoin d'être installé). Après un clone :

```bash
pre-commit install
```

Le hook [gitleaks](https://github.com/gitleaks) scanne les changements stagés à chaque commit et bloque l'ajout de secrets (clés API, mots de passe). La version est épinglée dans `.pre-commit-config.yaml`. Le premier commit après l'installation est lent (compilation de gitleaks) ; les suivants sont quasi instantanés. Pour le passer ponctuellement : `SKIP=gitleaks git commit ...` (les hooks locaux restent contournables ; ne le fais que si tu sais pourquoi).

## Intégration continue et publication

Deux workflows GitHub Actions accompagnent le CLI :

- **`ci.yml`** (sur chaque PR, et sur `main`) : vérification du formatage (`gofmt`), `go vet`, `go test -race`, puis tests et compilation CGO sur chaque plateforme native prise en charge.
- **`release-please.yml`** (sur `main`) : [release-please](https://github.com/googleapis/release-please) maintient une PR de release à partir des Conventional Commits (`feat:` = minor, `fix:` = patch). Fusionner cette PR écrit le `CHANGELOG.md`, crée le tag et la release GitHub, puis construit les cinq binaires (`darwin-arm64`, `linux-arm64`, `linux-x64`, `windows-x64.exe`, `windows-arm64.exe`) sur leurs runners natifs, génère `SHA256SUMS` et les attache à la release une fois toutes les plateformes réussies.

Le tag de release est la source de vérité de la version : il est injecté dans le binaire via `-ldflags "-X main.version=<tag>"`, donc `just-code version` affiche exactement la version publiée.

Pour publier : fusionner la PR de release-please. C'est tout. Le pipeline d'artefacts peut aussi être exercé sans couper une release : `workflow_dispatch` avec un tag existant reconstruit et réattache les assets.
