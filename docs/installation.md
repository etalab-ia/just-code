# Installation

## Binaire précompilé (recommandé)

Chaque [release](https://github.com/etalab-ia/just-code/releases) publie des binaires pour `darwin-arm64`, `linux-arm64`, `linux-x64`, `windows-x64.exe` et `windows-arm64.exe`, avec un fichier `SHA256SUMS`. macOS Intel n'est pas pris en charge : le SDK Microsandbox ne fournit pas de bibliothèque FFI pour `darwin/amd64`.

Sur macOS ou Linux, télécharge et lance l'installateur POSIX :

```sh
curl -fsSL https://raw.githubusercontent.com/etalab-ia/just-code/main/scripts/install.sh -o install-just-code.sh
sh install-just-code.sh
```

Dans PowerShell sous Windows :

```powershell
Invoke-WebRequest https://raw.githubusercontent.com/etalab-ia/just-code/main/scripts/install.ps1 -OutFile install-just-code.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File .\install-just-code.ps1
```

L'installateur choisit une seule release stable, télécharge le binaire et `SHA256SUMS` depuis cette même release, puis compare l'empreinte avant de remplacer l'installation. Le binaire est installé sans privilèges élevés sous `~/.local/bin` (macOS/Linux) ou `$env:LOCALAPPDATA\Programs\just-code` (Windows). Une mise à niveau conserve le binaire précédent sous `just-code.previous` (macOS/Linux) ou `just-code.previous.exe` (Windows). Sous Windows, ferme les sessions `just-code` en cours avant la mise à niveau. Si `just-code setup` échoue, restaure le binaire précédent dans le même répertoire ; sous Windows, par exemple : `Copy-Item -LiteralPath "$env:LOCALAPPDATA\Programs\just-code\just-code.previous.exe" -Destination "$env:LOCALAPPDATA\Programs\just-code\just-code.exe" -Force`. L'assistant `just-code setup` est lancé après l'installation.

Si le répertoire d'installation n'est pas déjà dans `PATH`, l'installateur affiche la commande à exécuter dans la session courante. Il ne modifie pas les profils de shell ni les variables d'environnement persistantes. Sous Windows, l'exécution utilise `-ExecutionPolicy Bypass` uniquement pour le processus PowerShell lancé par cette commande ; aucune stratégie permanente n'est changée.

La somme SHA-256 détecte une altération ou un mélange d'assets, mais elle est téléchargée depuis la même release GitHub que le binaire. Les releases des binaires `just-code` ne publient pas d'attestation vérifiée de provenance : cette vérification ne prouve donc pas l'identité de l'éditeur. Les scripts d'installation ne sont pas signés ni attestés non plus ; leur provenance doit être évaluée séparément.

Les binaires macOS ne sont ni signés ni notariés, et portent une signature ad-hoc (requise pour exécuter un binaire non signé sur Apple Silicon). `curl` ne pose pas l'attribut `com.apple.quarantine`, donc Gatekeeper ne bloque pas ce chemin d'installation ; un téléchargement via navigateur reste à débloquer avec `xattr -d com.apple.quarantine <binaire>`. La notarisation complète exigerait une adhésion Apple Developer et un certificat Developer ID.

## Dépendances de l'hôte

- OpenCode CLI sur l'hôte (`npm install -g opencode-ai`)
- Une **clé Albert API** dans l'environnement (ou dans le magasin d'identifiants, voir [Identifiants et secrets](credentials.md))
- L'un des runtimes disponibles :
  - [Microsandbox](https://github.com/superradcompany/microsandbox) sur un Mac Apple Silicon, Linux (KVM) ou Windows arm64/x64 (Windows Hypervisor Platform / WHP) ; l'exécutable `msb` n'a pas besoin d'être installé ;
  - [Tart](https://github.com/openai/tart) (`brew install openai/tools/tart`) sur un Mac Apple Silicon pour les environnements de dev macOS (notamment Xcode / iOS) ;
  - [agent-vm](https://github.com/sylvinus/agent-vm) ([Lima](https://lima-vm.io) requis) sur macOS ou Linux : une VM Debian persistante par workspace, clonée depuis un template de base construit par just-code.

Le runtime Microsandbox est téléchargé et vérifié automatiquement au premier démarrage ; le mode manuel (`MSB_PATH`) et les prérequis plateforme sont détaillés dans [Runtime Microsandbox](runtime-microsandbox.md).

## Depuis les sources (contributeurs)

Prérequis : [Go](https://go.dev) >= 1.22 et une chaîne C native (Xcode Command Line Tools sur macOS, `gcc` sur Linux, MinGW-w64 x64 ou LLVM-MinGW sur Windows ; sous Windows arm64, la chaîne doit cibler `aarch64-w64-mingw32`).

```bash
go build -o just-code ./cmd/just-code
go test ./...
```
