# Changelog

## [0.12.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.11.0...just-code-v0.12.0) (2026-10-23)


### Features

* **install:** messages français, PATH avant setup, postes non pris en charge ([b83a99c](https://github.com/etalab-ia/just-code/commit/b83a99c908e4cf07c2a36d55a089ca1c7753b7aa))
* **install:** messages français, PATH avant setup, postes non pris en charge ([ab503d4](https://github.com/etalab-ia/just-code/commit/ab503d407e594a9243e1af66852cf6e3b086b813))
* **setup:** écran d'introduction avant la première question ([399cffd](https://github.com/etalab-ia/just-code/commit/399cffdbbc7edb3428f231656a2fb25abf78d720))
* **setup:** écran d'introduction avant la première question ([88d5714](https://github.com/etalab-ia/just-code/commit/88d57144540afc6a455cbd02f802c98c877e0d69))
* **setup:** écran de la clé Albert API, ordre clé → modèles → GitHub ([98309b1](https://github.com/etalab-ia/just-code/commit/98309b1f433110c80326526b433b0b00abf9932f))
* **setup:** écran de la clé Albert API, ordre clé → modèles → GitHub ([1f20630](https://github.com/etalab-ia/just-code/commit/1f20630c8efd4a5c0ce10522091747ed920dd293))


### Bug Fixes

* **agent-vm:** épingler une image Lima résolvable et la vérifier avant reconstruction ([1f545a8](https://github.com/etalab-ia/just-code/commit/1f545a802cdca7ed7998d50c1b0814afe99532e0))
* **agent-vm:** épingler une image Lima résolvable et la vérifier avant reconstruction ([87ca8eb](https://github.com/etalab-ia/just-code/commit/87ca8eb337876cdd4c6ddaeff0d6ce46fe4c74c9))
* **agent-vm:** report an inconclusive image check, not a passed one ([9fb869e](https://github.com/etalab-ia/just-code/commit/9fb869e3447cc265c7f55d49713e05434abb3d1f))
* **agent-vm:** stop recommending the default image when it already failed ([94826ec](https://github.com/etalab-ia/just-code/commit/94826eca5a4f9650d58f962cdd8bb3ab287bc59a))
* **agent-vm:** témoin sans image pour ne pas imputer un refus de limactl à AGENT_VM_IMAGE ([32062aa](https://github.com/etalab-ia/just-code/commit/32062aacdcc8f524cbe4b34b3a3d7726332a1a56))
* **agent-vm:** use limactl info as the image-independent witness ([d4101d3](https://github.com/etalab-ia/just-code/commit/d4101d31e14f07d4427fc13026c2a1460631eeef))
* **credentials:** lire un identifiant dans le magasin qui l'a reçu ([dfbae65](https://github.com/etalab-ia/just-code/commit/dfbae65b97d7ae5dd4278c62d4c4ccb0947e6d34))
* **credentials:** read a credential from the store that received it ([4e5f8de](https://github.com/etalab-ia/just-code/commit/4e5f8def835451bf9cf11bbc21a194e544b5a2bc))
* **credentials:** serialize the store record and stop reporting unpinnable success ([6cb5f9c](https://github.com/etalab-ia/just-code/commit/6cb5f9c0ebfe3d5b66302ac5bc285f10d6baf4ea))
* **diagnostics:** sonde Go, cause de résolution nommée, portée de la sonde de magasin ([364263d](https://github.com/etalab-ia/just-code/commit/364263d08e63af30f3241959a838e58db1984748))
* **diagnostics:** sonde Go, cause de résolution nommée, portée de la sonde de magasin ([5acebe1](https://github.com/etalab-ia/just-code/commit/5acebe1cf16cd0f994cc4669aee0e057f0488704))
* **doc,test:** compteurs exacts, garde d'introduction réellement testée ([d03b736](https://github.com/etalab-ia/just-code/commit/d03b7365568bb73eae0c1452d9969d513dc817f5))
* **doctor:** distinguer inventaire incomplet et absence de journal ([ce93ccc](https://github.com/etalab-ia/just-code/commit/ce93ccc717de092e8c86765cef8bc45af42f5698))
* **doctor:** l'inventaire des identifiants vient du magasin, pas du journal ([bb37138](https://github.com/etalab-ia/just-code/commit/bb371388a4552a70a215a46916f2acaf0075c564))
* **doctor:** l'inventaire des identifiants vient du magasin, pas du journal ([bb2aef4](https://github.com/etalab-ia/just-code/commit/bb2aef425ad21ca5ba6483973e9c44a53c011dfc))
* **install:** fichier lu par un terminal ordinaire, chemins cités ([90f2711](https://github.com/etalab-ia/just-code/commit/90f2711d66edfc7abf20dc5bd11588a116b8ad9c))
* **install:** ne pas empiler la ligne PATH, doc alignée sur le code ([3e565d7](https://github.com/etalab-ia/just-code/commit/3e565d7be1b66a8024b1b745f77966916b3baa3d))
* **install:** profil bash, SHELL vide, reprise et retour arrière ([a6b617c](https://github.com/etalab-ia/just-code/commit/a6b617c9ae3c58df9b6783dbd4a641f4993d6f0d))
* **install:** reconnaître la ligne PATH exacte, pas le mot just-code ([bec51ba](https://github.com/etalab-ia/just-code/commit/bec51ba9fdb0e95e7d66f9a34639a8dd49fc3618))
* **install:** reprise, interruption, profil non modifiable, sauvegarde ([a22b4de](https://github.com/etalab-ia/just-code/commit/a22b4deb92c8e8cdee8efaa18a8160ae6286a6a2))
* **install:** shells non POSIX, commandes de récupération littérales ([f3942f6](https://github.com/etalab-ia/just-code/commit/f3942f6d9ce5b974d28b8592581d073a97c8e617))
* **install:** un seul fichier par plateforme, lecture ratée = refus ([be94056](https://github.com/etalab-ia/just-code/commit/be94056e9330cbde74ae983a03debf6251d3ebe9))
* **secrets:** accepter le type natif concret enregistré dans l'index ([d5017a7](https://github.com/etalab-ia/just-code/commit/d5017a7b756080bd60501e9cf476b72c5d84ed94))
* **secrets:** aligner les limites des réparses d'attribution ([a7e7936](https://github.com/etalab-ia/just-code/commit/a7e7936abfeee93b5f2a2440e76f57747da54ba9))
* **secrets:** classer le refus de backticks avant le retour chariot ([70c1861](https://github.com/etalab-ia/just-code/commit/70c18611896e13647de6cb13aa50d595ac067385))
* **secrets:** ne pas masquer un magasin demandé ni arrêter un bac à sable sain ([a9fa42a](https://github.com/etalab-ia/just-code/commit/a9fa42a23a4139fc5b60b18aeca09cdb2fc43cc9))
* **secrets:** ne rien enregistrer quand une approbation ne peut pas résoudre ([8efd5f5](https://github.com/etalab-ia/just-code/commit/8efd5f582c7705f35f1cba5718f2f4ae569a1399))
* **secrets:** ne rien enregistrer quand une approbation ne peut pas résoudre ([3e27678](https://github.com/etalab-ia/just-code/commit/3e276781bf9a5736b36dc5e1e567bd68e4194921))
* **secrets:** nommer la variable et la règle au refus d'import via go-rotini ([673af49](https://github.com/etalab-ia/just-code/commit/673af49a6e59ad7a5edc4b692a1176809aa2ba55))
* **secrets:** nommer la variable et la règle au refus d'import via go-rotini ([e605669](https://github.com/etalab-ia/just-code/commit/e6056695a167fe772c5c44e93e5575a0737fd6b7))
* **secrets:** nommer la variable et la règle au refus de backticks ([60997ea](https://github.com/etalab-ia/just-code/commit/60997eaa8f7575fcddd9304b1e01908e0f2db673))
* **secrets:** propager un échec de sélection du magasin avant tout redémarrage ([d5c5f8a](https://github.com/etalab-ia/just-code/commit/d5c5f8ad146ac3a5a03ec087b9725ff45db5a111))
* **secrets:** refuser avant d'ouvrir le magasin, et signaler une approbation existante ([cb4d3a4](https://github.com/etalab-ia/just-code/commit/cb4d3a4883d8d79c54b684fc34a9199df4e55556))
* **secrets:** rendre effective la confirmation de bindings approve ([0f6ccdc](https://github.com/etalab-ia/just-code/commit/0f6ccdceadca4e5ad26b512d2432b675f6db6ef5))
* **secrets:** rendre effective la confirmation de bindings approve ([f1e2b5d](https://github.com/etalab-ia/just-code/commit/f1e2b5d0f2b59ff99506dac02c815e8ac04270d8))
* **secrets:** résoudre le magasin depuis le type enregistré à l'import ([e3f68a4](https://github.com/etalab-ia/just-code/commit/e3f68a4c59be69a65f5d41a722069bedd0f06bcf))
* **secrets:** résoudre le magasin depuis le type enregistré à l'import ([2934733](https://github.com/etalab-ia/just-code/commit/2934733e85e763e66df77afdf352dbec2a81e67b))
* **setup:** l'intro attend Entrée sans afficher la saisie collée ([9c95ea8](https://github.com/etalab-ia/just-code/commit/9c95ea8d12dd8c98779a4fb2c140174d5e45f0ad))
* **setup:** l'intro ne consomme pas la saisie collée avec Entrée ([ed66859](https://github.com/etalab-ia/just-code/commit/ed6685953a40b0deff0d92418bade0da9886a041))
* **setup:** l'intro ne peut pas promettre qu'aucune écriture n'a lieu ([ad35b89](https://github.com/etalab-ia/just-code/commit/ad35b893385f1f6c8ee6116c6abff7df860983fc))
* **setup:** l'intro nomme aussi l'identité git comme enregistrée ([375efd4](https://github.com/etalab-ia/just-code/commit/375efd4159283faf7ad97b62953543ca55ec83ea))
* **setup:** le choix explicite du défaut survit à une reprise sans réponse ([17c959d](https://github.com/etalab-ia/just-code/commit/17c959db54f8a2917c1be97f24eec4819b55f9c1))
* **setup:** le modèle choisi survit à une interruption plus loin ([a46d028](https://github.com/etalab-ia/just-code/commit/a46d028d7962ada26e8d1e43ffdeac641fe623cc))
* **setup:** ne pas stocker la clé avant d'avoir demandé ([fcacdd1](https://github.com/etalab-ia/just-code/commit/fcacdd10d273b65a08df563d1f6ef22bda9a9ceb))
* **setup:** une faute de frappe redemande, le repli dit où va la clé ([81abe70](https://github.com/etalab-ia/just-code/commit/81abe70fcd2e27ab987dcad41785c94bed3f393e))
* **setup:** une question non répondue annule au lieu de stocker ([87ae8d0](https://github.com/etalab-ia/just-code/commit/87ae8d017891486c7a799c50cc9db04a9d315904))
* **setup:** une question sans réponse ne remet pas le modèle à zéro ([802c0c7](https://github.com/etalab-ia/just-code/commit/802c0c783bf297e82050e6ffb360165a8b602c4e))
* **setup:** une réponse vide revient au modèle par défaut ([68754e7](https://github.com/etalab-ia/just-code/commit/68754e7cdc86685c0460da7c3d790f29dc7e9693))
* **start:** inverser le classement — ne tolérer qu'une panne de magasin ([800f1d6](https://github.com/etalab-ia/just-code/commit/800f1d6e99550b4cc4b4eb909052a71ed22e3b53))
* **start:** refuser un démarrage no-op sur une liaison hors profil actif ([681bbbb](https://github.com/etalab-ia/just-code/commit/681bbbb78015db3abecb549f2e072763a1134610))
* **start:** refuser un démarrage no-op sur une liaison hors profil actif ([c46d8e1](https://github.com/etalab-ia/just-code/commit/c46d8e17c3540a859d725947e712356bf9522880))
* **start:** un secret de projet approuvé sans valeur importée est structurel ([bb160f2](https://github.com/etalab-ia/just-code/commit/bb160f23dc55d34d514db9d3135ed1e8d7276763))
* **tart:** ne pas diagnostiquer un démarrage quand on s'attache à une VM déjà lancée ([01f9ae2](https://github.com/etalab-ia/just-code/commit/01f9ae2ca12868d64469144e4a61d57b0471cedd))
* **tart:** ne pas relire l'échec d'une exécution précédente, et préciser la portée de la sonde ([d2e2115](https://github.com/etalab-ia/just-code/commit/d2e2115810774983fe095b88e15f912f17064499))
* **tart:** nommer le refus de Virtualization.framework au lieu d'un délai agent ([b982e41](https://github.com/etalab-ia/just-code/commit/b982e417b2f57efc146f8edb9adb20609d38dc50))
* **tart:** nommer le refus de Virtualization.framework au lieu d'un délai agent ([a0d6df2](https://github.com/etalab-ia/just-code/commit/a0d6df2b48bc97739e40fbbf5ce437403c4bdabb))
* **tart:** relever le journal avant le lancement, et cibler le vrai échec de trousseau ([d117f79](https://github.com/etalab-ia/just-code/commit/d117f79eb924a96c3f48af5c021665267a35ac88))
* **tests:** attendre le marqueur de setup dans le test pty du profil non modifiable ([eba353c](https://github.com/etalab-ia/just-code/commit/eba353c16f94c3f311384629ccf2f2bd1ca6ae18))
* **tests:** la boucle pty attend le marqueur de setup, pas la bannière ([e3c366f](https://github.com/etalab-ia/just-code/commit/e3c366f4eb9010a2434617b569cc4a31afc4aa91))
* **tests:** vider le pty avant d'asserter sur la sortie capturée ([ae3b0b7](https://github.com/etalab-ia/just-code/commit/ae3b0b70f81092648ca55c6ad81a4a17c5d624bd))
* **transfer:** conserver les dates sous la seconde avec un en-tête PAX ([af46206](https://github.com/etalab-ia/just-code/commit/af46206ce33234ca0f851b4599ca9eba0205264b))
* **transfer:** préserver les dates de modification dans l'espace de travail scellé ([3e96c0d](https://github.com/etalab-ia/just-code/commit/3e96c0ddcd6b6496ac6a77b3e9248f1b4fb20e63))
* **transfer:** préserver les dates de modification dans l'espace de travail scellé ([bd63d7f](https://github.com/etalab-ia/just-code/commit/bd63d7f2dbff2ed7f2669a65ad5455f37a41f709))

## [0.11.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.10.0...just-code-v0.11.0) (2026-10-19)


### Features

* **check:** report reachability of configured preview ports ([9357fc3](https://github.com/etalab-ia/just-code/commit/9357fc37f82ecc15e1557d08f342619e670cab0d))


### Bug Fixes

* **check:** probe the port mappings applied to the running sandbox ([d61f498](https://github.com/etalab-ia/just-code/commit/d61f498b0d8916d0c8329fb36812cd65c01c7b8a))
* **check:** report stale forwards and zone strips in emptied states ([a365020](https://github.com/etalab-ia/just-code/commit/a365020c6a85d477c19ac168afc820bccfb38b77))
* **import:** annoncer l'approbation de confiance requise au premier lancement ([8467335](https://github.com/etalab-ia/just-code/commit/846733576e94c56adc10896b19d77ceb6c6127b3))
* **import:** announce the trust approval the first run will require ([713c954](https://github.com/etalab-ia/just-code/commit/713c95468f1991a87a6d1c4a2f52895203db9664))
* **import:** harden reads of untrusted project OpenCode configs ([6368a02](https://github.com/etalab-ia/just-code/commit/6368a02bcddaf176292b6748685d593c3c855da5))
* **import:** name the project directory in the trust approval hint ([efdeb1d](https://github.com/etalab-ia/just-code/commit/efdeb1dbbc3d55e8a7ba702db9e76f99ca7f7ef0))
* **init:** annoncer les touches de sélection des MultiSelect du wizard ([6fde5b7](https://github.com/etalab-ia/just-code/commit/6fde5b754c34554e056acf7ec03e25348bdc3128))
* **init:** announce MultiSelect selection keys in the wizard ([96ea5c5](https://github.com/etalab-ia/just-code/commit/96ea5c5f163e6c26a71ba899853f91233a681905))
* **init:** base local-only AGENTS.md ownership on actual zone presence ([f5d0655](https://github.com/etalab-ia/just-code/commit/f5d06556a05b1649ea3c9ebf000d9f89329c2462))
* **init:** check AGENTS.md entry type before reading for a zone ([f8aa563](https://github.com/etalab-ia/just-code/commit/f8aa5630dd840f52207ef65404b46457ef438fb1))
* **init:** recheck AGENTS.md at apply time for zones added after planning ([51665d2](https://github.com/etalab-ia/just-code/commit/51665d259e9f74f376bf84004c4131a19ab02fd7))
* **init:** resolve the GitHub origin before the approval question ([3d5618a](https://github.com/etalab-ia/just-code/commit/3d5618a7db35cd4bef67a063e31e34efae2706d9))
* **init:** résoudre l'origine GitHub avant la question d'approbation ([3467324](https://github.com/etalab-ia/just-code/commit/3467324fe25e0ca060d2ad79124c1c5f54dbea87))
* **init:** scan AGENTS.md for zone markers in bounded memory ([56cae38](https://github.com/etalab-ia/just-code/commit/56cae38b459b278d4afa98f72284049b1072ef75))
* probe AGENTS.md and host ports without unbounded reads or waits ([e85152c](https://github.com/etalab-ia/just-code/commit/e85152c5e9a76274d022fc17f4d739a874ea10f8))
* scope managed guidance and port probes to the microsandbox runtime ([d238885](https://github.com/etalab-ia/just-code/commit/d23888513a1744ba1c4022c5761d78e0123bbe2e))
* **skills:** address review on runtime parsing and AGENTS.md claim ([18c559d](https://github.com/etalab-ia/just-code/commit/18c559dbd054fd6c8e615b18d9d85c55d557e93a))
* **skills:** keep standing managed instructions in every versioned project ([7cb93c5](https://github.com/etalab-ia/just-code/commit/7cb93c575118b4202129d5bc7336247570e6c73f))
* **trust:** refuse symlinks in project path components ([294863b](https://github.com/etalab-ia/just-code/commit/294863b21f9caae7342b1e47f6d67dff03c93764))
* web preview reachable through the host port forward ([1ff037e](https://github.com/etalab-ia/just-code/commit/1ff037eb2fa69d37ceff32b1046676187cb8bf70))

## [0.10.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.9.0...just-code-v0.10.0) (2026-10-16)


### Features

* deliver approved secrets readable inside the guest ([a6bbfa5](https://github.com/etalab-ia/just-code/commit/a6bbfa596c91909df1f16c186727e8c33bb8dc8a))
* livrer les secrets approuvés lisibles dans le guest (phase 4) ([08a0a15](https://github.com/etalab-ia/just-code/commit/08a0a15991245fa8d7ba58605bcc3aaeb18005b8))


### Bug Fixes

* apply guest-readable deliveries on the reconcile path, and restrict before writing ([26b934b](https://github.com/etalab-ia/just-code/commit/26b934b5debc075044c56a331bea199ffa18869b))
* compare each file against the content wanted now, not the last write ([aa0807c](https://github.com/etalab-ia/just-code/commit/aa0807ce2a8478e762d8efb1552352d492c1e434))
* compare managed guest files by digest instead of reading them back ([2daf132](https://github.com/etalab-ia/just-code/commit/2daf132bdf01a9cc1d6ecf10aaa417678b77501e)), closes [#190](https://github.com/etalab-ia/just-code/issues/190)
* compare the digest as a field, not as a whole sha256sum line ([a28614d](https://github.com/etalab-ia/just-code/commit/a28614df4f4829e95bf0e24768516a9cf8f6535e))
* compare the ownership header as bytes, not through a shell variable ([fccde22](https://github.com/etalab-ia/just-code/commit/fccde22e2759d7314354c34a64c28aeab7decd25))
* compare the ownership header by digest over its exact bytes ([fa3e8e0](https://github.com/etalab-ia/just-code/commit/fa3e8e0f8854dde0fd1b8b11158827ed26e36e98))
* comparer les fichiers gérés par condensat au lieu de les relire ([a37243d](https://github.com/etalab-ia/just-code/commit/a37243d1cc19d2e43959c2c3502894d4b32116cb))
* deliver backslash-pair and trailing-backslash values raw instead of refusing them ([2a0f2d5](https://github.com/etalab-ia/just-code/commit/2a0f2d5beb381540ded5e0db463ae4c5f8baaf5a))
* deliver representable non-ASCII and refuse what no encoding carries ([a7b4e28](https://github.com/etalab-ia/just-code/commit/a7b4e28dd488b69bd053344dcdef1841619895f6))
* encode guest-readable values instead of writing them raw ([595a8c8](https://github.com/etalab-ia/just-code/commit/595a8c8045138ee75a942bb1b83addbefd95032b))
* keep the content digest out of argv and stop normalising the header ([16019ab](https://github.com/etalab-ia/just-code/commit/16019ab9231a920afe33ded135b579d69b3666f3))
* livrer les formes d'antislash que les chargeurs dotenv ne lisent pas exactement entre guillemets ([7294eed](https://github.com/etalab-ia/just-code/commit/7294eed7e95d20dd0b4695aef09dae960ce769ab))
* make the phase 4 probe controls able to fail ([e1b1efa](https://github.com/etalab-ia/just-code/commit/e1b1efa05344a190c3938e18ee5c6b26e9dc8754))
* refuse the backslash shapes dotenv loaders do not read exactly ([d2a5a99](https://github.com/etalab-ia/just-code/commit/d2a5a99d68730e6a1636a249d85e5a07afc3dea2))
* state the phase 4 snapshot finding without overclaiming ([9c37d72](https://github.com/etalab-ia/just-code/commit/9c37d72b8812efdf69a0183b947eeb3510c390dd))
* treat quotes and backticks as positional in the raw dotenv form ([d63e31e](https://github.com/etalab-ia/just-code/commit/d63e31eb7cc53c836167db4bc03f2659d198a2ea))

## [0.9.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.8.1...just-code-v0.9.0) (2026-10-15)


### Features

* configure Microsandbox ports per project ([e35a002](https://github.com/etalab-ia/just-code/commit/e35a002f419db1053f1c8e83b6a81bed55e93932))
* deliver approved project secrets through the Microsandbox proxy ([1951c7e](https://github.com/etalab-ia/just-code/commit/1951c7e6d29031e6c408b793f69ee47f902727da))
* exposer les secrets projet approuvés via le proxy Microsandbox (phase 2) ([8d5ecf1](https://github.com/etalab-ia/just-code/commit/8d5ecf1cdc2b98d48527ecd10e1635cc8a81a4f9))
* fichiers dotenv gérés dans le guest pour les secrets approuvés (phase 3) ([f483a80](https://github.com/etalab-ia/just-code/commit/f483a8003d7745dfe3cdea7363d5f366af51eebc))
* import project-scoped dotenv secrets ([77a5695](https://github.com/etalab-ia/just-code/commit/77a5695a68df228444145f7c6520234022029e0a))
* importer les secrets dotenv par projet (phase 1) ([0af624b](https://github.com/etalab-ia/just-code/commit/0af624b58b0d3eeb648bd0a398e74b741aaa5099))
* publish approved project secrets as managed guest dotenv files ([c830b14](https://github.com/etalab-ia/just-code/commit/c830b14fe258fe9e58ce68fb231ad212ea7d30e3))


### Bug Fixes

* bind the qualification transcript to what actually ran ([f9bbcee](https://github.com/etalab-ia/just-code/commit/f9bbceefd653b234284803c80850891b290ae16b))
* make the native runner cleanup exception-safe and document lab exposure ([0764fdc](https://github.com/etalab-ia/just-code/commit/0764fdca63b600d7f761490c071a9ce168276e9a))
* make the project secret inventory authoritative on every apply path ([f3d31f7](https://github.com/etalab-ia/just-code/commit/f3d31f7d11fc8254723d3bce3d251f0626b68c21))
* pin python-dotenv 1.2.2 for the loader qualification ([252e1de](https://github.com/etalab-ia/just-code/commit/252e1dea7cc1e14ea96b0c398241a4d5f164549d))
* retry Windows credential-file contention ([52b4f6f](https://github.com/etalab-ia/just-code/commit/52b4f6f7a5a4839d001a8073db42db3261411580))
* serialize fallback mutations and validate Windows blob sizes ([e18f46a](https://github.com/etalab-ia/just-code/commit/e18f46a0a8b42f9884ea42122618a0f91915f995))

## [0.8.1](https://github.com/etalab-ia/just-code/compare/just-code-v0.8.0...just-code-v0.8.1) (2026-10-07)


### Bug Fixes

* **install:** reattach setup wizard to the terminal under curl | sh ([faa8ecd](https://github.com/etalab-ia/just-code/commit/faa8ecd1c47f857c7602823e69a0da8331fe0d61))
* **setup:** aligner le message de fin sur le runtime par défaut ([0e8a4ca](https://github.com/etalab-ia/just-code/commit/0e8a4ca949aec179df8f92e9a1c8e7664c17f00a))
* **setup:** clarify default guest launch message ([37658ca](https://github.com/etalab-ia/just-code/commit/37658cae04222725f2bb1bfdf0170a1b20b9c594))

## [0.8.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.7.4...just-code-v0.8.0) (2026-10-07)


### Features

* **import:** importer les projets Albert Code (P19) ([1e4dbe0](https://github.com/etalab-ia/just-code/commit/1e4dbe0adf3d6615e5eb230b572c2452e45cf53e))
* préparer les diagnostics P21 avant release ([41129dc](https://github.com/etalab-ia/just-code/commit/41129dcb07af632cfb2ea5b87e4d9abb05dce36f))
* **setup:** add read-only P21 diagnostics report ([f0d963b](https://github.com/etalab-ia/just-code/commit/f0d963b0522b32f4205663a5044cc2bf117ca57a))


### Bug Fixes

* **doctor:** address P21 diagnostics review findings ([e7b9e47](https://github.com/etalab-ia/just-code/commit/e7b9e4715cba815d916f6752dc387b837a20859e))
* **doctor:** validate the selected Microsandbox runtime ([44312f9](https://github.com/etalab-ia/just-code/commit/44312f9c55543f6b3c7d7e1c62b87f0c075616b8))
* **import:** reconcile existing Albert Code imports ([2e5a787](https://github.com/etalab-ia/just-code/commit/2e5a7874967091c68052fe31b8069b08857e80ad))
* **init:** keep skill catalogue lines under 80 columns ([55803d0](https://github.com/etalab-ia/just-code/commit/55803d045d746653d5cbe2a0eb38bdae85e1b96e))
* **init:** rendre la liste des compétences compacte et lisible ([1e95748](https://github.com/etalab-ia/just-code/commit/1e957484d309050f425cd4e44663ea312a62de84))
* **qualification:** address remaining doctor review comments ([5ca6f62](https://github.com/etalab-ia/just-code/commit/5ca6f62fcb734688f2c78bb670bda50fbc24d825))

## [0.7.4](https://github.com/etalab-ia/just-code/compare/just-code-v0.7.3...just-code-v0.7.4) (2026-10-05)


### Bug Fixes

* **ci:** donner au dispatch manuel le contexte de dépôt à gh ([473bd81](https://github.com/etalab-ia/just-code/commit/473bd811128d24674296eecf5292f65f40b2d416))
* **ci:** donner au dispatch manuel le contexte de dépôt à gh ([6807ba5](https://github.com/etalab-ia/just-code/commit/6807ba5196506b556cfe5532cac06e04dec2470e))
* **project:** réessayer la lecture du lock face aux échecs transitoires ([bfd77a8](https://github.com/etalab-ia/just-code/commit/bfd77a8c5afe3e1daee1cfe27212d0645fffcef0))
* **project:** réessayer la lecture du lock face aux échecs transitoires ([b0baac6](https://github.com/etalab-ia/just-code/commit/b0baac6e47329166e54dc4eb3ada7387a2799211))
* **project:** remonter la dernière erreur de lecture du lock à l'expiration ([b5088fa](https://github.com/etalab-ia/just-code/commit/b5088fa18ad7ab73fae6b3b636bd33ec7b14705e))

## [0.7.3](https://github.com/etalab-ia/just-code/compare/just-code-v0.7.2...just-code-v0.7.3) (2026-10-05)


### Bug Fixes

* **mcp:** empêcher l'installateur gh de terminer le script de démarrage ([#123](https://github.com/etalab-ia/just-code/issues/123)) ([c0f2d46](https://github.com/etalab-ia/just-code/commit/c0f2d4624f209e57359102611e3b51d7cbd52628))

## [0.7.2](https://github.com/etalab-ia/just-code/compare/just-code-v0.7.1...just-code-v0.7.2) (2026-10-05)


### Bug Fixes

* **mcp:** corriger le nom de la variable npm dans le script de préparation ([#121](https://github.com/etalab-ia/just-code/issues/121)) ([fdd532a](https://github.com/etalab-ia/just-code/commit/fdd532a892bc5f5ac874bd13e5ffef8258015b1a))

## [0.7.1](https://github.com/etalab-ia/just-code/compare/just-code-v0.7.0...just-code-v0.7.1) (2026-10-05)


### Bug Fixes

* **mcp:** réparer les épingles apt du profil navigateur (P16) ([#118](https://github.com/etalab-ia/just-code/issues/118)) ([7e87dfe](https://github.com/etalab-ia/just-code/commit/7e87dfed3d13b334611d8dd2ffaca75fb0189600))

## [0.7.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.6.0...just-code-v0.7.0) (2026-10-05)


### Features

* **mcp:** ajouter les MCP navigateur et unifier init (P16/P17) ([#116](https://github.com/etalab-ia/just-code/issues/116)) ([bfc1748](https://github.com/etalab-ia/just-code/commit/bfc17488d59dac8be2e818565326a385723e2cff))
* **mcp:** configurer data.gouv et Context7 (P15) ([#115](https://github.com/etalab-ia/just-code/issues/115)) ([7fe8bbe](https://github.com/etalab-ia/just-code/commit/7fe8bbe488f6325be5b9cc457a3b484edea1baa9))
* **skills:** gérer les skills projet épinglées (P14) ([#113](https://github.com/etalab-ia/just-code/issues/113)) ([26e942e](https://github.com/etalab-ia/just-code/commit/26e942efdb8437472aec427184a28eeac4c155b5))

## [0.6.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.5.0...just-code-v0.6.0) (2026-10-02)


### Features

* **github:** activer le workflow invité approuvé (P13) ([#105](https://github.com/etalab-ia/just-code/issues/105)) ([51879f7](https://github.com/etalab-ia/just-code/commit/51879f7221499ff15ca87ae52857c368ee53feb6))


### Bug Fixes

* **tests:** fiabiliser le laboratoire d identifiants P02 ([#110](https://github.com/etalab-ia/just-code/issues/110)) ([a819397](https://github.com/etalab-ia/just-code/commit/a819397bc10997bc16898f7b74e168e88a417a1e))

## [0.5.0](https://github.com/etalab-ia/just-code/compare/just-code-v0.4.2...just-code-v0.5.0) (2026-09-28)


### Features

* **auth:** stocker les identifiants hors des projets (P08, en cours) ([7bf35b5](https://github.com/etalab-ia/just-code/commit/7bf35b59af060897b6d62419c6eda7ee109c96a4))
* **auth:** stocker les identifiants hors des projets (P08) ([4103ff5](https://github.com/etalab-ia/just-code/commit/4103ff505656cc46b54bc45e0419f72fa8c5cea5))
* **auth:** stocker les identifiants hors des projets (P08) ([b3357d1](https://github.com/etalab-ia/just-code/commit/b3357d12a963bc19b8f1ad22fea93f0b92db4316))
* **config:** résolution typée, schémas et provenance (P04) ([abbc6ef](https://github.com/etalab-ia/just-code/commit/abbc6ef54ea0a843b6dfd9dc7a19ae71391d7268))
* **config:** résolution typée, schémas et provenance (P04) ([24e9869](https://github.com/etalab-ia/just-code/commit/24e98693a78892c94ae0f1a0c8927581d8b98eb2))
* **init:** commande just-code init et contrat non-TTY (P12b-3) ([#97](https://github.com/etalab-ia/just-code/issues/97)) ([651d22d](https://github.com/etalab-ia/just-code/commit/651d22d2fcbb659baa0b7df937d0cd54493be483))
* **init:** moteur de configuration minimale d'un projet (P12b-2) ([#96](https://github.com/etalab-ia/just-code/issues/96)) ([2c88b55](https://github.com/etalab-ia/just-code/commit/2c88b55d42a7241f986a90b906302bd3bef20e3a))
* **init:** proposer la configuration au lancement nu (P12b-4) ([#98](https://github.com/etalab-ia/just-code/issues/98)) ([b7cd92a](https://github.com/etalab-ia/just-code/commit/b7cd92aad59bf6686a4e176d69b73ff11727ff79))
* Microsandbox en isolation complète par défaut (P12a) ([#94](https://github.com/etalab-ia/just-code/issues/94)) ([c4a0ec4](https://github.com/etalab-ia/just-code/commit/c4a0ec4ab9e84b3039def633ebfb98d0d49cfcb7))
* **msb:** honorer le dimensionnement invité du projet (P12b-1) ([#95](https://github.com/etalab-ia/just-code/issues/95)) ([e801216](https://github.com/etalab-ia/just-code/commit/e801216b7f7da2f253be6badd4d22234a34c9bd7))
* **runtime:** réconciliation et redémarrage non destructif (P07) ([bd4e068](https://github.com/etalab-ia/just-code/commit/bd4e06882f812754331ba033b565966f4606beca))
* **runtime:** réconciliation et redémarrage non destructif (P07) ([a7e1e79](https://github.com/etalab-ia/just-code/commit/a7e1e79cd4f44d5b163898d148439c31b52c0687))
* **tui:** reprendre la dernière session à l’attachement (P12) ([#102](https://github.com/etalab-ia/just-code/issues/102)) ([aae0599](https://github.com/etalab-ia/just-code/commit/aae059927308e1ae0e96c8e26a6ff7fab6271bc5))
* **workspace:** clone invité scellé, transfert filtré et retour revu (P22) ([#91](https://github.com/etalab-ia/just-code/issues/91)) ([b8ce092](https://github.com/etalab-ia/just-code/commit/b8ce0922e92e08c6001c8a097c76a1bd2ec14fe8))


### Bug Fixes

* **auth:** assouplir l'assertion de permissions pour Windows (PR [#86](https://github.com/etalab-ia/just-code/issues/86)) ([643743d](https://github.com/etalab-ia/just-code/commit/643743d94dc8b41cdef41f5fdc3629bd5ded893a))
* **auth:** baseline Go 1.22 preserve et collections Secret Service verrouillees (revue Codex PR [#86](https://github.com/etalab-ia/just-code/issues/86)) ([cb7b10d](https://github.com/etalab-ia/just-code/commit/cb7b10d1d6cbddcd29895b03a2cc8ef1c395b034))
* **auth:** branche darwin et disposition CREDENTIALW exacte (revue Codex PR [#86](https://github.com/etalab-ia/just-code/issues/86)) ([f97f4b0](https://github.com/etalab-ia/just-code/commit/f97f4b0b046292e0d4ad4e66903e132f38d5931b))
* **auth:** fake Runner partage entre les tests d'adaptateurs (PR [#86](https://github.com/etalab-ia/just-code/issues/86)) ([05ff75a](https://github.com/etalab-ia/just-code/commit/05ff75acf91fc9a43baa33b4a648d4cb241daa21))
* **auth:** fixture denied distinct de locked (PR [#86](https://github.com/etalab-ia/just-code/issues/86)) ([3ad0696](https://github.com/etalab-ia/just-code/commit/3ad0696ff100ee942eb4320aa8e51ab7e876592d))
* **auth:** injection op/cible dans le script wincred, statut et remove honnetes (revue Codex PR [#86](https://github.com/etalab-ia/just-code/issues/86)) ([ac6ffac](https://github.com/etalab-ia/just-code/commit/ac6ffac66c64bd98d58ffd3abd34c4b93afc45df))
* **config:** traversée des flags, erreurs de manifeste et valeurs par défaut dans explain ([bbe2e80](https://github.com/etalab-ia/just-code/commit/bbe2e802bec496df2be6ddfcc116367c05472672))
* **lifecycle:** balayages globaux par instance (revue Codex PR [#81](https://github.com/etalab-ia/just-code/issues/81)) ([7fa14a4](https://github.com/etalab-ia/just-code/commit/7fa14a47821a3b6842dab2bc88efd5bda0e2d125))
* **msb:** ne pas publier le port serveur en isolation full (P12) ([#101](https://github.com/etalab-ia/just-code/issues/101)) ([81fd115](https://github.com/etalab-ia/just-code/commit/81fd115599a65daff4ce3876eef7787ed26606a7))
* **msb:** refuser un montage sans preuve de propriété ([#92](https://github.com/etalab-ia/just-code/issues/92)) ([#100](https://github.com/etalab-ia/just-code/issues/100)) ([4278a2a](https://github.com/etalab-ia/just-code/commit/4278a2adac49bdc5f9c68e427e0f67aeff8f1c5c))
* **registry:** verrouiller les mutations entre processus (revue Codex PR [#80](https://github.com/etalab-ia/just-code/issues/80)) ([908b9d6](https://github.com/etalab-ia/just-code/commit/908b9d6444fe31b2a640e191f6a92eb866b4e138))
* **runtime:** brancher la réconciliation et durcir le journal (revue Codex PR [#82](https://github.com/etalab-ia/just-code/issues/82)) ([0a5fa3b](https://github.com/etalab-ia/just-code/commit/0a5fa3bf88def3afc9427f40dffa08a5ac259a91))
* **tui:** éviter l’erreur de reprise au premier lancement ([#103](https://github.com/etalab-ia/just-code/issues/103)) ([49fe767](https://github.com/etalab-ia/just-code/commit/49fe767f182e740b286c0d9671495f07f4e7fab6))

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
