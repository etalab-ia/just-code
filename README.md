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
- L'un des deux runtimes :
  - Docker ou [Colima](https://github.com/abiosoft/colima) ;
  - [Microsandbox](https://github.com/superradcompany/microsandbox) (`msb` >= 0.6.16) sur un Mac Apple Silicon ou un hôte Linux avec KVM.

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

Renseigne `ALBERT_API_KEY`, puis choisis le runtime dans `.env` :

```dotenv
RUNTIME=docker
# ou
RUNTIME=microsandbox
```

Docker est utilisé par défaut quand `RUNTIME` n'est pas défini. Les deux runtimes publient les mêmes ports et ne doivent pas tourner simultanément : exécute `just stop` avant de modifier `RUNTIME`.

## Utilisation

```bash
just          # liste les commandes
just doctor   # vérifie l'installation du runtime sélectionné
just code     # démarre le sandbox sélectionné et attache le TUI OpenCode
just stop     # arrête le sandbox sélectionné
just check    # santé du backend + provider Albert
just logs     # logs du backend
just shell    # shell dans le sandbox sélectionné
just build    # construit ou télécharge l'image du runtime sélectionné
just restart  # recrée le sandbox sélectionné (destructif)
just clean    # supprime le sandbox et son image ou état local
```

Pour tester ponctuellement l'autre runtime sans modifier `.env` :

```bash
RUNTIME=microsandbox just code
```

Par défaut, `./workspace` est monté comme projet. Pour pointer sur un vrai dépôt :

```bash
export PROJECT_DIR="$HOME/Code/mon-projet"
just code
```

Les serveurs de dev lancés par l'agent sur les ports **3000-3010** sont accessibles depuis le navigateur de l'hôte (`http://localhost:3000`, etc.).

## Prompts d'exemple

Une fois attaché, ces prompts exercent les dimensions clés de l'expérience :

1. **« Qu'y a-t-il dans ce workspace ? Décris la structure du projet. »** — inférence et outils fichiers de base.
2. **« Sur quel OS, noyau et architecture tournes-tu ? »** — confirme que l'agent s'exécute dans le sandbox Linux, pas directement sur ton Mac.
3. **« Code un petit jeu snake servi par un serveur Node sur le port 3000, puis lance-le. »** — écriture de fichiers + serveur de dev ; ouvre `http://localhost:3000` pour vérifier la preview.
4. **« Écris un script python qui affiche la suite de Fibonacci et exécute-le. »** — toolchain polyglotte dans le sandbox.
5. **« Modifie un fichier, puis annule ta modification. »** — outils d'édition et revue de diff.
6. **Détache-toi (Ctrl+C / quitte) et relance `just code`** — continuité de session et reconnexion.
7. **« Envoie les logs de ton serveur de dev dans /tmp/server.log et montre-moi les dernières lignes. »** — comportement du scratch space hors du répertoire projet.

## Choix de conception

- **Deux runtimes, une interface.** Les commandes `just` pilotent Docker par défaut ou Microsandbox avec `RUNTIME=microsandbox`. Le répertoire projet, le port OpenCode et les ports de preview restent identiques.
- **Docker préservé.** Le conteneur d'origine reste disponible pour une installation familière et compatible avec les machines sans hyperviseur Microsandbox.
- **MicroVM nommée et persistante.** Avec Microsandbox, `just stop` conserve le système de fichiers inscriptible et `just code` le redémarre. `just restart` repart de l'image OCI et de `microsandbox.yaml` quand la configuration, l'image ou le projet monté change.
- **Pas de démon Docker pour Microsandbox.** La microVM démarre à la demande depuis l'image OCI officielle `ghcr.io/anomalyco/opencode:latest`.
- **Pas de fichier de config OpenCode bind-mounté.** La configuration du provider Albert est passée inline via `OPENCODE_CONFIG_CONTENT` dans le fichier du runtime. Le seul bind-mount est le répertoire projet.
- **Éditions visibles sur l'hôte.** Les modifications de l'agent atterrissent directement dans ton checkout local. Le modèle « remote-authoritative » (clone dans le sandbox, livraison via branche/PR) reste une expérience ultérieure.
- **Permissions permissives dans le sandbox.** Le runtime sélectionné est la frontière de confinement : `edit`, `bash` et `external_directory` sont autorisés à l'intérieur.
- **Secrets.** `.env` est ignoré par git. Avec Docker, `ALBERT_API_KEY` est injectée dans l'environnement du conteneur. Avec Microsandbox, la vraie valeur reste sur l'hôte : seule une valeur de substitution entre dans la microVM et le proxy réseau ne la remplace que pour `albert.api.etalab.gouv.fr`.

---

just code · département IA dans l'État (IAE), DINUM · Licence MIT
