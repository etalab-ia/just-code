# Changelog

## [0.4.2](https://github.com/etalab-ia/just-code/compare/just-code-v0.4.1...just-code-v0.4.2) (2026-09-22)


### Bug Fixes

* **agent-vm:** créer le workspace avant la pré-vérification du restart ([1ad8bc9](https://github.com/etalab-ia/just-code/commit/1ad8bc92b3674ccbfdef42a12db02c313688e5e7))
* **agent-vm:** passerelle de sécurité workspace au démarrage et au restart ([7952609](https://github.com/etalab-ia/just-code/commit/7952609575891730c82736f93e60b57cd8a45ba8))
* **agent-vm:** passerelle de sécurité workspace au démarrage et au restart ([9dd9eb9](https://github.com/etalab-ia/just-code/commit/9dd9eb9207cdbd846d8706ff27368b6b3defa645))
* **microsandbox:** lancement du script au premier démarrage et détection du montage obsolète ([e7f2dc0](https://github.com/etalab-ia/just-code/commit/e7f2dc0ce6414f71121da1614bcc3cebd55062a9))
* **microsandbox:** launch the start script after creation ([a130914](https://github.com/etalab-ia/just-code/commit/a130914891350f1c53cfe6b9a442415504a35cae))
* **microsandbox:** lire le montage /workspace depuis le document persisté ([8272c0e](https://github.com/etalab-ia/just-code/commit/8272c0ebc466b0300bd943e77e86a9102e94ae4c))
* **microsandbox:** relancer un invité full inactif et refuser un timeout invalide ([a549f93](https://github.com/etalab-ia/just-code/commit/a549f93c0d98c5e244797bc1cb4e036036b26386))
* **microsandbox:** revérifier la préparation full sur sandbox existant ([867ee50](https://github.com/etalab-ia/just-code/commit/867ee5029b85d3eaee91d307d80a5ad01b0dd96e))
* **microsandbox:** vérifier la survie du lanceur et attendre la toolchain en mode full ([b94801a](https://github.com/etalab-ia/just-code/commit/b94801a4777c1a71b0b66f00cede6e1c108557e5))
* **tart,msb:** créer le workspace avant la pré-vérification du restart ([1278ace](https://github.com/etalab-ia/just-code/commit/1278ace19548e00edf02b6e80a2eb8da1ed95c0d))
* **tart,msb:** créer le workspace avant la pré-vérification du restart ([1ee7c57](https://github.com/etalab-ia/just-code/commit/1ee7c5725031e4952d5443734e6307db87bc389f))
* **tart,msb:** valider la configuration avant le Clean du restart ([f5b6d6b](https://github.com/etalab-ia/just-code/commit/f5b6d6b97bc3d6ab2f8d90a54706e029a49348f1))

## [0.4.1](https://github.com/etalab-ia/just-code/compare/just-code-v0.4.0...just-code-v0.4.1) (2026-09-21)


### Bug Fixes

* **microsandbox:** le garde legacy lit vraiment la config persistée ([dbe32df](https://github.com/etalab-ia/just-code/commit/dbe32dfae7976f69d603b6738de1528cefd0b552))
* **microsandbox:** protéger la clé Albert en mode full ([ec770a8](https://github.com/etalab-ia/just-code/commit/ec770a80a82d3b0032d63d8df231c826a434fa23))
* **microsandbox:** protéger la clé Albert en mode full ([2d18e50](https://github.com/etalab-ia/just-code/commit/2d18e507a3788c7d3283147af5b417688bf3e113))

## [0.4.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.3.0...just-code-v0.4.0) (2026-09-20)


### Features

* add --isolation backend|full execution mode ([db3d1ab](https://github.com/etalab-ia/just-code/commit/db3d1ab983e0a0bd96a048797dae50a371171ba7))
* add --isolation backend|full execution mode ([1b02925](https://github.com/etalab-ia/just-code/commit/1b02925a2a49b2e8ee97cd0ddc6bbc0ec4d35f1b))
* **agent-vm:** build the base template ourselves ([e063e33](https://github.com/etalab-ia/just-code/commit/e063e331dec72a558c0fcb5d3c62cc65b5f1012c))
* **agent-vm:** construire nous-mêmes le template de base ([ae7fd5f](https://github.com/etalab-ia/just-code/commit/ae7fd5f8f863a2673f2ed2099092ed7b1575bf28))


### Bug Fixes

* address review findings on the isolation mode ([3bd6681](https://github.com/etalab-ia/just-code/commit/3bd6681584218efdf68ea39457e487c36c6ef9da))
* **agent-vm:** address review findings on the base template ([6e14779](https://github.com/etalab-ia/just-code/commit/6e14779d1ba23f888846442d24307ba00ea64b7d))
* **msb:** télécharge le runtime depuis la release amont immuable ([1db0d15](https://github.com/etalab-ia/just-code/commit/1db0d151ac755be4b87bc0e2f84366483ec2310d))
* **msb:** télécharge le runtime depuis la release amont immuable ([2e9fd19](https://github.com/etalab-ia/just-code/commit/2e9fd1991b77a609377883f18db2a31fd258b4ba))

## [0.3.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.2.0...just-code-v0.3.0) (2026-09-17)


### Features

* add agent-vm runtime (Lima VM backend) ([3de7848](https://github.com/etalab-ia/just-code/commit/3de78483a6ee8bba2ecd50cfb8d370bf3fc6417e))
* aligner le CLI sur la version TypeScript et ajouter CI/CD ([96a09be](https://github.com/etalab-ia/just-code/commit/96a09bedf1528723e2d99cf3a6c6c7df131b57b6))
* default to microsandbox and reject docker/tart on Windows ([b5a3467](https://github.com/etalab-ia/just-code/commit/b5a346789f9b1ed2d99791835c78b61f797a1734))
* embarquer les ressources de runtime dans le binaire Go ([900029c](https://github.com/etalab-ia/just-code/commit/900029cf503dddcdd16c42b3d622bf2722bdc9e8))
* embed the Microsandbox Go SDK ([0249ebe](https://github.com/etalab-ia/just-code/commit/0249ebe90ae72ad1d6776bc751dd21321849b61c))
* héberger et vérifier le runtime Microsandbox ([2d10395](https://github.com/etalab-ia/just-code/commit/2d10395555f70c7c102977cb649d761821f18058))
* identifier le binaire avec la commande version ([9c96b4f](https://github.com/etalab-ia/just-code/commit/9c96b4faac66e45a4fa73375b2610ed6253e36b5))
* intégrer le SDK Go Microsandbox ([d59bc44](https://github.com/etalab-ia/just-code/commit/d59bc44e5550c5239d13d33f635794a9716555ef))
* **msb:** passage du runtime Microsandbox en v0.7.0 ([389adb3](https://github.com/etalab-ia/just-code/commit/389adb3794b04ec17c4cad5e48acacce7aadba47))
* **msb:** passe le runtime Microsandbox en v0.7.0 ([3e512fb](https://github.com/etalab-ia/just-code/commit/3e512fb20c468cf0548f2a24afc0e499e632c0f9))
* porter Docker et Microsandbox en Go et retirer le justfile ([1182ba7](https://github.com/etalab-ia/just-code/commit/1182ba7708db7f646e0b4e389cac5d38175507bb))
* porter just-code en Go (remplace le justfile) ([e252382](https://github.com/etalab-ia/just-code/commit/e252382a0cb3296f1a5b6551bab86cc0b33040c7))
* porter le bootstrap Tart en Go et supprimer le dernier script shell ([2f5987d](https://github.com/etalab-ia/just-code/commit/2f5987d526423648805047c2c7795b919bbdefdd))
* porter le cycle de vie Tart en Go (durcissement) ([cd250b3](https://github.com/etalab-ia/just-code/commit/cd250b31f93e81161b7530eb3b346dd6a9589e3d))
* refuse startup when the workspace contains secrets ([05fc252](https://github.com/etalab-ia/just-code/commit/05fc252aa3f08af52f2f9abaf116a9073773b276))
* renomme les tags de release (runtime et application) ([2f63e0b](https://github.com/etalab-ia/just-code/commit/2f63e0bbbe1b5718ac9b9827d690a7634aa077ef))
* renomme les tags de release du runtime et de l'application ([79286a5](https://github.com/etalab-ia/just-code/commit/79286a5aa1d0cd61bfdb5569d4002b9260c93cdf))
* retirer la commande build ([d348223](https://github.com/etalab-ia/just-code/commit/d348223770585671b69e847e8ab60a3725f203e5))
* runtime agent-vm (VM Lima persistante) ([a8449d5](https://github.com/etalab-ia/just-code/commit/a8449d59f7567f5a03a080f5480dbfbe9dfad20b))
* supporter Windows via le runtime Microsandbox (msb) ([b2a9d6d](https://github.com/etalab-ia/just-code/commit/b2a9d6d93d71598f510d47a9d72b81183840e273))
* supporter Windows via le runtime Microsandbox (msb) ([46a6ceb](https://github.com/etalab-ia/just-code/commit/46a6ceb8ba5411ab06526f5849b6752a5c7804d3))
* vérifier le runtime Microsandbox hébergé ([0907d4d](https://github.com/etalab-ia/just-code/commit/0907d4dfc51f022a25d3da9c5d0b1ff290205b32))


### Bug Fixes

* accepter les reponses /provider plus larges que 4 MiB ([baf6746](https://github.com/etalab-ia/just-code/commit/baf67461901bc1b58c31244fa7d34e98ff299d28))
* address codex review on release assets and docs ([83c3916](https://github.com/etalab-ia/just-code/commit/83c39161a6994e91adf7d8962db9f220d3e084cf))
* address Codex review on Windows attach and test portability ([b9b64cc](https://github.com/etalab-ia/just-code/commit/b9b64cc14f810ba7bd6e619fe1daa4661a8939cf))
* address review findings on the agent-vm backend ([08f5c8d](https://github.com/etalab-ia/just-code/commit/08f5c8dda85370bee720c5484742d6b4d2ffbfe9))
* address review findings on the workspace gate ([8476903](https://github.com/etalab-ia/just-code/commit/8476903068377c80dcaad81c8a1ed6915f67d3db))
* align Windows process detachment with Setsid semantics ([b81f4f1](https://github.com/etalab-ia/just-code/commit/b81f4f133c42c4576b7e0612e6f4585d411ad58d))
* annoncer la suppression avant de detruire un sandbox ([84092b7](https://github.com/etalab-ia/just-code/commit/84092b77e64c2c11b9f31c5b54e4d5ecaf84f40a))
* clean up the legacy Docker container on upgrade ([15d6b81](https://github.com/etalab-ia/just-code/commit/15d6b81cf75e5183904b61b762a1d9763b963b37))
* diagnostiquer un backend deja demarre mais non sain ([327a6a8](https://github.com/etalab-ia/just-code/commit/327a6a8d3c11421f0da853594007566f2c5eae6e))
* doctor doit toujours dire ce qu il a trouve ([542765d](https://github.com/etalab-ia/just-code/commit/542765db6e9fefbfca9200dbc86e2e7f7c53cf85))
* faire de just-code sans argument l equivalent de just code ([ef4b390](https://github.com/etalab-ia/just-code/commit/ef4b390d2df85ce3f1ec518bb4fadde0bf8565bc))
* livrer stdin aux processus detaches et chercher .env aupres du binaire ([7f9b5d5](https://github.com/etalab-ia/just-code/commit/7f9b5d54016d9a51a73325850e0a58f5ecb73515))
* **msb-watch:** titre la release miroir avec le nom du tag ([e131f29](https://github.com/etalab-ia/just-code/commit/e131f294df93ea97677f9f3579ee490696c49c9c))
* **msb-watch:** titre la release miroir avec le nom du tag ([504b71b](https://github.com/etalab-ia/just-code/commit/504b71b4925ba4295b94297bdd8a5daf1e1bf2ed))
* **msb:** pointe l'URL du runtime vers microsandbox-v0.7.0 ([29d5015](https://github.com/etalab-ia/just-code/commit/29d5015293d4ae64ba3e39cb0d3523be78c0d191))
* normalize scanned workspace paths to forward slashes ([bb247bf](https://github.com/etalab-ia/just-code/commit/bb247bf40e73849febf16c1d826d42a309c5088a))
* reparer le backend microsandbox et aligner la config sur le port TS ([77c7efd](https://github.com/etalab-ia/just-code/commit/77c7efdd426280bc25757c3a718f898f5c9255a7))
* reset release-please to start at 0.1.0 instead of 1.0.0 ([bad1648](https://github.com/etalab-ia/just-code/commit/bad1648f9d1eac96d97426e8772f4fce3b012541))
* **test:** skip 0600 permission assert on Windows ([7d1ad98](https://github.com/etalab-ia/just-code/commit/7d1ad989225c8521c9d0f86d5aa88c3adaf569b7))
* utilise le champ component supporté par release-please ([50ba13e](https://github.com/etalab-ia/just-code/commit/50ba13e1e6a7b6dec9224310479b2c86b5037588))

## [0.2.0](https://github.com/etalab-ia/just-code/compare/v0.1.0...v0.2.0) (2026-09-15)


### Features

* intégrer le SDK Go Microsandbox ([d59bc44](https://github.com/etalab-ia/just-code/commit/d59bc44e5550c5239d13d33f635794a9716555ef))


### Bug Fixes

* address codex review on release assets and docs ([83c3916](https://github.com/etalab-ia/just-code/commit/83c39161a6994e91adf7d8962db9f220d3e084cf))

## 0.1.0 (2026-09-14)


### Features

* aligner le CLI sur la version TypeScript et ajouter CI/CD ([96a09be](https://github.com/etalab-ia/just-code/commit/96a09bedf1528723e2d99cf3a6c6c7df131b57b6))
* default to microsandbox and reject docker/tart on Windows ([b5a3467](https://github.com/etalab-ia/just-code/commit/b5a346789f9b1ed2d99791835c78b61f797a1734))
* embarquer les ressources de runtime dans le binaire Go ([900029c](https://github.com/etalab-ia/just-code/commit/900029cf503dddcdd16c42b3d622bf2722bdc9e8))
* identifier le binaire avec la commande version ([9c96b4f](https://github.com/etalab-ia/just-code/commit/9c96b4faac66e45a4fa73375b2610ed6253e36b5))
* porter Docker et Microsandbox en Go et retirer le justfile ([1182ba7](https://github.com/etalab-ia/just-code/commit/1182ba7708db7f646e0b4e389cac5d38175507bb))
* porter just-code en Go (remplace le justfile) ([e252382](https://github.com/etalab-ia/just-code/commit/e252382a0cb3296f1a5b6551bab86cc0b33040c7))
* porter le bootstrap Tart en Go et supprimer le dernier script shell ([2f5987d](https://github.com/etalab-ia/just-code/commit/2f5987d526423648805047c2c7795b919bbdefdd))
* porter le cycle de vie Tart en Go (durcissement) ([cd250b3](https://github.com/etalab-ia/just-code/commit/cd250b31f93e81161b7530eb3b346dd6a9589e3d))
* retirer la commande build ([d348223](https://github.com/etalab-ia/just-code/commit/d348223770585671b69e847e8ab60a3725f203e5))
* supporter Windows via le runtime Microsandbox (msb) ([b2a9d6d](https://github.com/etalab-ia/just-code/commit/b2a9d6d93d71598f510d47a9d72b81183840e273))
* supporter Windows via le runtime Microsandbox (msb) ([46a6ceb](https://github.com/etalab-ia/just-code/commit/46a6ceb8ba5411ab06526f5849b6752a5c7804d3))


### Bug Fixes

* accepter les reponses /provider plus larges que 4 MiB ([baf6746](https://github.com/etalab-ia/just-code/commit/baf67461901bc1b58c31244fa7d34e98ff299d28))
* address Codex review on Windows attach and test portability ([b9b64cc](https://github.com/etalab-ia/just-code/commit/b9b64cc14f810ba7bd6e619fe1daa4661a8939cf))
* align Windows process detachment with Setsid semantics ([b81f4f1](https://github.com/etalab-ia/just-code/commit/b81f4f133c42c4576b7e0612e6f4585d411ad58d))
* annoncer la suppression avant de detruire un sandbox ([84092b7](https://github.com/etalab-ia/just-code/commit/84092b77e64c2c11b9f31c5b54e4d5ecaf84f40a))
* clean up the legacy Docker container on upgrade ([15d6b81](https://github.com/etalab-ia/just-code/commit/15d6b81cf75e5183904b61b762a1d9763b963b37))
* diagnostiquer un backend deja demarre mais non sain ([327a6a8](https://github.com/etalab-ia/just-code/commit/327a6a8d3c11421f0da853594007566f2c5eae6e))
* doctor doit toujours dire ce qu il a trouve ([542765d](https://github.com/etalab-ia/just-code/commit/542765db6e9fefbfca9200dbc86e2e7f7c53cf85))
* faire de just-code sans argument l equivalent de just code ([ef4b390](https://github.com/etalab-ia/just-code/commit/ef4b390d2df85ce3f1ec518bb4fadde0bf8565bc))
* livrer stdin aux processus detaches et chercher .env aupres du binaire ([7f9b5d5](https://github.com/etalab-ia/just-code/commit/7f9b5d54016d9a51a73325850e0a58f5ecb73515))
* reparer le backend microsandbox et aligner la config sur le port TS ([77c7efd](https://github.com/etalab-ia/just-code/commit/77c7efdd426280bc25757c3a718f898f5c9255a7))
* reset release-please to start at 0.1.0 instead of 1.0.0 ([bad1648](https://github.com/etalab-ia/just-code/commit/bad1648f9d1eac96d97426e8772f4fce3b012541))

## Changelog

All notable changes to this project will be documented in this file.
