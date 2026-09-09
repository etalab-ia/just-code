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

**Un backend OpenCode dans une microVM Microsandbox isolée, piloté par le TUI OpenCode natif de ta machine.**

Première brique d'expérimentation pour la piste « exécution distante » d'[Albert Code](https://github.com/etalab-ia/albert-code) : l'agent tourne dans une microVM Linux isolée matériellement avec les modèles souverains d'[Albert API](https://albert.api.etalab.gouv.fr), pendant que tu gardes ton interface habituelle.

```text
terminal hôte (opencode attach) ──> microVM :4096 (opencode serve)
                                       ├── /workspace  = ton projet (bind-mount)
                                       ├── outils      = bash, git, node, python
                                       └── inférence   = Albert API (deepseek-v4-flash)
```

Ce n'est **pas un produit** : c'est un terrain de jeu pour mesurer l'UX (latence, reconnexion, persistance de session, previews web) avec une frontière d'isolation adaptée à l'exécution de code non fiable.

## Prérequis

- [Microsandbox](https://github.com/superradcompany/microsandbox) (`msb` >= 0.6.16) sur un Mac Apple Silicon ou un hôte Linux avec KVM
- [`just`](https://just.systems) (`brew install just`)
- OpenCode CLI sur l'hôte (`npm install -g opencode-ai`)
- Une **clé Albert API** dans l'environnement (ou dans `.env`, ignoré par git)

Installation de Microsandbox :

```bash
curl -fsSL https://install.microsandbox.dev | sh
msb doctor
```

## Utilisation

```bash
just          # liste les commandes
just code     # démarre le sandbox et attache le TUI OpenCode
just stop     # arrête la microVM en conservant son état
just check    # santé du backend + provider Albert
just logs     # logs du backend
just shell    # shell dans la microVM
just pull     # précharge l'image OCI OpenCode
just doctor   # vérifie l'installation et la virtualisation
just restart  # recrée la microVM depuis la configuration (destructif)
just clean    # supprime la microVM et son état inscriptible
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
2. **« Sur quel OS, noyau et architecture tournes-tu ? »** — confirme que l'agent s'exécute dans la microVM Linux, pas sur ton Mac.
3. **« Code un petit jeu snake servi par un serveur Node sur le port 3000, puis lance-le. »** — écriture de fichiers + serveur de dev ; ouvre `http://localhost:3000` pour vérifier la preview.
4. **« Écris un script python qui affiche la suite de Fibonacci et exécute-le. »** — toolchain polyglotte dans la microVM.
5. **« Modifie un fichier, puis annule ta modification. »** — outils d'édition et revue de diff.
6. **Détache-toi (Ctrl+C / quitte) et relance `just code`** — continuité de session et reconnexion.
7. **« Envoie les logs de ton serveur de dev dans /tmp/server.log et montre-moi les dernières lignes. »** — comportement du scratch space hors du répertoire projet.

## Choix de conception

- **MicroVM nommée et persistante.** `just stop` conserve le système de fichiers inscriptible et `just code` le redémarre. `just restart` repart de l'image OCI et de `microsandbox.yaml` quand la configuration, l'image ou le projet monté change.
- **Pas de démon Docker.** Microsandbox démarre une microVM à la demande depuis l'image OCI officielle `ghcr.io/anomalyco/opencode:latest`.
- **Pas de fichier de config OpenCode bind-mounté.** La configuration du provider Albert est passée inline via `OPENCODE_CONFIG_CONTENT` dans `microsandbox.yaml`. Le seul bind-mount est le répertoire projet.
- **Éditions visibles sur l'hôte.** Les modifications de l'agent atterrissent directement dans ton checkout local. Le modèle « remote-authoritative » (clone dans le sandbox, livraison via branche/PR) reste une expérience ultérieure.
- **Permissions permissives dans le sandbox.** La microVM est la frontière de confinement : `edit`, `bash` et `external_directory` sont autorisés à l'intérieur.
- **Secrets.** `.env` est ignoré par git. Microsandbox conserve la vraie valeur d'`ALBERT_API_KEY` sur l'hôte et n'expose qu'un placeholder dans la microVM ; le proxy réseau ne le remplace que pour `albert.api.etalab.gouv.fr`. La clé n'est écrite ni dans l'image, ni dans le dépôt, ni dans l'état persistant de la microVM.

---

just code · département IA dans l'État (IAE), DINUM · Licence MIT
