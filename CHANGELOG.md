# Changelog

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
