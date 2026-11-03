# Changelog

## [6.0.0](https://github.com/AppsGanin/rospanel/compare/v5.13.0...v6.0.0) (2026-11-03)


### ⚠ BREAKING CHANGES

* **roles:** the MCP address …/v1/mcp/<key>/write is gone — one address per key, its tools decided by the key's role; the plain address of a key issued before now offers that key's write tools too, so give an assistant that should only look a key with a read-only role. The administrator role no longer reaches the Telegram bots, backups, restore or the factory reset, which are the owner's. Creating and revoking a full-access API key is the owner's.

### Features

* a cut-off user loses the connections already open, and quotas are checked every 10 s ([4ede3c4](https://github.com/AppsGanin/rospanel/commit/4ede3c45393036b12422afed40dfe0f4b1464fc6))
* **abuse:** keep blocklist matches for 30 days ([6f9bba6](https://github.com/AppsGanin/rospanel/commit/6f9bba63630ca5a4ee4cf69c9b31f7d680c5d28c))
* **alerts:** rules on a server's figures, told to the admin chats and webhooks ([df3ac65](https://github.com/AppsGanin/rospanel/commit/df3ac6541e47daf9ced4030c6c7c3ea45a6e0eca))
* AmneziaWG as a built-in lane ([ce9f875](https://github.com/AppsGanin/rospanel/commit/ce9f8757a50fd4b160e9760870db70bd92b87a9d))
* answer port 80 instead of leaving it closed ([2bc7aba](https://github.com/AppsGanin/rospanel/commit/2bc7abadc69b88d14635bec88f35a4211cc7944d))
* answer port 80 on nodes too ([3349e6f](https://github.com/AppsGanin/rospanel/commit/3349e6fd58b4d9e5531b2af8c72ee53506529caf))
* AnyTLS lane, VLESS Encryption/CDN/pinned cert, UUID reset, per-client protocol rules ([5353790](https://github.com/AppsGanin/rospanel/commit/53537908009da2b699d1098595f067009e2f2322))
* **api:** an API key holds its own permissions instead of a role ([01f833b](https://github.com/AppsGanin/rospanel/commit/01f833bf8f43a7f1fd82e7bf42ba60f2d7b3051e))
* **api:** methods only for keys; retire the API-only permissions ([496a3c3](https://github.com/AppsGanin/rospanel/commit/496a3c365d6e51248025c0b1a49df5d458c10322))
* **api:** publish the configuration surface over /v1 (and therefore MCP) ([d120e51](https://github.com/AppsGanin/rospanel/commit/d120e51876c5b1200062ea153654def31a0251fe))
* **api:** the external API reaches the new subscription, security and server settings ([c8993bd](https://github.com/AppsGanin/rospanel/commit/c8993bdf7a17fc9b9014ea332918d6e8cd589ce9))
* **api:** tick exact API methods for a key ([5db2b1d](https://github.com/AppsGanin/rospanel/commit/5db2b1de6d440d2cbd0d3f9d9f748878efa7aa61))
* **api:** website sign-up under the self-registration rules ([c4075e5](https://github.com/AppsGanin/rospanel/commit/c4075e5b0c767efd465deaf98f9ab72cfd5c7638))
* **api:** what your own bot or user cabinet needs, and a selling permission ([41e7b50](https://github.com/AppsGanin/rospanel/commit/41e7b50e1a1a14aed02863295c54755026cf2f20))
* **audit:** search, date-range filter and CSV export for the panel journal ([0d538c7](https://github.com/AppsGanin/rospanel/commit/0d538c72f505bdc0fb697293d04f0895896d8fda))
* **awg:** AmneziaWG 3.1 ([dd0b2cc](https://github.com/AppsGanin/rospanel/commit/dd0b2ccbcd3018918a89b0b85d942d176abe6dea))
* **awg:** imitate a real protocol ahead of the handshake ([964bfda](https://github.com/AppsGanin/rospanel/commit/964bfdaa239e6e9b80241bc689734449d16052bd))
* **backup:** a restore asks for the backup's own second factor ([abeb1f6](https://github.com/AppsGanin/rospanel/commit/abeb1f6bfccc9566344364d2917e85fddff5a2ca))
* **billing:** a journal of what the payment systems sent ([e6cdfa5](https://github.com/AppsGanin/rospanel/commit/e6cdfa5e6bef687aa0195d375b103862c2030e72))
* **billing:** a picture for manual payment, shown with the instructions ([ce34479](https://github.com/AppsGanin/rospanel/commit/ce344790ac3771b69c519972b9b51a2bc037a2e4))
* **billing:** a tariff carries its own traffic refill cycle ([11de52f](https://github.com/AppsGanin/rospanel/commit/11de52fdec2f74dc852037e86a6d035de23b55ea))
* **billing:** add manual payment as a first-class method ([ab0ece4](https://github.com/AppsGanin/rospanel/commit/ab0ece4cace4f7da96b9b4823bacb4a15a98c7a8))
* **billing:** balance, promo codes and a referral programme ([1d671e6](https://github.com/AppsGanin/rospanel/commit/1d671e6a818de498625c755a0cb008e5ebf5526b))
* **billing:** chargebacks, win-back codes and a sales funnel ([5f15fb3](https://github.com/AppsGanin/rospanel/commit/5f15fb31530cc336aae5f7c97ecb8aa759bf6043))
* **billing:** exit survey for users who did not renew ([f469562](https://github.com/AppsGanin/rospanel/commit/f469562ab2ce4d3e43c1272d5a8e73532a5ff808))
* **billing:** flexible plans — extra devices, traffic packs, plan changes ([5e69e6d](https://github.com/AppsGanin/rospanel/commit/5e69e6d21e0f0c3bad58f277bd3f0bcadd2a8887))
* **billing:** operator-set largest top-up ([fa8a2d3](https://github.com/AppsGanin/rospanel/commit/fa8a2d3badd38c18d23f671144363fdbe60a90d2)), closes [#122](https://github.com/AppsGanin/rospanel/issues/122)
* **billing:** source tags on bot links and a funnel per source ([5040f28](https://github.com/AppsGanin/rospanel/commit/5040f28ce982c4cb0dbadeb815e1b01a4adf47d6))
* **billing:** suspicious activity on the payments page ([6331175](https://github.com/AppsGanin/rospanel/commit/633117578f7c0bbead43242ae0b1013055abc749))
* **cli:** add a rescue command to regain locked-out access ([005651a](https://github.com/AppsGanin/rospanel/commit/005651a5b34f6eef1e0020df8a4d165ca11f73a9))
* client-side DPI evasion in the subscription (fragment, noise, record fragment) ([f17816a](https://github.com/AppsGanin/rospanel/commit/f17816a95b94284cd0d8d13be2804b90b24ca676))
* **config:** snapshot/rollback the whole server config, not just routing ([114c056](https://github.com/AppsGanin/rospanel/commit/114c0569b4b24abbcd479d6ecde80429d7364b5c))
* **core:** reduce long-poll hold interval to 20s ([559018b](https://github.com/AppsGanin/rospanel/commit/559018b1d89bdf7155574b9a1ad8b66bdfb20840))
* **decoy:** add a self-hosted Nextcloud login page ([594c6d5](https://github.com/AppsGanin/rospanel/commit/594c6d57dc29359f1e0f510d45057e0d124f1616))
* **dns:** more presets, grouped by provider with IP/DoH variants ([7469740](https://github.com/AppsGanin/rospanel/commit/7469740b59fab70bad51ac023a823efc8ef050c6))
* external subscriptions, lanes through foreign servers, changelog, factory reset ([26d7d03](https://github.com/AppsGanin/rospanel/commit/26d7d03db753fcb2f1b08c355592a2615490db52))
* **external:** name relayed entries by a template, flag undelivered WireGuard ([4ec49ad](https://github.com/AppsGanin/rospanel/commit/4ec49ad1f7462a2fd7acd6c5520d76067c32aa41))
* **external:** present a device when reading somebody else's subscription ([9b75738](https://github.com/AppsGanin/rospanel/commit/9b75738f485dbfdd5f8fc9c240e90db98f8e7ee9))
* **extsub:** relay an external subscription through one of our servers ([79931ee](https://github.com/AppsGanin/rospanel/commit/79931ee8b59267866a51c55981623c95dd119dae))
* **extsub:** relay through many servers, with routing of its own ([4500ba4](https://github.com/AppsGanin/rospanel/commit/4500ba43db532448ddcf85d66ecf95be72dd00b4))
* **extsub:** WireGuard servers from a provider ([4500ba4](https://github.com/AppsGanin/rospanel/commit/4500ba43db532448ddcf85d66ecf95be72dd00b4))
* **groups:** a group saved with no connection ticked leaves access open ([4cbd13c](https://github.com/AppsGanin/rospanel/commit/4cbd13c4037fcbe053c9123a956a15b36760fe99))
* **groups:** an access group can cap its members' speed ([476f2f1](https://github.com/AppsGanin/rospanel/commit/476f2f1bb17d7eb5b20fe321e5f789fcb4bd4276))
* harden the subscription actions, the stops, the bulk paths and the sweeps ([c8868bb](https://github.com/AppsGanin/rospanel/commit/c8868bb9d64bbc4cf7ba0dbf38b81615065527bd))
* **hostfw:** open the server's ports in ufw, iptables and firewalld ([6e30c5d](https://github.com/AppsGanin/rospanel/commit/6e30c5d868d974c57dfb202b31bdd76d74ee37d5))
* **hysteria2:** Salamander obfuscation for the lane and for custom inbounds ([07554a3](https://github.com/AppsGanin/rospanel/commit/07554a31bc3b65c5dc2e7fb60a94df1dcc3a9e46))
* **hysteria2:** the obfuscation key is generated, never typed ([1337e4d](https://github.com/AppsGanin/rospanel/commit/1337e4d9b8b0abc850f955d6a5e115a7c1b0964f))
* **i18n:** Persian, right to left ([6d5ff6f](https://github.com/AppsGanin/rospanel/commit/6d5ff6f1dd4580904d9962d74a0da56ea3c6bca1))
* **i18n:** Simplified Chinese and Arabic, with a right-to-left layout ([ef25f4d](https://github.com/AppsGanin/rospanel/commit/ef25f4d2115c3d42c1494d3d21ee343e7b64b86e))
* import and export users (Marzban, 3x-ui, and this panel's own file) ([7b3ad79](https://github.com/AppsGanin/rospanel/commit/7b3ad79217d14a47d42e7a48f34561c5a241c071))
* **importer:** read Marzneshin and Rebecca databases ([9dec98b](https://github.com/AppsGanin/rospanel/commit/9dec98b1bdb066505854cf6f243efe1588b5ce6b))
* **inbounds:** add Shadowsocks-2022 as a custom inbound protocol ([c26da56](https://github.com/AppsGanin/rospanel/commit/c26da5680b2e92ed267d81e983edba32af81826e))
* **inbounds:** allow emoji in connection and lane names ([73ebc53](https://github.com/AppsGanin/rospanel/commit/73ebc53ecb392edcce5d671fcf3d4218573746c4))
* **inbounds:** plain WireGuard inbound ([08352a6](https://github.com/AppsGanin/rospanel/commit/08352a6935b344068246cb6642679e96536d8fa8))
* **inbounds:** WireGuard over calls (TURN), experimental ([d3e50b9](https://github.com/AppsGanin/rospanel/commit/d3e50b9c43d42c853482b6d35c431893e00add6e))
* **incidents:** give users the downtime back ([f469562](https://github.com/AppsGanin/rospanel/commit/f469562ab2ce4d3e43c1272d5a8e73532a5ff808))
* **journal:** logs and cancelled orders are kept for a year ([e135fca](https://github.com/AppsGanin/rospanel/commit/e135fcaaaa678c1c4f546be122a966f0a6b28d66))
* list and revoke your own panel sessions ([b4c07eb](https://github.com/AppsGanin/rospanel/commit/b4c07eb02ca0b39c95c28a12e1d785c578e3b83d))
* maintenance off subscriptions, scheduled broadcasts, self-service link and Telegram limits, monthly summary ([6cb05bc](https://github.com/AppsGanin/rospanel/commit/6cb05bca1d01910d66cea99738c5ae0975f84936))
* **masquerade:** serve the decoy over HTTP/3, draw REALITY's donor and port ([cd2ecc3](https://github.com/AppsGanin/rospanel/commit/cd2ecc3df8a39caf022fff6820e0166ea1e328b9))
* **mieru:** mieru lane on a port of its own ([604e4fe](https://github.com/AppsGanin/rospanel/commit/604e4fee258fcd249299110b109de4ef45a3c433))
* move the security lists to the statistics page ([15c0264](https://github.com/AppsGanin/rospanel/commit/15c02644a5e3329d0aac9738a57b73c17f61b3d5))
* **mtproto:** Telegram proxy on a port of its own ([878378c](https://github.com/AppsGanin/rospanel/commit/878378cf5a9e7e62125470cd8c394394f1b81d09))
* **naive:** NaiveProxy lane through the :443 decoy ([7ac54cb](https://github.com/AppsGanin/rospanel/commit/7ac54cb78e937b5b7dbc4953b357877e79d89aab))
* name the country and network operator behind each scanner ([47a6b39](https://github.com/AppsGanin/rospanel/commit/47a6b39ac917560a3c969dd9376c038fde26c31a))
* **nodes:** a server's WARP and Opera as configs of their own ([4500ba4](https://github.com/AppsGanin/rospanel/commit/4500ba43db532448ddcf85d66ecf95be72dd00b4))
* **nodes:** per-node traffic coefficient for quota ([2b2a8ea](https://github.com/AppsGanin/rospanel/commit/2b2a8eaf5c7577bef869c97c5a9f3f5b9ef48432))
* **nodes:** the AmneziaWG tunnel is watched on nodes, not just on the master ([8f1362a](https://github.com/AppsGanin/rospanel/commit/8f1362ad9137aa4a1917412be3c59f0f7e2473e7))
* **node:** surface sync-quality + smart backoff for a limping transport ([ae2642e](https://github.com/AppsGanin/rospanel/commit/ae2642e965fb87cc353011ae535f6c4afb411f79))
* **notify:** tell the admins when a lane leaves or rejoins subscriptions ([969f16a](https://github.com/AppsGanin/rospanel/commit/969f16ab6a3bf5dfbf09bfa2fd2bf9cee9eeb9e4))
* one Save and one Cancel per settings screen ([e05eecb](https://github.com/AppsGanin/rospanel/commit/e05eecba7ff352d3cc4e3136b8af63121876e10b))
* operator notes and tags on users, with the client id shown in the list ([b282d5c](https://github.com/AppsGanin/rospanel/commit/b282d5c8fd69404a240396aa1ea463b2c2b78d12))
* **ops:** maintenance mode ([9faa1d2](https://github.com/AppsGanin/rospanel/commit/9faa1d26c33a16b3a99c4adc3668e8b08d9ef7e4))
* **ops:** make the Xray watchdog operator-visible and toggleable ([1dfb99f](https://github.com/AppsGanin/rospanel/commit/1dfb99f288a3035aa5d0ef9d314adb25c60958e5))
* optionally hide a server from subscriptions while it is offline ([33d102e](https://github.com/AppsGanin/rospanel/commit/33d102e5f62fbae24722d886e25bd83979854815))
* order subscription servers by load, weight and capacity ([0ac878f](https://github.com/AppsGanin/rospanel/commit/0ac878f35385d9966eb38bbd09b75e259925a5d8))
* **panel:** a name that lands in a config file offers only {server} and {user} ([72a5ab8](https://github.com/AppsGanin/rospanel/commit/72a5ab818341fc1da1234ee958f37bd5e47795e6))
* **payments:** YooMoney wallet transfers and Telegram Stars ([b3af67b](https://github.com/AppsGanin/rospanel/commit/b3af67b11e893e2f825f3b3a9aa4d9c2fda809ed))
* **plugins:** community plugins — sandboxed engine, extension points, in-panel editor ([#108](https://github.com/AppsGanin/rospanel/issues/108)) ([9325be6](https://github.com/AppsGanin/rospanel/commit/9325be629d66b41552210090aec21ba8ad533568))
* **reach:** check servers from other countries through Globalping ([4aa4ba2](https://github.com/AppsGanin/rospanel/commit/4aa4ba2d80f85a3bb918d1b532a54692280f2d36))
* **reality:** a fresh install accepts the donor's own twin name too ([8196c85](https://github.com/AppsGanin/rospanel/commit/8196c85ba709119e03f126d17338b7f6327e2922))
* **reality:** draw a fresh install's donor from a wider pool of Russian sites ([883aafb](https://github.com/AppsGanin/rospanel/commit/883aafb0957a2466ab53ca742ab771f4da386eab))
* **reality:** REALITY on 443, sharing the port with the decoy by SNI ([947e720](https://github.com/AppsGanin/rospanel/commit/947e7209536d96010e854e246ef99f9ddfe3a202))
* **reality:** the server's own domain is the default XHTTP masquerade ([8de9a87](https://github.com/AppsGanin/rospanel/commit/8de9a8718f7634e24d9581b44576d04a83ef9156))
* refuse connections by source country and network ([30e8d85](https://github.com/AppsGanin/rospanel/commit/30e8d85e26edd8b86702e72e80c6f5a937b0a847))
* **roles:** roles are permission sets the owner builds ([04a5632](https://github.com/AppsGanin/rospanel/commit/04a5632fd8508cfb6217d7cc8f7e4d5cd022d5b1))
* **routing:** cascade — leave through our own servers ([cc9fafe](https://github.com/AppsGanin/rospanel/commit/cc9fafe5da593341aa84dc50424c0fc3527c41a5))
* **routing:** config snapshots with one-click rollback ([6c310b9](https://github.com/AppsGanin/rospanel/commit/6c310b928c724baee29ab811fb8cc05ae303601e))
* **routing:** never fall back to direct when a lane is down ([87938a0](https://github.com/AppsGanin/rospanel/commit/87938a009a9451f61bef6fab1d5aceec7df41c41)), closes [#85](https://github.com/AppsGanin/rospanel/issues/85)
* say why a config was rolled back, and roll back to one that ran ([e95c3e1](https://github.com/AppsGanin/rospanel/commit/e95c3e1391cfaeb6d6d888977d4640227f842c7c))
* **security:** a fresh second factor for the two actions with no undo ([d1a5d36](https://github.com/AppsGanin/rospanel/commit/d1a5d36e08653095a68248caa9a4af7c86c2c140))
* **security:** ban an address from a user's addresses, and one list of every ban ([cca7c35](https://github.com/AppsGanin/rospanel/commit/cca7c35f744e709990ba6f1624d081840e13c42e))
* **security:** detect IPs scanning for the hidden panel path ([883253b](https://github.com/AppsGanin/rospanel/commit/883253b27b876df88e9c3e3c4b7ec803aa964cce))
* **security:** probe detection — optional firewall auto-block + daily digest ([2f0dd80](https://github.com/AppsGanin/rospanel/commit/2f0dd80ec4bb9db648bda6382ce519da77ecb90a))
* **security:** the panel says when a server cannot drop addresses, and the installer brings nftables ([36cbd16](https://github.com/AppsGanin/rospanel/commit/36cbd16f5ffb3abafa7b09e272f51db3824bbd7c))
* **security:** trusted addresses are never banned automatically ([f1cfe09](https://github.com/AppsGanin/rospanel/commit/f1cfe094c6fb038e7b5183e0e89d0982b969ea39))
* **servers:** a monthly traffic limit starts on the hosting's billing day ([2217720](https://github.com/AppsGanin/rospanel/commit/2217720fda63b9dc8d8203a1fb611c50b5221341))
* **servers:** a traffic limit per server, with an alert and optional hiding ([9fbd49a](https://github.com/AppsGanin/rospanel/commit/9fbd49a13677e2febd3271819f90e3593de78bd5))
* **servers:** copy routing and connections from another server ([803b032](https://github.com/AppsGanin/rospanel/commit/803b0328fdd834a4696140d07d41c8a923db71ff))
* sign-in alerts and automatic blocklist measures ([6b20854](https://github.com/AppsGanin/rospanel/commit/6b20854eaaad0b6dbd1081cfe4b144cbc1785b9b))
* **stats:** connection geo map — source IPs by country ([d8c5a32](https://github.com/AppsGanin/rospanel/commit/d8c5a32f66f26a2698ce572c17e633038646e8a7))
* **stats:** connection map by network operator (ASN), not just country ([11fbe5e](https://github.com/AppsGanin/rospanel/commit/11fbe5e8488f56988ecf5daaf9c4e405a94f6369))
* **stats:** traffic by protocol, per server ([7ac54cb](https://github.com/AppsGanin/rospanel/commit/7ac54cb78e937b5b7dbc4953b357877e79d89aab))
* **status:** incident timeline, subscription page notices, incident webhooks ([582a004](https://github.com/AppsGanin/rospanel/commit/582a0048fd63beace3d418c93ea5afdf12f49995))
* **status:** incidents and maintenance on a redesigned status page ([7aeccee](https://github.com/AppsGanin/rospanel/commit/7aeccee035a98a56160f01846ff5cc0069d3cae7))
* step-up on reset/restore; widen egress floor; fix misleading hint ([03ad4b9](https://github.com/AppsGanin/rospanel/commit/03ad4b9ba4251bfbfcd64ea86a6a9a86fae34352))
* **sub:** a clearer active-plan block on the subscription page ([e4b1c66](https://github.com/AppsGanin/rospanel/commit/e4b1c66606b6d682330b370727082dba1677ab02))
* **sub:** AmneziaWG 3.1 in the Clash profile for mihomo 1.19.30+ and FlClash 0.8.99+, and an amneziawg:// line for INCY ([7ac54cb](https://github.com/AppsGanin/rospanel/commit/7ac54cb78e937b5b7dbc4953b357877e79d89aab))
* **sub:** AmneziaWG, TURN and single configs fold away by default ([d92b371](https://github.com/AppsGanin/rospanel/commit/d92b371b0f4d36aa0e18b19c12ec058d4e3063f6))
* **sub:** an access card — the page is the account, keep its link ([42f6325](https://github.com/AppsGanin/rospanel/commit/42f6325e73a0ad2e4ee7ab5ce731c2dd687f9b16))
* **sub:** default to a url-test group in Clash ([9bebb36](https://github.com/AppsGanin/rospanel/commit/9bebb36f743d9d5d513bee5afb95f59224637289))
* **sub:** every app on the subscription page links to its own page ([8801222](https://github.com/AppsGanin/rospanel/commit/880122289634a618b99cbaebb1e09c90968b9a14))
* **sub:** leave a lane that is down out of subscriptions ([e954099](https://github.com/AppsGanin/rospanel/commit/e95409924f0127f42b2c3f4513b2c756127c1158))
* **sub:** Mini App entrance, a connect wizard, a home-screen page, Telegram look ([e5057b1](https://github.com/AppsGanin/rospanel/commit/e5057b12823c3aa96f3fa72d8a0676ce0b5ed84d))
* **sub:** NaiveProxy for Throne (with UDP), Exclave and husi; AmneziaWG 3.1 for ClashMi, ClashMetaForAndroid, Clash Verge Rev, Mihomo Party, Koala Clash and Throne ([604e4fe](https://github.com/AppsGanin/rospanel/commit/604e4fee258fcd249299110b109de4ef45a3c433))
* **sub:** response rules — force format or block by client/OS ([f07df43](https://github.com/AppsGanin/rospanel/commit/f07df4353fe1e78f20f30648ba85aba99de2934f))
* **subscriptions:** a random server order spreads clients across the fleet ([023cd36](https://github.com/AppsGanin/rospanel/commit/023cd36ce7504178288af430f19da9ee4d56cb09))
* **subscriptions:** Happ adds the subscription through an encrypted link ([8c61072](https://github.com/AppsGanin/rospanel/commit/8c610729e46eb97ccb8b42015bfaf79a073f12a5))
* **subscriptions:** operator-editable profile templates for mihomo, sing-box and Xray JSON ([f4468f5](https://github.com/AppsGanin/rospanel/commit/f4468f52ebefbca224c0c600a7585c782e2d9f84))
* **subscriptions:** variables in connection names ([a0a32cd](https://github.com/AppsGanin/rospanel/commit/a0a32cd2f1cdb970752b5c1fb5827ea58b7a2f42))
* **sub:** switch the subscription page off ([1bb01bf](https://github.com/AppsGanin/rospanel/commit/1bb01bf999fcd70e8f5af7365f4c46d38377e35c))
* **sub:** the device add-on names its term — the date and the days left ([2e8abd9](https://github.com/AppsGanin/rospanel/commit/2e8abd9c9379d5ae0bd5e2b1f5200bb27bdffef5))
* **sub:** title the TURN card TURN-VK ([bde8a78](https://github.com/AppsGanin/rospanel/commit/bde8a788a481810c7c4da02906375bbb2c29fcec))
* **sub:** your own page keeps the subscription settings ([27d05ac](https://github.com/AppsGanin/rospanel/commit/27d05acf8a8de3078b2df71bface1bf0bb6ea39a))
* **sub:** your own subscription page — browsers go to your site, the view carries tg_link ([bb679ad](https://github.com/AppsGanin/rospanel/commit/bb679ad70cf32218eda762fd50da25a594814072))
* **telegram:** a mailing switch on the bot's screens, admin notices that tell users apart ([81d841e](https://github.com/AppsGanin/rospanel/commit/81d841e8f446dab3965ef359989f80a423f21add))
* **telegram:** ask before a link code moves a chat off another account ([07a7273](https://github.com/AppsGanin/rospanel/commit/07a727334fbafdaafa0596374d90949d4bb04eba))
* **telegram:** automatic messages on the customer's way ([8a41819](https://github.com/AppsGanin/rospanel/commit/8a418196762504199366fac0263ac0946b87cf9f))
* **telegram:** change Telegram from the subscription page, with switches ([0da8cd9](https://github.com/AppsGanin/rospanel/commit/0da8cd9e9fdff76757a4a6cbf096ed5f0fb82b9c))
* **telegram:** refuse signups to accounts on the shared VPN blacklist ([0f046c4](https://github.com/AppsGanin/rospanel/commit/0f046c46e5c5a712ca008c85cfea5fca92921a41))
* **tg:** split the client-bot device line into IP and HWID counts ([3f171ec](https://github.com/AppsGanin/rospanel/commit/3f171ec09045aa42e5ca20eba98df5f042085e26))
* **ui:** surface devices, speed limits, nodes and the new settings ([ae0b964](https://github.com/AppsGanin/rospanel/commit/ae0b964fc0298b6d66eea387661d83711a33d6cc))
* **ui:** table views for users, the journal and both payment lists ([b117401](https://github.com/AppsGanin/rospanel/commit/b11740150dc18b502d60112b9c7fd15dacc44180))
* **ui:** the whole panel reads dates and times in the panel's timezone ([08a36c0](https://github.com/AppsGanin/rospanel/commit/08a36c0b5bbaf9a6f59e6e5ebe351eba7318a3a7))
* update Xray-core to 26.7.28 ([cf28dda](https://github.com/AppsGanin/rospanel/commit/cf28dda3ddb814bb4f22da6770fe4fb43193d7a2))
* **update:** follow node updates, update the master from Servers, schedule only in settings ([3ec2668](https://github.com/AppsGanin/rospanel/commit/3ec26683d37566b4e1f00e13b14bc119ecc00ac3))
* **update:** the panel updates itself and its servers on a schedule ([2953f1f](https://github.com/AppsGanin/rospanel/commit/2953f1f02cb42e87086e64e2d733b4bc2d66b635))
* user agreement and privacy policy ([137c789](https://github.com/AppsGanin/rospanel/commit/137c789f3967ef48529a9ca9b071746ba07f4b62))
* **user-detail:** clarify device list labels and tooltips ([3da9c61](https://github.com/AppsGanin/rospanel/commit/3da9c61ee8ee214af0b6d898c7507568bbedb0c1))
* **users:** a term can start on the user's first connection ([58d6b0b](https://github.com/AppsGanin/rospanel/commit/58d6b0be2079c7221ae64d4ba91460e09ea7b393))
* **webhooks:** reach users without Telegram; mailing and language over the API ([d13e447](https://github.com/AppsGanin/rospanel/commit/d13e4476b47ac40f5cf8e687e0b8f4e428518872))
* **webhooks:** reminders, a durable outbox and the events an external system needs ([4da42c4](https://github.com/AppsGanin/rospanel/commit/4da42c40885c75c29c72abfc25549d0eb1eaed85))
* **web:** the language picker in the top bar, and a row on the phone ([908be95](https://github.com/AppsGanin/rospanel/commit/908be9572a741c71615cd284aa9b4be731a062d6))
* **xray:** pin Xray-core v26.10.10 ([38801db](https://github.com/AppsGanin/rospanel/commit/38801db4f3de9cde506027cbe0202fbccef6ac46))
* **xray:** watchdog auto-recovers a wedged (alive but not serving) Xray ([e8266ce](https://github.com/AppsGanin/rospanel/commit/e8266ce2e4069a72a4d2d81c89ba80659ca1acd1))


### Bug Fixes

* a manager built without a shaper must not crash the tests ([2233894](https://github.com/AppsGanin/rospanel/commit/22338946232cd1113571c0b740f03756523b1fa6))
* a re-ask no longer re-arms a node command already delivered ([b218d08](https://github.com/AppsGanin/rospanel/commit/b218d08dafed16024cdf08ce2342fdd6a8369907))
* a refused subscription must not look like an empty one ([20e50da](https://github.com/AppsGanin/rospanel/commit/20e50da42e7369181f050633d707f53f4a7794cf))
* act on the three-agent review of the new API surface ([43c5571](https://github.com/AppsGanin/rospanel/commit/43c557139a70397cadd891ef54bb194eb8dc279d))
* **admins:** the owner's password no longer has to be ASCII to remove an account ([77331ff](https://github.com/AppsGanin/rospanel/commit/77331ff2432ac97a74e902abf3298b5130b00e3f))
* **alerts:** a panel restart is not an AmneziaWG outage ([d152285](https://github.com/AppsGanin/rospanel/commit/d152285f2379ecf6fbeab4396b1932b042c36aa3))
* answer an unknown API path with JSON, not with the app shell ([23b6770](https://github.com/AppsGanin/rospanel/commit/23b6770fd7c44efa58219b2a316a33cbbce07757))
* **api:** apply the live half of the settings a PATCH changes ([f0093e7](https://github.com/AppsGanin/rospanel/commit/f0093e700613300851669979de15dfa3c467935c))
* **api:** honour enabled when creating a webhook ([9bc9d50](https://github.com/AppsGanin/rospanel/commit/9bc9d507ecd8e0060844bf3776499a3be643c0ac))
* **api:** reject unknown and read-only body fields ([832fc92](https://github.com/AppsGanin/rospanel/commit/832fc9230bdf8c81db1f148dd20c1e3b97155745))
* **api:** self-host Swagger UI; add Dependabot for Go and actions ([642f755](https://github.com/AppsGanin/rospanel/commit/642f755ccca36652206256a3497e5f50525f277b))
* **audit:** label a retired settings section and show nested values in the journal ([f061171](https://github.com/AppsGanin/rospanel/commit/f061171ab00ccc200b85c4318b1bc83162f7b8b3))
* **audit:** skip journal rows on no-op user edits ([66ba340](https://github.com/AppsGanin/rospanel/commit/66ba340c7cbcc2a6d7edc040652205def9d97ad1))
* **auth:** set Secure and SameSite on the session-clearing cookie ([9b3a231](https://github.com/AppsGanin/rospanel/commit/9b3a231506434c876a88e26512c354e42721e9bd))
* **awg:** open iptables FORWARD for the tunnel on Docker/ufw hosts ([3661e1b](https://github.com/AppsGanin/rospanel/commit/3661e1ba33c7b9de46304326848dbb42f0caf2b5))
* **awg:** the panel says which protocol a server imitates ([db85724](https://github.com/AppsGanin/rospanel/commit/db8572414857dc24b8e0b3944cebe4b9adb76319))
* **awg:** tunnel addresses no longer run out with the user ids ([a45eb59](https://github.com/AppsGanin/rospanel/commit/a45eb591b04397100c3d7dbd7c023bb81cf3ced0))
* **backup:** restoring a backup asks for the second factor ([a4eaac9](https://github.com/AppsGanin/rospanel/commit/a4eaac9840ca25f801d88a9820c48136426ec064))
* **billing:** extra devices cost the same per device whatever the count ([3cbb460](https://github.com/AppsGanin/rospanel/commit/3cbb460735f8e71e024da3a81801a29dc2ed1c23))
* **billing:** what the second review of the money fixes found ([033fc55](https://github.com/AppsGanin/rospanel/commit/033fc55b36bc4285a37e99e3acf69a9318ed9432))
* bound a bulk user action ([df9a539](https://github.com/AppsGanin/rospanel/commit/df9a539b0e857b2f06054c98ed3d9b36794ece4a))
* bound device roster, cache public pages, close billing gaps ([405c915](https://github.com/AppsGanin/rospanel/commit/405c915df2a7e51743d03c2348604d3eef6d1208))
* bound probeblock set, quiet nft failures, snapshot-rollback UX, geo re-parse ([b223ceb](https://github.com/AppsGanin/rospanel/commit/b223ceb07e395969530708a3db240a37889f7f5b))
* **branding:** labels on the accent turn dark when the accent is too light for white ([4554d35](https://github.com/AppsGanin/rospanel/commit/4554d35d3c5b03c0ae4b06c37659636a7288ab1e))
* **cli:** say so when a rescue password is redirected out of the terminal ([542ac8f](https://github.com/AppsGanin/rospanel/commit/542ac8f9c16598c577ce7c78898d8fa4eee65075))
* **conn:** the obfuscation summary shows the whole 3.1 parameter set ([d712532](https://github.com/AppsGanin/rospanel/commit/d712532434b534217ede62898b23e23208c52d53))
* **core:** shutdown no longer waits out a template download ([3c8c12b](https://github.com/AppsGanin/rospanel/commit/3c8c12b4943c7f8ed6b967625e754511e626e4f8))
* **core:** the brute-force guard's cleanup stops with the manager ([a493129](https://github.com/AppsGanin/rospanel/commit/a493129f38e127afacfa389964b523a92ddd47ff))
* **core:** the manager's background loops stop when it does ([6c4ffc6](https://github.com/AppsGanin/rospanel/commit/6c4ffc6ac916e72feae72435dc23293c5c5ddb37))
* count devices by HWID when HWID is what identifies them ([#66](https://github.com/AppsGanin/rospanel/issues/66)) ([7df4b95](https://github.com/AppsGanin/rospanel/commit/7df4b95ddee54c9ff72aa0b27ea4e34cfa156c7c))
* **decoy:** serve every decoy asset from our own origin ([fbabecd](https://github.com/AppsGanin/rospanel/commit/fbabecd84ae13b6c40bcf081f6fc00e2c4740be5))
* **decoy:** stop advertising HTTP/3, it locked browsers out of the panel ([1860d7b](https://github.com/AppsGanin/rospanel/commit/1860d7b1abbbc78b39acb9ae29d6e5f034437a98))
* **deps:** bump Go to 1.26.6 for stdlib security fixes ([c24c388](https://github.com/AppsGanin/rospanel/commit/c24c38860f199be3ad8e7182b9375c91fc008dad))
* **deps:** pion/dtls v3.1.9, past the panic on a crafted ServerKeyExchange ([0b97f06](https://github.com/AppsGanin/rospanel/commit/0b97f065379591f2b4941ea1bd47869f2cb88314))
* **deps:** x/crypto 0.56.0 for the two ssh channel DoS advisories ([aac8346](https://github.com/AppsGanin/rospanel/commit/aac834675380b04316b1f526807526f809655d7a))
* **devices:** a WireGuard tunnel address is not a device ([f636536](https://github.com/AppsGanin/rospanel/commit/f63653656db55a337ffb8b70a28581b38f4c5821))
* **dns:** refuse servers Xray cannot use, and ask host-named resolvers last ([309b943](https://github.com/AppsGanin/rospanel/commit/309b943bdc3ecaed0d22b9e0ebb78b0e1c885135))
* **docker:** bump build image to golang:1.26.6 ([3a974b8](https://github.com/AppsGanin/rospanel/commit/3a974b81d202b0383f56267e493264063731f590))
* error on ASN byte-cap truncation; close probeblock disarm race ([a7473a1](https://github.com/AppsGanin/rospanel/commit/a7473a1599ffa79b05425ef4d692d4a0a88689a7))
* external servers no longer vanish with a hidden master ([0c033b8](https://github.com/AppsGanin/rospanel/commit/0c033b80988bd567b9994873b3c79d8265e6c033))
* forgive a network handover instead of giving up the device limit ([a7c5dd0](https://github.com/AppsGanin/rospanel/commit/a7c5dd0b7fe72605f3dbd2237f553ab3618a53b2))
* **groups:** the speed limit field spans the editor's full width ([62643b7](https://github.com/AppsGanin/rospanel/commit/62643b7acc6c7d17002495115230bb13cb4028ee))
* guard restores against a newer schema, unlock decoy reads, bound broadcasts ([1e3fa4b](https://github.com/AppsGanin/rospanel/commit/1e3fa4b31c4ff6d226ff295f9034bd054ecfed00))
* harden geoip parser, probe miss-detection and CSV export (review) ([7a89c36](https://github.com/AppsGanin/rospanel/commit/7a89c36b0189f1779b6204f8488673fe90f758f8))
* harden snapshot rollback, node backoff, probeblock, ASN parsing ([57463fa](https://github.com/AppsGanin/rospanel/commit/57463faa058d8ecefa7435b3898d135acfdf5b0c))
* **health:** the AmneziaWG row sits under Xray config, not at the end ([f1f74ba](https://github.com/AppsGanin/rospanel/commit/f1f74ba837db016f679f332d220762e24957ed90))
* **health:** the master's own AmneziaWG tunnel is in its diagnostics too ([0c9aad1](https://github.com/AppsGanin/rospanel/commit/0c9aad10af28f92e3787f823a274247075355ab4))
* **http:** a held HTTP/2 response outlives ReadHeaderTimeout ([18e3ae3](https://github.com/AppsGanin/rospanel/commit/18e3ae3b38b07e0ba2174f36ecf7558295c4ac93))
* **i18n:** remove country note from placement hint ([15dea4b](https://github.com/AppsGanin/rospanel/commit/15dea4b2f46b6916030564e10268b741bb994ae9))
* **i18n:** restore the health hint for a stale panel ([320daa9](https://github.com/AppsGanin/rospanel/commit/320daa9a2c8e462763a8abd8ef1d6e2ec1681322))
* **i18n:** reword the daily scanner digest ([c0823ae](https://github.com/AppsGanin/rospanel/commit/c0823aea6a47c1e981aea1c7b0d8c89687accc6f))
* **i18n:** shorten the AmneziaWG settings hint ([8792c4e](https://github.com/AppsGanin/rospanel/commit/8792c4ee8ef1a811eb79201d3af827e1e415c93c))
* **inbounds:** carry the Shadowsocks method through the request mapping ([7667c91](https://github.com/AppsGanin/rospanel/commit/7667c9109317180719fd2104da3935048773248b))
* **journal:** the actor for a user's own action reads "user" ([7a40910](https://github.com/AppsGanin/rospanel/commit/7a40910fddc48e8b3e1ca1e017cfe07b105f67fa))
* **mcp:** keep the whole backup surface out of the assistant's toolbox ([fc1b4f6](https://github.com/AppsGanin/rospanel/commit/fc1b4f6b9fd1bda12e1500d3256137db3f2983d9))
* **memory:** the panel and the node agent keep their Go heap under a soft limit ([a316572](https://github.com/AppsGanin/rospanel/commit/a316572749d74011e367cb4008fd5a9a93c4977e))
* **memory:** the panel's heap limit leaves room for the Xray it runs ([1f878a4](https://github.com/AppsGanin/rospanel/commit/1f878a4a5eadf6d4a21cf6673e7237e163fd5fe2))
* **mieru:** keep accepting after an Accept error ([0ef4edb](https://github.com/AppsGanin/rospanel/commit/0ef4edb969f92eeaaa2af0f038dae9b9f1ed1631))
* modal/drawer panels no longer scroll themselves off-screen ([4fdd874](https://github.com/AppsGanin/rospanel/commit/4fdd874ce30af505194b7de611a8d14ae223813c)), closes [#75](https://github.com/AppsGanin/rospanel/issues/75)
* **mtproto:** bound the DC port to 16 bits and reuse one record buffer ([0764387](https://github.com/AppsGanin/rospanel/commit/07643874af60654413f2bc5b6bab1597303d7f1b))
* name every journal event from the dictionary, with details for devices, policy and abuse ([f135280](https://github.com/AppsGanin/rospanel/commit/f1352800c008ce127c2a9a0eff924e30704801e2))
* node commands survive handover; subscription access fails closed ([f014edd](https://github.com/AppsGanin/rospanel/commit/f014eddbe484e27160a4a35524d3899033712903))
* **nodeagent:** a held poll clears the node's sync-failure window ([8b4999c](https://github.com/AppsGanin/rospanel/commit/8b4999cd32b4cd957fa8db3779007ce3dc85618d))
* **nodeagent:** bound the sites budget by the body's length before subtracting ([6039182](https://github.com/AppsGanin/rospanel/commit/6039182fb12c7cdfdc63abb7f13c2d228930581a))
* **node:** force HTTP/1.1 for the sync long-poll (stop GOAWAY flapping) ([465b8a9](https://github.com/AppsGanin/rospanel/commit/465b8a9ef62f65bff6dc88526caafc0701c80758))
* **nodes:** a busy node's traffic report no longer stalls its sync for good ([94e73bc](https://github.com/AppsGanin/rospanel/commit/94e73bc146d92854ff3835c8a1aefaf2ebcb3e8a))
* **nodes:** a node's health reads a state held in parts as its sync would ([204b424](https://github.com/AppsGanin/rospanel/commit/204b424ed78d1ef1e63417f80fa5a2a15e2b6ee4))
* **nodes:** a node's report is believed only about the users its config lets in ([55276a0](https://github.com/AppsGanin/rospanel/commit/55276a08ae6312ab13b81486143999726eed3f5a))
* **nodes:** connection samples are no longer dropped past 8,192 pairs ([cea56f2](https://github.com/AppsGanin/rospanel/commit/cea56f2bb8d09f34f3368a4fdb77d56544d2176e))
* **nodes:** destination rows fit what the panel takes, and users take turns ([3eff106](https://github.com/AppsGanin/rospanel/commit/3eff1065600dafe22350e1997ab66498f3d0693c))
* **nodes:** move the front's REALITY route only when Xray took the config ([75c8024](https://github.com/AppsGanin/rospanel/commit/75c80242ad281fc80d9eafbab62623170ccb6760))
* **nodes:** the users a node is believed about come from the newest read ([25ec3dd](https://github.com/AppsGanin/rospanel/commit/25ec3dd8b39e3bd3356e2a49ab244575f30a31b7))
* **nodes:** validate a server's host on create and edit ([7ac54cb](https://github.com/AppsGanin/rospanel/commit/7ac54cb78e937b5b7dbc4953b357877e79d89aab))
* **overview:** recent events carry a date, not just a clock ([ca2a555](https://github.com/AppsGanin/rospanel/commit/ca2a5556fce3201587b8856318193f99d0504777))
* **overview:** the dashboard counts blocklist matches over the real window ([f1ed2a1](https://github.com/AppsGanin/rospanel/commit/f1ed2a16ed2189cfc4460b6cff81bcc12f4d963d))
* page the recent-scanners list instead of dumping every row ([6de6372](https://github.com/AppsGanin/rospanel/commit/6de637255e45ae4f2f43d513c6f9578a417d9956))
* paid-renewal quota, tunnel→control-API escape, order cancel guard ([2b388c8](https://github.com/AppsGanin/rospanel/commit/2b388c8d1645319aab1e6be02a9584e35bc54d31))
* persist node commands; make GET /v1/nodes/{id} match the list ([8ae59d7](https://github.com/AppsGanin/rospanel/commit/8ae59d722e489376cb6d567b11ad4a977eb3301c))
* port 80 names this machine, and keeps naming the right one ([61b842d](https://github.com/AppsGanin/rospanel/commit/61b842d2ad235cdd5bbc34d273bc01d54cd6618e))
* post-review fixes for the three new features ([7c84ffa](https://github.com/AppsGanin/rospanel/commit/7c84ffaf27cd45e7efbf8d74328ed0dec8e634b5))
* **proxy:** a list host that fails a refresh no longer empties the lane ([c843451](https://github.com/AppsGanin/rospanel/commit/c843451fdccb8b69bd9769a4d287d69d40e271d8))
* put the factory-reset button under its text ([975bc5f](https://github.com/AppsGanin/rospanel/commit/975bc5ffd65a8dd7a7873272cf477017b32ad98e))
* **ratelimit:** a flood can no longer reset a throttled address ([5b6accc](https://github.com/AppsGanin/rospanel/commit/5b6accca11651767af270ba6b0d2e5f665f3896c))
* re-send the user set when Xray comes back from a crash ([b635e1c](https://github.com/AppsGanin/rospanel/commit/b635e1cbc5a6d179cfbcd5c52abe37bf8674b3e1))
* **reality:** let a node leave its own name for the shared port ([710fb48](https://github.com/AppsGanin/rospanel/commit/710fb4854dba5064ade47c6bcbeb7971660aa6c4))
* roll back an unloadable config even when no Apply preceded it ([023471e](https://github.com/AppsGanin/rospanel/commit/023471eaf1f77ba119c854c036a5ddaa8e508148))
* **security:** bound what a subscription template can expand into, and close the setup-time step-up gap ([fa2893e](https://github.com/AppsGanin/rospanel/commit/fa2893ef84ff62c175deaef48f29a11c64e47020))
* **security:** four more from a second review pass ([246d5ee](https://github.com/AppsGanin/rospanel/commit/246d5ee93cdfa259a99fa785a329ba6cdc96e28c))
* **servers:** the traffic alert stopped repeating itself, and stopped lying ([e088429](https://github.com/AppsGanin/rospanel/commit/e088429272da917b7efb07a217a51d8cc0205477))
* **server:** the API and node segments are compared in constant time ([59b2900](https://github.com/AppsGanin/rospanel/commit/59b2900644c10dfa5893997bd16e744ebd2a6517))
* **shaper:** a nil applier shapes nothing instead of panicking ([f912e3a](https://github.com/AppsGanin/rospanel/commit/f912e3a204d014330a87682f359fdd178b451446))
* show a proxy listener's default port as a value while it is off ([ad5f765](https://github.com/AppsGanin/rospanel/commit/ad5f765430ae0c1492e61ec38842048b8a7f7f14))
* snapshot the config before Xray reads it, and pin what was only assumed ([0258290](https://github.com/AppsGanin/rospanel/commit/0258290ef58d0608aeb22bc1bfed936576021f7a))
* spell the scanner's country the way the rest of the panel does ([e78e9b5](https://github.com/AppsGanin/rospanel/commit/e78e9b570f7e7543dfee60f372531e98a1e2434f))
* **stats:** keep the traffic counted before a restart ([d98a71a](https://github.com/AppsGanin/rospanel/commit/d98a71a668748b2c1f376e859f06544ba5056088))
* **stats:** say which window each report on the page covers ([4d6bcf9](https://github.com/AppsGanin/rospanel/commit/4d6bcf9fb16953fdd0c1963b9fe80fbc900c7c0e))
* stop offering AsIs twice in the direct DNS strategy ([d645bc7](https://github.com/AppsGanin/rospanel/commit/d645bc7cb4d88bdb319c1cced62b163df256a343))
* **sub:** drop the home-screen block from the subscription page ([c5712df](https://github.com/AppsGanin/rospanel/commit/c5712dfa8fe79e2630bcff788b32c439025ddd9b))
* **sub:** external XHTTP servers reach mihomo, and an HTTPUpgrade one no longer breaks the profile ([c602a2d](https://github.com/AppsGanin/rospanel/commit/c602a2d1a5786372f7ac9d956f10b121f68e5941))
* **sub:** keep Telegram deep links working without the SDK ([75d0312](https://github.com/AppsGanin/rospanel/commit/75d0312d6fefcd18f93125334bfbf6dcc1886793))
* **subscriptions:** connection names drop {flag} and {country} ([31338e0](https://github.com/AppsGanin/rospanel/commit/31338e0bf344222b94cc7c2e645c69964dcddedb))
* **subscriptions:** name variables reach the share links, and rendered names cannot collide ([64376b2](https://github.com/AppsGanin/rospanel/commit/64376b26d03cb141c773e761a96ec01d617d6e05))
* **sub:** sing-box 1.13 and 1.14 load the generated profile again ([1e20789](https://github.com/AppsGanin/rospanel/commit/1e207898cdb64712e892155b582464952c23cc0a)), closes [#89](https://github.com/AppsGanin/rospanel/issues/89)
* **sub:** the lane name comes back typed, not read out of the config map ([32a6d47](https://github.com/AppsGanin/rospanel/commit/32a6d47ce25d83ef1d61804ce1e885ea093c470d))
* **sub:** the Telegram proxy's Copy takes the tg:// link ([98d94bc](https://github.com/AppsGanin/rospanel/commit/98d94bc535d75c2a06e93dcacec68d0b87d5dca1))
* **sub:** what the review of the Mini App entrance found ([e723391](https://github.com/AppsGanin/rospanel/commit/e723391649f11de2170fd30c3e63cde26e87dda7))
* support impersonation, resurrected nodes, half-applied rollbacks ([6855194](https://github.com/AppsGanin/rospanel/commit/6855194f3f61cc65b26bdbf310d80aee8b364218))
* take port 80 before issuing the certificate, not after ([e996f72](https://github.com/AppsGanin/rospanel/commit/e996f72caa9a23dc3d965f58772b28dbbf89b110))
* **telegram:** move the menu button off the old Mini App address ([13e65b5](https://github.com/AppsGanin/rospanel/commit/13e65b5b19ca5eb8149585f9aeaba4abf3551487))
* the JSON fallback has to cover every method, not just GET ([9633dc1](https://github.com/AppsGanin/rospanel/commit/9633dc14ca85a1593cc57a514a90add755d17151))
* the rest of the three-agent sweep ([d647f38](https://github.com/AppsGanin/rospanel/commit/d647f38da4814869759a15c53b2eafbf5321f780))
* the subscription page shows external servers, and addresses the panel ([88a9a61](https://github.com/AppsGanin/rospanel/commit/88a9a6167d863b50f81d2d68f2d41c418a823cfe))
* TLS lifecycle and subscription rendering defects ([ec82fe0](https://github.com/AppsGanin/rospanel/commit/ec82fe0c68e42423f516d4a6864cd68782cdfbd5))
* **turn:** stopping a relay closes its legs instead of deadlining them ([f9347eb](https://github.com/AppsGanin/rospanel/commit/f9347ebe0421a328cccc36a6d53e5ea51b2bb5d3))
* **ui:** a settings hint with nothing beside it spans its row ([0a84e11](https://github.com/AppsGanin/rospanel/commit/0a84e11683a03f6b5338781ccf8d41964fd06d77))
* **ui:** a user's addresses show even when device binding is on ([0ceea40](https://github.com/AppsGanin/rospanel/commit/0ceea407a1cd4e9d05708d80c2c8b7c8eda00250))
* **ui:** an empty date reads as a dash ([40f254a](https://github.com/AppsGanin/rospanel/commit/40f254ab903aecbc05d6122ffe800fccd1c503fa))
* **ui:** indicate unsaved egress toggle state in status badges ([0b76ae3](https://github.com/AppsGanin/rospanel/commit/0b76ae395621a5924177d8e149b7f5c11cf668e8))
* **ui:** the API tab's save bar sits at the bottom, below the webhooks ([36e8961](https://github.com/AppsGanin/rospanel/commit/36e89612c4124be8e7a686b4d45e8a51b9c41039))
* **update:** offer a release only once its binaries are uploaded ([72f36c3](https://github.com/AppsGanin/rospanel/commit/72f36c3b238a5e77cf9faaedd9713af4f78e5637))
* **users:** a traffic reset restarts the quota cycle ([63b2c12](https://github.com/AppsGanin/rospanel/commit/63b2c128a35ffb1fdacee37c1a6ca9f22492a79c))
* **users:** adding a user granted a Shadowsocks inbound no longer restarts Xray ([2169205](https://github.com/AppsGanin/rospanel/commit/216920542ee969944b08831a54573f957af77fd4))
* **users:** bulk reset and import both anchor the quota cycle ([df484c8](https://github.com/AppsGanin/rospanel/commit/df484c847c40505e7cfdb2ad617b55ff7b3ec52d))
* **users:** the list's loading placeholder has the page's shape ([6907dd8](https://github.com/AppsGanin/rospanel/commit/6907dd8879cd67e920c7cf16b7acdb58a5443fa7))
* wait out a network change before the device limit cuts anyone ([cd810fe](https://github.com/AppsGanin/rospanel/commit/cd810fe4b3476a23f86c0675baf11408d0fc127a))
* **warp:** use IP endpoints and multi-endpoint pool for WARP lane ([c0fada0](https://github.com/AppsGanin/rospanel/commit/c0fada0e26fdfe91b3d1997a37be426f950fedf7))
* **watchdog:** still detect+alert a wedged Xray when auto-recovery is off ([6aa9588](https://github.com/AppsGanin/rospanel/commit/6aa95881806e7ee6d66cdbea11d24061263fa033))
* **web:** biome's exhaustive-deps on the automatic-message and blacklist panels ([b94c833](https://github.com/AppsGanin/rospanel/commit/b94c833dd039ef80dd5feda3cc94010dc665276a))
* **web:** five defects the fork's frontend audit pointed at, done our way ([341b436](https://github.com/AppsGanin/rospanel/commit/341b436a6bfc7791e55788a351770b83473d56b5))
* **webhooks:** size the delivery batch by the items, not a product of two lengths ([b57d4db](https://github.com/AppsGanin/rospanel/commit/b57d4db141cca468f8befeead6777dbf3ff8ec5a))
* **web:** name a payment provider's field in its error, not its dictionary key ([992066d](https://github.com/AppsGanin/rospanel/commit/992066da83b97cbed8c3e7663ee5cc57d709afa3))
* **web:** open the editor's sources zip as a draft again ([6af30b5](https://github.com/AppsGanin/rospanel/commit/6af30b5a448dbd30073ba9b548d98d0b7857145c))
* what the review of today's features found ([4de2b6e](https://github.com/AppsGanin/rospanel/commit/4de2b6e78d04096cb2d87088d219bf0399ac0adf))
* **xray:** a live change whose api call cannot reach xray is asked again before the full reload ([6e9ebf5](https://github.com/AppsGanin/rospanel/commit/6e9ebf5b1a1e8faf7230ad53347862237ac807de))
* **xray:** adding or removing a Hysteria2 user no longer drops everyone on the lane ([c7db569](https://github.com/AppsGanin/rospanel/commit/c7db569cd0e58439ffe65accc1b3a22388c316c8))
* **xray:** keep only a real port as an inbound's public port ([9bf3437](https://github.com/AppsGanin/rospanel/commit/9bf3437e6e8ed62a026f7100642dae09eb26fa94))
* **xray:** pin Xray-core v26.10.21, the old release is gone ([91b9669](https://github.com/AppsGanin/rospanel/commit/91b9669d7859668b213b2bd6ae29f1c1f7c6b69c))
* **xray:** skip an inbound of a protocol this build does not know ([60b587a](https://github.com/AppsGanin/rospanel/commit/60b587a17da114ea9835e80b3fe09391375d7337))
* **xray:** the API client refuses a message too big for a gRPC frame ([f51f8b5](https://github.com/AppsGanin/rospanel/commit/f51f8b5703f4c50e256c4925211979ce811273a8))


### Performance Improvements

* **access:** the access flush and throttle stop scaling with every user and pair ([541eb7f](https://github.com/AppsGanin/rospanel/commit/541eb7f43e06efbccfb9e99040f352c111d1e983))
* **awg:** a tunnel's peer list stops re-deriving every user's public key ([b73fefc](https://github.com/AppsGanin/rospanel/commit/b73fefcc6cd998b47f1d56b374bcabe480513c76))
* **connections:** a connected device is written every 45 seconds, and device limits are checked every 30 ([adecc69](https://github.com/AppsGanin/rospanel/commit/adecc69ba137733094f2dad06a6cb259162c6907))
* **connections:** connection sightings are written with prepared statements ([b2c1a67](https://github.com/AppsGanin/rospanel/commit/b2c1a676a4f8b79bd2167fdb9d12134a937b9fcf))
* count devices in one pass instead of once per connection row ([8728382](https://github.com/AppsGanin/rospanel/commit/8728382737b78a74a48b216b5df582327269d4c7))
* **dashboard:** the status feed recounts users every 15 seconds, not every 2 ([de8cee5](https://github.com/AppsGanin/rospanel/commit/de8cee56316c9f743e98791e4366dea29fbf23f9))
* **nodes:** a node changes users without restarting Xray ([db1167e](https://github.com/AppsGanin/rospanel/commit/db1167e805dc5c8fd92cb982434f57dc14d5bc0f))
* **nodes:** a node is sent the users that changed, not its whole config again ([7ec34af](https://github.com/AppsGanin/rospanel/commit/7ec34afea5f8046a48259c8c94034e50db6a8f9b))
* **nodes:** a node that is current is told so without its state being rebuilt ([350f470](https://github.com/AppsGanin/rospanel/commit/350f470c92a47fb68b6fd603337c46d79d4fd584))
* **nodes:** a node's certificate status does not count against its remembered state ([f6277d3](https://github.com/AppsGanin/rospanel/commit/f6277d328b30403228feae8250c7e6d4211300ef))
* **nodes:** a node's config is built alone, and hashed without a copy of itself ([695c3b6](https://github.com/AppsGanin/rospanel/commit/695c3b6893759abeecf2dd34f801b82c4093297d))
* **nodes:** a node's state is held under the gate until it is encoded, and written out after ([f3d89eb](https://github.com/AppsGanin/rospanel/commit/f3d89eb0194e4d6abfa493c9cde8a883a6c50fab))
* **nodes:** config builds and traffic passes read only what they use ([949c65d](https://github.com/AppsGanin/rospanel/commit/949c65df13530b5dc33c09394aa56e5bb880e5a9))
* **nodes:** config pushes go out gzip-compressed ([892a610](https://github.com/AppsGanin/rospanel/commit/892a6100e7df4bb28b8782a85ba6bbb8486eee45))
* **nodes:** nodes share one read of the fleet's users, and one enforcement pass ([677f45b](https://github.com/AppsGanin/rospanel/commit/677f45b14d623924eb2864d28a9649d7e11ea3ec))
* **nodes:** one read of the working set answers both reconcile questions ([3084af4](https://github.com/AppsGanin/rospanel/commit/3084af4be564274ac14c1420a818d15bbcee869b))
* **nodes:** the nodes are woken only for a change they can see, and the shared read keeps the credentials it has ([aab1898](https://github.com/AppsGanin/rospanel/commit/aab1898aac5583b5faf32efc858e1970fb06b702))
* **secrets:** encrypted fields open without rebuilding the cipher each time ([14bded3](https://github.com/AppsGanin/rospanel/commit/14bded34ff81c67134b96cce7836988b3baf28ec))
* **settings:** the settings are decoded once and read again only when they change ([a2e6a87](https://github.com/AppsGanin/rospanel/commit/a2e6a879274fd1543785f77e37ce8598620644a5))
* **stats:** the stats poll and the enforcement pass stop reading every user whole ([d4809fb](https://github.com/AppsGanin/rospanel/commit/d4809fb6b55dcd341a60324f8169ba81c388d1fd))
* **store:** bounded lookups read from a pool beside the writer ([cc32f29](https://github.com/AppsGanin/rospanel/commit/cc32f29cbcc1cce8ae4636c300473cd632789871))
* **sub:** the certificate file is read when it changes, not on every fetch ([7703235](https://github.com/AppsGanin/rospanel/commit/77032353dad513cd2e8b33046f653a89a7fb7395))
* **sub:** the per-server online count answers for two seconds ([f022321](https://github.com/AppsGanin/rospanel/commit/f02232185094115f95210a135046d426232d3efa))
* **sub:** what every subscription shares is read once for a few seconds ([67af6ed](https://github.com/AppsGanin/rospanel/commit/67af6eda4ded73ac57feca677351f64b2402ad58))
* **traffic:** a traffic batch is written in two statements, not three per user ([d26a8ae](https://github.com/AppsGanin/rospanel/commit/d26a8aef7ce49558a5c8d9a07dff3af2e288bb8a))
* **users:** an edit to one user rereads that row of the list, and each order is sorted once ([5089a5e](https://github.com/AppsGanin/rospanel/commit/5089a5e6a8414a87af543316540ba5195c45106f))
* **users:** reading one user counts only that user's devices ([b82d2d8](https://github.com/AppsGanin/rospanel/commit/b82d2d8b2b03f060d2d3b958b1fd77d1aa658a29))
* **users:** subscription and Telegram lookups use their indexes ([88a8f84](https://github.com/AppsGanin/rospanel/commit/88a8f84e07fc1e5c6e392263e6da411dfc3a554a))
* **users:** the users list is read once for a burst of requests, and afresh after any change ([2d1a617](https://github.com/AppsGanin/rospanel/commit/2d1a617a19dc4ba87d53399576dfce4ca38fc029))
* **users:** the users page loads one window of rows instead of every user ([86cfd45](https://github.com/AppsGanin/rospanel/commit/86cfd45bcb665a08f6678f141f94346c8f286d37))
* **users:** the users page reads summaries, not whole users ([c2bf8d9](https://github.com/AppsGanin/rospanel/commit/c2bf8d9edc926f6caf0b683fd202fcdc641b992d))
* **users:** the users page works out its counts, tags and search text once per read ([365aa1e](https://github.com/AppsGanin/rospanel/commit/365aa1efa6fdcc5425d05aa12552419cc18bad57))
* write a fleet-wide node command in one transaction ([618717d](https://github.com/AppsGanin/rospanel/commit/618717db34ba5dd986671c3e3e24fbef51b4926a))


### Reverts

* drop the handover grace, it did not work and cost enforcement ([fe17442](https://github.com/AppsGanin/rospanel/commit/fe17442752e623c69aff1b7802ed0f349aa93f78))
* **nodes:** drop a server's WARP and Opera as configs of their own ([37700ac](https://github.com/AppsGanin/rospanel/commit/37700ac5b9195381ad43dcf92000fe722b6a6542))

## [5.13.0](https://github.com/AppsGanin/rospanel/compare/v5.12.0...v5.13.0) (2026-11-03)


### Features

* **billing:** a picture for manual payment, shown with the instructions ([c370e5e](https://github.com/AppsGanin/rospanel/commit/c370e5ec2b11272f9875c8acec7fffdef12cf7f2))
* **reality:** a fresh install accepts the donor's own twin name too ([f21af0c](https://github.com/AppsGanin/rospanel/commit/f21af0cfceb20bbf8f3fdaaf646c172896cf666b))
* **reality:** draw a fresh install's donor from a wider pool of Russian sites ([44f8abd](https://github.com/AppsGanin/rospanel/commit/44f8abd19a07901571534c404c91fc9eea3ec22a))
* **reality:** REALITY on 443, sharing the port with the decoy by SNI ([d54e5f9](https://github.com/AppsGanin/rospanel/commit/d54e5f98054c863f48300602b23bf3bb3bd7ea18))


### Bug Fixes

* **decoy:** stop advertising HTTP/3, it locked browsers out of the panel ([c3ca037](https://github.com/AppsGanin/rospanel/commit/c3ca037e7b24dd70684c5a5e9cab6c441b6ceef4))
* **nodes:** move the front's REALITY route only when Xray took the config ([f90049b](https://github.com/AppsGanin/rospanel/commit/f90049b74c1559485c66c8b7f4262f50404e86aa))
* **reality:** let a node leave its own name for the shared port ([7df42ee](https://github.com/AppsGanin/rospanel/commit/7df42ee5ee59a65ff507bf6f42712cd6fa944e59))

## [5.12.0](https://github.com/AppsGanin/rospanel/compare/v5.11.1...v5.12.0) (2026-11-02)


### Features

* **servers:** copy routing and connections from another server ([f6a2afa](https://github.com/AppsGanin/rospanel/commit/f6a2afabaf6177bdb3eeb5636d927c2544cd9432))

## [5.11.1](https://github.com/AppsGanin/rospanel/compare/v5.11.0...v5.11.1) (2026-10-30)


### Bug Fixes

* **xray:** skip an inbound of a protocol this build does not know ([3f65969](https://github.com/AppsGanin/rospanel/commit/3f65969f47e9a113dcacf4257475b7cff71ae2cd))

## [5.11.0](https://github.com/AppsGanin/rospanel/compare/v5.10.0...v5.11.0) (2026-10-27)


### Features

* **alerts:** rules on a server's figures, told to the admin chats and webhooks ([e23d980](https://github.com/AppsGanin/rospanel/commit/e23d9802a261f869d430e1a4d773e042e6bed1f8))
* **external:** name relayed entries by a template, flag undelivered WireGuard ([06a046a](https://github.com/AppsGanin/rospanel/commit/06a046a25a3fdecee4d85224eaf93253b06c0859))


### Bug Fixes

* **audit:** label a retired settings section and show nested values in the journal ([434709c](https://github.com/AppsGanin/rospanel/commit/434709cb0b680c6fd50fc3d4baa6d549e6bb2dfc))

## [5.10.0](https://github.com/AppsGanin/rospanel/compare/v5.9.0...v5.10.0) (2026-10-23)


### Features

* **billing:** exit survey for users who did not renew ([cf4709f](https://github.com/AppsGanin/rospanel/commit/cf4709fce21afbc8c14b4f02d9fa3856f8ae9afd))
* **extsub:** relay through many servers, with routing of its own ([03782e9](https://github.com/AppsGanin/rospanel/commit/03782e935055242b1e2e160beb1f0b16f4c07c54))
* **extsub:** WireGuard servers from a provider ([03782e9](https://github.com/AppsGanin/rospanel/commit/03782e935055242b1e2e160beb1f0b16f4c07c54))
* **incidents:** give users the downtime back ([cf4709f](https://github.com/AppsGanin/rospanel/commit/cf4709fce21afbc8c14b4f02d9fa3856f8ae9afd))
* **nodes:** a server's WARP and Opera as configs of their own ([03782e9](https://github.com/AppsGanin/rospanel/commit/03782e935055242b1e2e160beb1f0b16f4c07c54))


### Bug Fixes

* **xray:** pin Xray-core v26.10.21, the old release is gone ([9499e66](https://github.com/AppsGanin/rospanel/commit/9499e66b9ac7fb726405b7e8e5d2aa3c3a3968b9))


### Reverts

* **nodes:** drop a server's WARP and Opera as configs of their own ([6db78d1](https://github.com/AppsGanin/rospanel/commit/6db78d1304b10b872cfbbe4601ce0ef3b59cee1b))

## [5.9.0](https://github.com/AppsGanin/rospanel/compare/v5.8.0...v5.9.0) (2026-10-21)


### Features

* **billing:** operator-set largest top-up ([43bc313](https://github.com/AppsGanin/rospanel/commit/43bc3135467495abff276d54b656b9726863062b)), closes [#122](https://github.com/AppsGanin/rospanel/issues/122)
* **reality:** the server's own domain is the default XHTTP masquerade ([1994d22](https://github.com/AppsGanin/rospanel/commit/1994d229bdb0689cb9a236c9ce732b08c6d70aaf))
* **sub:** title the TURN card TURN-VK ([aea5663](https://github.com/AppsGanin/rospanel/commit/aea5663a2c78b33db6ab1c7539ef9e3619ce92fa))

## [5.8.0](https://github.com/AppsGanin/rospanel/compare/v5.7.0...v5.8.0) (2026-10-16)


### Features

* maintenance off subscriptions, scheduled broadcasts, self-service link and Telegram limits, monthly summary ([c9e7c12](https://github.com/AppsGanin/rospanel/commit/c9e7c12d229648dfab23cd6c1c076cb9d96fceb6))
* **status:** incident timeline, subscription page notices, incident webhooks ([82810f5](https://github.com/AppsGanin/rospanel/commit/82810f5bf554ebd83b312c4b483e7fa53478d307))
* **status:** incidents and maintenance on a redesigned status page ([f421a94](https://github.com/AppsGanin/rospanel/commit/f421a9478e9e3d189ea14028e99108e26540b6bc))
* **sub:** switch the subscription page off ([ec67638](https://github.com/AppsGanin/rospanel/commit/ec67638fbc966b827bc4c7f9344eb1c2687ab2d4))
* **xray:** pin Xray-core v26.10.10 ([111982d](https://github.com/AppsGanin/rospanel/commit/111982def45881c5d81e455cf5c0144bfe28d537))


### Bug Fixes

* **mieru:** keep accepting after an Accept error ([4b68919](https://github.com/AppsGanin/rospanel/commit/4b6891934d1e332ee6d1c20a80925145583d3ab0))
* **stats:** keep the traffic counted before a restart ([eed8859](https://github.com/AppsGanin/rospanel/commit/eed885939918a49d0b0ba6ff2254be0ee0a6ceb4))

## [5.7.0](https://github.com/AppsGanin/rospanel/compare/v5.6.0...v5.7.0) (2026-10-15)


### Features

* **i18n:** Persian, right to left ([f318d93](https://github.com/AppsGanin/rospanel/commit/f318d9358f7780057ce9b36be6834d2f5b0279aa))
* **mtproto:** Telegram proxy on a port of its own ([d7c5c2a](https://github.com/AppsGanin/rospanel/commit/d7c5c2a82bb1fe24c6c9becc7265b25042e7ce66))
* **reach:** check servers from other countries through Globalping ([c247167](https://github.com/AppsGanin/rospanel/commit/c24716790bb8945f961fd4310fb55232ab03c6b6))
* **web:** the language picker in the top bar, and a row on the phone ([f12e496](https://github.com/AppsGanin/rospanel/commit/f12e4960653aace5e2a86b311ca558a846256bbd))


### Bug Fixes

* **mtproto:** bound the DC port to 16 bits and reuse one record buffer ([616313e](https://github.com/AppsGanin/rospanel/commit/616313e5409dd4c43393d79d4ba7ed7377e312b7))
* **sub:** the Telegram proxy's Copy takes the tg:// link ([ee68384](https://github.com/AppsGanin/rospanel/commit/ee68384b88f22cda8f1340dda546bfbd5a22fa27))
* **update:** offer a release only once its binaries are uploaded ([28b8a32](https://github.com/AppsGanin/rospanel/commit/28b8a32a84ed3a06625b0c11cd27d8d34e488e1e))

## [5.6.0](https://github.com/AppsGanin/rospanel/compare/v5.5.0...v5.6.0) (2026-10-15)


### Features

* **update:** follow node updates, update the master from Servers, schedule only in settings ([3355f9c](https://github.com/AppsGanin/rospanel/commit/3355f9cce2c052de874700e0d3cad99e712cbbd4))

## [5.5.0](https://github.com/AppsGanin/rospanel/compare/v5.4.0...v5.5.0) (2026-10-15)


### Features

* **dns:** more presets, grouped by provider with IP/DoH variants ([7bb4c1f](https://github.com/AppsGanin/rospanel/commit/7bb4c1ff4f394b6312a55dff205af382c9b6ef18))

## [5.4.0](https://github.com/AppsGanin/rospanel/compare/v5.3.0...v5.4.0) (2026-10-14)


### Features

* **i18n:** Simplified Chinese and Arabic, with a right-to-left layout ([060c82f](https://github.com/AppsGanin/rospanel/commit/060c82f5d24ea7f641db42dc4523ce4314ffbf1f))

## [5.3.0](https://github.com/AppsGanin/rospanel/compare/v5.2.1...v5.3.0) (2026-10-14)


### Features

* **hostfw:** open the server's ports in ufw, iptables and firewalld ([4453d0f](https://github.com/AppsGanin/rospanel/commit/4453d0fd69787fdf8233be2abb06bd103d0edca8))
* **masquerade:** serve the decoy over HTTP/3, draw REALITY's donor and port ([8ab3128](https://github.com/AppsGanin/rospanel/commit/8ab3128f168426fa3227f3620b7b769bc172d1ae))
* **notify:** tell the admins when a lane leaves or rejoins subscriptions ([3b4918f](https://github.com/AppsGanin/rospanel/commit/3b4918fb75f7a0f3b40c77b2488e443ee70544ac))
* **routing:** cascade — leave through our own servers ([9bf91cc](https://github.com/AppsGanin/rospanel/commit/9bf91ccf3e55d5627e6a1b67ca7523e407efcd41))
* **sub:** leave a lane that is down out of subscriptions ([d1b41e7](https://github.com/AppsGanin/rospanel/commit/d1b41e7b7d3d068db53ee27b3bb5d0bb53c95a8d))


### Bug Fixes

* **i18n:** reword the daily scanner digest ([fc2860d](https://github.com/AppsGanin/rospanel/commit/fc2860d5866113d1654250ed824ac17756d70f28))

## [5.2.1](https://github.com/AppsGanin/rospanel/compare/v5.2.0...v5.2.1) (2026-10-13)


### Bug Fixes

* **awg:** open iptables FORWARD for the tunnel on Docker/ufw hosts ([0ce5b19](https://github.com/AppsGanin/rospanel/commit/0ce5b19acd43f5b96d2213919c971edf13ce66a2))

## [5.2.0](https://github.com/AppsGanin/rospanel/compare/v5.1.0...v5.2.0) (2026-10-13)


### Features

* AnyTLS lane, VLESS Encryption/CDN/pinned cert, UUID reset, per-client protocol rules ([64f96b6](https://github.com/AppsGanin/rospanel/commit/64f96b6d8840c60f839fc26e8ac994e0b744cac4))
* **mieru:** mieru lane on a port of its own ([2655978](https://github.com/AppsGanin/rospanel/commit/26559789e2a52acb3041bddedcf13a0242cd4a9b))
* **sub:** NaiveProxy for Throne (with UDP), Exclave and husi; AmneziaWG 3.1 for ClashMi, ClashMetaForAndroid, Clash Verge Rev, Mihomo Party, Koala Clash and Throne ([2655978](https://github.com/AppsGanin/rospanel/commit/26559789e2a52acb3041bddedcf13a0242cd4a9b))

## [5.1.0](https://github.com/AppsGanin/rospanel/compare/v5.0.1...v5.1.0) (2026-10-12)


### Features

* **naive:** NaiveProxy lane through the :443 decoy ([24ece96](https://github.com/AppsGanin/rospanel/commit/24ece965d7cbbb3bd285e6efbc0a5f105f4b2d9d))
* **stats:** traffic by protocol, per server ([24ece96](https://github.com/AppsGanin/rospanel/commit/24ece965d7cbbb3bd285e6efbc0a5f105f4b2d9d))
* **sub:** AmneziaWG 3.1 in the Clash profile for mihomo 1.19.30+ and FlClash 0.8.99+, and an amneziawg:// line for INCY ([24ece96](https://github.com/AppsGanin/rospanel/commit/24ece965d7cbbb3bd285e6efbc0a5f105f4b2d9d))


### Bug Fixes

* **nodes:** validate a server's host on create and edit ([24ece96](https://github.com/AppsGanin/rospanel/commit/24ece965d7cbbb3bd285e6efbc0a5f105f4b2d9d))

## [5.0.1](https://github.com/AppsGanin/rospanel/compare/v5.0.0...v5.0.1) (2026-10-09)


### Bug Fixes

* **web:** open the editor's sources zip as a draft again ([eb96f84](https://github.com/AppsGanin/rospanel/commit/eb96f849ce4d12f3eab77dab48496b31110d5c56))

## [5.0.0](https://github.com/AppsGanin/rospanel/compare/v4.4.0...v5.0.0) (2026-10-09)


### Features

* **plugins:** community plugins — sandboxed engine, extension points, in-panel editor ([#108](https://github.com/AppsGanin/rospanel/issues/108)) ([4453864](https://github.com/AppsGanin/rospanel/commit/4453864e2044ebfc6322cf3f756018918fbe8f5f))


### Bug Fixes

* **webhooks:** size the delivery batch by the items, not a product of two lengths ([bd6e8e3](https://github.com/AppsGanin/rospanel/commit/bd6e8e38611c2c7f5c82ae46c16c1a6ccb46a51c))


### Miscellaneous Chores

* release 5.0.0 ([ac62d48](https://github.com/AppsGanin/rospanel/commit/ac62d48e738c030116adf44bc772e6ce57304e58))

## [4.4.0](https://github.com/AppsGanin/rospanel/compare/v4.3.0...v4.4.0) (2026-10-06)


### Features

* **sub:** your own page keeps the subscription settings ([ae2713e](https://github.com/AppsGanin/rospanel/commit/ae2713eea6dd270e1eddf45a44518b1b142b1c2d))
* **sub:** your own subscription page — browsers go to your site, the view carries tg_link ([e321f4c](https://github.com/AppsGanin/rospanel/commit/e321f4c9afce03028d05098de6156aa8205fd31c))
* **telegram:** a mailing switch on the bot's screens, admin notices that tell users apart ([6929491](https://github.com/AppsGanin/rospanel/commit/6929491a19811f1a74722dfc03d6b1c3e9b0c081))
* user agreement and privacy policy ([48f32e5](https://github.com/AppsGanin/rospanel/commit/48f32e592aa8e234cb84e182f746b2e386af7048))
* **webhooks:** reach users without Telegram; mailing and language over the API ([50ffdf1](https://github.com/AppsGanin/rospanel/commit/50ffdf1407518a5ee1b850ad59b47fde3db1b12e))
* **webhooks:** reminders, a durable outbox and the events an external system needs ([3f3b463](https://github.com/AppsGanin/rospanel/commit/3f3b46332c294a33698c7a755c90edfc9030e997))

## [4.3.0](https://github.com/AppsGanin/rospanel/compare/v4.2.0...v4.3.0) (2026-10-01)


### Features

* **api:** an API key holds its own permissions instead of a role ([835feb4](https://github.com/AppsGanin/rospanel/commit/835feb44a2c00edd062063d772ca16dbe801757e))
* **api:** methods only for keys; retire the API-only permissions ([a3a6b97](https://github.com/AppsGanin/rospanel/commit/a3a6b97440f8480a92b1a6727b11863de53d4d77))
* **api:** tick exact API methods for a key ([110a72b](https://github.com/AppsGanin/rospanel/commit/110a72bfdfb9b8d2318a78f98587bf21505a117b))
* **api:** website sign-up under the self-registration rules ([07b417e](https://github.com/AppsGanin/rospanel/commit/07b417e3aa0ceb0f6d1723b2b53aceae13a7b0f5))
* **sub:** an access card — the page is the account, keep its link ([48f8352](https://github.com/AppsGanin/rospanel/commit/48f835268125456c13d89db956ead5de21bd45b2))
* **sub:** the device add-on names its term — the date and the days left ([e859d3b](https://github.com/AppsGanin/rospanel/commit/e859d3b0a9295bdf6bbdb22d99cbc5c2c1a6c47d))
* **telegram:** ask before a link code moves a chat off another account ([5b095f2](https://github.com/AppsGanin/rospanel/commit/5b095f2bfa3f586458b22be1b08513b1fc1a10f9))
* **telegram:** change Telegram from the subscription page, with switches ([f781103](https://github.com/AppsGanin/rospanel/commit/f781103772308160f74feaa704fab3cb82304155))

## [4.2.0](https://github.com/AppsGanin/rospanel/compare/v4.1.0...v4.2.0) (2026-09-29)


### Features

* **billing:** a journal of what the payment systems sent ([acbd3d5](https://github.com/AppsGanin/rospanel/commit/acbd3d5f1c0526609468af8f3368bf775f602d54))
* **billing:** flexible plans — extra devices, traffic packs, plan changes ([0589051](https://github.com/AppsGanin/rospanel/commit/0589051f908facb5b8a9942280d847978c2b1412))
* **billing:** source tags on bot links and a funnel per source ([4c780aa](https://github.com/AppsGanin/rospanel/commit/4c780aa735f215c6f717e9e5c14e04aa790e40b0))
* **billing:** suspicious activity on the payments page ([ae04ecc](https://github.com/AppsGanin/rospanel/commit/ae04ecca5e26959cd315c7df162a4a83e00e6c08))
* **payments:** YooMoney wallet transfers and Telegram Stars ([9f90082](https://github.com/AppsGanin/rospanel/commit/9f900820004fd9c51c6d0e524dccbf68d43d27fd))
* **sub:** a clearer active-plan block on the subscription page ([f61931b](https://github.com/AppsGanin/rospanel/commit/f61931b5d92c582d8735d74ec6f1d70f710d734a))
* **sub:** AmneziaWG, TURN and single configs fold away by default ([2172df7](https://github.com/AppsGanin/rospanel/commit/2172df7e217eb7bb926008ef536d249fa3201d9f))
* **sub:** Mini App entrance, a connect wizard, a home-screen page, Telegram look ([b1edd26](https://github.com/AppsGanin/rospanel/commit/b1edd2680e7916d1d4b8d4ba059bcb6f0940d10b))
* **telegram:** automatic messages on the customer's way ([3f91bab](https://github.com/AppsGanin/rospanel/commit/3f91babb3594ec93138438cbb6c75e784dae3b80))
* **telegram:** refuse signups to accounts on the shared VPN blacklist ([cd81586](https://github.com/AppsGanin/rospanel/commit/cd81586325fb44110fd3eb0f5e8f61635a89898b))


### Bug Fixes

* **billing:** extra devices cost the same per device whatever the count ([0ad8441](https://github.com/AppsGanin/rospanel/commit/0ad8441fb0966eb414bda4ae26d95654c7b02bcd))
* **billing:** what the second review of the money fixes found ([e4ca184](https://github.com/AppsGanin/rospanel/commit/e4ca1847319f6e56488aa47e0ed21016a4daed32))
* **sub:** drop the home-screen block from the subscription page ([e8211fd](https://github.com/AppsGanin/rospanel/commit/e8211fd15e2994a8c8b4124309433bfbbb908cb4))
* **sub:** what the review of the Mini App entrance found ([7e0116e](https://github.com/AppsGanin/rospanel/commit/7e0116e5a1980c31073d8cfb804e6113927c4d3c))
* **telegram:** move the menu button off the old Mini App address ([7e54f54](https://github.com/AppsGanin/rospanel/commit/7e54f546afbbb74e871213b63f701a0bfbdfa29f))
* **web:** biome's exhaustive-deps on the automatic-message and blacklist panels ([a363017](https://github.com/AppsGanin/rospanel/commit/a363017fbe2a5828575cb5574f8e81bf7a774402))
* **web:** name a payment provider's field in its error, not its dictionary key ([eab4c2b](https://github.com/AppsGanin/rospanel/commit/eab4c2bce594362b2b4f3413802ad055a63f5cb7))
* what the review of today's features found ([a1d81e9](https://github.com/AppsGanin/rospanel/commit/a1d81e95a645b2ea54cc781820f2adcc70b10cfd))
* **xray:** keep only a real port as an inbound's public port ([cf641e1](https://github.com/AppsGanin/rospanel/commit/cf641e1aca8126df3add54c6397e8995bbd4e2f3))

## [4.1.0](https://github.com/AppsGanin/rospanel/compare/v4.0.0...v4.1.0) (2026-09-28)


### Features

* **api:** what your own bot or user cabinet needs, and a selling permission ([3a390eb](https://github.com/AppsGanin/rospanel/commit/3a390eb94b66927d4fe10be54e282a200567fe2a))
* **billing:** balance, promo codes and a referral programme ([20b81dc](https://github.com/AppsGanin/rospanel/commit/20b81dce5f493ed1af28ab32f70e69c6bc808413))
* **billing:** chargebacks, win-back codes and a sales funnel ([48cd7db](https://github.com/AppsGanin/rospanel/commit/48cd7db3b972decb9006d9b3f4f4974290bbcb11))
* **journal:** logs and cancelled orders are kept for a year ([1b06cf2](https://github.com/AppsGanin/rospanel/commit/1b06cf2baad5b0a27df15f548679e269d9d551a2))
* **update:** the panel updates itself and its servers on a schedule ([dc32d21](https://github.com/AppsGanin/rospanel/commit/dc32d21cda380d6df948f72915d54091510b6bc1))

## [4.0.0](https://github.com/AppsGanin/rospanel/compare/v3.9.0...v4.0.0) (2026-09-23)


### ⚠ BREAKING CHANGES

* **roles:** the MCP address …/v1/mcp/<key>/write is gone — one address per key, its tools decided by the key's role; the plain address of a key issued before now offers that key's write tools too, so give an assistant that should only look a key with a read-only role. The administrator role no longer reaches the Telegram bots, backups, restore or the factory reset, which are the owner's. Creating and revoking a full-access API key is the owner's.

### Features

* **roles:** roles are permission sets the owner builds ([2a803b3](https://github.com/AppsGanin/rospanel/commit/2a803b38a7a8d76eabcd72490e50392978e0263d))


### Bug Fixes

* **core:** shutdown no longer waits out a template download ([d3c8102](https://github.com/AppsGanin/rospanel/commit/d3c810236a82ec405d02d3568dd17536dc47a1ec))
* **ui:** an empty date reads as a dash ([a690219](https://github.com/AppsGanin/rospanel/commit/a6902193bbcd5c62546f7a3abfdd0cccea333383))

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
