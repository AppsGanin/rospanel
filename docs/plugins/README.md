# Writing RosPanel plugins

[Русская версия](README-RU.md)

A plugin is JavaScript that runs inside the panel: it reacts to the panel's events,
runs on a schedule, keeps its own data and calls the panel's REST API and the
internet — only as far as the operator agreed when installing it.

Plugins are in beta. This page describes what the panel runs today.

## Quick start

All the tools are in the `rospanel` binary; no Node is needed.

```sh
rospanel plugin new my-plugin   # a working plugin from the template
cd my-plugin
rospanel plugin test .          # run test.js
rospanel plugin dev .           # try it by hand; reloads on save
rospanel plugin pack .          # my-plugin-0.1.0.zip, installable in the panel
```

Install the zip in the panel: **Settings → Plugins → Install**. The panel shows what
the plugin asks for, installs it switched off, and you switch it on after filling in
its settings.

[examples/plugins](../../examples/plugins) has complete plugins with tests.

## The package

```
plugin.json            the manifest
main.js                all the code, one ES module (a bundler may build it)
migrations/0001_*.sql  the plugin's own database, applied in name order
i18n/ru.json, en.json  strings for panel.t()
README.md, icon.svg    shown in the panel
```

`pack` leaves everything else out: `test.js`, `rospanel.d.ts`, sources a bundler
reads. `main.js` cannot `import` other files.

## plugin.json

```json
{
  "id": "my-plugin",
  "version": "1.0.0",
  "api": 1,
  "panel": ">=4.5.0",
  "name": {"ru": "Мой плагин", "en": "My plugin"},
  "description": {"ru": "…", "en": "…"},
  "author": "you",
  "permissions": ["users.view"],
  "net": ["api.example.com"],
  "settings": [
    {"key": "token", "kind": "secret", "label": "API token"}
  ],
  "provides": {
    "events": ["user.created", "payment.paid"],
    "cron": [{"name": "sync", "schedule": "*/15 * * * *"}]
  },
  "db_quota_mb": 100
}
```

| Field | |
|---|---|
| `id` | 3-40 characters of `a-z`, `0-9`, `-`. Never changes: it names the database file and the audit entries. |
| `version` | `X.Y.Z`. |
| `api` | `1`. |
| `panel` | The oldest panel version the plugin runs on, as `>=X.Y.Z`. |
| `name`, `description` | A string, or `{"ru": …, "en": …}`. |
| `permissions` | What `panel.api` may do — the same names as API key permissions (`users.view`, `billing.manage`, …). Never granted: `api.manage`, `security.manage`, `system.update`, `webhooks.manage`, `plugins.*`. |
| `net` | The hosts `panel.http.fetch` may reach: `api.example.com`, `*.example.com`, optionally with `:port`. |
| `settings` | The form the operator fills in. `kind`: `text`, `secret`, `bool`, `number`, `textarea`, `select` (with `options`). `optional`, `default`, `help`, `placeholder`. A `secret` is stored encrypted and never shown again. |
| `provides.events` | Event names: the same as the webhooks' (see `docs/api.md`). The plugin must export `onEvent`. |
| `provides.cron` | Jobs: `name` is an exported function, `schedule` a five-field cron expression in the panel's time zone, at most once a minute. |
| `provides.payment` | `{"label": …, "note": …}`: a payment method. The plugin exports `payment.create`, `payment.status`, `payment.webhook` — see below. |
| `provides.http` | `true`: the plugin answers requests at its own address. It exports `onHttp`. |
| `db_quota_mb` | The database size limit, 100 by default, up to 1024. |
| `memory_mb` | The JavaScript heap limit, 32 by default, up to 128. |

## main.js

The panel calls what `main.js` exports:

```js
export function onEnable() { /* optional: once when switched on */ }

/** @param {PanelEvent} e — {id, event, created_at, data} */
export function onEvent(e) {
  if (e.event === "user.created") panel.log.info("new user", e.data);
}

export function sync() { /* a cron job */ }
```

- Every `panel.*` call is synchronous and returns its answer: no callbacks, no
  timers. `async`/`await` work, but nothing waits on time.
- A delivery of an event is at-least-once: keep `e.id` to skip a repeat.
- A thrown error is logged. An event whose `onEvent` threw is delivered again later
  (after 10 s, 30 s, 2 min, 10 min).
- Keep state in `panel.kv` or `panel.db`: the panel may restart the plugin between
  any two calls.

## A payment method

```js
export const payment = {
  create({ amount_rub, order_id, description, return_url, webhook_url, email }) {
    return { provider_id: "inv-1", pay_url: "https://pay.example/inv-1" };
  },
  status(provider_id) {
    return { status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
  webhook({ body, headers }) {          // headers are lower-case
    return { provider_id: "inv-1", status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
};
```

The method appears in **Settings → Payments** as `plugin.<id>`, and the operator switches it
on like any other. The panel opens the order, sends the payer to `pay_url`, routes the payment
system's callbacks (`webhook_url`) to `payment.webhook`, polls `payment.status` when a callback
is missed, and grants the plan.

- `status` is `paid`, `pending`, `cancelled` or `refunded`.
- A `paid` or `refunded` answer must carry `amount_kopecks` and `currency`: the panel grants
  the plan only when they match the order.
- `payment.webhook` must prove the callback is real — check its signature, or, when the
  payment system signs nothing, ask its API with `status` and report that instead of the body.
- [examples/plugins/lava](../../examples/plugins/lava) is a complete one.

## Requests from the internet

With `"http": true` the plugin answers at `https://<panel>/<callback path>/x/<id>/…` (its card in
the panel shows the address): for a shop that calls you after a sale, a form on a site, an OAuth
callback.

```js
export function onHttp({ method, path, query, headers, body, ip }) {
  return { status: 200, headers: { "Content-Type": "application/json" }, body: "{}" };
}
```

The plugin checks who is calling (a signature, a token in its settings). Bodies are up to 1 MB
each way; the answer cannot set cookies and is served sandboxed. See
[examples/plugins/shop-webhook](../../examples/plugins/shop-webhook).

## The panel API

`rospanel.d.ts` — written by `plugin new` — is the full reference, with types your
editor picks up.

| | |
|---|---|
| `panel.config` | The operator's settings, secrets included. |
| `panel.kv.get/set/delete/list` | Simple values (any JSON, up to 64 KB). |
| `panel.db.exec/query/tx` | The plugin's own SQLite database. `PRAGMA`, `ATTACH`, `VACUUM` and transaction statements are refused — use `tx(fn)`. |
| `panel.api(method, path, body)` | The panel's REST API (`/v1`, see `/v1/docs`) with the plugin's permissions. Changes are recorded in the panel's journal under the plugin's name. |
| `panel.users.get/list/update` | Shortcuts for `panel.api`. |
| `panel.http.fetch(url, opts)` | Requests to the hosts in `net`. Private and local addresses are never reachable. |
| `panel.crypto.*` | `hash`, `hmac` (md5, sha1, sha256, sha512), `sign`/`verify` (RS256, RS512, ES256, Ed25519), `jwt`, `randomHex`, `randomUUID`. |
| `panel.t(key, params, lang)` | Strings from `i18n/`. |
| `panel.log.*`, `console.*` | The plugin's log in the panel. |

## Limits

| | |
|---|---|
| Memory | `memory_mb` (32 MB). Past it the call throws `out of memory`. |
| Time | 10 s for an event or a request, 15 s for a payment call, 60 s for a cron job. Past it the call throws `interrupted`. |
| Database | `db_quota_mb`; at most 10 000 rows or 4 MB per query. |
| HTTP | 4 MB per answer, 30 s, 5 redirects within `net`. |
| `panel.api` | 1 MB per request, 4 MB per answer. |
| Package | 5 MB zipped, `main.js` up to 2 MB. |

The panel pauses a plugin after 10 failed calls in a row, when its database outgrows
the quota, or when it handles over 300 events a minute for three minutes (usually a
plugin reacting to its own changes). The operator is told and switches it back on.

## Testing

`test.js` runs in a sandbox of its own, beside the plugin:

```js
test("greets a new user", () => {
  mock.http("https://api.example.com/", { status: 200, body: { ok: true } });
  mock.api("GET", "/v1/users/*", { body: { id: 7, name: "Ann" } });

  plugin.event("user.created", { id: 7, name: "Ann" });

  assert.equal(plugin.db.query("SELECT count(*) AS n FROM greetings"), [{ n: 1 }]);
  assert.equal(mock.calls().length, 1);
});
```

`plugin.call(export, arg)`, `plugin.event(name, data)`, `plugin.cron(name)` call the
plugin; `plugin.kv` and `plugin.db` read and seed its storage; `mock.http` and
`mock.api` answer what it asks for — anything unmocked is refused; `crypto.hmac` and friends sign
what a test sends, as a payment system would. Mocks are reset
before each test; the database is not. `dev.config.json` holds the settings `test`
and `dev` run with.

`rospanel plugin dev .` gives a prompt (`event`, `call`, `cron`, `kv`, `sql`, `mock`,
`logs`). With `--panel https://host/<api path> --key <API key>`, `panel.api` goes to
that panel for real.

## Updates

Install a new zip over the old one from the plugin's **Update** button. The panel keeps
the previous version and a copy of the database taken before the new migrations:
**Roll back** restores both. Never edit a migration that shipped — add a new one;
`rospanel plugin pack . --prev old.zip` checks this.

An update asking for more permissions or hosts shows the operator the difference.

## How the panel treats a plugin

- It runs in a WebAssembly sandbox: no files, no processes, no network except through
  `panel.http.fetch`.
- The operator sees what it asks for before installing and can read `main.js` in
  the panel.
- It runs on the main server only.
- Its database is a file of its own (`plugins/<id>.db` in the data directory) and goes
  into the panel's backups.
