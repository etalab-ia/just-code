# D-004 : espace de travail invité scellé (clone/instantané filtré, retour revu)

Date : 24 septembre 2026
Statut : accepté
PR : P22 (`feat(workspace): clone invité et retour des changements revus`)
Suivi : #90 (chantier espace scellé), #41 (scan workspace, rôle remplacé), #29 (contrat de configuration)

## Contexte

Le runtime Microsandbox montait le checkout hôte **en écriture** dans
l'invité (`/workspace` ↔ `WorkspaceDir`). Le scan au démarrage introduit par
#41 refusait un workspace porteur de `.env` ou de secrets détectés, mais un
scan ne ferme pas cette fuite : un secret ajouté après le démarrage, dans un
format non détecté, ou écrit par l'agent lui-même reste lisible. La décision
du 22 septembre (plan de parité, P22) est de supprimer le montage : l'invité
possède son espace de travail, sans échappatoire.

Quatre choix étaient explicitement à trancher dans cette PR.

## Décisions

**1. Racine non-Git : instantané filtré, pas de rejet à la découverte.**
`ProjectContext.IsGit` (P05) classe déjà la racine, mais rejeter une racine
non-Git priverait de l'invité scellé tout projet sans dépôt, pour un bénéfice
nul : l'instantané filtré traverse le même filtre, fichier par fichier. C'est
donc l'instantané qui est retenu, et le contrat de découverte de P05 n'a pas
besoin de promettre un rejet.

**2. L'historique Git de l'hôte n'est pas transféré.**
Un `git bundle` aurait préservé l'historique, mais un bundle est un objet
opaque : le filtre ne peut pas inspecter son contenu, donc un `.env` **commité**
aurait traversé la frontière. Le plan est explicite — « every sync resolves a
concrete, reviewable file set before any byte crosses into the guest ». Le
transfert est donc un instantané de l'arbre de travail, filtré fichier par
fichier, et le dépôt de l'invité est créé **dans l'invité** (`git init` +
commit initial). L'URL d'origine de l'hôte est enregistrée comme remote pour
la livraison par branche/PR de P13.

**3. Modifications hôte non commitées : incluses via l'instantané filtré,**
puisque c'est précisément l'arbre de travail qui est transféré. Elles ne sont
jamais incluses par une voie qui contournerait le filtre.

**4. Rafraîchissement additif, refusé si l'invité a du travail non commité.**
L'extraction n'efface pas les fichiers ajoutés dans l'invité, mais un fichier
modifié des deux côtés serait écrasé en silence : c'est le cas qui justifie
un arrêt. `workspace sync` refuse donc tant que `git status --porcelain` dans
l'invité n'est pas vide, nomme les fichiers, et n'écrase qu'avec `--force`.

## Conséquences

- **`WORKSPACE_DIR` change de sens** : dans le modèle scellé, ce n'est plus le
  répertoire monté, c'est le **répertoire source** côté hôte dont le contenu
  filtré est transféré. Le défaut `./workspace` (un sous-répertoire) n'a plus
  de raison d'être pour le parcours à zéro option : P12 fera pointer la source
  sur la racine du projet. Cette PR ne change pas le défaut (c'est le contrat
  de configuration, et le changer ici brouillerait la frontière de revue) mais
  distingue déjà les deux cas de source vide — répertoire sans fichier
  (projet légitimement vide, l'invité reçoit quand même un dépôt) et tous les
  candidats refusés par le filtre (refus explicite, avec les raisons).
- Le scan de workspace perd son rôle de frontière : il devient un **conseil
  d'hygiène** côté hôte, et sa logique de détection est réutilisée **à la
  frontière de transfert**, où un refus est effectif (`ResolveTransferSet`).
- Le filtre est structurellement inévitable : le transfert écrit **un** payload
  construit depuis l'ensemble résolu (`BuildTransferArchive`), jamais une copie
  d'arborescence (`CopyFromHost` du SDK est délibérément non utilisé).
- Les instances existantes à montage lié ne sont jamais adoptées ni converties :
  elles sont héritées et exigent une recréation explicite. Le contrôle lit la
  configuration **persistée** du sandbox, pas notre spec.
- Le retour des changements est un **patch revu** (`workspace export`) tant que
  le grant GitHub n'est pas activé ; P13 ajoute le push de branche/PR.
- La vérification de bout en bout (canari illisible depuis l'invité) exige un
  hôte à virtualisation : la logique de filtre, d'archive et de provisionnement
  est testée unitairement ici, et la preuve d'exécution invité suit le modèle
  de P02/P03.

👾 Generated with [Letta Code](https://letta.com)
