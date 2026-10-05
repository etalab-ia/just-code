# Configuration (P04)

Ce document décrit la résolution typée introduite par P04 : sources, précédence,
schémas des fichiers gérés et transition depuis la configuration legacy `.env`.

## Principe

La configuration est résolue champ par champ, sans mutation de l'environnement du
processus et sans lecture implicite de fichier. Chaque champ porte sa
provenance. Les secrets n'entrent jamais dans les fichiers gérés : les
identifiants sont référencés par nom (`credentialRef`) et stockés séparément
(P08).

## Précédence (champs non secrets)

Du plus fort au plus faible :

1. Flags explicites (`--runtime`, etc.)
2. Environnement nommé (`JUST_CODE_*`)
3. Manifeste projet (`.just-code/project.json`)
4. Réglages utilisateur globaux (`settings.json`)
5. Valeurs intégrées par défaut

Une valeur explicitement vide gagne sur toute source inférieure : elle signifie
« désactivé », comme le mot de passe serveur vide dans le chemin legacy. La
présence d'une valeur est distinguée de son absence (`Set`).

Seules les variables nommées `JUST_CODE_*` sont lues par le nouveau résolveur.
Les variables legacy non préfixées (`RUNTIME`, `ISOLATION`, `WORKSPACE_DIR`,
`ALBERT_API_KEY`, …) conservent leur sens documenté pendant l'intervalle de
dépréciation, mais n'entrent pas dans la nouvelle résolution.

## Fichiers gérés

| Fichier | Rôle | Version de schéma |
|---|---|---|
| `~/.config/just-code/settings.json` | Réglages globaux utilisateur (sans secrets) | 1 |
| `.just-code/project.json` | Manifeste projet (sans secrets, sans chemins absolus hôte) | 4 |
| `.just-code/lock.json` | Verrou : révisions et digests des éléments épinglés | 3 |

## Skills de projet (P14)

Les skills sélectionnés pendant `just-code init` sont épinglés à une révision
du dépôt `etalab-ia/skills`. En mode versionné, les IDs sont dans
`.just-code/project.json`, et le commit source avec le SHA-256 de chaque
archive sont dans `.just-code/lock.json`. Chaque archive inclut la licence
amont MIT. Les archives vérifiées sont conservées dans le cache utilisateur :
le lancement lit uniquement le lock et ce cache, sans
suivre une branche distante ni exécuter d'installateur côté hôte.

Dans l'invité Microsandbox, les fichiers sont installés sous le répertoire
skills global d'OpenCode (`$XDG_CONFIG_HOME/opencode/skills`, ou
`~/.config/opencode/skills`). Les skills restent hors du checkout invité; une
collision avec un skill existant est refusée sans l'écraser. Les archives sont
limitées aux fichiers réguliers : symlinks, chemins traversants, doublons,
archives corrompues et contenu modifié après installation font échouer la
préparation.

La commande interactive `init` présente le catalogue officiel et les entrées
expérimentales explicitement marquées. En script, répéter `--skill` pour chaque
sélection, par exemple `--skill official/rgaa`. La désélection se fait avec
`--clear-skills`. Le mode versionné ajoute uniquement une zone bornée gérée
dans `AGENTS.md`; tout le texte hors de cette zone est préservé. Avec
`--local-only-skills`, IDs et pins sont stockés dans l'état hôte de l'instance,
et aucun texte de skill n'est ajouté au checkout. Si le projet avait auparavant
une zone gérée versionnée dans `AGENTS.md`, son retrait est montré avant Apply;
le texte utilisateur hors de cette zone reste intact. Pour changer la
sélection ou les pins après création du guest, recrée explicitement
l'environnement après avoir exporté les éventuels changements invités.
Le passage du mode versionné au mode local-only ne rafraîchit pas la copie
scellée d'`AGENTS.md` du guest. Si elle porte encore l'ancienne zone gérée,
`start` et `attach` refusent de poursuivre : exporte les changements invités,
puis lance `just-code workspace sync` ou recrée le guest.

Chaque fichier porte un `schemaVersion`. Un schéma plus récent que la version
supportée par le binaire est une erreur explicite (mettre à jour just-code), pas
une lecture approximative. Les champs à allure de secret (`apiKey`, `token`,
`password`, `secret`, `credentials`) sont rejetés au parse : seule la forme
`credentialRef` est acceptée. Les écritures sont atomiques (temporaire + rename).

La valeur référencée par `credentialRef` vit dans le magasin natif de l'OS
(Keychain, Gestionnaire d'identifiants, Secret Service), géré par
`just-code auth` (P08). Sur les hôtes sans magasin natif, un repli fichier
`0600` existe mais n'est jamais créé sans consentement explicite
(`just-code auth add --fallback`).

## Mises à jour projet (P18)

`just-code update` compare les skills versionnés du projet avec la révision
HEAD du catalogue officiel. La commande affiche les révisions et digests
proposés, puis demande confirmation ; `--skill <id>` répété limite la sélection.
Sans TTY, elle reste en aperçu sauf si `--yes` approuve explicitement toutes
les mises à jour affichées. Une panne réseau ou une archive invalide ne modifie
ni le manifeste, ni le lock, ni les instructions gérées. La résolution peut
alimenter le cache utilisateur avant confirmation ; aucun fichier projet n'est
écrit avant l'approbation.

Les archives sont adressées par révision et SHA-256 dans le cache utilisateur.
P18 ne les purge pas automatiquement : d'anciens locks doivent rester
utilisables hors ligne. Les entrées devenues inutiles ne peuvent être supprimées
qu'après vérification qu'aucun projet ne référence encore leur révision.

Après confirmation, le manifeste et le lock reçoivent le même identifiant de
génération. Un journal temporaire `.just-code/update-journal.json` permet de
restaurer l'ancienne paire si l'écriture s'interrompt ; le prochain `start`,
`init` ou `update` récupère le journal avant de lire la configuration. Il ne
contient que les snapshots validés du manifeste et du lock, jamais les octets
d'`AGENTS.md`. La zone de skills gérée est recalculée en préservant le texte
utilisateur hors marqueurs. Un lancement refuse toute paire dont les
identifiants de génération divergent.

Si le manifeste ou le lock contient des octets qui ne correspondent ni à
l'ancienne ni à la nouvelle version du journal, la récupération automatique
refuse d'écraser ces modifications et nomme le journal concerné. Après examen,
`just-code update --recover --rollback` restaure la paire précédente et recalcule
uniquement la zone gérée dans `AGENTS.md`. Les copies des fichiers gérés présents
sont conservées dans `.just-code/recovery-backups/`. En mode non interactif,
ajouter `--yes` pour approuver explicitement le rollback.

Cette commande met à jour les dépendances projet, pas le binaire just-code.
L'image de base Microsandbox est épinglée au digest OCI
`sha256:b34342987ca889fc2cc19cbc046eefc2418e5980a3d696e209fbb401a288f631` ;
les MCP distants sont des services HTTP sans révision immuable exposée. Les
images et outils livrés avec une version du CLI se mettent à jour via une
version ultérieure du CLI, pas silencieusement au lancement. Lors d'une mise à
niveau depuis just-code 0.7.0, un invité existant créé avec l'ancienne référence
flottante peut demander une recréation explicite. Celle-ci détruit ses sessions,
outils installés et fichiers invités : synchroniser ou exporter le travail
avant de confirmer.

## Résolution de l'identifiant Albert (P09)

Au démarrage d'un runtime, la clé Albert est résolue dans l'ordre :

1. `credentialRef` : `JUST_CODE_CREDENTIAL_REF` > `credentialRef` du
   manifeste projet > celui des réglages utilisateur. Une référence qui ne
   nomme rien dans le magasin est une erreur explicite, jamais un repli
   silencieux.
2. La variable legacy `ALBERT_API_KEY` (ou `.env`).
3. L'identifiant `albert` du magasin.

Sur Microsandbox, la valeur n'est jamais persistée : la liaison est une
référence à une variable d'environnement hôte re-résolue à chaque démarrage
(voir `docs/decisions/2026-09-24-microsandbox-credential-transport.md`,
addendum P09). Les liaisons optionnelles (`github`, `context7`) exigent une
approbation par projet (`just-code bindings approve`), enregistrée dans
l'état local de l'hôte — jamais dans le dépôt.

## Modèle et configuration OpenCode (P10)

La couche gérée de la configuration OpenCode (`OPENCODE_CONFIG_CONTENT`)
porte uniquement les champs managés : `model` et `small_model`. Précédence de
la sélection : `JUST_CODE_MODEL` > `model` du manifeste projet >
`defaultModel` des réglages utilisateur > valeur intégrée
(`albert/deepseek-v4-flash`). Le contenu composé fusionne l'asset embarqué
(provider, permissions) avec la sélection ; OpenCode applique ensuite sa
propre fusion finale, la config projet et la config utilisateur survivant
champ par champ en dessous. Un conflit entre un champ managé et la config
projet est affiché en diff avant lancement (la valeur gérée gagne).

`just-code models` valide la sélection contre le catalogue Albert
(`text-generation` uniquement) ; le catalogue en échec réseau retombe sur le
dernier-known-good persisté dans l'état hôte. Une sélection absente du
catalogue est signalée, jamais effacée.

Les entrées de projet exécutant du code au chargement d'OpenCode (plugins
déclarés et auto-découverts, commandes MCP locales, destinations MCP distantes)
sont approuvées par
contenu via `just-code trust approve` (enregistrement hôte, hors dépôt) ;
`start` refuse tant qu'une entrée est non approuvée ou modifiée depuis
l'approbation.

## MCP distants (P15)

`just-code init --mcp data-gouv --mcp context7` sélectionne les connecteurs
distants gérés ; `--clear-mcps` les désélectionne tous. Le manifeste ne contient
que leurs identifiants et la configuration générée est injectée dans OpenCode
sans modifier les fichiers du projet. Les entrées personnalisées portant un
nom différent restent intactes.

Les destinations sont `https://mcp.data.gouv.fr/mcp` et
`https://mcp.context7.com/mcp`. data.gouv.fr est public. Context7 accepte les
appels anonymes avec une limite plus basse ; le connecteur géré utilise ce
mode et `init` n'exige ni ne transmet de clé.

`just-code mcp status` envoie un MCP `initialize` depuis l'hôte et distingue la
configuration, la vérification du protocole, l'authentification requise, un
accès refusé, une indisponibilité réseau et une dérive de contrat. Le contrôle
est anonyme et ne qualifie donc jamais une réponse 403 d'identifiants invalides.
Il ne prouve pas la connectivité du guest ni l'exécution d'un appel d'outil
depuis OpenCode.

## MCP navigateur (P16)

`playwright` et `chrome-devtools` sont des processus locaux au guest, distincts
des destinations distantes :

```bash
just-code init --root . --mcp playwright --mcp chrome-devtools --yes
```

La sélection compose ces outils dans OpenCode sans démarrer de navigateur sur
l'hôte. Playwright utilise Chromium headless installé dans le guest ;
Chrome DevTools se connecte au serveur de débogage lié à `127.0.0.1:9222`
dans ce même guest. Le statut MCP sur l'hôte ne sonde pas ces processus.

Le profil navigateur est séparé du guest léger : Debian bookworm-slim est
épinglé par digest, avec Chromium `154.0.8037.92` (amd64) / `154.0.8037.57`
(arm64), `fonts-liberation` `1:1.07.4-11`, Node.js
`22.14.0`, Playwright MCP `0.0.82` et Chrome DevTools MCP `1.10.1`. Le
provisionnement vérifie les empreintes et marqueurs avant de déclarer le profil
prêt. Les paquets navigateur sont téléchargés lors du premier démarrage du
profil ; l'initialisation n'installe ni ne redémarre rien dans un guest en
cours. Un changement de profil d'image demande une recréation explicite, qui
efface l'état invité : exporter d'abord les éventuels changements non exportés.

L'accès au serveur local de développement est possible seulement si ce serveur
est joignable depuis le guest. Aucun tunnel automatique vers `localhost` de
l'hôte n'est fourni. La qualification réelle du profil doit être exécutée sur
macOS arm64 et x86_64 ; le passage sur le runtime hôte Linux/KVM reste également
à qualifier.

## `just-code config`

- `just-code config explain` : affiche chaque champ géré avec sa valeur
  effective et sa source. Lecture seule ; n'affiche jamais de valeur secrète.
- `just-code config import-env <chemin>` : prévisualise l'import borné d'un
  `.env` legacy. Les clés reconnues sont mappées, les clés inconnues listées,
  les clés d'identifiants (dont `ALBERT_API_KEY`) ne sont jamais copiées :
  l'outil signale qu'elles doivent rejoindre le magasin d'identifiants. Le
  fichier original n'est jamais modifié.

## Transition legacy

**P12 est arrivé.** Le fichier `.env` n'est plus lu du tout : `LoadConfigEnv`
résout la configuration depuis l'environnement du processus, et un `.env`
présent est signalé avec la commande qui l'adopte
(`just-code config import-env <chemin>`, qui prévisualise et ne modifie jamais
l'original). Les variables non préfixées restent lues depuis l'environnement
pour l'instant ; leur retrait est prévu dans la première version mineure qui
suit l'achèvement du réglage par défaut entièrement typé.

## Compatibilité

`LoadConfig` garde le comportement « déjà exporté gagne », mais **ses défauts
ont changé en P12** : `RUNTIME` vaut `microsandbox` (au lieu d'aucun défaut), et
`ISOLATION` vaut `full` (au lieu de `backend`). `WORKSPACE_DIR` n'a plus de
valeur par défaut dans le fichier : le lancement utilise la racine du projet
découverte quand rien n'est configuré, et un `WORKSPACE_DIR` explicite est
honoré tel quel (`Config.WorkspaceDirSet` enregistre cette provenance).
