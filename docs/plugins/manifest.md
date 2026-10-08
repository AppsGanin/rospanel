# Manifest and package

[Русская версия](manifest-RU.md) · [Contents](README.md)

## The package

A plugin is a zip with a fixed set of files:

```
plugin.json              the manifest (required)
main.js                  all the code, one ES module (required, up to 2 MB)
migrations/NNNN*.sql     its own database, applied in name order (up to 100 files, 256 KB each)
i18n/<lang>.json         strings for panel.t(): {"key": "text"} (up to 256 KB)
theme/theme.css, …       a theme of the subscription page (see "Extension points")
README.md                shown in the panel (up to 256 KB)
icon.svg                 the icon (up to 128 KB)
LICENSE, CHANGELOG.md
```

- Any other file fails the install: the operator reviews everything there is.
- One common top-level folder is accepted (what zipping a folder produces); `__MACOSX` and
  `.DS_Store` are skipped.
- Up to 5 MB zipped and 12 MB unpacked, at most 200 files. Symbolic links and `../` paths
  are refused.
- `main.js` cannot import other files. If you prefer modules or TypeScript, build one file
  with a bundler (esbuild, rollup) in ESM format.

`rospanel plugin pack .` puts only these files into the zip and leaves the rest (`test.js`,
`dev.config.json`, `rospanel.d.ts`, sources) out.

## plugin.json in full

```json
{
  "id": "my-plugin",
  "version": "1.2.0",
  "api": 1,
  "panel": ">=4.4.0",
  "name": {"ru": "Мой плагин", "en": "My plugin"},
  "description": {"ru": "…", "en": "A sentence or two on what it does"},
  "author": "Name or nickname",
  "homepage": "https://github.com/you/my-plugin",
  "license": "MIT",

  "permissions": ["users.view", "users.manage"],
  "net": ["api.example.com", "*.example.org:8443"],
  "settings": [ … ],
  "provides": { … },
  "experimental": [],

  "db_quota_mb": 100,
  "memory_mb": 32
}
```

## Fields

### Who the plugin is

| Field | Required | |
|---|---|---|
| `id` | yes | 3–40 characters: `a-z`, `0-9`, `-`, starting with a letter. Not `rospanel`, `core`, `builtin`, `panel`, `plugin`, `system`. Never changes: it names the database file, the settings and the journal entries. |
| `version` | yes | `X.Y.Z`. An update installs over the old one only with the same `id`. |
| `api` | yes | `1`. |
| `panel` | no | `>=X.Y.Z` — the oldest panel the plugin runs on. An older panel refuses it, saying why. |
| `name` | yes | A string or `{"ru": …, "en": …}`, up to 80 characters. |
| `description` | no | The same, up to 500 characters. |
| `author`, `license` | no | Up to 100 and 50 characters. |
| `homepage` | no | `http(s)://…` — where the sources and questions go. |

Text in several languages is an object with two-letter codes: `{"ru": "…", "en": "…"}`. The
panel takes the admin's language, then English, then Russian, then any.

### What it may do

**`permissions`** — permissions for `panel.api`, the same as API keys':

| Permission | Opens |
|---|---|
| `users.view` | reading users, their subscriptions, devices, traffic |
| `users.manage` | creating and changing users, extending, enabling and disabling |
| `users.delete` | deleting users ⚠️ |
| `users.export` | exporting users |
| `groups.view`, `groups.manage` | access groups |
| `stats.view`, `stats.manage` | statistics; `manage` — resetting ⚠️ |
| `billing.view`, `billing.manage` | plans, orders, balance, promo codes; `manage` — changing ⚠️ |
| `payments.manage` | payment provider settings ⚠️ |
| `broadcasts.manage` | broadcasts and messages to users |
| `servers.view`, `servers.manage` | servers and nodes |
| `routing.view`, `routing.manage` | routing |
| `settings.view`, `settings.manage` | panel settings; `manage` — changing ⚠️ |
| `security.view` | the sign-in and ban journal |
| `logs.view`, `audit.view` | logs and the action journal |

⚠️ — "risky" permissions, marked red on the consent screen. Never granted: `owner`,
`api.manage`, `security.manage`, `system.update`, `webhooks.manage`, `plugins.view`,
`plugins.manage` — a plugin cannot mint keys, change admins, update the panel or install
other plugins.

Which permission a route needs is shown in the panel's `/v1/docs`. Ask for less: the operator
reads this list before installing.

Extension points that hand the plugin users' data, or let it decide for them, need a
permission too — the "Needs" column of [`provides`](#what-it-answers). Without it validation
fails: `provides.price needs the "billing.manage" permission: it sets what users pay`.

Only an admin holding every requested permission can install or update the plugin; a rollback
needs the permissions of the version it brings back.

**`net`** — hosts for `panel.http.fetch`, up to 20:

- `api.example.com` — exactly this host, ports 80 and 443;
- `*.example.com` — any subdomain (but not `example.com` itself), ports 80 and 443;
- `api.example.com:8443` — this port only.

Lower-case names only: IP addresses and names without a dot are refused. Even through an
allowed name the panel never connects to its own addresses or to private, loopback, CGNAT,
NAT64, 6to4 and site-local ones (DNS rebinding included).

### Settings

`settings` — the form the operator fills in after installing (up to 50 fields). The plugin
sees the values in `panel.config` — **always as strings**.

```json
"settings": [
  {"key": "token", "kind": "secret", "label": "API token", "help": {"en": "In the service's dashboard"}},
  {"key": "shop_id", "kind": "text", "label": {"ru": "ID магазина", "en": "Shop ID"}, "placeholder": "12345"},
  {"key": "notes", "kind": "textarea", "label": "E-mail text", "optional": true},
  {"key": "enabled_log", "kind": "bool", "label": "Verbose log", "default": "false"},
  {"key": "limit", "kind": "number", "label": "Daily limit", "default": "100"},
  {"key": "mode", "kind": "select", "label": "Mode", "default": "test",
   "options": [{"value": "test", "label": "Test"}, {"value": "live", "label": "Live"}]}
]
```

| Property | |
|---|---|
| `key` | `a-z`, `0-9`, `_`, starting with a letter, up to 40 characters, unique. |
| `kind` | `text`, `secret`, `textarea`, `bool` (always `"true"` or `"false"`; unset is `"false"`), `number` (a string holding a number), `select` (one of `options`). |
| `label` | The label, required. |
| `help`, `placeholder` | A hint under the field and in the empty field. |
| `optional` | Not required. A required field left empty keeps the plugin from being switched on. |
| `default` | The default value (a `secret` cannot have one). A `bool` takes `"true"`/`"false"`, or `"1"`/`"0"`, read as the same. |
| `options` | For `select`: `[{"value", "label"}]`. |

A `secret` is stored encrypted and never shown in the panel again; saving the form with it
empty keeps the stored one. A value is up to 16 KB. Changing the settings restarts the
plugin's VM; the next call sees the new `panel.config`.

### What it answers

`provides` — the extension points. Each one is described in the
[extension points reference](exports.md).

| Key | Value | The plugin exports | Needs |
|---|---|---|---|
| `events` | event names, `["user.created", …]` | `onEvent` | `users.view`; `payment.*`, `plan.*`, `balance.*`, `referral.*`, `promo.*` also `billing.view`; `node.*` nothing |
| `cron` | `[{"name": "sync", "schedule": "*/15 * * * *"}]`, up to 10 | functions of those names | — |
| `payment` | `{"label": …, "note": …}` | `payment.create`, `payment.status`, `payment.webhook` | `payments.manage` |
| `http` | `true` | `onHttp` | — |
| `hooks` | `["beforeSignup", "beforeDeviceBind"]` | functions of those names | `users.view` |
| `user_fields` | `[{"key", "label"}]`, up to 20 | `userFields` | `users.view` |
| `actions` | `[{"key", "label", "scope", "perm", "confirm"}]`, up to 20 | `onAction` | — |
| `widgets` | `[{"key", "label"}]`, up to 20 | `widget` | — |
| `sub_blocks` | `true` | `subBlocks` | `users.view` |
| `bot` | `{"menu": true, "commands": [{"command", "description"}]}` | `bot.menu`, `bot.onCallback` (with `menu`), `bot.onCommand` (with `commands`) | `users.view` |
| `channel` | `{"label": …}` | `channel.send` | `users.view` |
| `price` | `true` | `quotePrice` | `billing.manage` |
| `subscription` | `true` | `transformSubscription` | `users.manage` |
| `theme` | `true` | — (needs `theme/theme.css`) | — |

If `main.js` does not export what is declared, the plugin will not switch on: "main.js does
not export what plugin.json declares: …". Declaring a point this panel version does not have
fails the install.

`cron.schedule` has five fields (`minute hour day month weekday`) in the panel's time zone;
`*`, lists `1,15`, ranges `1-5` and steps `*/10` work. Once a minute is the most often.

### Experimental points

`channel`, `price` and `subscription` may still change. A plugin using them lists them in
`"experimental": ["price"]` — the author acknowledges it, and the operator sees a mark on the
consent screen.

### Resources

| Field | Default | Maximum | |
|---|---|---|---|
| `db_quota_mb` | 100 | 1024 | the size of its database; a write past it is refused ([details](api.md#paneldb--its-own-sqlite-database)) |
| `memory_mb` | 32 | 128 | JavaScript memory; past it a call throws `out of memory` |

## Checking

```sh
rospanel plugin validate .          # a folder
rospanel plugin validate my.zip     # a built package
```

`validate` reads the package the way the panel does, loads the code and checks the exports.
Every problem is listed at once, for example:

```
error: plugin: permissions[1] "owner": never granted to plugins; net[0] "10.0.0.1": a host name like api.example.com or *.example.com, optionally with :port
```
