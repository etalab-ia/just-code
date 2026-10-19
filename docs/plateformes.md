# Plateformes prises en charge et niveau de qualification

Ce document distingue deux choses qu'il est tentant de confondre :

- **pris en charge** : le runtime existe pour cette plateforme, just-code sait le
  télécharger et le vérifier, et la compilation comme les tests passent en
  intégration continue ;
- **qualifié** : une microVM a réellement démarré sur cette plateforme, avec une
  preuve exécutée et conservée dans le dépôt.

Une plateforme peut être prise en charge sans être qualifiée. C'est le cas de
toutes sauf macOS arm64 et Linux amd64 aujourd'hui. Aucun résultat de compilation ne remplace
un démarrage réel : les runners hébergés n'ont pas de virtualisation imbriquée,
et le fichier `.github/workflows/ci.yml` le dit à l'endroit où il définit ces
tâches.

## Runtime Microsandbox

| Plateforme | Artefact du runtime | Compilé et testé en CI | MicroVM réellement démarrée |
| --- | --- | --- | --- |
| macOS arm64 (`darwin/arm64`) | oui | oui | **oui** |
| Linux x86-64 (`linux/amd64`) | oui | oui | **oui** |
| Linux arm64 (`linux/arm64`) | oui | oui | non |
| Windows x86-64 (`windows/amd64`) | oui | oui | non |
| Windows arm64 (`windows/arm64`) | oui | oui | non |
| macOS Intel (`darwin/amd64`) | **non** | — | — |

macOS Intel n'est pas une plateforme prise en charge, et ne doit pas être
annoncée comme telle : la version épinglée du runtime ne publie aucun artefact
`darwin-x86_64`, ni binaire `msb`, ni bibliothèque FFI `darwin-amd64`. just-code
refuse donc explicitement cette combinaison plutôt que d'échouer plus tard, et
un test le vérifie (`msbRuntimeArtifactFor`).

Les cinq combinaisons prises en charge ont une empreinte SHA256 épinglée dans
`internal/justcode/msb_runtime_install.go` : le runtime téléchargé est vérifié
avant d'être extrait, et une version publiée différente n'est pas adoptée
silencieusement.

## Magasins d'identifiants natifs

Le magasin est une question distincte du runtime : il vit sur l'hôte et ne
dépend pas de la virtualisation. C'est pourquoi sa matrice se qualifie
séparément.

| Plateforme | Adaptateur | Contrat de l'adaptateur testé | Magasin réel exercé |
| --- | --- | --- | --- |
| macOS | Trousseau (`security`) | oui | non (preuve non conservée) |
| Linux | Secret Service (`secret-tool`) | oui | **oui** |
| Windows | Credential Manager (PowerShell, P/Invoke `advapi32`) | oui | non |

Le Trousseau a bien été exercé sur macOS arm64 : `put` / `get` / `remove`,
rotation, et absence d'erreur sur un retrait d'élément absent. Mais le
transcript de cette exécution n'a pas été conservé dans le dépôt, et ce document
ne reconnaît comme qualifié que ce qui est adossé à une preuve conservée. La
colonne reste donc à « non » jusqu'à ce qu'une exécution produise un fichier
dans `tests/integration/credentials/evidence/`. La commande qui la produit est
`go test ./internal/justcode/ -run TestRealStore -v` sur une machine où le
service répond. Le magasin Linux est qualifié depuis 2026-10-16 : deux
transcripts `--- PASS` conservés (`linux-amd64-run-1.txt`,
`linux-amd64-run-2.txt`), obtenus dans une session D-Bus avec
`gnome-keyring-daemon` déverrouillé. Hors session, le test
`TestRealStorePutGetRemoveRoundTrip` ne s'ignore pas : il **échoue** sur un
hôte Linux sans tête où la collection est verrouillée
(`linux-amd64-nosession.txt`), parce que la sonde de disponibilité est en
lecture seule et déclare le magasin « accessible » alors que les écritures
échouent. Un transcript qui échoue ainsi n'est pas une régression du produit
mais il n'est pas une preuve non plus : seule une exécution passante sur un
magasin réellement utilisable qualifie la ligne.

Le contrat testé partout est celui qui compte pour la confidentialité : la
valeur ne passe jamais par argv — elle est écrite sur l'entrée standard de
`secret-tool` sous Linux, et sur celle de PowerShell sous Windows, où le script
est transmis encodé pour qu'une liste de processus ne montre rien du secret —
et les échecs du magasin sont traduits en états explicites plutôt qu'en silence.
Sous Windows, `cmdkey` n'est pas utilisé : il ne sait pas relire une valeur
générique. Les
tests du magasin réel sont conditionnés à sa disponibilité : ils s'exécutent sur
une machine où le service répond, et sont ignorés ailleurs. Un conteneur
d'intégration continue a bien `secret-tool`, mais aucune session D-Bus ne
répond ; c'est pourquoi la colonne « magasin réel exercé » n'est pas la même que
la colonne « testé ».

Le magasin de repli, en clair et lisible par le seul propriétaire, n'est jamais
sélectionné implicitement : il exige un consentement explicite (`--fallback`),
et son état est rappelé à chaque affichage.

## Où vivent les preuves

| Preuve | Emplacement |
| --- | --- |
| Qualification complète Linux amd64 (parcours commun, secrets, terminal, installateur) | `tests/integration/qualification/linux-amd64/` |
| Qualification native du transport proxy et des marqueurs (phase 0) | `tests/integration/project-secrets/native/evidence/` |
| Qualification native de bout en bout des fichiers dotenv gérés (phase 3) | `tests/integration/project-secrets/native-phase3/evidence/` |
| Magasin d'identifiants natif exercé (macOS, Linux, Windows) | `tests/integration/credentials/evidence/` |
| Matrice des chargeurs dotenv (Node, Python) | `tests/integration/project-secrets/` |
| Protocole de qualification manuelle par hôte | `docs/qualification-beta.md` |

Chaque transcript conservé porte une ligne `META` nommant la révision du
harnais et le commit qualifié : une preuve qui ne dit pas ce qu'elle a exécuté
n'en est pas une.

## Ce qui n'est pas promis

- Démarrer une microVM sur Windows : possible en droit, non vérifié ici.
- Exercer le magasin natif Windows : l'adaptateur est testé, le service ne
  l'est pas.
- macOS Intel, sous aucune forme.
- Une plateforme non listée : le comportement est de refuser, pas de dégrader.
