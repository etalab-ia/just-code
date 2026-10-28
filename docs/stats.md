# Statistiques d'usage (avec consentement)

just-code peut partager avec l'équipe des informations **non nominatives** sur
les choix faits dans l'assistant, pour orienter le produit. Le partage est
**désactivé par défaut** : rien n'est construit, mis en file ni envoyé sans un
« o » explicite.

## Consentement

La question est posée une seule fois, à la fin de `just-code setup`, juste
avant le récapitulatif. Elle vaut aussi pour les `init` suivants. Le
récapitulatif indique `Statistiques d'usage : partagées` ou `non partagées`.

En mode non interactif (entrée piped), la question n'est pas posée — la
prochaine ligne appartient à un autre écran — et le partage reste désactivé.
Utiliser `just-code stats on` pour l'activer explicitement.

## Commandes

```bash
just-code stats on      # accepte le partage
just-code stats off     # arrête tout envoi et vide la file locale
just-code stats show    # affiche exactement ce qui est (ou serait) envoyé
```

`stats show` affiche l'état du consentement, la configuration de l'endpoint,
les identifiants locaux et le contenu de la file d'envoi, événement par
événement, en JSON. C'est la représentation exacte de l'envoi.

## Données envoyées

Chaque événement porte un identifiant aléatoire d'événement, la version de
just-code, le système et l'architecture, un identifiant aléatoire de poste et,
pour un `init`, un identifiant aléatoire de projet. Ces identifiants sont
générés localement ; ils ne sont dérivés ni d'un nom, ni d'un chemin, ni d'une
identité.

Champs collectés :

- `setup` : plateformes connectées (type uniquement), modèles cochés, modèle
  par défaut, présence d'un jeton GitHub (0/1), présence d'une identité git
  (0/1) ;
- `init` : chaque skill (0/1), chaque MCP (0/1), cas GitHub 1 à 4, réglages
  avancés ouverts (0/1), harnais, runtime, isolation, stockage des skills ;
- abandon : dernière étape atteinte quand l'assistant est quitté.

Un abandon n'est enregistré que si la question de consentement a déjà reçu une
réponse (y compris un refus) : quitter un assistant avant d'atteindre cet écran
ne produit rien, même si le partage a été activé plus tard avec `stats on`.

Ne sont **jamais** collectés : code, prompts, réponses de l'agent, noms de
projet ou de dépôt, chemins, clés, jetons, identité git, adresse IP.
Le nom saisi pour une plateforme compatible OpenAI n'est pas envoyé : seul son
type l'est.

## Stockage et envoi

Les événements sont mis en file dans l'état local du poste
(`stats/queue.json`), sous permissions `0600`, avec une limite de 32
événements. L'envoi se fait dans un processus détaché, avec un délai court :
un endpoint injoignable ne ralentit ni ne bloque l'assistant, et un échec
laisse la file intacte pour une tentative ultérieure.

L'endpoint est géré par l'équipe just-code. Cette version n'en configure aucun
par défaut : sans endpoint, les événements restent locaux. Une construction de
release peut le fixer avec
`-ldflags "-X github.com/etalab-ia/just-code/internal/justcode.StatsEndpointDefault=<url>"`,
et `JUST_CODE_STATS_URL` le remplace pour les tests et le développement.
