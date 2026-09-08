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

**Un backend OpenCode dans un conteneur Docker isolé, piloté par le TUI OpenCode natif de ta machine.**

Première brique d'expérimentation pour la piste « exécution distante » d'[Albert Code](https://github.com/etalab-ia/albert-code) : l'agent tourne dans un conteneur Linux avec les modèles souverains d'[Albert API](https://albert.api.etalab.gouv.fr), pendant que tu gardes ton interface habituelle.

```text
terminal hôte (opencode attach) ──> conteneur :4096 (opencode serve)
                                        ├── /workspace  = ton projet (bind-mount)
                                        ├── outils      = bash, git, node, python
                                        └── inférence   = Albert API (deepseek-v4-flash)
```

Ce n'est **pas un produit** : c'est un terrain de jeu pour mesurer l'UX (latence, reconnexion, persistance de session, previews web) avant d'envisager un backend type E2B auto-hébergé.

## Prérequis

- Docker ou [Colima](https://github.com/abiosoft/colima) qui tourne
- [`just`](https://just.systems) (`brew install just`)
- OpenCode CLI sur l'hôte (`npm install -g opencode-ai`)
- Une **clé Albert API** dans l'environnement (ou dans `.env`, ignoré par git)

## Utilisation

```bash
just          # liste les commandes
just code     # démarre le sandbox et attache le TUI OpenCode
just stop     # arrête le sandbox
just check    # santé du backend + provider Albert
just logs     # logs du backend
just shell    # shell dans le conteneur
just restart  # rebuild complet et redémarrage (destructif)
just clean    # supprime conteneur et image
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
2. **« Sur quel OS, noyau et architecture tournes-tu ? »** — confirme que l'agent s'exécute dans le conteneur Linux, pas sur ton Mac.
3. **« Code un petit jeu snake servi par un serveur Node sur le port 3000, puis lance-le. »** — écriture de fichiers + serveur de dev ; ouvre `http://localhost:3000` pour vérifier la preview.
4. **« Écris un script python qui affiche la suite de Fibonacci et exécute-le. »** — toolchain polyglotte dans le sandbox.
5. **« Modifie un fichier, puis annule ta modification. »** — outils d'édition et revue de diff.
6. **Détache-toi (Ctrl+C / quitte) et relance `just code`** — continuité de session et reconnexion.
7. **« Envoie les logs de ton serveur de dev dans /tmp/server.log et montre-moi les dernières lignes. »** — comportement du scratch space hors du répertoire projet.

## Choix de conception

- **Pas de fichier de config bind-mounté.** La config du provider Albert est passée inline via `OPENCODE_CONFIG_CONTENT` dans `docker-compose.yml`. Un bind-mount de fichier unique apparaîtrait dans le conteneur comme un mountpoint en lecture seule impossible à supprimer, ce qui perturbe les agents.
- **Le seul montage hôte est le répertoire projet.** Les éditions de l'agent atterrissent directement dans ton checkout local. Ça reflète le comportement Lima actuel ; le modèle « remote-authoritative » (clone dans le sandbox, livraison via branche/PR) est une expérience ultérieure.
- **Permissions permissives dans le sandbox.** Le conteneur est la frontière de confinement : `edit`, `bash` et `external_directory` sont autorisés à l'intérieur.
- **Secrets.** `.env` est ignoré par git ; `ALBERT_API_KEY` vient de l'environnement et n'est jamais écrite dans l'image.

---

just code · département IA dans l'État (IAE), DINUM · Licence MIT
