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
just-code check                  # santé du backend actif + provider Albert
just-code logs --tart            # logs d'un runtime explicite
just-code shell --tart           # shell dans un runtime explicite
just-code restart --tart         # arrête puis relance l'instance existante
just-code recreate --tart        # recrée le sandbox (destructif, demande confirmation)
just-code clean --tart            # supprime le sandbox et son état local
just-code doctor --tart           # vérifie l'installation du runtime
just-code version                # identifie le binaire (version, commit, plateforme)
just-code help                   # liste les commandes
```

Lancer `just-code` sans commande utilise Microsandbox/full par défaut et lance le TUI OpenCode dans l'invité. S'il existe déjà une session dans le projet, le TUI la reprend automatiquement (`opencode --continue`) ; au premier lancement, il s'ouvre normalement. `/new` démarre une nouvelle conversation. Si just-code ne peut pas vérifier l'existence de la session ou si le CLI OpenCode installé n'annonce pas `--continue`, il avertit et ouvre le TUI sans reprise plutôt que d'échouer. En mode backend explicite, le serveur invité écoute sur 4096 et le TUI hôte s'y attache ; ce port n'est pas transféré en mode full. Les ports de prévisualisation 3000-3010 restent liés à `127.0.0.1`. Les commandes et les flags de runtime peuvent être donnés dans n'importe quel ordre (`just-code --microsandbox start` et `just-code start --microsandbox` sont équivalents). La commande `code` n'existe pas : la taper renvoie une erreur explicite.

Les environnements sont identifiés par projet (répertoire racine du worktree Git) : chaque projet a sa propre instance `jc-<nom>-<suffixe>`, et les commandes `stop`, `logs`, `shell`, `clean` et `check` ciblent le projet courant. `just-code stop` n'arrête que l'instance du projet courant ; `just-code stop --all` arrête toutes les instances just-code, tous runtimes confondus. Les instances legacy (singleton d'avant l'identification par projet) restent découvrables pour les opérations explicites et ne sont jamais renommées ni supprimées automatiquement.

Les runtimes partagent les ports de prévisualisation 3000-3010, liés à `127.0.0.1`, et ne doivent pas tourner simultanément si ces ports entrent en conflit. Le port serveur 4096 n'est transféré qu'en mode backend. Si un autre runtime est déjà actif, `just-code` (attachement), `just-code start` et `just-code restart` proposent de l'arrêter avant de continuer. Quand tu quittes le TUI OpenCode, l'attachement propose aussi d'arrêter le backend ; répondre non le laisse disponible pour une reconnexion. `just-code restart` arrête puis relance la même instance sans la recréer ; `just-code recreate` détruit et reconstruit l'environnement, perdant les sessions, les outils installés et les fichiers propres à l'invité. La recréation demande une confirmation interactive (refusée hors TTY, pour CI et scripts).

## Niveaux d'isolation

`--isolation backend|full` (ou la variable d'environnement `ISOLATION`) choisit où vit l'agent :

```bash
just-code --microsandbox --isolation full    # tout l'agent tourne dans la microVM
just-code --tart --isolation backend         # comportement historique
```

En mode `full` (défaut sur un hôte/projet non configuré), le TUI lui-même tourne dans l'invité et l'hôte n'est qu'un passe-plat terminal ; `just-code check` rapporte alors l'état de la VM au lieu de sonder un endpoint de santé, qui n'existe pas dans ce mode. Le mode `backend` est un choix explicite : `opencode serve` tourne dans le sandbox et le TUI s'y attache depuis l'hôte ; seul le processus serveur est confiné, le TUI et les identifiants de connexion restent côté hôte.

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
