# Identifiants et secrets

## Protection des identifiants par runtime

Le niveau d'isolation ne détermine pas à lui seul ce qu'un agent peut lire : c'est le runtime qui décide si la clé Albert est substituée à la frontière réseau ou déposée en clair dans l'invité.


| Runtime | Injection protégée | Portée |
|---|---|---|
| Microsandbox | Oui, en `backend` **et** en `full` | La clé n'existe que côté hôte ; l'invité reçoit le placeholder `$MSB_ALBERT_API_KEY`, remplacé par le proxy réseau uniquement vers `albert.api.etalab.gouv.fr` |
| Tart | Non | La clé est lisible dans l'invité, en `backend` comme en `full` |
| agent-vm | Non | Idem |

Sur Microsandbox, le secret est déclaré via l'API de secrets du runtime et la substitution réseau se fait à la frontière : passer en `full` place le TUI dans la microVM sans exposer la clé pour autant. Un sandbox créé par une version antérieure, qui persistait la clé en clair dans l'environnement invité, est refusé au démarrage avec la commande de recréation à lancer ; l'ancienne valeur ne peut pas être remplacée sur place. Si l'environnement invité ne peut pas être relu, le démarrage est refusé de la même façon : sans cette lecture, rien ne distingue un sandbox sain d'un sandbox qui expose encore la clé.

Sur Tart et agent-vm, les secrets ne passent jamais par la ligne de commande : en mode `full` ils sont transmis sur l'entrée standard (Tart) ou par un fichier `0600` copié dans l'invité (agent-vm), et le TUI les lit depuis ce fichier. Le fichier porte aussi la configuration du provider Albert, sans quoi le TUI n'aurait pas de modèle à utiliser. Ces deux runtimes n'ont pas de proxy audité : la clé y est en clair dans l'invité, quel que soit le niveau d'isolation. Le démarrage l'exige donc explicitement : sans `--acknowledge-guest-credentials`, `start` est refusé ; avec, l'avertissement reste affiché à chaque démarrage. Réservez-les aux travaux qui n'ont pas besoin de la clé, ou traitez l'invité comme portant un identifiant vivant.


## Stockage global des identifiants (`auth`)

`just-code auth` stocke les identifiants globaux dans le magasin natif de l'OS — Keychain macOS, Gestionnaire d'identifiants Windows, Secret Service Linux — jamais dans un fichier projet ni un profil shell. Les commandes :

```bash
just-code auth add [albert|github|context7]   # saisie masquée interactive (ou --stdin pour un pipe)
just-code auth status                         # état du magasin, jamais les valeurs
just-code auth remove [albert|github|context7] # révocation puis suppression
```

Le secret ne passe jamais par la ligne de commande (argv) : la saisie interactive est masquée, `--stdin` lit une ligne sur l'entrée standard. Sur les hôtes sans magasin natif (Linux headless sans Secret Service), le repli `--fallback` écrit un fichier JSON `0600` dans le répertoire de configuration — **jamais créé implicitement** : sans consentement explicite (`auth add --fallback`), toute écriture échoue.

L'ordre de résolution de la clé au démarrage est détaillé plus bas. Les identifiants restent référencés par nom (`credentialRef`) dans la configuration gérée ; la valeur ne figure jamais dans `settings.json`, `project.json` ni les exports.

Un magasin natif indisponible ou verrouillé est une erreur explicite au démarrage, jamais un repli silencieux vers le fichier : ce fichier peut détenir un identifiant périmé ou différent précisément quand l'attendu ne peut pas être vérifié. Seul un « introuvable » (aucune entrée) poursuit vers le repli consentit.

La rotation est détectée sans jamais toucher la valeur : `auth add` incrémente un compteur par identifiant (état hôte, non secret), et la réconciliation compare ce compteur — remplacer une clé sur une instance saine planifie un rafraîchissement au lieu du no-op.


## Résolution de la clé Albert

Au démarrage d'un runtime, la clé Albert est résolue dans l'ordre :

1. `credentialRef` : `JUST_CODE_CREDENTIAL_REF` > `credentialRef` du
   manifeste projet > celui des réglages utilisateur. Une référence qui ne
   nomme rien dans le magasin est une erreur explicite, jamais un repli
   silencieux.
2. La variable d'environnement `ALBERT_API_KEY`.
3. L'identifiant `albert` du magasin.

Sur Microsandbox, la valeur n'est jamais persistée : la liaison est une
référence à une variable d'environnement hôte re-résolue à chaque démarrage
(voir [le document de décision dédié](decisions/2026-09-24-microsandbox-credential-transport.md)).
Les liaisons optionnelles (`github`, `context7`) exigent une
approbation par projet (`just-code bindings approve`), enregistrée dans
l'état local de l'hôte — jamais dans le dépôt.


## Liaisons d'identifiants (`bindings`)

Sur Microsandbox, les identifiants sont injectés par le proxy de secrets : la valeur brute n'est jamais persistée (ni dans la base du runtime, ni dans l'environnement invité) — la liaison est une référence à une variable d'environnement hôte, re-résolue à chaque application et à chaque démarrage. La liaison `albert` est obligatoire ; `github` et `context7` sont **optionnelles et approuvées projet par projet** :

```bash
just-code bindings list              # liaisons connues, approbations, état du magasin
just-code bindings approve github    # autorise la liaison github pour ce projet
just-code bindings revoke github     # retire l'approbation et révoque l'instance du projet
```

L'approbation est un enregistrement local à l'hôte (`~/.local/state/just-code/instances/<instance>/bindings.json`) : elle n'est jamais versionnée et ne voyage pas avec un clone. Les hôtes autorisés sont disjoints entre liaisons, donc une liaison ne peut pas être substituée vers la destination d'une autre.

## GitHub dans l'invité

Le workflow est optionnel et limité à Microsandbox. Stocke d'abord le jeton,
puis active le workflow pendant l'initialisation du projet :

```bash
just-code auth add github
just-code init --github
```

L'assistant vérifie que le projet a un `origin` GitHub.com et que le jeton est
stocké. L'approbation reste locale à l'hôte ; elle n'apparaît ni dans le
manifeste ni dans le dépôt. Pour un projet déjà initialisé, utilise
`just-code bindings approve github` après avoir stocké le jeton. Le parcours
sans `--github` n'ajoute aucune approbation et ne modifie pas les approbations
existantes. Sans approbation locale, `gh` n'est pas installé par just-code.
Pour désactiver un accès déjà approuvé : `just-code bindings revoke github`.

Au premier lancement approuvé, just-code installe GitHub CLI 2.100.0 dans la
microVM uniquement, vérifie son empreinte, puis teste l'authentification,
l'accès au dépôt et le rôle d'écriture du compte. Le checkout hôte n'est jamais monté :
la branche invitée est basée sur la branche par défaut du dépôt et reçoit
l'instantané filtré du workspace. Les fichiers déjà présents sur la branche distante
mais absents de l'instantané restent préservés. Si l'invité contient des
modifications non commitées, la préparation s'arrête sans les écraser.
L'historique invité antérieur est conservé dans une branche locale
`<branche-invitée>-snapshot-backup` : son contenu est repris dans la nouvelle
branche sans perdre l'accès aux commits antérieurs. L'origine et les marqueurs
de préparation sont publiés ensemble. Un changement d'origine approuvée est
refusé avant un redémarrage ; relis et exporte les changements invités avant
de recréer l'environnement.

La préparation ne publie rien automatiquement. Après revue des changements
dans le guest, l'agent peut pousser sa branche puis ouvrir une PR en brouillon :

```bash
git push -u origin HEAD
gh pr create --draft
```

Pour un jeton fine-grained, accorde `Contents: read and write` et
`Pull requests: read and write` sur le seul dépôt concerné ; une organisation
peut aussi exiger son approbation. Le rôle du compte est vérifié à l'initialisation,
mais GitHub applique aussi les permissions propres au jeton lors du push et de
la création de PR. Le GitHub CLI de l'hôte n'est pas utilisé.

## Révocation

`just-code auth remove` révoque l'accès avant de supprimer l'entrée du magasin : sur les instances Microsandbox en cours d'exécution, la liaison proxy est supprimée à chaud (l'invité garde un placeholder inerte jusqu'au prochain redémarrage — un avertissement le signale) ; sur les instances arrêtées, la référence persistée est retirée pour le prochain démarrage. La révocation raisonne par **entrée de magasin**, pas par nom de liaison : un `credentialRef` peut alimenter la liaison Albert depuis une entrée nommée autrement, et c'est la liaison invitée effectivement alimentée qui est retirée (l'instantané le consigne sous la forme `entrée@magasin#liaison`). Supprimer l'entrée d'un magasin ne touche pas les instances liées à l'autre magasin ; une instance dont la clé venait de l'environnement (variable `ALBERT_API_KEY`) est ignorée, cette variable n'appartenant pas à just-code ; une source non confirmable est révoquée puis signalée. Sur Tart et agent-vm, la clé est lisible en clair dans l'invité : la suppression est **refusée** tant qu'une telle instance tourne — y compris lorsque l'entrée supprimée n'est pas nommée `albert` mais alimente la liaison Albert — car retirer la copie du magasin ne révoquerait rien.
