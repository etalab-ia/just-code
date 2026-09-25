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
| `.just-code/project.json` | Manifeste projet, versionné (sans secrets, sans chemins absolus hôte) | 1 |
| `.just-code/lock.json` | Verrou : révisions résolues des éléments épinglés | 1 |

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
déclarés et auto-découverts, commandes MCP locales) sont approuvées par
contenu via `just-code trust approve` (enregistrement hôte, hors dépôt) ;
`start` refuse tant qu'une entrée est non approuvée ou modifiée depuis
l'approbation.

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
