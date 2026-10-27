# Preuves de qualification guest-capabilities (P03)

Ces transcripts documentent la requalification de la matrice P03 sur MSB
0.7.6. Ils concernent les capacités de base d'un invité Microsandbox. Ils ne
qualifient ni le transport des identifiants P02, ni les parcours intégrés à
just-code.

## Résultats du 27 octobre 2026

Les tests ont été exécutés sur MacBook-Pro-Luis.local, sous macOS 27.0.1
(arm64), avec libkrun. La révision du harnais est
`011a681b571bf13ebfe97d21e7911ff4fb0133ac`. Le SDK Go déclaré est v0.7.6.

- `darwin-arm64-msb-0.7.6-non-network.txt` : 6 réussites, 0 échec,
  sortie 0. Ce run couvre les images, le cycle de vie et le PTY. Les cas
  réseau sont explicitement ignorés.
- `darwin-arm64-msb-0.7.6-network.txt` : 16 réussites, 0 échec, sortie 0.
  `MSB_GUEST_INTEGRATION_NETWORK=1` active les outils invités, Chromium,
  Playwright MCP et les clones Git. T10a-T10c passent depuis un clone Git
  principal temporaire.
- `darwin-arm64-msb-0.7.6-archive-failure.txt` : tentative depuis une archive
  sans `.git`. T1-T9b passent, puis le script sort 1 avant T10. Il manque le
  checkout principal nécessaire au montage hôte. Ce transcript ne constitue
  pas une qualification. Le run complet depuis un clone principal le remplace.

Les deux runs de qualification utilisent le même harnais, sans modification
des assertions. Le run réseau a été répété depuis un clone principal pour
satisfaire le prérequis de T10. Les deux tentatives sont conservées pour
rendre cette différence d'environnement vérifiable.

## Isolement de la qualification

Le runtime 0.7.6 a été téléchargé dans un répertoire temporaire. Son SHA-256
`a4f1722fee6460d4e2813e62b60dae4c092021d8120b7aaa9c2f4b5679d97d89`
correspond à celui gravé dans just-code. `msb doctor` confirme libkrunfw et
Apple Silicon. Le `MSB_HOME` temporaire évite toute modification du runtime
0.7.9 déjà installé sur le Mac.

Les deux sandboxes jetables `p03-guest-harness-{alpine,debian}` ont été
supprimées après chaque run. La commande `msb list` finale ne retourne aucune
sandbox. Les tests ne touchent ni les dépôts personnels, ni le
`~/.microsandbox` de l'utilisateur.

## Limites

La vérification T1 de la présence des images ne valide pas leur taille : les
variables de taille ne sont pas assertées par le script. Le run non réseau a
aussi affiché des diagnostics JSON lors de l'inspection initiale des images,
avant leur mise en cache. Ces observations ne changent pas le résultat des
assertions exécutées.

Les mesures de temps sont celles d'un poste de développement. Elles ne
constituent pas un objectif de performance. Les limites antérieures sur
SIGINT en vrai terminal, x86_64, les sous-modules et le navigateur
auto-téléchargé restent applicables. Cette matrice ne requalifie pas le profil
navigateur P16.
