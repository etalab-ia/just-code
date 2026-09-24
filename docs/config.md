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

## `just-code config`

- `just-code config explain` : affiche chaque champ géré avec sa valeur
  effective et sa source. Lecture seule ; n'affiche jamais de valeur secrète.
- `just-code config import-env <chemin>` : prévisualise l'import borné d'un
  `.env` legacy. Les clés reconnues sont mappées, les clés inconnues listées,
  les clés d'identifiants (dont `ALBERT_API_KEY`) ne sont jamais copiées :
  l'outil signale qu'elles doivent rejoindre le magasin d'identifiants. Le
  fichier original n'est jamais modifié.

## Transition legacy

Le chemin legacy (`.env` + variables non préfixées) reste le chemin de lancement
actif jusqu'à P12 ; le résolveur typé est interne et exposé uniquement via
`just-code config`. Fenêtre de dépréciation retenue en P04 : les variables non
préfixées sont retirées dans la première version mineure qui suit l'achèvement
de P12 (réglage par défaut entièrement typé). Aucun fichier `.env` n'est lu
implicitement par le nouveau chemin ; l'import est explicite et laisse
l'original intact.

## Compatibilité

Le chemin legacy (`LoadConfig`) est inchangé : mêmes clés, mêmes défauts, même
comportement « déjà exporté gagne ». Le nouveau résolveur ne remplace aucun
appelant existant ; l'adaptation finale est P12.
