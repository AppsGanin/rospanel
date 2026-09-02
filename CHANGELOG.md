# Changelog

## [2.11.0](https://github.com/AppsGanin/rospanel/compare/v2.10.1...v2.11.0) (2026-09-02)


### Features

* AmneziaWG as a built-in lane ([94af079](https://github.com/AppsGanin/rospanel/commit/94af0795d12f041f98170cf21a5817778d31fc9e))
* answer port 80 instead of leaving it closed ([6ce7dfd](https://github.com/AppsGanin/rospanel/commit/6ce7dfd2b9860d7c5789239297c9248112932400))
* answer port 80 on nodes too ([ba0d549](https://github.com/AppsGanin/rospanel/commit/ba0d54969c35399111e2b36c8f7a902b380c40a8))
* client-side DPI evasion in the subscription (fragment, noise, record fragment) ([a24a543](https://github.com/AppsGanin/rospanel/commit/a24a543f1ea686a7bf69c7d575d6a9830a64a14f))
* import and export users (Marzban, 3x-ui, and this panel's own file) ([1d43f8e](https://github.com/AppsGanin/rospanel/commit/1d43f8e4f6c4753cf2146145575cd42c46b0760b))
* list and revoke your own panel sessions ([e97a5cb](https://github.com/AppsGanin/rospanel/commit/e97a5cb5b22e5e1bf54a6c03c1295aa68331c03e))
* move the security lists to the statistics page ([1e5fc83](https://github.com/AppsGanin/rospanel/commit/1e5fc8354aa3d4f566e9a6fd64f83ef270dff044))
* name the country and network operator behind each scanner ([b82f8f5](https://github.com/AppsGanin/rospanel/commit/b82f8f54599074c3dfadd3d77b62cd70aff88b5e))
* one Save and one Cancel per settings screen ([4968feb](https://github.com/AppsGanin/rospanel/commit/4968febc3ccd5ce06253bd5cc508095d15f1d11b))
* operator notes and tags on users, with the client id shown in the list ([3199c21](https://github.com/AppsGanin/rospanel/commit/3199c21067795159734d88adbb7a406e4ec8220e))
* optionally hide a server from subscriptions while it is offline ([30cd873](https://github.com/AppsGanin/rospanel/commit/30cd8733907332682d2dcf900ab85d198bd1d0a3))
* order subscription servers by load, weight and capacity ([60f8846](https://github.com/AppsGanin/rospanel/commit/60f884613b3ab8b3c3f0307aa7fc059711e867ca))
* refuse connections by source country and network ([158f78e](https://github.com/AppsGanin/rospanel/commit/158f78e87711b991c57eb6fab122149ab1ebda74))
* say why a config was rolled back, and roll back to one that ran ([3087eab](https://github.com/AppsGanin/rospanel/commit/3087eab83048f61833c56a58114b717271d2fd14))
* sign-in alerts and automatic blocklist measures ([7b7ce88](https://github.com/AppsGanin/rospanel/commit/7b7ce88d11b5949006e3e02afdcfa473d25a20fe))
* update Xray-core to 26.7.28 ([e728ac6](https://github.com/AppsGanin/rospanel/commit/e728ac658c48ce5d371a270a576de95d6d56ef74))


### Bug Fixes

* a manager built without a shaper must not crash the tests ([14f29e3](https://github.com/AppsGanin/rospanel/commit/14f29e374fdc41031c43b67be4bd8e455794b54e))
* answer an unknown API path with JSON, not with the app shell ([88bc658](https://github.com/AppsGanin/rospanel/commit/88bc6587663891d635e5e030530873b699c7f939))
* page the recent-scanners list instead of dumping every row ([28ae26a](https://github.com/AppsGanin/rospanel/commit/28ae26a43f7359ecd63c9c4e8654e7d04dd42029))
* port 80 names this machine, and keeps naming the right one ([8dcfed3](https://github.com/AppsGanin/rospanel/commit/8dcfed33abea47095c70bf3a7576b3fcc4ff6bcd))
* re-send the user set when Xray comes back from a crash ([62bbde0](https://github.com/AppsGanin/rospanel/commit/62bbde0b0a72b0aefc878fc78e50e684c5337363))
* roll back an unloadable config even when no Apply preceded it ([707bfc9](https://github.com/AppsGanin/rospanel/commit/707bfc98dcd33bb4b527bc163469852064e0bf59))
* show a proxy listener's default port as a value while it is off ([45c74c5](https://github.com/AppsGanin/rospanel/commit/45c74c5582d6b552593a940ca217fa6124e18002))
* snapshot the config before Xray reads it, and pin what was only assumed ([7860433](https://github.com/AppsGanin/rospanel/commit/786043313ac0917124c3fd3e3317587d46cf76e1))
* spell the scanner's country the way the rest of the panel does ([3beaafd](https://github.com/AppsGanin/rospanel/commit/3beaafd915a880577d799eafad5548ac2d05f958))
* take port 80 before issuing the certificate, not after ([fdc42ee](https://github.com/AppsGanin/rospanel/commit/fdc42ee13bea00795c99db17c98d6d3528294fd8))
* the JSON fallback has to cover every method, not just GET ([ceaa62f](https://github.com/AppsGanin/rospanel/commit/ceaa62f3697e45d7e7e8d667059ad22c0dbeccd4))

## [2.10.1](https://github.com/AppsGanin/rospanel/compare/v2.10.0...v2.10.1) (2026-08-24)


### Bug Fixes

* count devices by HWID when HWID is what identifies them ([#66](https://github.com/AppsGanin/rospanel/issues/66)) ([38a51ea](https://github.com/AppsGanin/rospanel/commit/38a51ea0cd8014cc8f74934f1d9856bc841a327a))
* forgive a network handover instead of giving up the device limit ([84afd47](https://github.com/AppsGanin/rospanel/commit/84afd476a07faefd459141f71fb99ef4f9b72d78))
* wait out a network change before the device limit cuts anyone ([6fa0043](https://github.com/AppsGanin/rospanel/commit/6fa00438c696977a70f452d2b37815d345713446))


### Performance Improvements

* count devices in one pass instead of once per connection row ([3674986](https://github.com/AppsGanin/rospanel/commit/3674986d7b619aa7c0a1212e02b7283207a95408))


### Reverts

* drop the handover grace, it did not work and cost enforcement ([33f8745](https://github.com/AppsGanin/rospanel/commit/33f8745dda581c4870d16389d8aa1ed7248d74ff))

## [2.10.0](https://github.com/AppsGanin/rospanel/compare/v2.9.1...v2.10.0) (2026-08-22)


### Features

* **ui:** table views for users, the journal and both payment lists ([fd21de9](https://github.com/AppsGanin/rospanel/commit/fd21de940524e91f5113ea6067a93702450a1d24))


### Bug Fixes

* **cli:** say so when a rescue password is redirected out of the terminal ([56e88cb](https://github.com/AppsGanin/rospanel/commit/56e88cb1c291840b1df57b06799aad69b78a63b9))
* TLS lifecycle and subscription rendering defects ([ec41ee2](https://github.com/AppsGanin/rospanel/commit/ec41ee2f303a6186638f0da89ed8c2bd57981b67))

## [2.9.1](https://github.com/AppsGanin/rospanel/compare/v2.9.0...v2.9.1) (2026-08-19)


### Bug Fixes

* a re-ask no longer re-arms a node command already delivered ([820dc86](https://github.com/AppsGanin/rospanel/commit/820dc86e3d575bdec5d2224c50507fcb25e26fc7))
* a refused subscription must not look like an empty one ([a1375b3](https://github.com/AppsGanin/rospanel/commit/a1375b3e453ce253ca089cf2e0878ba91c4eba2e))
* bound a bulk user action ([435aaed](https://github.com/AppsGanin/rospanel/commit/435aaedd9521f252828bb5fffda2619c3075f2e1))
* **mcp:** keep the whole backup surface out of the assistant's toolbox ([3c11034](https://github.com/AppsGanin/rospanel/commit/3c110344dbf0dd1739ad10677f9fecdafb3ba761))
* node commands survive handover; subscription access fails closed ([9ac057c](https://github.com/AppsGanin/rospanel/commit/9ac057c8ae5d8a436d7dc1964529893a63e83fc3))
* persist node commands; make GET /v1/nodes/{id} match the list ([66ffe8f](https://github.com/AppsGanin/rospanel/commit/66ffe8f29572ffae0ade60af476be65da134f975))


### Performance Improvements

* write a fleet-wide node command in one transaction ([de92428](https://github.com/AppsGanin/rospanel/commit/de92428e00873fc1ded9201ce87f14ce6affaac0))

## [2.9.0](https://github.com/AppsGanin/rospanel/compare/v2.8.0...v2.9.0) (2026-08-18)


### Features

* **api:** publish the configuration surface over /v1 (and therefore MCP) ([7959249](https://github.com/AppsGanin/rospanel/commit/79592490a1d96b275c6d97f71d7f397849b6fb36))
* **config:** snapshot/rollback the whole server config, not just routing ([e39bcbb](https://github.com/AppsGanin/rospanel/commit/e39bcbb82919bd450ccd936839e64ffcf3d61cef))
* **node:** surface sync-quality + smart backoff for a limping transport ([022ef8b](https://github.com/AppsGanin/rospanel/commit/022ef8ba06950276fea7be2462343be0b7aaddca))
* **ops:** make the Xray watchdog operator-visible and toggleable ([5a723d8](https://github.com/AppsGanin/rospanel/commit/5a723d8379a02084f8ac91601f58d4476f6a5f13))
* **security:** probe detection — optional firewall auto-block + daily digest ([6bc6bcc](https://github.com/AppsGanin/rospanel/commit/6bc6bcc87baabcbda8bf3a72dd25eb14cccb9d89))
* **stats:** connection map by network operator (ASN), not just country ([2df16d7](https://github.com/AppsGanin/rospanel/commit/2df16d7ecc4f4a54f4c9d88d79e942ef6636ade9))
* step-up on reset/restore; widen egress floor; fix misleading hint ([302cb98](https://github.com/AppsGanin/rospanel/commit/302cb982ad5ed15db913a479b68164a66d33eb78))
* **tg:** split the client-bot device line into IP and HWID counts ([b4b5e14](https://github.com/AppsGanin/rospanel/commit/b4b5e14ad6f5d65623e9dabb07c930e19860d473))


### Bug Fixes

* act on the three-agent review of the new API surface ([c162af9](https://github.com/AppsGanin/rospanel/commit/c162af9b8061f87b03ee7c1c433862c164c9d219))
* **api:** apply the live half of the settings a PATCH changes ([fbdefab](https://github.com/AppsGanin/rospanel/commit/fbdefab44efa6ab2bd90d7d1725b83b35ee7e502))
* bound device roster, cache public pages, close billing gaps ([5e2a84a](https://github.com/AppsGanin/rospanel/commit/5e2a84a812447bde797b584ed9b5512292486fe0))
* bound probeblock set, quiet nft failures, snapshot-rollback UX, geo re-parse ([6fe3f86](https://github.com/AppsGanin/rospanel/commit/6fe3f86b652f02dd13728f494f10143ca67e40f7))
* error on ASN byte-cap truncation; close probeblock disarm race ([028cad5](https://github.com/AppsGanin/rospanel/commit/028cad5c4ef7673e2c18f0cbf3c5ded200c89811))
* guard restores against a newer schema, unlock decoy reads, bound broadcasts ([032e0b7](https://github.com/AppsGanin/rospanel/commit/032e0b7067a9402b7702da4cd61cad615bee8644))
* harden snapshot rollback, node backoff, probeblock, ASN parsing ([ef2f1bc](https://github.com/AppsGanin/rospanel/commit/ef2f1bc98a3497f3fe4adf3282a364cc1fdd01d8))
* **node:** force HTTP/1.1 for the sync long-poll (stop GOAWAY flapping) ([c01a9c9](https://github.com/AppsGanin/rospanel/commit/c01a9c900197f00edf73da226787610a5f40143c))
* paid-renewal quota, tunnel→control-API escape, order cancel guard ([4e1664d](https://github.com/AppsGanin/rospanel/commit/4e1664dc749385e6982b56e96a3964f2b010483d))
* support impersonation, resurrected nodes, half-applied rollbacks ([53b9b7d](https://github.com/AppsGanin/rospanel/commit/53b9b7d880929a6fa24c92c2efaa33edbaf3dc78))
* the rest of the three-agent sweep ([daa530e](https://github.com/AppsGanin/rospanel/commit/daa530e2f188af99f345bc03d1755633326b349c))
* **watchdog:** still detect+alert a wedged Xray when auto-recovery is off ([6aeeab4](https://github.com/AppsGanin/rospanel/commit/6aeeab4fff937e94f8074e5e0d69b1cbe33e507a))

## [2.8.0](https://github.com/AppsGanin/rospanel/compare/v2.7.1...v2.8.0) (2026-08-15)


### Features

* **audit:** search, date-range filter and CSV export for the panel journal ([fdbfe89](https://github.com/AppsGanin/rospanel/commit/fdbfe8943b6b8ccea20ad99400fa21c2f8068104))
* **cli:** add a rescue command to regain locked-out access ([c807b2b](https://github.com/AppsGanin/rospanel/commit/c807b2b9089f01be501d83092c420f883c8dbc64))
* **nodes:** per-node traffic coefficient for quota ([0a13450](https://github.com/AppsGanin/rospanel/commit/0a134500cab43fd5018ad72f4443c99505c06b28))
* **ops:** maintenance mode ([44571af](https://github.com/AppsGanin/rospanel/commit/44571af995084e914bece2a407019739f1daec21))
* **routing:** config snapshots with one-click rollback ([d93d130](https://github.com/AppsGanin/rospanel/commit/d93d1301c2988899e45a069804a05acdf4e2dc4a))
* **security:** detect IPs scanning for the hidden panel path ([8f5852a](https://github.com/AppsGanin/rospanel/commit/8f5852af9a6038124a1e15f0f85edbbe21acde21))
* **stats:** connection geo map — source IPs by country ([3f8e15b](https://github.com/AppsGanin/rospanel/commit/3f8e15b8f9bed570128cec663d87b1ce5610f30e))
* **sub:** response rules — force format or block by client/OS ([545698c](https://github.com/AppsGanin/rospanel/commit/545698cab569580a995e6073d970181dbac76d54))
* **xray:** watchdog auto-recovers a wedged (alive but not serving) Xray ([f5bb172](https://github.com/AppsGanin/rospanel/commit/f5bb1728549598643d63fe85439e490b65f9676b))


### Bug Fixes

* **auth:** set Secure and SameSite on the session-clearing cookie ([3b7385b](https://github.com/AppsGanin/rospanel/commit/3b7385bcf4e9b7679de7ee67d033def13097fd5d))
* harden geoip parser, probe miss-detection and CSV export (review) ([4de933e](https://github.com/AppsGanin/rospanel/commit/4de933e46ddfc3d8ad61842263001e2e5649153f))
* post-review fixes for the three new features ([e4cc344](https://github.com/AppsGanin/rospanel/commit/e4cc344f884f2c656a6b972cefaac698a1ecb62c))

## [2.7.1](https://github.com/AppsGanin/rospanel/compare/v2.7.0...v2.7.1) (2026-08-14)


### Bug Fixes

* **docker:** bump build image to golang:1.26.6 ([f809173](https://github.com/AppsGanin/rospanel/commit/f8091738f63da83cca868d94f923a1c1234c1723))

## [2.7.0](https://github.com/AppsGanin/rospanel/compare/v2.6.0...v2.7.0) (2026-08-14)


### Features

* **decoy:** add a self-hosted Nextcloud login page ([15119b2](https://github.com/AppsGanin/rospanel/commit/15119b292f2980ea63107e73db1e97aa7c8b2ef6))
* **inbounds:** add Shadowsocks-2022 as a custom inbound protocol ([1e6c6b6](https://github.com/AppsGanin/rospanel/commit/1e6c6b67f8e73ced48bef63a7fb4cf974dcaf8cd))
* **inbounds:** allow emoji in connection and lane names ([9f10280](https://github.com/AppsGanin/rospanel/commit/9f102803e732c1040cce01effc0e9d52677fcf35))


### Bug Fixes

* **api:** self-host Swagger UI; add Dependabot for Go and actions ([383d171](https://github.com/AppsGanin/rospanel/commit/383d171210d5683adbe89c2666c3a4b5996ad5da))
* **deps:** bump Go to 1.26.6 for stdlib security fixes ([609c4af](https://github.com/AppsGanin/rospanel/commit/609c4af1e28c540eb9595b726111152c6f0660f3))
* **inbounds:** carry the Shadowsocks method through the request mapping ([5e0cb7c](https://github.com/AppsGanin/rospanel/commit/5e0cb7c2868f95556e48e91fd2c1d415d2f7f3ad))

## [2.6.0](https://github.com/AppsGanin/rospanel/compare/v2.5.0...v2.6.0) (2026-08-13)


### Features

* **api:** serve the panel API to assistants over MCP ([db89bec](https://github.com/AppsGanin/rospanel/commit/db89becf7325a04b9b4e466bd9f300445bf44be4))
* **api:** window every list with limit and offset ([5b971d5](https://github.com/AppsGanin/rospanel/commit/5b971d5b8659dad896ac0af2c4572b2ff21ae7cf))
* **devices:** bind installs by HWID and cap them per user ([3f0c320](https://github.com/AppsGanin/rospanel/commit/3f0c32023561918cbc69fe10d93bd4ff5005b8f5))
* **monitoring:** add Prometheus metrics and a public status page ([f87452a](https://github.com/AppsGanin/rospanel/commit/f87452a1301fd3c83832a3aa308ac7662f1300bd))
* **shaping:** cap per-user speed with kernel traffic shaping ([a45843e](https://github.com/AppsGanin/rospanel/commit/a45843ee8865f0dabdd0bd350a7eebb1ae9edb20))
* **stats:** add a per-server daily series and fill quiet days ([5d9ba81](https://github.com/AppsGanin/rospanel/commit/5d9ba811bf0be1bd174e7693b39f600a8f54d4f0))
* **store:** add schema for devices, speed limits, uptime and status page ([3c23a41](https://github.com/AppsGanin/rospanel/commit/3c23a411aa1a173233b01b8de4dad8ee6f016df8))
* **ui:** surface devices, speed limits, nodes and the new settings ([dcc56fa](https://github.com/AppsGanin/rospanel/commit/dcc56fa0326ed93ab3d792dfad175d5f1b2c6db7))


### Bug Fixes

* **api:** honour enabled when creating a webhook ([82bf667](https://github.com/AppsGanin/rospanel/commit/82bf667cf2265da17c298bc801c989358f53616b))
* **api:** reject unknown and read-only body fields ([64845a8](https://github.com/AppsGanin/rospanel/commit/64845a8d6647c30f2d204ca8926b9ac187d486e0))
* **decoy:** serve every decoy asset from our own origin ([226c3f1](https://github.com/AppsGanin/rospanel/commit/226c3f17fe10101d9ccb336a4eb6e9bce83e2250))
* **sub:** keep Telegram deep links working without the SDK ([a52d165](https://github.com/AppsGanin/rospanel/commit/a52d16545fec0e75ecc4451fe9e83f9667e82ee3))

## [2.5.0](https://github.com/AppsGanin/rospanel/compare/v2.4.1...v2.5.0) (2026-08-11)


### Features

* **sub:** add v2RayTun deep link support ([8473936](https://github.com/AppsGanin/rospanel/commit/8473936f789b71264ae01d5d2cfc674364db3bb3))
* **ui:** add client-side pagination for long lists ([0a96691](https://github.com/AppsGanin/rospanel/commit/0a966919fec8e6375386c110aa956c6384fd39de))


### Bug Fixes

* **tls:** restart Xray on certificate update instead of reconcile ([a9b16b1](https://github.com/AppsGanin/rospanel/commit/a9b16b1bcc25da43fcd7d457a0c2690042d39fa1))
* **web:** standardize background colors on groups panel cards ([1981c8c](https://github.com/AppsGanin/rospanel/commit/1981c8cd9c11e1dc12428cee807acfc0c6370012))

## [2.4.1](https://github.com/AppsGanin/rospanel/compare/v2.4.0...v2.4.1) (2026-08-10)


### Bug Fixes

* **billing:** reset usage counters on plan change ([18f10e9](https://github.com/AppsGanin/rospanel/commit/18f10e9ea691b28455e949aca22e5b2d7eb554f8))

## [2.4.0](https://github.com/AppsGanin/rospanel/compare/v2.3.0...v2.4.0) (2026-08-10)


### Features

* **auth:** add TOTP two-factor authentication for admin logins ([8e0e191](https://github.com/AppsGanin/rospanel/commit/8e0e191bce786d077da5b5f9f903ddd805a8769b))
* **node:** support join via ROSPANEL_JOIN env for containerized nodes ([0906546](https://github.com/AppsGanin/rospanel/commit/090654696bdd428be7f17a076e7f83de90967f2b))


### Performance Improvements

* **store:** cache schema for fresh databases to speed up tests ([e0e95d2](https://github.com/AppsGanin/rospanel/commit/e0e95d2bdef70ab50e3833eb08082d3a2fe69536))

## [2.3.0](https://github.com/AppsGanin/rospanel/compare/v2.2.0...v2.3.0) (2026-08-06)


### Features

* **api:** add per-node system proxy endpoints (SOCKS/HTTP) ([031395c](https://github.com/AppsGanin/rospanel/commit/031395cb477cfc837171454b1616414b451a0ad9))


### Bug Fixes

* **core:** prevent system proxy from colliding with panel port ([aa6052f](https://github.com/AppsGanin/rospanel/commit/aa6052fed7c8570a07b0db49c70924037dd7e103))
* **proxy:** clarify proxy user validation and prevent save on invalid state ([43be684](https://github.com/AppsGanin/rospanel/commit/43be684ebb2e9cda63921fe9637749ef4cf1f40d))

## [2.2.0](https://github.com/AppsGanin/rospanel/compare/v2.1.0...v2.2.0) (2026-08-02)


### Features

* **billing:** tie tariff plans to access groups ([b4c5607](https://github.com/AppsGanin/rospanel/commit/b4c560727c9a38b6170e331cf28e48d23e1125ae))


### Bug Fixes

* **sub:** add `udp: true` to all Clash proxy entries and fix rule quoting ([7b28b6b](https://github.com/AppsGanin/rospanel/commit/7b28b6b18c89893f29ba52d58aace23e57cb627f))

## [2.1.0](https://github.com/AppsGanin/rospanel/compare/v2.0.0...v2.1.0) (2026-07-30)


### Features

* **routing:** give WARP a local address and publish both egresses ([603fcb8](https://github.com/AppsGanin/rospanel/commit/603fcb8e1e435f1342eedc333a17881fe93ff17f))
* **telegram:** route everything Telegram-bound through an optional proxy ([0c94e1f](https://github.com/AppsGanin/rospanel/commit/0c94e1f85b6a1b2d829ad7d986587bdc5c47eae0))


### Bug Fixes

* **telegram:** hold the bots until a local egress actually answers ([298bada](https://github.com/AppsGanin/rospanel/commit/298badaf344599d7a6c34557213e2e6b491667ba))
* **telegram:** log why the Mini App SDK fetch failed ([9319614](https://github.com/AppsGanin/rospanel/commit/931961431df753d67188dc8efa430e1165f9446b))
* **xray:** rebuild Hysteria2 inbounds instead of restarting for user changes ([52a93e8](https://github.com/AppsGanin/rospanel/commit/52a93e8a139160e544f750f9baaae9fa7cde17fd))
* **xray:** run WARP's WireGuard in userspace to stop a routing-table leak ([fe7556c](https://github.com/AppsGanin/rospanel/commit/fe7556c4ce6de211a14149a7df9d72412149b589))


### Performance Improvements

* **xray:** skip the restart when the generated config is unchanged ([c76f3d9](https://github.com/AppsGanin/rospanel/commit/c76f3d971626e71dc7214e2b65f206cf616788c2))

## [2.0.0](https://github.com/AppsGanin/rospanel/compare/v1.5.0...v2.0.0) (2026-07-29)


### ⚠ BREAKING CHANGES

* **i18n:** response shapes that carried rendered Russian now carry dictionary keys, so anything reading them has to word them itself.

### Features

* **i18n:** Russian/English across the panel, bots, subscription page and CLI ([bfe6c88](https://github.com/AppsGanin/rospanel/commit/bfe6c8832fd04d5556279bd2e21783b045e27265))

## [1.5.0](https://github.com/AppsGanin/rospanel/compare/v1.4.0...v1.5.0) (2026-07-26)


### Features

* **core:** add custom inbounds, access groups, and runtime port guard refresh ([21fe5ba](https://github.com/AppsGanin/rospanel/commit/21fe5ba330643a5c5022e20b7bbec3284e8cff75))
* **core:** add fleet-wide Xray and TLS alerts for remote nodes ([e361e05](https://github.com/AppsGanin/rospanel/commit/e361e057e53e3ed2cf5b53dcceec1c08c82ace96))
* **core:** proxy Telegram Mini App SDK through our origin ([6453ea0](https://github.com/AppsGanin/rospanel/commit/6453ea0a066d7c4878e4026d91978cac46b6c054))
* **decoy:** harden decoy to mimic real static file server ([30ce448](https://github.com/AppsGanin/rospanel/commit/30ce448553d697f5d3f59d4ca03c3686e7cd95f3))


### Bug Fixes

* **sub:** self-host Mulish webfonts to eliminate render-blocking Google Fonts ([b3d6b01](https://github.com/AppsGanin/rospanel/commit/b3d6b01d42c64fe0445ee47a8d6162d3bf27496c))

## [1.4.0](https://github.com/AppsGanin/rospanel/compare/v1.3.0...v1.4.0) (2026-07-23)


### Features

* **core:** add confirmed Xray restart tracking for remote nodes ([b0cd098](https://github.com/AppsGanin/rospanel/commit/b0cd098affb1deef291fbdc6c9029372d43372e0))


### Bug Fixes

* **web:** add typecheck script and update run() API usage ([9ccfef6](https://github.com/AppsGanin/rospanel/commit/9ccfef62fb71111fb7d7e8b11ae172302eb06db3))

## [1.3.0](https://github.com/AppsGanin/rospanel/compare/v1.2.0...v1.3.0) (2026-07-22)


### Features

* add abuse detection with blocklist matching ([34d65fa](https://github.com/AppsGanin/rospanel/commit/34d65fa0b1b2927aa657f335cb58aef6fec73a83))
* **api:** add /v1/stats/nodes endpoint for traffic breakdown by server ([b471b2c](https://github.com/AppsGanin/rospanel/commit/b471b2cb48d7d5f4ebe3cb6fb4581a4fe12e0929))

## [1.2.0](https://github.com/AppsGanin/rospanel/compare/v1.1.0...v1.2.0) (2026-07-20)


### Features

* **telegram:** support relay, broadcasts, and the scaling work that came out of it ([#34](https://github.com/AppsGanin/rospanel/issues/34)) ([a5bc0f8](https://github.com/AppsGanin/rospanel/commit/a5bc0f8a035da7a0e2f155d092974e038dde0454))


### Bug Fixes

* **telegram:** handle 429 rate limits with retry logic ([170a95d](https://github.com/AppsGanin/rospanel/commit/170a95d7875e28ed133d52d07ab2065fe9856b7f))

## [1.1.0](https://github.com/AppsGanin/rospanel/compare/v1.0.0...v1.1.0) (2026-07-16)


### Features

* **iplist:** add iplist database support with independent refresh cadence ([33042a5](https://github.com/AppsGanin/rospanel/commit/33042a524c3d8768f01c04773bbbf27e94bfd5cf))

## [1.0.0](https://github.com/AppsGanin/rospanel/compare/v0.15.0...v1.0.0) (2026-07-15)


### Features

* multi-node fleet, payment provider registry, flexible self-registration ([6aa1b45](https://github.com/AppsGanin/rospanel/commit/6aa1b450cab5fafdabeb42717b0d117e2c8c7f77))

## [0.15.0](https://github.com/AppsGanin/rospanel/compare/v0.14.0...v0.15.0) (2026-07-13)


### Features

* **health:** connection self-test that verifies traffic actually flows ([8fd1a4e](https://github.com/AppsGanin/rospanel/commit/8fd1a4ef2cca739754b65450ce2e0edff98f8f18))
* **health:** self-test flags egress that isn't the server's own IP ([04dc816](https://github.com/AppsGanin/rospanel/commit/04dc816dd084774da0192d6baf5b684fc0ddb622))
* subscription announce, expired-user auto-delete, DB integrity recovery ([346f174](https://github.com/AppsGanin/rospanel/commit/346f17423dd922dae12d395f45b7178527962bc6))


### Bug Fixes

* **reality:** reject donors with oversized certs ([#6402](https://github.com/AppsGanin/rospanel/issues/6402)) + name the cause in self-test ([8dbcb26](https://github.com/AppsGanin/rospanel/commit/8dbcb26ce4694491ed1aed1809b602c483dd4820))
* self-test TLS pin on self-signed, DB recovery blank-boot, egress wording ([639559e](https://github.com/AppsGanin/rospanel/commit/639559e99b9a0034d8ec115c4f0e154560b5b006))

## [0.14.0](https://github.com/AppsGanin/rospanel/compare/v0.13.0...v0.14.0) (2026-07-12)


### Features

* **admins:** multiple admins with roles and an admin audit trail ([3ac3c84](https://github.com/AppsGanin/rospanel/commit/3ac3c8408e7308b0838cd99f9e2fd6556208d813))
* **admins:** multiple admins with roles and an admin audit trail ([35c18ba](https://github.com/AppsGanin/rospanel/commit/35c18ba19a93fec532596514d830335b5a7c1277))


### Bug Fixes

* **ui:** stop the version badge overlapping the panel name in the header ([347710a](https://github.com/AppsGanin/rospanel/commit/347710acebdc06687fccc0bf59998a85dd4ece97))

## [0.13.0](https://github.com/AppsGanin/rospanel/compare/v0.12.0...v0.13.0) (2026-07-11)


### Features

* **audit:** add comprehensive audit log for user and billing actions ([d75eb33](https://github.com/AppsGanin/rospanel/commit/d75eb334a0adb6ac24a445bf1449eacb5b7f5941))
* **backup:** scheduled local backups, independent of Telegram ([c9293e1](https://github.com/AppsGanin/rospanel/commit/c9293e1c301bac9ea674e6db596c2980313a4948))
* **core:** surface connguard and BBR status in the health report ([7a2693e](https://github.com/AppsGanin/rospanel/commit/7a2693e856b5590e64dadb6ec2359ba44927096b))
* **log:** timestamp panel logs, in the operator's timezone ([64bb14d](https://github.com/AppsGanin/rospanel/commit/64bb14d60816f12a2dcae201cbab3937102381b6))
* **server:** lock out repeated invalid API keys, add a key-free liveness probe ([94f84cb](https://github.com/AppsGanin/rospanel/commit/94f84cb47bc1563b2188a2e6dcf40e5fe2b9db46))
* **xray:** add a Restart button to the Xray card ([82e8ad1](https://github.com/AppsGanin/rospanel/commit/82e8ad12628ba8e1e77254ab31b95365d097978e))
* **xray:** run Xray in the operator's timezone so its log matches the panel's ([09e58f7](https://github.com/AppsGanin/rospanel/commit/09e58f7de040923397b65d32426e74afd0f3ee4d))


### Bug Fixes

* **billing:** always show tariff settings regardless of enabled state ([7cc5c32](https://github.com/AppsGanin/rospanel/commit/7cc5c32ce1dc39634abb674c215df1a805300b00))
* **datasec:** correct error message about secrets.key recovery ([26abc24](https://github.com/AppsGanin/rospanel/commit/26abc248988755581634a690bed3af8f0d5661d8))
* **lint:** name the UserEventRetentionDays doc comment after its const ([6b8c784](https://github.com/AppsGanin/rospanel/commit/6b8c784908d356f949e6ec912cec52bd49511636))
* **log:** pass structured key/value args to the slog helpers ([33d87d4](https://github.com/AppsGanin/rospanel/commit/33d87d468cc5b93f93a42b80df6abbedbe17766e))
* payment amount verification, login lockout hardening, and timezone-correct logs ([4169129](https://github.com/AppsGanin/rospanel/commit/416912958fece0101a58c9468ae87baf3077e0c3))
* **payments:** verify the charged amount matches the order before granting a plan ([f585749](https://github.com/AppsGanin/rospanel/commit/f585749ec08eb0fbeac45d9d52158611037fbddb))
* **security:** bump Go toolchain to 1.26.5 to patch stdlib CVEs ([f4c7b2f](https://github.com/AppsGanin/rospanel/commit/f4c7b2ffbda3041b4aacb3964678b7da154bf0a8))
* **server:** keep login lockouts when shedding the rate-limiter map ([33e3774](https://github.com/AppsGanin/rospanel/commit/33e3774b982f6459699dc81dbd9c3ab23f1b23ca))
* **sub:** hide the tariff and payment block for users with no plan ([94d2d19](https://github.com/AppsGanin/rospanel/commit/94d2d193119c91fc80ef67882bf891b714fe8d0b))
* **ui:** stop the Xray card overflowing on mobile ([849fd65](https://github.com/AppsGanin/rospanel/commit/849fd65b5f5e1d32f0dfeab440beaaf57a2e8c0b))
* unify first-run URL label to "Full URL" ([a6e8912](https://github.com/AppsGanin/rospanel/commit/a6e8912721a35466b70b99e754680fdec1ed1bae))


### Performance Improvements

* **store:** index and prune the connections table ([92e66e2](https://github.com/AppsGanin/rospanel/commit/92e66e26cebe14efe96c4a4dc0a5569dbade3c17))

## [0.12.0](https://github.com/AppsGanin/rospanel/compare/v0.11.0...v0.12.0) (2026-07-10)


### Features

* add startup stage logging, improve UX for secret path and update flows ([fe8ceb3](https://github.com/AppsGanin/rospanel/commit/fe8ceb3bd847994ff48810ccd6a2e14d4437ff37))


### Bug Fixes

* return 403 instead of 401 for wrong step-up password ([e34a328](https://github.com/AppsGanin/rospanel/commit/e34a3280ac3e33e242d3ba29a74b09257e47273e))

## [0.11.0](https://github.com/AppsGanin/rospanel/compare/v0.10.0...v0.11.0) (2026-07-10)


### Features

* **billing:** add plan migration, cancellation, renewal and idempotent payments ([84b4ece](https://github.com/AppsGanin/rospanel/commit/84b4eceb08b76a40b752be65b26d228fadbb6c47))
* **core,store,link:** add custom per-protocol node display names ([9fd6403](https://github.com/AppsGanin/rospanel/commit/9fd6403abbf7b1ea0aafae06fd2fd68fda7ad7ee))


### Bug Fixes

* **billing:** refactor free plan logic and upgrade Xray to v26.6.27 ([4d031a3](https://github.com/AppsGanin/rospanel/commit/4d031a3a633266d38870b117fae8f34e3d248132))

## [0.10.0](https://github.com/AppsGanin/rospanel/compare/v0.9.0...v0.10.0) (2026-07-08)


### Features

* **api:** add REST API, webhook delivery, and payment stats ([fa009ae](https://github.com/AppsGanin/rospanel/commit/fa009aec4aaade219fbc3a8aec48b65941739253))

## [0.9.0](https://github.com/AppsGanin/rospanel/compare/v0.8.1...v0.9.0) (2026-07-04)


### Features

* **dns:** add support for primary/secondary DNS presets with migration ([3ef7cbd](https://github.com/AppsGanin/rospanel/commit/3ef7cbd3e480a142de9b56029e7b8de56eb82281))


### Bug Fixes

* **server:** allow TLS status read during first-run wizard ([5a3014b](https://github.com/AppsGanin/rospanel/commit/5a3014b3acd1400e35dfff774a06235f676011c6))

## [0.8.1](https://github.com/AppsGanin/rospanel/compare/v0.8.0...v0.8.1) (2026-07-01)


### Bug Fixes

* **billing:** adjust trial and free plan data limits ([a9b4e3b](https://github.com/AppsGanin/rospanel/commit/a9b4e3b27dc9b1da5f0f58cb54afb8c5896afbc4))
* **core:** add concurrency, validation, and robustness safeguards ([ce1d0e5](https://github.com/AppsGanin/rospanel/commit/ce1d0e55b6a0199acd4a666d2603c9e7a62d76c5))
* **ui:** adjust selection bar breakpoints to match header nav ([80b0842](https://github.com/AppsGanin/rospanel/commit/80b0842bcc2b10dfb4c98b965630f61ee4238624))
* **ui:** update user card selection ring color to brand-600 ([c5c614e](https://github.com/AppsGanin/rospanel/commit/c5c614e2d0c897a25c40dfefc8fce2cfd70512a1))

## [0.8.0](https://github.com/AppsGanin/rospanel/compare/v0.7.0...v0.8.0) (2026-06-30)


### Features

* **connguard:** add host-level per-IP connection guard ([511f963](https://github.com/AppsGanin/rospanel/commit/511f96399ef0b81da51896cff9f4bcee85b12d1b))
* **security:** harden systemd service and Argon2id concurrency ([7b58c1b](https://github.com/AppsGanin/rospanel/commit/7b58c1b8e7b41a66616d6ba37e78729b9f2ee04d))


### Bug Fixes

* **core:** force full reconcile when hysteria is enabled on user sync ([f1274b6](https://github.com/AppsGanin/rospanel/commit/f1274b6cab70bd20eab39d023067fbbd379b8c0f))

## [0.7.0](https://github.com/AppsGanin/rospanel/compare/v0.6.0...v0.7.0) (2026-06-30)


### Features

* **payments,panel:** add test-friendly base URL overrides and implement health diagnostics ([87e007a](https://github.com/AppsGanin/rospanel/commit/87e007a1c3d69b5528070105b53917082fe93a0c))
* **telegram:** admin event notifications ([838f066](https://github.com/AppsGanin/rospanel/commit/838f066ef7b9ac1bedf5a201d78f5d1b760f1f67))
* **users:** add bulk actions with single Xray sync ([52c1d07](https://github.com/AppsGanin/rospanel/commit/52c1d0779f6a2a5725e12feb0bae6bbcad11b0ec))
* **users:** add confirmation dialog for bulk actions ([cc8ab0a](https://github.com/AppsGanin/rospanel/commit/cc8ab0adf6fc14ce2ab409b2a334ff9434dad37d))

## [0.6.0](https://github.com/AppsGanin/rospanel/compare/v0.5.0...v0.6.0) (2026-06-28)


### Features

* **billing:** add payment provider integration with YooKassa and CryptoBot ([b206250](https://github.com/AppsGanin/rospanel/commit/b2062500ffd54b7f584b9c4f06c36d8a3b1397c6))

## [0.5.0](https://github.com/AppsGanin/rospanel/compare/v0.4.0...v0.5.0) (2026-06-25)


### Features

* **branding:** add customizable panel name and colour theme ([4cdb329](https://github.com/AppsGanin/rospanel/commit/4cdb3298e004ec4bb0f136984634c7dbf4672728))
* **cli:** add path command to show panel URL and check secrets/DB health ([ca6768d](https://github.com/AppsGanin/rospanel/commit/ca6768dc9b32f097379b2bc713e8f63f536cc33a))


### Bug Fixes

* **core:** normalize ACME host names to lowercase ([a63ac59](https://github.com/AppsGanin/rospanel/commit/a63ac5967f400f3ccf8cea640c5e307192a00bd2))

## [0.4.0](https://github.com/AppsGanin/rospanel/compare/v0.3.0...v0.4.0) (2026-06-24)


### Features

* **billing:** tariffs, payment orders, trial and free tiers ([ea67e3a](https://github.com/AppsGanin/rospanel/commit/ea67e3a60ed34a8d7d58e24e96d9d7b442d2c5f7))
* **security:** encrypt secrets at rest, add step-up auth and session pepper ([961adec](https://github.com/AppsGanin/rospanel/commit/961adec55c8fa88fa24b91b02e4d97b80a2eaebc))
* **security:** SSRF-safe outbound HTTP for proxy lists and routing templates ([ddc9456](https://github.com/AppsGanin/rospanel/commit/ddc9456cfa6343bc27d70b7658f7d975f5e7e93a))
* **telegram:** one-time per-user bind codes instead of sub-token links ([410cf7c](https://github.com/AppsGanin/rospanel/commit/410cf7c0b5773651ef0dbc68452a063eddbcef67))


### Bug Fixes

* **ui:** minor card layout and Telegram settings copy tweaks ([598a00f](https://github.com/AppsGanin/rospanel/commit/598a00f484be06a1fb131a633984873ad8af9931))

## [0.3.0](https://github.com/AppsGanin/rospanel/compare/v0.2.0...v0.3.0) (2026-06-24)


### Features

* device limit, sub-token rotation, name-in-title ([5288eee](https://github.com/AppsGanin/rospanel/commit/5288eeea00080bdfcc4dbf682b75bf6ea2ebc977))
* device limit, sub-token rotation, name-in-title (+ slog deadlock fix) ([fc99fd8](https://github.com/AppsGanin/rospanel/commit/fc99fd8134811f2d2f633fa873eb76d72ea533ed))
* **logging:** migrate core logs from log to slog with structured attributes ([b9b15e6](https://github.com/AppsGanin/rospanel/commit/b9b15e6c0483d8c7432122262cb2fb07bb8fbab4))
* **telegram:** add public user bot (self-registration + self-service) ([32d31c2](https://github.com/AppsGanin/rospanel/commit/32d31c2f7f102e0e2545eaf1748abc2de0571f7e))
* **telegram:** public user bot — bring to main (missed by stacked merge) ([39fd53a](https://github.com/AppsGanin/rospanel/commit/39fd53a0fc54a2112c55b6b3c16f0087a28f9b66))
* **web:** replace center loader with skeleton placeholders for all panels ([43546db](https://github.com/AppsGanin/rospanel/commit/43546db95a11339849d75e035382f2d989a11372))


### Bug Fixes

* **logging:** stop slog→log recursion deadlock on startup ([eae6071](https://github.com/AppsGanin/rospanel/commit/eae60710a17b5adf1f8618ca88d22c33d91b5718))
* **logging:** stop slog→log recursion deadlock on startup ([004979c](https://github.com/AppsGanin/rospanel/commit/004979cfe35be7e47d413ca29678353fcc0be3ef))
* **web:** correct device limit label in statusInfo ([531698f](https://github.com/AppsGanin/rospanel/commit/531698f0a05e8aa3c51f1bfdc8166fbae2d10350))

## [0.2.0](https://github.com/AppsGanin/rospanel/compare/v0.1.1...v0.2.0) (2026-06-22)


### Features

* **telegram:** add Telegram admin bot for user management and backups ([b1a5afa](https://github.com/AppsGanin/rospanel/commit/b1a5afaa7e7a433bc799c0775474a0fdd3b830b3))

## [0.1.1](https://github.com/AppsGanin/rospanel/compare/v0.1.0...v0.1.1) (2026-06-21)


### Bug Fixes

* **install:** make the curl | sudo bash one-liner robust (pipe form, /dev/tty prompts, cursor-based FIRST-RUN capture) ([464cb91](https://github.com/AppsGanin/rospanel/commit/464cb912139a785f46b6ada0c75c4acce3eb21bc))
* **install:** widen FIRST-RUN credential wait to ~30s and scope to this install ([c2005b4](https://github.com/AppsGanin/rospanel/commit/c2005b4d062456a4b3f50df5e0fa59c368039850))
* **wizard:** reflect real TLS state in address step (domain/IP, self-signed vs issued), settings-style validation ([497bf4e](https://github.com/AppsGanin/rospanel/commit/497bf4ef528263c5b9f9bef7363c32981196dc30))

## 0.1.0 (2026-06-21)


### Miscellaneous Chores

* release 0.1.0 ([587ed48](https://github.com/AppsGanin/rospanel/commit/587ed4824f1749f7f3cedaece1980486495092fc))
