# Développement

Ce document décrit l'architecture interne du CLI, l'outillage de développement et le pipeline de publication. Pour l'installation et l'utilisation, voir le [README](../README.md).

## Configuration locale

Depuis un checkout des sources, pars du modèle commenté puis renseigne au minimum `ALBERT_API_KEY` :

```bash
cp .env.example .env
```

La référence complète des variables et de leurs règles de priorité se trouve dans la section [Configuration du README](../README.md#configuration).

## Portage Go

Le CLI est un binaire Go unique (`cmd/just-code`) qui remplace entièrement le `justfile`. Il orchestre les deux runtimes via une bibliothèque testée (`internal/justcode`) et le SDK Go Microsandbox, sans dépendre d'une commande `msb` externe.

**Binaire autonome.** La configuration OpenCode et le script de démarrage Microsandbox vivent dans `assets/` et sont embarqués dans le binaire via `go:embed`. Ils sont transmis directement au SDK sans fichier de configuration temporaire. Le runtime natif téléchargé reste séparé sous `~/.microsandbox/` (ou `$MSB_HOME`). Il provient d'une release autonome `microsandbox-v<version>` du dépôt : son URL et ses empreintes SHA-256 sont compilées dans `just-code`, l'archive est vérifiée avant décompression, puis le SDK est appelé avec `WithSkipDownload()`.

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
- **Runtime Microsandbox vérifié** : un runtime déjà présent n'est accepté comme installation gérée que si son marqueur de provenance correspond à l'empreinte compilée. Les installations antérieures sans marqueur sont remplacées depuis la release du projet avant toute exécution de `msb`. `MSB_PATH` et `MSB_LIBKRUNFW_PATH` restent une alternative explicite pour un provisionnement administré.

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

Trois workflows GitHub Actions accompagnent le CLI :

- **`ci.yml`** (sur chaque PR, et sur `main`) : vérification du formatage (`gofmt`), `go vet`, `go test -race`, puis tests et compilation CGO sur chaque plateforme native prise en charge.
- **`release-please.yml`** (sur `main`) : [release-please](https://github.com/googleapis/release-please) maintient une PR de release à partir des Conventional Commits (`feat:` = minor, `fix:` = patch). Fusionner cette PR écrit le `CHANGELOG.md`, crée le tag et la release GitHub, puis construit les cinq binaires (`darwin-arm64`, `linux-arm64`, `linux-x64`, `windows-x64.exe`, `windows-arm64.exe`) sur leurs runners natifs, génère `SHA256SUMS` et les attache à la release une fois toutes les plateformes réussies.
- **`msb-runtime-watch.yml`** (cron hebdomadaire, lundi 09:00 UTC) : surveille les releases amont du runtime Microsandbox ; voir « Mise à jour du runtime Microsandbox » ci-dessous.

Le tag de release est la source de vérité de la version : il est injecté dans le binaire via `-ldflags "-X main.version=<tag>"` (sans le préfixe `just-code-`), donc `just-code version` affiche exactement la version publiée.

Pour publier : fusionner la PR de release-please. C'est tout. Le pipeline d'artefacts peut aussi être exercé sans couper une release : `workflow_dispatch` avec un tag existant reconstruit et réattache les assets.

### Mise à jour du runtime Microsandbox

Le runtime évolue indépendamment du CLI. Le workflow `msb-runtime-watch.yml` automatise la détection et la mise en miroir, mais **jamais la gravure des empreintes**. La frontière de confiance est double. Les releases amont sont immuables : GitHub émet une attestation de release signée (Sigstore, signataire `https://dotcom.releases.github.com`) couvrant tous les assets, et le workflow la vérifie cryptographiquement via `gh release verify` puis source toutes ses empreintes des sujets attestés. Le `checksums.sha256` amont ne sert que de contre-vérification : il est téléversé avec les archives et pourrait être remplacé dans le même geste, donc tout désaccord avec l'attestation échoue fermé et demande une investigation humaine. Au-delà, la revue humaine des empreintes gravées dans le binaire reste la frontière finale.

**Détection (automatique, cron hebdomadaire).** Le workflow compare la dernière release amont `superradcompany/microsandbox` à la version du SDK embarqué (via le flag caché `just-code -print-msb-sdk-version`). S'il existe un `vX.Y.Z` amont plus récent sans release `microsandbox-vX.Y.Z` en miroir, une issue de suivi est ouverte avec les empreintes SHA-256 des cinq archives, issues de l'attestation de release vérifiée. Il ne publie rien et ne touche pas au code.

**Mise en miroir (dispatch manuel, après revue).** Workflow_dispatch de `msb-runtime-watch` avec `version=<X.Y.Z>` : vérifie d'abord l'attestation de la release amont (`gh release verify`), télécharge les cinq archives, vérifie chacune contre son empreinte attestée, exige que le `checksums.sha256` amont soit en accord avec l'attestation (tout désaccord échoue fermé), refuse toute release partielle (les cinq plateformes doivent être présentes), puis publie la release `microsandbox-v<version>` avec `--latest=false` et un `SHA256SUMS` construit à partir des empreintes attestées. Une release runtime ne doit jamais remplacer la release applicative comme « Latest », car les URL d'installation du CLI utilisent `/releases/latest/download/`. Le tag pointe vers un SHA de `main`, jamais vers un tag amont. `dry_run=true` exécute le chemin complet sans publier.

**Montée de version du SDK (PR revue).** Lors d'une montée de version de `github.com/superradcompany/microsandbox/sdk/go` :

1. Mettre à jour la dépendance dans `go.mod` et confirmer la valeur renvoyée par `msb.SDKVersion()`.
2. Mettre en miroir la release amont correspondante via le dispatch décrit ci-dessus. Une mise en miroir manuelle de secours doit reproduire le même contrôle : `gh release verify v<X.Y.Z> --repo superradcompany/microsandbox --format json`, extraire les empreintes attestées des cinq archives depuis les sujets vérifiés, vérifier chaque archive téléchargée contre son empreinte attestée (le `checksums.sha256` amont ne sert que de contre-vérification), puis créer la release `microsandbox-v<version>` avec `--latest=false`. Ne jamais publier ni graver des empreintes issues du seul fichier de sommes amont.
3. Mettre à jour ensemble `msbRuntimeVersion`, `msbRuntimeReleaseURL` et les cinq empreintes dans `internal/justcode/msb_runtime_install.go`. Le test `TestMSBRuntimeVersionMatchesSDK` bloque un décalage entre le SDK et le runtime hébergé.
4. Tester au minimum `go test -race ./...`, puis exécuter `just-code doctor --microsandbox` avec un `$MSB_HOME` vide sur une plateforme réelle pour valider le téléchargement, l'empreinte, l'extraction et `msb --version` de bout en bout.
