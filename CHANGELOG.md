# Changelog

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
