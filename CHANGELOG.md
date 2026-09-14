# Changelog

## 1.0.0 (2026-09-14)


### Features

* aligner le CLI sur la version TypeScript et ajouter CI/CD ([96a09be](https://github.com/etalab-ia/just-code/commit/96a09bedf1528723e2d99cf3a6c6c7df131b57b6))
* embarquer les ressources de runtime dans le binaire Go ([900029c](https://github.com/etalab-ia/just-code/commit/900029cf503dddcdd16c42b3d622bf2722bdc9e8))
* identifier le binaire avec la commande version ([9c96b4f](https://github.com/etalab-ia/just-code/commit/9c96b4faac66e45a4fa73375b2610ed6253e36b5))
* porter Docker et Microsandbox en Go et retirer le justfile ([1182ba7](https://github.com/etalab-ia/just-code/commit/1182ba7708db7f646e0b4e389cac5d38175507bb))
* porter just-code en Go (remplace le justfile) ([e252382](https://github.com/etalab-ia/just-code/commit/e252382a0cb3296f1a5b6551bab86cc0b33040c7))
* porter le bootstrap Tart en Go et supprimer le dernier script shell ([2f5987d](https://github.com/etalab-ia/just-code/commit/2f5987d526423648805047c2c7795b919bbdefdd))
* porter le cycle de vie Tart en Go (durcissement) ([cd250b3](https://github.com/etalab-ia/just-code/commit/cd250b31f93e81161b7530eb3b346dd6a9589e3d))
* retirer la commande build ([d348223](https://github.com/etalab-ia/just-code/commit/d348223770585671b69e847e8ab60a3725f203e5))


### Bug Fixes

* accepter les reponses /provider plus larges que 4 MiB ([baf6746](https://github.com/etalab-ia/just-code/commit/baf67461901bc1b58c31244fa7d34e98ff299d28))
* annoncer la suppression avant de detruire un sandbox ([84092b7](https://github.com/etalab-ia/just-code/commit/84092b77e64c2c11b9f31c5b54e4d5ecaf84f40a))
* diagnostiquer un backend deja demarre mais non sain ([327a6a8](https://github.com/etalab-ia/just-code/commit/327a6a8d3c11421f0da853594007566f2c5eae6e))
* doctor doit toujours dire ce qu il a trouve ([542765d](https://github.com/etalab-ia/just-code/commit/542765db6e9fefbfca9200dbc86e2e7f7c53cf85))
* faire de just-code sans argument l equivalent de just code ([ef4b390](https://github.com/etalab-ia/just-code/commit/ef4b390d2df85ce3f1ec518bb4fadde0bf8565bc))
* livrer stdin aux processus detaches et chercher .env aupres du binaire ([7f9b5d5](https://github.com/etalab-ia/just-code/commit/7f9b5d54016d9a51a73325850e0a58f5ecb73515))
* reparer le backend microsandbox et aligner la config sur le port TS ([77c7efd](https://github.com/etalab-ia/just-code/commit/77c7efdd426280bc25757c3a718f898f5c9255a7))
