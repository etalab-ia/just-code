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

## Importer les secrets applicatifs

L'import commence par une prévisualisation :

```bash
just-code secrets import --profile development --file .env --file apps/api/.env.local
just-code secrets import --profile development --file .env --apply
```

Dans un terminal, l'assistant peut proposer les fichiers dotenv du projet et
des sous-projets ; aucun fichier n'est importé automatiquement. Les chemins
doivent rester dans la racine canonique du projet. Les liens symboliques et
les répertoires usuels de dépendances/build sont exclus de la découverte. En
mode non interactif, indique chaque `--file`. L'aperçu masque les valeurs et
signale les clés répétées ; la dernière définition prévaut. `--apply` demande
confirmation dans un terminal ; un script doit utiliser `--yes` explicitement.

Le périmètre est **checkout projet → profil → fichier/composant → variable**.
Deux projets, profils ou fichiers ayant le même nom de variable restent
distincts. Les aliases symboliques de la même racine convergent ; deux clones
différents ne partagent pas leurs secrets implicitement. Le manifeste projet
ne reçoit ni valeurs ni références locales. Les fichiers sources ne sont pas
modifiés.

Le parseur utilise le dialecte `godotenv` sans expansion : `$VAR`, `${VAR}` et
`$(...)` restent des valeurs littérales ; aucune variable d'environnement
n'est lue et aucune commande exécutée. La taille d'un fichier est plafonnée à
1 MiB ; chaque valeur à 951 octets UTF-8 pour que son enveloppe base64, encodée
en UTF-16 par Windows Credential Manager, reste dans sa limite de 2 560 octets.

L'import stocke uniquement les valeurs : il n'approuve aucune destination,
n'active pas de liaison proxy et ne livre aucun fichier au guest. `--fallback`
choisit explicitement le magasin JSON local propriétaire (`0600`), qui n'est
pas chiffré par just-code.

## Exposer un secret importé au guest (`secrets bindings`)

Importer ne donne aucun accès. L'étape séparée qui autorise le proxy à exposer
une valeur est l'approbation de liaison, enregistrée localement à l'hôte :

```bash
just-code secrets profile development
just-code secrets bindings approve development .env API_KEY --destination api.example.com
just-code secrets bindings list
just-code secrets bindings revoke development .env API_KEY
```

`--destination` est répétable. Approuver une destination **est** la décision de
sécurité : c'est ce qui autorise la substitution de la valeur vers ces hôtes et
nulle part ailleurs. L'approbation vit dans l'état local de l'hôte, jamais dans
le dépôt, donc elle ne suit pas un clone. Une même destination peut être
partagée par plusieurs liaisons : la qualification native montre que
l'autorisation reste propre à chaque liaison.

La variable invitée est dérivée du composant et du nom (`JC_API_KEY` pour
`.env`, `JC_APPS_API_API_KEY` pour `apps/api/.env`), sauf `--guest-env`
explicite. Les noms réservés aux liaisons système (`ALBERT_API_KEY`,
`GITHUB_TOKEN`, `CONTEXT7_API_KEY`) sont refusés : deux liaisons ne doivent
jamais se disputer une variable.

Dans le guest, la variable contient un marqueur opaque sans dollar
(`__JUST_CODE_SECRET_…__`), jamais la valeur. Le choix de ce marqueur est
qualifié : les chargeurs dotenv usuels suppriment une référence `$MSB_…` non
définie, y compris entre guillemets, alors qu'un marqueur opaque survit aux
modes testés (voir `tests/integration/project-secrets/`).

Le profil actif est explicite : `secrets profile <name>` sélectionne le seul
profil livré, et `--clear` est refusé tant que des liaisons restent approuvées,
car cet état ferait échouer chaque démarrage. Une liaison approuvée dont la
valeur importée a disparu, ou dont le magasin est verrouillé, **bloque le
démarrage** au lieu d'être ignorée : omettre une liaison la transformerait en
état désiré, et le runtime retirerait alors une inscription active.

Retirer une approbation (`revoke`) supprime l'accès du guest, pas la valeur
importée ; l'inscription proxy est retirée à la prochaine application, et le
guest conserve un marqueur inerte jusqu'au redémarrage.

## Secrets lisibles dans le guest (`--delivery guest`)

Certaines valeurs ne peuvent pas passer par le proxy réseau, parce que
l'application les consomme localement : un mot de passe de base de données, une
clé de signature. Il n'y a alors aucune requête sortante à réécrire, donc la
valeur réelle doit exister dans l'invité. C'est un mode **distinct**, approuvé
séparément :

```bash
just-code secrets bindings approve development .env DB_PASSWORD --delivery guest
```

Ce mode n'est pas le mode protégé, et l'approbation affiche l'avertissement
correspondant :

- tout ce qui tourne dans le sandbox, **y compris l'agent**, peut lire la valeur ;
- elle reste lisible aussi longtemps que le sandbox la détient ;
- un instantané pris pendant qu'elle est livrée la capture, et supprimer le
  fichier ensuite ne nettoie pas cet instantané.

Le mode par défaut (`--delivery proxy`, ou sans option) reste la protection par
proxy : la valeur ne franchit jamais la frontière. Les deux modes sont
exclusifs — une liaison lisible dans l'invité n'a ni destination ni variable
invitée, puisque rien n'est substitué vers un hôte — et il n'existe **aucun
repli automatique** de l'un vers l'autre : une valeur n'est jamais lisible dans
l'invité sans une approbation explicite.

Deux encodages sont utilisés selon ce que la valeur contient. Les guillemets
simples sont la forme par défaut : ils portent exactement les retours à la
ligne, les espaces, `#`, les guillemets doubles et un antislash isolé. Quand
les guillemets ne suffisent pas — deux antislashs consécutifs, ou une valeur qui
se termine par un antislash — la valeur est écrite **sans guillemets**. Dans une
valeur non quotée, aucun des trois chargeurs ne traite les antislashs : les deux
formes y passent donc verbatim. C'est ce qui rend livrables les chemins Windows
et UNC (`C:\Users\`, `\\serveur\partage\`) et toute valeur terminée par un
séparateur de répertoire que les guillemets altéreraient ou feraient refuser le
fichier entier.

La forme brute n'accepte pas n'importe quoi : un `#`, un `$`, une apostrophe, un
guillemet, un accent grave, un caractère de contrôle, ou un caractère que les
chargeurs suppriment en début ou en fin de valeur y sont refusés. Le jeu accepté
a été mesuré, pas supposé, contre les trois chargeurs épinglés. Le **non-ASCII
est accepté** : une valeur non quotée n'est jamais décodée, donc `C:\Répertoire\`
ou `naïve\` passent octet pour octet. Seuls restent **refusés** à l'approbation
les cas qu'aucune des deux formes ne porte : une apostrophe, un `$`, un retour
chariot, et une séquence UTF-8 invalide. Le refus nomme la variable et la raison ;
une valeur refusée est un désagrément, une valeur silencieusement modifiée est un
incident qui se produit loin d'ici. Pour ces valeurs, la livraison par proxy
reste la bonne réponse dès lors que l'application joint le service par le réseau.

Le fichier livré est écrit en `0600`, et la restriction est appliquée au fichier
temporaire **avant** le renommage : le runtime crée les fichiers en `0644`, donc
restreindre après coup laisserait une fenêtre pendant laquelle le secret serait
lisible par tous les comptes de l'invité. Un fichier qui contient une valeur
réelle porte un avertissement en tête, et `just-code secrets bindings list`
distingue les deux modes.

## Fichiers dotenv générés dans le guest

À chaque démarrage, les liaisons approuvées sont aussi écrites dans le
workspace du guest, à l'emplacement de leur composant :

```
/workspace/.env
/workspace/apps/api/.env
```

Chaque fichier ne contient que les marqueurs opaques des variables approuvées
pour ce composant, précédés d'un en-tête `# just-code managed: do not edit`.
Les composants gardent des fichiers distincts : l'ordre de chargement du
framework est donc préservé, et deux sous-projets qui définissent la même
variable ne se mélangent pas. Un composant sans liaison approuvée ne produit
aucun fichier, pour ne pas masquer un fichier fourni par le projet.

just-code n'écrase jamais un fichier qu'il n'a pas écrit. Si un `.env` fourni
par le projet occupe la cible, la publication est **refusée** avec le chemin
concerné, au lieu de détruire votre contenu. Les écritures passent par un
fichier temporaire puis un renommage, et un fichier géré dont l'approbation est
retirée est supprimé au démarrage suivant — la suppression revérifie l'en-tête
au moment d'agir, pour ne jamais effacer un fichier que vous auriez mis à la
place.

L'inventaire de ce que le sandbox contient réellement (variables invitées et
fichiers gérés) est conservé localement à l'hôte, à côté des approbations
(`~/.local/state/just-code/project-secrets/<projet>/applied.json`). Il est relu
à chaque application : c'est ce qui permet à une révocation de désinscrire
effectivement la liaison, y compris après un `restart`.

Ces fichiers ne contiennent jamais de valeur : seulement les mêmes marqueurs que
les variables d'environnement du guest. La valeur réelle reste sur l'hôte et
n'est substituée que par le proxy réseau, uniquement vers les destinations
approuvées. Un client qui signe localement le marqueur reste hors périmètre.

## Révocation

`just-code auth remove` révoque l'accès avant de supprimer l'entrée du magasin : sur les instances Microsandbox en cours d'exécution, la liaison proxy est supprimée à chaud (l'invité garde un placeholder inerte jusqu'au prochain redémarrage — un avertissement le signale) ; sur les instances arrêtées, la référence persistée est retirée pour le prochain démarrage. La révocation raisonne par **entrée de magasin**, pas par nom de liaison : un `credentialRef` peut alimenter la liaison Albert depuis une entrée nommée autrement, et c'est la liaison invitée effectivement alimentée qui est retirée (l'instantané le consigne sous la forme `entrée@magasin#liaison`). Supprimer l'entrée d'un magasin ne touche pas les instances liées à l'autre magasin ; une instance dont la clé venait de l'environnement (variable `ALBERT_API_KEY`) est ignorée, cette variable n'appartenant pas à just-code ; une source non confirmable est révoquée puis signalée. Sur Tart et agent-vm, la clé est lisible en clair dans l'invité : la suppression est **refusée** tant qu'une telle instance tourne — y compris lorsque l'entrée supprimée n'est pas nommée `albert` mais alimente la liaison Albert — car retirer la copie du magasin ne révoquerait rien.
