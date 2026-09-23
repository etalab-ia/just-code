# Harnais d'intégration OpenCode (P01)

Rejoue la matrice de caractérisation du contrat de configuration OpenCode
(décision `docs/decisions/2026-09-23-opencode-configuration-contract.md`)
contre un vrai processus OpenCode épinglé, hors microVM.

## Usage

```bash
# cas non réseau (fusion, découverte, JSONC, plugins) — sans clé
tests/integration/opencode/run.sh

# cas réseau (sélection effective de modèle via Albert) — opt-in
OPENCODE_INTEGRATION_NETWORK=1 ALBERT_API_KEY=... tests/integration/opencode/run.sh
```

Le harnais installe `opencode-ai` à la version épinglée dans un répertoire
temporaire (npm), construit des projets synthétiques sous un `HOME` et un
`XDG_CONFIG_HOME` isolés, et vérifie par assertions shell chaque constat du
décision record. Un échec d'assertion sort non-zéro avec le nom du cas.

## Épinglage

La version d'OpenCode testée est épinglée dans `pin.json`. Le harnais
échoue si la version installée ne correspond pas. Une montée de version
d'OpenCode doit reproduire la matrice complète et mettre à jour le décision
record avant de changer l'épingle.

## Relation avec l'invité

Ce harnais qualifie le comportement d'OpenCode lui-même, pas l'image invité
`ghcr.io/anomalyco/opencode` : la version embarquée par l'image peut différer.
P03 confirme la version réelle de l'image invité et rejoue le cas
d'exécution de plugin dans l'invité.
