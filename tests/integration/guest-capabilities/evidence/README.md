# Preuves de qualification guest-capabilities (P03)

Ces transcripts documentent la matrice P03 sur MSB 0.7.6. Ils couvrent les
capacités de base d'un invité Microsandbox, pilotées par le CLI `msb`. Ils ne
qualifient ni le transport des identifiants P02, ni les parcours intégrés à
just-code, ni l'exécution du SDK Go.

## Résultats du 27 octobre 2026

Les tests ont été exécutés sur MacBook-Pro-Luis.local, sous macOS 27.0.1
(arm64), avec libkrun. Le harnais provient de la révision
`7f8b3dec70c7738989a4f97a62f2ad24d228194f`.

- `darwin-arm64-msb-0.7.6-non-network.txt` : **8 réussites, 0 échec**,
  sortie 0. T1 vérifie les références des deux images et leur architecture
  arm64. Le script indique explicitement que les cas réseau sont ignorés.
- `darwin-arm64-msb-0.7.6-network.txt` : **18 réussites, 0 échec**, sortie 0.
  `MSB_GUEST_INTEGRATION_NETWORK=1` active les outils invités, Chromium,
  Playwright MCP et les clones Git. T10a-T10c passent depuis un clone Git
  principal temporaire.
- `darwin-arm64-msb-0.7.6-archive-failure.txt` : **échec de préparation
  attendu**, sortie 2. T1-T9b passent depuis une archive sans `.git`, puis le
  prévol T10 affiche que le clone principal est introuvable. Le diagnostic
  apparaît avant tout arrêt ou retrait de la sandbox T10. Ce run ne qualifie
  pas la matrice ; il prouve que le prérequis Git échoue clairement.

T1 comporte trois assertions : référence Alpine, référence Debian et
architecture arm64 des deux images. Il ne valide pas leur taille. Les comptes
8 et 18 incluent ces trois assertions.

## Environnement et versions

Chaque transcript conserve la commande complète, dont `MSB_BIN`, `MSB_HOME`,
`MSB_PATH` et `MSB_LIBKRUNFW_PATH`. Le binaire temporaire répond `msb 0.7.6`.
`msb doctor` confirme libkrunfw et Apple Silicon. Le SHA-256 du runtime est
`a4f1722fee6460d4e2813e62b60dae4c092021d8120b7aaa9c2f4b5679d97d89`,
identique à celui gravé dans just-code.

La sortie brute `go list -m` déclare le SDK Go v0.7.6 dans la révision testée.
La matrice P03 appelle le CLI : elle n'exécute pas ce SDK. Le `MSB_HOME`
temporaire évite de modifier le runtime 0.7.9 déjà installé sur le Mac.

## Nettoyage et limites

Les trois transcripts incluent la sortie brute finale de `msb list` : aucune
sandbox ne subsiste. Les tests n'ont touché ni les dépôts personnels, ni le
`~/.microsandbox` de l'utilisateur.

Le run réseau de qualification utilise un clone principal temporaire pour le
montage hôte de T10. Son harnais est byte-identique à la révision déclarée,
indépendamment du HEAD de ce clone temporaire. Un worktree lié est accepté
lorsque son clone principal reste accessible.

Les mesures de temps sont celles d'un poste de développement, pas des
objectifs de performance. Les limites antérieures sur SIGINT en vrai terminal,
x86_64, les sous-modules et le navigateur auto-téléchargé restent applicables.
Cette matrice ne requalifie pas le profil navigateur P16.

## Historique de la revue

La première proposition comptait 6/6 et 16/16 en incluant une réussite T1
inconditionnelle. La revue Kimi a montré que ce résultat ne vérifiait pas la
présence des images. Elle a aussi relevé la traçabilité incomplète de
l'environnement et l'échec silencieux d'une archive sans `.git`. Ces résultats
ont été remplacés par les trois transcripts ci-dessus. Le commit `011a681`
dans l'historique de la PR conserve la première proposition, mais il n'est
plus la référence de qualification.
