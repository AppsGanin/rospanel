# Changelog

## [3.9.0](https://github.com/AppsGanin/rospanel/compare/v3.8.1...v3.9.0) (2026-09-22)


### Features

* **extsub:** relay an external subscription through one of our servers ([4e3f2c1](https://github.com/AppsGanin/rospanel/commit/4e3f2c1dfdbc0e66a2f995b9a96c4c1197784080))

## [3.8.1](https://github.com/AppsGanin/rospanel/compare/v3.8.0...v3.8.1) (2026-09-22)


### Bug Fixes

* **sub:** external XHTTP servers reach mihomo, and an HTTPUpgrade one no longer breaks the profile ([e122057](https://github.com/AppsGanin/rospanel/commit/e1220575bc0847f528100ffefdde6b058a99659e))

## [3.8.0](https://github.com/AppsGanin/rospanel/compare/v3.7.0...v3.8.0) (2026-09-21)


### Features

* a cut-off user loses the connections already open, and quotas are checked every 10 s ([dd61757](https://github.com/AppsGanin/rospanel/commit/dd6175731d5d8750b6089f5893f31f9d71252e61))


### Performance Improvements

* **store:** bounded lookups read from a pool beside the writer ([0c6c1fd](https://github.com/AppsGanin/rospanel/commit/0c6c1fd94bffb0aec47c94eb3be21a4d26ae3dcb))

## [3.7.0](https://github.com/AppsGanin/rospanel/compare/v3.6.0...v3.7.0) (2026-09-20)


### Features

* **billing:** add manual payment as a first-class method ([095be9a](https://github.com/AppsGanin/rospanel/commit/095be9a40db0531aef8d75990e598fa2e2bb7ea0))
* **user-detail:** clarify device list labels and tooltips ([38cb9a2](https://github.com/AppsGanin/rospanel/commit/38cb9a21ec0fa52d32ab009690120fec27eca7ec))


### Bug Fixes

* **i18n:** remove country note from placement hint ([8c5a575](https://github.com/AppsGanin/rospanel/commit/8c5a575055116ca9cc3ba0604a3c86c1e82ed873))

## [3.6.0](https://github.com/AppsGanin/rospanel/compare/v3.5.0...v3.6.0) (2026-09-17)


### Features

* **panel:** a name that lands in a config file offers only {server} and {user} ([9465617](https://github.com/AppsGanin/rospanel/commit/9465617731ce15528e41bbf8ca52959261213c15))
* **sub:** every app on the subscription page links to its own page ([8b92ede](https://github.com/AppsGanin/rospanel/commit/8b92ede96ae5436500aec9c322efc11b355a2c8e))


### Bug Fixes

* **turn:** stopping a relay closes its legs instead of deadlining them ([3c9dd4c](https://github.com/AppsGanin/rospanel/commit/3c9dd4cf022215a5240111ec5aa23b4a9e226678))

## [3.5.0](https://github.com/AppsGanin/rospanel/compare/v3.4.0...v3.5.0) (2026-09-17)


### Features

* **inbounds:** WireGuard over calls (TURN), experimental ([43dadda](https://github.com/AppsGanin/rospanel/commit/43dadda12bb28dd9a45f8fbe74cf6b6259f954cb))
* **security:** ban an address from a user's addresses, and one list of every ban ([6997aa7](https://github.com/AppsGanin/rospanel/commit/6997aa77054e8ed561280431c7533307f2b27fc5))
* **security:** the panel says when a server cannot drop addresses, and the installer brings nftables ([9b019e4](https://github.com/AppsGanin/rospanel/commit/9b019e4054068e1771795a051685b57dfb5d8819))


### Bug Fixes

* **deps:** pion/dtls v3.1.9, past the panic on a crafted ServerKeyExchange ([7ed4b82](https://github.com/AppsGanin/rospanel/commit/7ed4b829d40626cd78bfdf5afbf4f168b1a2a711))
* **dns:** refuse servers Xray cannot use, and ask host-named resolvers last ([390cbc9](https://github.com/AppsGanin/rospanel/commit/390cbc9ad2ee6ba10491d079c2f650b7deec5dc2))
* **subscriptions:** connection names drop {flag} and {country} ([a7e1659](https://github.com/AppsGanin/rospanel/commit/a7e16590a11f850a4e2a201f7773337bfaa056f2))
* **ui:** a user's addresses show even when device binding is on ([1a3521b](https://github.com/AppsGanin/rospanel/commit/1a3521b45dcca8949806fbea2634e6db4d629ae8))
* **xray:** the API client refuses a message too big for a gRPC frame ([8535b5e](https://github.com/AppsGanin/rospanel/commit/8535b5ee0725075706bf09784883ec079550337f))

## [3.4.0](https://github.com/AppsGanin/rospanel/compare/v3.3.1...v3.4.0) (2026-09-17)


### Features

* **ui:** the whole panel reads dates and times in the panel's timezone ([9d60c7d](https://github.com/AppsGanin/rospanel/commit/9d60c7dcb791775761785e4908072db2f4878afb))


### Bug Fixes

* **nodeagent:** bound the sites budget by the body's length before subtracting ([1faa745](https://github.com/AppsGanin/rospanel/commit/1faa745aa824e4ddc0f9f202c7da25c90632beda))
* **nodes:** a node's health reads a state held in parts as its sync would ([db498f0](https://github.com/AppsGanin/rospanel/commit/db498f0acd63f210a9ebf582960f9f4ba155e92b))
* **nodes:** a node's report is believed only about the users its config lets in ([4805e6b](https://github.com/AppsGanin/rospanel/commit/4805e6b5d644534d9601948fd01f3abb6a9d5146))
* **nodes:** the users a node is believed about come from the newest read ([d547bbc](https://github.com/AppsGanin/rospanel/commit/d547bbcf25ae3b4777b909eb097157558d0403f3))
* **shaper:** a nil applier shapes nothing instead of panicking ([7feb4bb](https://github.com/AppsGanin/rospanel/commit/7feb4bb94a3ba51854b008fa32308779aa9326e6))
* **ui:** a settings hint with nothing beside it spans its row ([52a2baf](https://github.com/AppsGanin/rospanel/commit/52a2baf4466764c72a1d270f95fd769c19d372a8))
* **ui:** the API tab's save bar sits at the bottom, below the webhooks ([5c8fcef](https://github.com/AppsGanin/rospanel/commit/5c8fcef9158c8b48fce820a3992d18faaf58b2ce))
* **xray:** adding or removing a Hysteria2 user no longer drops everyone on the lane ([5a8a704](https://github.com/AppsGanin/rospanel/commit/5a8a704043abb062f5a05a3ca03ec31c521dc087))


### Performance Improvements

* **nodes:** a node is sent the users that changed, not its whole config again ([718644e](https://github.com/AppsGanin/rospanel/commit/718644ef0c6b2482d2ca8fdec3f9d5796f315aa8))

## [3.3.1](https://github.com/AppsGanin/rospanel/compare/v3.3.0...v3.3.1) (2026-09-16)


### Bug Fixes

* **branding:** labels on the accent turn dark when the accent is too light for white ([d42af09](https://github.com/AppsGanin/rospanel/commit/d42af09b7abb0c1c82f72f10173d925e533051fc))
* **journal:** the actor for a user's own action reads "user" ([6cde852](https://github.com/AppsGanin/rospanel/commit/6cde852b34988228299224d689f17525bc56a5d7))
* **memory:** the panel and the node agent keep their Go heap under a soft limit ([3a00604](https://github.com/AppsGanin/rospanel/commit/3a00604699f24478ead1da876f13f66ae708fb0e))
* **memory:** the panel's heap limit leaves room for the Xray it runs ([9b08124](https://github.com/AppsGanin/rospanel/commit/9b0812444e4b85d45b30fe67ad0c38e511caf22b))
* **nodes:** connection samples are no longer dropped past 8,192 pairs ([b7da819](https://github.com/AppsGanin/rospanel/commit/b7da819853c3769d11fad772c2a2a0ce759e17a1))
* **nodes:** destination rows fit what the panel takes, and users take turns ([facaea9](https://github.com/AppsGanin/rospanel/commit/facaea90cc57c436dec91fcf88cfa9d665949468))
* **sub:** sing-box 1.13 and 1.14 load the generated profile again ([c13aea8](https://github.com/AppsGanin/rospanel/commit/c13aea80a34fe4291b588cde71c61f1612abbc69)), closes [#89](https://github.com/AppsGanin/rospanel/issues/89)
* **users:** the list's loading placeholder has the page's shape ([8955df1](https://github.com/AppsGanin/rospanel/commit/8955df1f956fd548d623ec68888f8d326ff229de))
* **xray:** a live change whose api call cannot reach xray is asked again before the full reload ([3c1b486](https://github.com/AppsGanin/rospanel/commit/3c1b486c725a0d3d19d54078cb5bb54f72a2b1f9))


### Performance Improvements

* **connections:** a connected device is written every 45 seconds, and device limits are checked every 30 ([cf7310a](https://github.com/AppsGanin/rospanel/commit/cf7310a78558384c1edb55d7c138669c994d2a74))
* **connections:** connection sightings are written with prepared statements ([d70666c](https://github.com/AppsGanin/rospanel/commit/d70666c586dbeed2c87c4d8b5d61cfc88e994aeb))
* **dashboard:** the status feed recounts users every 15 seconds, not every 2 ([c1ce800](https://github.com/AppsGanin/rospanel/commit/c1ce800996436759e2785b84cb061468dbac08e5))
* **nodes:** a node that is current is told so without its state being rebuilt ([5e0c5b3](https://github.com/AppsGanin/rospanel/commit/5e0c5b3304577ce26c277e2cb30964d647633cf7))
* **nodes:** a node's certificate status does not count against its remembered state ([9cb5bf6](https://github.com/AppsGanin/rospanel/commit/9cb5bf6be813e0983fb6aa9d8ceaad38f5be99de))
* **nodes:** a node's config is built alone, and hashed without a copy of itself ([43f0025](https://github.com/AppsGanin/rospanel/commit/43f00256b14d873b13fe8767ad9cdaeadca69221))
* **nodes:** a node's state is held under the gate until it is encoded, and written out after ([2f7bb87](https://github.com/AppsGanin/rospanel/commit/2f7bb87d06755d8f72a9c295396db717172c8bd0))
* **nodes:** config pushes go out gzip-compressed ([19c3aaf](https://github.com/AppsGanin/rospanel/commit/19c3aaf1059659f57fffb9cc6a32907e60ac1154))
* **nodes:** one read of the working set answers both reconcile questions ([1c3a498](https://github.com/AppsGanin/rospanel/commit/1c3a498768a2ba7dd33e2137ac911c0239875464))
* **nodes:** the nodes are woken only for a change they can see, and the shared read keeps the credentials it has ([eec904c](https://github.com/AppsGanin/rospanel/commit/eec904cf53253452a2ab9c22b90ce614a1c0afac))
* **settings:** the settings are decoded once and read again only when they change ([c22a16c](https://github.com/AppsGanin/rospanel/commit/c22a16c3c821ef8a9e152b58baa266f2de8478d0))
* **stats:** the stats poll and the enforcement pass stop reading every user whole ([0422c11](https://github.com/AppsGanin/rospanel/commit/0422c11f26d6581c2fb8894bbef30331f7583636))
* **sub:** the certificate file is read when it changes, not on every fetch ([3633860](https://github.com/AppsGanin/rospanel/commit/3633860b33befe34887fc60e5b24787dff97a09c))
* **sub:** the per-server online count answers for two seconds ([ece3e0d](https://github.com/AppsGanin/rospanel/commit/ece3e0d7a49357b547ef4316a893ecf5fc4280a8))
* **sub:** what every subscription shares is read once for a few seconds ([2d391f8](https://github.com/AppsGanin/rospanel/commit/2d391f821d26ed093b35c9cc465c4c94a7fee459))
* **traffic:** a traffic batch is written in two statements, not three per user ([35982af](https://github.com/AppsGanin/rospanel/commit/35982af8f51fe0df28146a1d84d9c5ec5153c6a7))
* **users:** an edit to one user rereads that row of the list, and each order is sorted once ([14040f9](https://github.com/AppsGanin/rospanel/commit/14040f977fc07f4d072d5bb81590258c354446a1))
* **users:** subscription and Telegram lookups use their indexes ([6ca60c6](https://github.com/AppsGanin/rospanel/commit/6ca60c6d5eca50b1840bf18825cab149ce9b0844))
* **users:** the users list is read once for a burst of requests, and afresh after any change ([06f2ff0](https://github.com/AppsGanin/rospanel/commit/06f2ff074fac6c9f2a0d04ecc7f2dd5ebf0b0214))
* **users:** the users page reads summaries, not whole users ([fa20987](https://github.com/AppsGanin/rospanel/commit/fa2098746ac044ca03be69b58b30747ea176100f))
* **users:** the users page works out its counts, tags and search text once per read ([b2f6b3b](https://github.com/AppsGanin/rospanel/commit/b2f6b3b2b0e663b8cdac0305aca571fb1db9e724))

## [3.3.0](https://github.com/AppsGanin/rospanel/compare/v3.2.0...v3.3.0) (2026-09-14)


### Features

* **api:** the external API reaches the new subscription, security and server settings ([8f7dd60](https://github.com/AppsGanin/rospanel/commit/8f7dd60c86cf3436548005d182260ec923f32471))
* **backup:** a restore asks for the backup's own second factor ([90ed9d2](https://github.com/AppsGanin/rospanel/commit/90ed9d232e8807df247eeb7364ae52f8c52b8b95))
* **groups:** a group saved with no connection ticked leaves access open ([52ae84b](https://github.com/AppsGanin/rospanel/commit/52ae84bb15d2ea213adc7f1398891eb587b8fd41))
* **groups:** an access group can cap its members' speed ([0dcf5c1](https://github.com/AppsGanin/rospanel/commit/0dcf5c1894056416ac1e7b66f864978777dc6aae))
* **security:** trusted addresses are never banned automatically ([4bba9bb](https://github.com/AppsGanin/rospanel/commit/4bba9bb61183ca1832028cfedb4e297a81052586))
* **servers:** a monthly traffic limit starts on the hosting's billing day ([23078ad](https://github.com/AppsGanin/rospanel/commit/23078ad3ec500877e49bab03ef06e915663e7c00))
* **subscriptions:** a random server order spreads clients across the fleet ([ca0df33](https://github.com/AppsGanin/rospanel/commit/ca0df33f3f1a3c2b3bae2e110c2fac22fe865f96))
* **subscriptions:** Happ adds the subscription through an encrypted link ([23a71c8](https://github.com/AppsGanin/rospanel/commit/23a71c87a216bca2c81f38df6b00d179ceeadd8c))
* **users:** a term can start on the user's first connection ([1f4fd03](https://github.com/AppsGanin/rospanel/commit/1f4fd03d5e03610e058f2db4300a05c6669dd79a))


### Bug Fixes

* **awg:** tunnel addresses no longer run out with the user ids ([eb3331f](https://github.com/AppsGanin/rospanel/commit/eb3331f46471462afd65c8e8215f27252302cbd4))
* **backup:** restoring a backup asks for the second factor ([eb21357](https://github.com/AppsGanin/rospanel/commit/eb213576e15c1bb1df8bde46d4ff3f69c5f2a6e0))
* **core:** the brute-force guard's cleanup stops with the manager ([836ca75](https://github.com/AppsGanin/rospanel/commit/836ca75333b9121851c2df7e15115a686bb46b63))
* **groups:** the speed limit field spans the editor's full width ([8e60568](https://github.com/AppsGanin/rospanel/commit/8e605689ff57cc8e4881370693d95795ecdfdbc9))
* **nodes:** a busy node's traffic report no longer stalls its sync for good ([ca7792a](https://github.com/AppsGanin/rospanel/commit/ca7792a58d873d2545dd11d0a5fc5226f4e8013d))
* **overview:** the dashboard counts blocklist matches over the real window ([9ccddc1](https://github.com/AppsGanin/rospanel/commit/9ccddc163d0a28b74423446ea1cd95f8f67ada4e))
* **ratelimit:** a flood can no longer reset a throttled address ([414961e](https://github.com/AppsGanin/rospanel/commit/414961ea35358a3de1206ca43faa99560a4bc1b6))
* **server:** the API and node segments are compared in constant time ([4818a7a](https://github.com/AppsGanin/rospanel/commit/4818a7a627337e279eab9e2122297ff37719a07b))
* **users:** adding a user granted a Shadowsocks inbound no longer restarts Xray ([bb16a4b](https://github.com/AppsGanin/rospanel/commit/bb16a4b43390df50828ebe43ec59e56256f43d13))
* **users:** bulk reset and import both anchor the quota cycle ([856c774](https://github.com/AppsGanin/rospanel/commit/856c7744c2beff0c5a96cfb664ee17696dc74394))


### Performance Improvements

* **access:** the access flush and throttle stop scaling with every user and pair ([bedba4f](https://github.com/AppsGanin/rospanel/commit/bedba4f38cfcda503aafb0fd25f58e9fdd9621f2))
* **awg:** a tunnel's peer list stops re-deriving every user's public key ([d2f0ac8](https://github.com/AppsGanin/rospanel/commit/d2f0ac861687475424bbede9a7aa809398f29b68))
* **nodes:** a node changes users without restarting Xray ([a1215bf](https://github.com/AppsGanin/rospanel/commit/a1215bf480e2d1d1cfe7596e62a459f62d19aa3f))
* **nodes:** config builds and traffic passes read only what they use ([67ef8a8](https://github.com/AppsGanin/rospanel/commit/67ef8a8ff901206f207650aecb7bb931138f6da3))
* **nodes:** nodes share one read of the fleet's users, and one enforcement pass ([35c5c46](https://github.com/AppsGanin/rospanel/commit/35c5c4627a66e75d7f43c26503a1f74aad2dded2))
* **secrets:** encrypted fields open without rebuilding the cipher each time ([afb0e0f](https://github.com/AppsGanin/rospanel/commit/afb0e0fe50ea5cf91fad10b705d0ce9bc0967a3c))
* **users:** reading one user counts only that user's devices ([3364abf](https://github.com/AppsGanin/rospanel/commit/3364abf02499c7469a8f3f3ce26b50a393eb04b9))
* **users:** the users page loads one window of rows instead of every user ([ebce6e4](https://github.com/AppsGanin/rospanel/commit/ebce6e4ab1b93113aae6f6747a85232c97612186))

## [3.2.0](https://github.com/AppsGanin/rospanel/compare/v3.1.0...v3.2.0) (2026-09-12)


### Features

* **routing:** never fall back to direct when a lane is down ([55be958](https://github.com/AppsGanin/rospanel/commit/55be9580c6b4abd8d0a1547db8f78efb88fe4b1e)), closes [#85](https://github.com/AppsGanin/rospanel/issues/85)


### Bug Fixes

* **proxy:** a list host that fails a refresh no longer empties the lane ([8b7eab4](https://github.com/AppsGanin/rospanel/commit/8b7eab41d0cc215bd1b86b15550cfc97f8e3cbd3))

## [3.1.0](https://github.com/AppsGanin/rospanel/compare/v3.0.0...v3.1.0) (2026-09-10)


### Features

* **awg:** AmneziaWG 3.1 ([59d9a9f](https://github.com/AppsGanin/rospanel/commit/59d9a9ff128c0698718f8a23e70a4ca9020a3458))
* **awg:** imitate a real protocol ahead of the handshake ([b14c81d](https://github.com/AppsGanin/rospanel/commit/b14c81d5d4867428de497b3712b078d25f2d099a))


### Bug Fixes

* **awg:** the panel says which protocol a server imitates ([f957d81](https://github.com/AppsGanin/rospanel/commit/f957d810e83bcc298d61c92ab4a17d887a2fece2))
* **conn:** the obfuscation summary shows the whole 3.1 parameter set ([4a31102](https://github.com/AppsGanin/rospanel/commit/4a31102c1d99973601e346c7916b2f5ca515ce86))
* **i18n:** shorten the AmneziaWG settings hint ([d1184f6](https://github.com/AppsGanin/rospanel/commit/d1184f6880cedd66ae82494cc564d0d83cb431fd))
* **overview:** recent events carry a date, not just a clock ([5e69b62](https://github.com/AppsGanin/rospanel/commit/5e69b62520ec1d551a0b36aae8fb8ead41692698))

## [3.0.0](https://github.com/AppsGanin/rospanel/compare/v2.14.2...v3.0.0) (2026-09-09)


### ⚠ BREAKING CHANGES

* the connection policy no longer refuses networks by ASN. The "forbidden networks" list is gone from the settings page and from the model, so an operator who had one configured is no longer enforcing it — the country rule is what remains. The stored value is ignored on load and dropped on the next save; blocks already recorded under the ASN reason keep their label, so the history still reads correctly.

### Features

* **abuse:** keep blocklist matches for 30 days ([4fd715b](https://github.com/AppsGanin/rospanel/commit/4fd715ba98775bd54a4aaf09a2b1d42c4644428c))
* **core:** reduce long-poll hold interval to 20s ([bf128bf](https://github.com/AppsGanin/rospanel/commit/bf128bf2a15219fc9e619562477c6870ef4c41a0))
* the console redesign ([e30ea13](https://github.com/AppsGanin/rospanel/commit/e30ea1384ec6fb142b85f2942236eb9ea75b19d4))


### Bug Fixes

* **audit:** skip journal rows on no-op user edits ([1e640b6](https://github.com/AppsGanin/rospanel/commit/1e640b6805a342d7bb2e840834b093390fbce4a9))
* **i18n:** restore the health hint for a stale panel ([561480e](https://github.com/AppsGanin/rospanel/commit/561480e7593fcfd3cb499f05e2eee84851d1c178))
* **stats:** say which window each report on the page covers ([c982c69](https://github.com/AppsGanin/rospanel/commit/c982c69c3d17cd4b84295328355d67f125d52aea))
* **ui:** indicate unsaved egress toggle state in status badges ([3e46622](https://github.com/AppsGanin/rospanel/commit/3e466224f1e1f7193bcb02d25db8f588d136462e))
* **users:** a traffic reset restarts the quota cycle ([5b30a16](https://github.com/AppsGanin/rospanel/commit/5b30a161f487b5651e28961686f4d43ea17decb4))

## [2.14.2](https://github.com/AppsGanin/rospanel/compare/v2.14.1...v2.14.2) (2026-09-09)


### Bug Fixes

* **warp:** use IP endpoints and multi-endpoint pool for WARP lane ([3cfaf19](https://github.com/AppsGanin/rospanel/commit/3cfaf192ef52cc88802f560e351004c952833ec2))

## [2.14.1](https://github.com/AppsGanin/rospanel/compare/v2.14.0...v2.14.1) (2026-09-08)


### Bug Fixes

* **alerts:** a panel restart is not an AmneziaWG outage ([451778b](https://github.com/AppsGanin/rospanel/commit/451778b816345f3689c52c8477039f702380c647))
* **http:** a held HTTP/2 response outlives ReadHeaderTimeout ([2000a0e](https://github.com/AppsGanin/rospanel/commit/2000a0e2bc2651916411b82c75a9fd1565214446))
* **nodeagent:** a held poll clears the node's sync-failure window ([6c6e30e](https://github.com/AppsGanin/rospanel/commit/6c6e30eb52d5185d8c7128af65d5ee9439e4692a))
* **sub:** the lane name comes back typed, not read out of the config map ([1c12158](https://github.com/AppsGanin/rospanel/commit/1c12158a18e3eaa1bc99b68845760bef9d5fee9c))

## [2.14.0](https://github.com/AppsGanin/rospanel/compare/v2.13.0...v2.14.0) (2026-09-06)


### Features

* **external:** present a device when reading somebody else's subscription ([cab6a0f](https://github.com/AppsGanin/rospanel/commit/cab6a0f9cfa07f728df880de828244ab1025a450))
* **hysteria2:** the obfuscation key is generated, never typed ([9c2cb2e](https://github.com/AppsGanin/rospanel/commit/9c2cb2e0b333dda52169fb45729a1edc5557ae64))
* **nodes:** the AmneziaWG tunnel is watched on nodes, not just on the master ([4bbf73f](https://github.com/AppsGanin/rospanel/commit/4bbf73fe56903d83bbaf9cc191977c36b184e705))


### Bug Fixes

* **core:** the manager's background loops stop when it does ([3d3d20a](https://github.com/AppsGanin/rospanel/commit/3d3d20aa9caf7750a49b5dfa05de1e1adef6f682))
* **health:** the AmneziaWG row sits under Xray config, not at the end ([ed6fd75](https://github.com/AppsGanin/rospanel/commit/ed6fd751d8d4cc46887c0d8e60ae8d9adeb78e36))
* **health:** the master's own AmneziaWG tunnel is in its diagnostics too ([a02b4b2](https://github.com/AppsGanin/rospanel/commit/a02b4b2a405bb13042c75be0829736c2bc5dd859))
* **web:** five defects the fork's frontend audit pointed at, done our way ([74e3035](https://github.com/AppsGanin/rospanel/commit/74e30354ab916e39db1fe60d640449937805af63))

## [2.13.0](https://github.com/AppsGanin/rospanel/compare/v2.12.1...v2.13.0) (2026-09-05)


### Features

* **billing:** a tariff carries its own traffic refill cycle ([4d4d47b](https://github.com/AppsGanin/rospanel/commit/4d4d47b4b2425cf410fc943f08a444221ee3d059))
* **hysteria2:** Salamander obfuscation for the lane and for custom inbounds ([2a86b00](https://github.com/AppsGanin/rospanel/commit/2a86b005b293d552ca18b83c1cb84cba25104bb9))
* **security:** a fresh second factor for the two actions with no undo ([feff7d5](https://github.com/AppsGanin/rospanel/commit/feff7d58a6a82e05677cc83b36095e90532e6f2f))
* **servers:** a traffic limit per server, with an alert and optional hiding ([45c70b8](https://github.com/AppsGanin/rospanel/commit/45c70b86c46ce49f8010a0d37f787809674e0872))
* **subscriptions:** operator-editable profile templates for mihomo, sing-box and Xray JSON ([ab1afd3](https://github.com/AppsGanin/rospanel/commit/ab1afd36b44493bf6b7ac36643fb9c1a9aac9ebe))
* **subscriptions:** variables in connection names ([2653cb4](https://github.com/AppsGanin/rospanel/commit/2653cb47a3e3700bb48cae067e301ab3759e2d13))


### Bug Fixes

* **admins:** the owner's password no longer has to be ASCII to remove an account ([a5fed37](https://github.com/AppsGanin/rospanel/commit/a5fed374c1d21889b6544f319b755934249326e5))
* external servers no longer vanish with a hidden master ([d81120b](https://github.com/AppsGanin/rospanel/commit/d81120be2475c2119c6f28fd5d7603d96ba1b59b))
* **security:** bound what a subscription template can expand into, and close the setup-time step-up gap ([f88615e](https://github.com/AppsGanin/rospanel/commit/f88615ef3ab7750fd8d4098e0f6a09dbab1be920))
* **security:** four more from a second review pass ([0c15853](https://github.com/AppsGanin/rospanel/commit/0c15853258ea241b92e88e2a4fb10b9478d36342))
* **servers:** the traffic alert stopped repeating itself, and stopped lying ([54547ba](https://github.com/AppsGanin/rospanel/commit/54547bad5b50396e83b27b93c88e7b4665f5056f))
* **subscriptions:** name variables reach the share links, and rendered names cannot collide ([21c9658](https://github.com/AppsGanin/rospanel/commit/21c9658bd0e611ef9becfb839c3762a041a6cbcb))
* the subscription page shows external servers, and addresses the panel ([e5572f1](https://github.com/AppsGanin/rospanel/commit/e5572f1aee71c00141c026acd45b8e6b2d4ce883))

## [2.12.1](https://github.com/AppsGanin/rospanel/compare/v2.12.0...v2.12.1) (2026-09-03)


### Bug Fixes

* **deps:** x/crypto 0.56.0 for the two ssh channel DoS advisories ([4a87891](https://github.com/AppsGanin/rospanel/commit/4a87891d72811d0a5c6ab7bfba2a8a7e43c3baa1))
* modal/drawer panels no longer scroll themselves off-screen ([464e30b](https://github.com/AppsGanin/rospanel/commit/464e30b2b2121f8c27f7ad96437ceea656aa9d1e)), closes [#75](https://github.com/AppsGanin/rospanel/issues/75)

## [2.12.0](https://github.com/AppsGanin/rospanel/compare/v2.11.0...v2.12.0) (2026-09-02)


### Features

* external subscriptions, lanes through foreign servers, changelog, factory reset ([b37843e](https://github.com/AppsGanin/rospanel/commit/b37843e9c108b56b88e9763ec63a69e3726c786f))
* harden the subscription actions, the stops, the bulk paths and the sweeps ([4147468](https://github.com/AppsGanin/rospanel/commit/4147468ffc09b62144430c759a4fc321aecde666))


### Bug Fixes

* name every journal event from the dictionary, with details for devices, policy and abuse ([3da54dc](https://github.com/AppsGanin/rospanel/commit/3da54dc296f930b977a1329faaf587ef915d7cc5))
* put the factory-reset button under its text ([9d047e6](https://github.com/AppsGanin/rospanel/commit/9d047e6ee156c378b6d259cb270f725d5db0cb34))
* stop offering AsIs twice in the direct DNS strategy ([de69159](https://github.com/AppsGanin/rospanel/commit/de691593c83a6a5451a1685060284a64519920a4))

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
