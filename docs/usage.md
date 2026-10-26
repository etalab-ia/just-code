# Utilisation

## Commandes

```bash
just-code                        # démarre (RUNTIME) et attache le TUI
just-code --tart                 # démarre Tart et attache le TUI
just-code --microsandbox         # démarre Microsandbox et attache le TUI
just-code --agent-vm             # démarre agent-vm et attache le TUI
just-code start --tart           # démarre un backend sans attacher le TUI
just-code stop                   # arrête l'instance du projet courant
just-code stop --all             # arrête toutes les instances just-code
just-code check                  # santé du backend actif + provider Albert + ports forwardés (Microsandbox)
just-code logs --tart            # logs d'un runtime explicite
just-code shell --tart           # shell dans un runtime explicite
just-code restart --tart         # arrête puis relance l'instance existante
just-code recreate --tart        # recrée le sandbox (destructif, demande confirmation)
just-code clean --tart            # supprime le sandbox et son état local
just-code doctor --tart           # vérifie l'installation du runtime
just-code version                # identifie le binaire (version, commit, plateforme)
just-code ports list             # liste les previews configurées du projet
just-code ports add 8080:80       # publie le port invité 80 sur localhost:8080
just-code ports remove 8080       # retire cette redirection
just-code help                   # liste les commandes
```

Lancer `just-code` sans commande utilise Microsandbox/full par défaut et lance le TUI OpenCode dans l'invité. S'il existe déjà une session dans le projet, le TUI la reprend automatiquement (`opencode --continue`) ; au premier lancement, il s'ouvre normalement. `/new` démarre une nouvelle conversation. Si just-code ne peut pas vérifier l'existence de la session ou si le CLI OpenCode installé n'annonce pas `--continue`, il avertit et ouvre le TUI sans reprise plutôt que d'échouer. En mode backend explicite, le serveur invité écoute sur 4096 et le TUI hôte s'y attache ; ce port n'est pas transféré en mode full. Les previews Microsandbox utilisent par défaut 3000-3010 sur `127.0.0.1` ; la liste se configure par projet avec `just-code ports` (voir [Configuration](config.md)). Les commandes et les flags de runtime peuvent être donnés dans n'importe quel ordre (`just-code --microsandbox start` et `just-code start --microsandbox` sont équivalents). La commande `code` n'existe pas : la taper renvoie une erreur explicite.

Les environnements sont identifiés par projet (répertoire racine du worktree Git) : chaque projet a sa propre instance `jc-<nom>-<suffixe>`, et les commandes `stop`, `logs`, `shell`, `clean` et `check` ciblent le projet courant. `just-code stop` n'arrête que l'instance du projet courant ; `just-code stop --all` arrête toutes les instances just-code, tous runtimes confondus. Les instances legacy (singleton d'avant l'identification par projet) restent découvrables pour les opérations explicites et ne sont jamais renommées ni supprimées automatiquement.

Les runtimes ne doivent pas tourner simultanément si leurs ports de prévisualisation entrent en conflit. Le port serveur 4096 n'est transféré qu'en mode backend. Si un autre runtime est déjà actif, `just-code` (attachement), `just-code start` et `just-code restart` proposent de l'arrêter avant de continuer. Quand tu quittes le TUI OpenCode, l'attachement propose aussi d'arrêter le backend ; répondre non le laisse disponible pour une reconnexion. `just-code restart` arrête puis relance la même instance sans la recréer ; `just-code recreate` détruit et reconstruit l'environnement, perdant les sessions, les outils installés et les fichiers propres à l'invité. La recréation demande une confirmation interactive (refusée hors TTY, pour CI et scripts).

## Premier lancement Microsandbox

Dans un terminal interactif, le lancement affiche les étapes terminées : copie du projet, skills, MCP et disponibilité de l'agent. La copie indique les fichiers `.env` et les fichiers sensibles exclus. Les avertissements sont regroupés sur une seule ligne.

Si le projet contient des plugins ou des commandes MCP non approuvés, un menu propose **Approuver et démarrer**, **Voir le détail** ou **Quitter**. L'approbation reste sur le poste et porte sur le contenu présenté. Un fichier modifié exige une nouvelle approbation. Sans terminal interactif, le lancement reste bloqué : relire les entrées avec `just-code trust status`, puis les approuver explicitement avec `just-code trust approve`.

La durée complète d'un démarrage réussi, y compris la mise à jour de la configuration de l'instance, est enregistrée dans le répertoire d'état local (`launch-timings.json`). En mode backend, la mesure inclut l'attente de disponibilité de l'agent ; une attente qui échoue n'enregistre aucune durée. Le premier démarrage avec installation des outils et les démarrages suivants ont des mesures séparées. Sans mesure préalable, aucune durée chiffrée n'est annoncée. La première durée avec installation reste conservée, même après de nouveaux démarrages.

OpenCode conserve la version installée dans l'invité : les mises à jour automatiques sont désactivées au lancement.

## Niveaux d'isolation

`--isolation backend|full` (ou la variable d'environnement `ISOLATION`) choisit où vit l'agent :

```bash
just-code --microsandbox --isolation full    # tout l'agent tourne dans la microVM
just-code --tart --isolation backend         # comportement historique
```

En mode `full` (défaut sur un hôte/projet non configuré), le TUI lui-même tourne dans l'invité et l'hôte n'est qu'un passe-plat terminal ; `just-code check` rapporte alors l'état de la VM au lieu de sonder un endpoint de santé, qui n'existe pas dans ce mode. Le mode `backend` est un choix explicite : `opencode serve` tourne dans le sandbox et le TUI s'y attache depuis l'hôte ; seul le processus serveur est confiné, le TUI et les identifiants de connexion restent côté hôte. Sur le runtime Microsandbox, `check` sonde aussi chaque port explicitement configuré par le projet depuis l'hôte : toute réponse HTTP est une prévisualisation joignable, aucune réponse suggère le correctif côté invité (bind `0.0.0.0`). Les autres runtimes n'ont pas de forward de port hôte et ne sont pas sondés.

```bash
just-code check --isolation full             # état de la VM, pas de health check
```


## Répertoire de travail

Sans `WORKSPACE_DIR` explicite, la racine du projet courant est utilisée : Microsandbox en transfère une copie filtrée vers l'invité ; Tart et agent-vm la montent sur `/workspace`. Pour choisir une autre source/racine :

```bash
export WORKSPACE_DIR="$HOME/Code/mon-projet"
just-code
```

`PROJECT_DIR` reste accepté mais est déprécié (un avertissement le signale).
