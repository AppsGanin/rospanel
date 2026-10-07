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
theme/theme.css, …     a theme of the subscription page, with its images and fonts
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
| `provides.hooks` | `beforeSignup`, `beforeDeviceBind`: the plugin takes part in these decisions — see below. |
| `provides.bot` | `{"menu": true, "commands": [{"command", "description"}]}`: buttons and commands in the panel's user bot. |
| `provides.price` | `true`: the plugin prices plans per user (`quotePrice`). Experimental. |
| `provides.subscription` | `true`: the plugin rewrites what clients are served (`transformSubscription`). Experimental. |
| `provides.theme` | `true`: `theme/theme.css` restyles the subscription page — see below. |
| `experimental` | The experimental points the plugin uses (`channel`, `price`, `subscription`): they may still change. |
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

## Screens of the panel

These are drawn by the panel from plain values — a plugin never puts HTML in front of
an admin or a user.

| `provides` | Export | Shows |
|---|---|---|
| `user_fields: [{key, label}]` | `userFields(userId)` → `{key: value}` | values on the user's card (asked within 0.3 s) |
| `actions: [{key, label, scope, perm, confirm}]` | `onAction({key, user_ids})` → `{ok, message}` | buttons: `user` on the card, `users` for the users list's selection, `global` on the plugin's card. Only admins holding `perm` (`users.manage` by default) see and press them; every press is in the panel's journal |
| `widgets: [{key, label}]` | `widget(key)` → `{type: "stat"\|"table"\|"list", …}` | dashboard tiles, cached for a minute |
| `sub_blocks: true` | `subBlocks({user, lang})` → `[{type, text, label, url}]` | blocks at the top of the subscription page (`text`, `notice`, `markdown` without raw HTML, `button` to https); asked within 0.3 s and cached for 5 minutes per user. A page of your own gets them as `blocks` in `GET /v1/users/{id}/subscription` |
| `channel: {label}` | `channel.send(msg)` | delivers messages, broadcasts and reminders to the users the panel's bot does not reach — see `ChannelMessage` in `rospanel.d.ts`. Experimental: list `"channel"` in `experimental` |

Examples: [email](../../examples/plugins/email) (a channel with user fields and a widget),
[announcement](../../examples/plugins/announcement) (page blocks and a button).

## Decisions

The panel asks these before it acts. Each is answered within 0.3 s; an error, a timeout or a
plugin busy with something else counts as "no objection", so a broken plugin never stops
anyone signing up or paying. Inside them `panel.api` only reads.

| Export | Asked | Answer |
|---|---|---|
| `beforeSignup(req)` | before a new account: the bot, the Mini App, `POST /v1/signup` (`channel` says which). An account that already exists is not asked about | `{allow, reason}` or `false` |
| `beforeDeviceBind(req)` | before a device the user has not bound yet takes a slot (device limits on). A refusal is kept for a minute | `{allow, reason}` or `false` |
| `quotePrice(req)` | while pricing a plan or its renewal for a user: `base_rub` is the panel's price after its period discount, before a promo code | `{price_rub, note}`, or `null` |

- Several plugins are asked in their order; the first refusal wins.
- `reason` and `note` are keys of the plugin's `i18n/` files (or plain text): the person reads them.
- A price counts only from half of `base_rub` to `base_rub` — never more, never free; otherwise
  it is ignored and the plugin's log says why. The order keeps the price it was opened with.
  Prices are kept for a minute per user and plan.

[examples/plugins/channel-gate](../../examples/plugins/channel-gate) lets only a channel's
subscribers sign up through Telegram.

## The bot

```js
export const bot = {
  menu({ user, lang }) { return [{ text: "🎁 Bonus", data: "bonus" }]; },
  onCallback({ user, data, lang }) { return { text: "Done", buttons: [{ text: "Site", url: "https://…" }] }; },
  onCommand({ user, command, args, lang }) { return "Plain text"; },
};
```

- `menu` adds buttons under the bot's own menu (asked within 0.3 s, kept for 5 minutes). A
  press comes to `onCallback` with its `data` (up to about 20 bytes, so `px:<id>:<data>` fits
  Telegram's 64).
- `commands` are listed in the bot's command menu next to `/start`; `onCommand` answers them.
  `args` is the text after the command.
- An answer is plain text — the panel escapes it — with rows of buttons: `data` comes back to
  `onCallback`, `url` opens an https page. The bot adds the way back to its menu. 3 s per answer;
  past it the person sees "try later".
- `user.id` is 0 for a Telegram without an account.

## Subscriptions (experimental)

```js
export function transformSubscription({ user, format, links, config }) {
  if (format === "links") return { links: links.map(l => l.replace(/#.*/, "#My VPN")) };
  return null; // as it is
}
```

`format` is `links` (share links), `clash` (`config` is the YAML text), `singbox` or `xray`
(`config` is the JSON document). Return the same shape, or `null`. Every client refresh passes
here, so an answer is kept for 5 minutes per user and input, and each plugin has 0.3 s; an answer
in another shape, a link that is not a proxy link, or a failure serves the panel's own profile.

## A theme

```
theme/theme.css     the stylesheet, applied over the page's own
theme/stars.svg     images (png, jpg, webp, gif, svg) and fonts (woff2, woff) it uses
```

The page stays the panel's — payments, devices, Telegram all keep working — and the theme
changes how it looks. Its colours are CSS variables: `--brand`, `--bg`, `--surface`, `--ink`,
`--muted`, `--on-brand`, `--success-fg`, `--warning-fg`, `--danger-fg`; overriding them is most
of a theme. `url()` may name only a file of `theme/` (or a `data:image/`, `data:font/` URI) and
`@import` is refused: a page that loads from elsewhere hangs where that host is blocked. Up to 50
files, 1 MB each, 3 MB together. With several themes switched on, the first in order applies.
See [examples/plugins/theme-night](../../examples/plugins/theme-night).

## Big data: blobs

A blob is data the panel keeps outside the plugin's memory for the length of one call: an
export to upload, a file to download.

```js
const csv = panel.blob.from(text);                                   // or {base64}
panel.http.fetch(url, { method: "PUT", body: csv });                   // sent as it is
panel.http.fetch(url, { method: "POST", form: {                        // multipart/form-data
  chat_id: "42", document: { blob: csv, filename: "users.csv", type: "text/csv" } } });
const file = panel.http.fetch(url, { blob: true }).body;               // the answer as a blob
const big = panel.api("GET", "/v1/…", null, { blob: true }).body;      // the same for the API
panel.blob.hash(file, "sha256"); panel.blob.text(file);                // text: up to 4 MB
```

Up to 256 MB a blob, 16 blobs and 512 MB a call; a handle kept for another call is dead. See
[examples/plugins/users-csv](../../examples/plugins/users-csv).

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
| `panel.blob.*` | `from`, `hash`, `text` — see above. |
| `panel.crypto.*` | `hash`, `hmac` (md5, sha1, sha256, sha512), `sign`/`verify` (RS256, RS512, ES256, Ed25519), `jwt`, `randomHex`, `randomUUID`. |
| `panel.t(key, params, lang)` | Strings from `i18n/`. |
| `panel.log.*`, `console.*` | The plugin's log in the panel. |

## Limits

| | |
|---|---|
| Memory | `memory_mb` (32 MB). Past it the call throws `out of memory`. |
| Time | 10 s for an event or a request, 15 s for a payment call, 60 s for a cron job, 3 s in the bot, 0.3 s for a decision or a subscription. Past it the call throws `interrupted`. |
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

## The catalog

The panel lists the community catalog under **Settings → Plugins → Catalog**: install and
update from there go through the same consent screen as a zip. The catalog's index is signed
with a key built into the panel, and a package is installed only if its sha256 is the one in
the index. "Reviewed" marks a version whose code the catalog's maintainers read. The panel tells
the operator about a new version once and never updates by itself.

To publish a plugin, open a pull request in
[rospanel-plugins](https://github.com/AppsGanin/rospanel-plugins) with
`plugins/<id>/<id>-<version>.zip` and a link to its source. Where GitHub is unreachable, an
operator can point the panel at a mirror — a copy of that folder; the signature is the same.

## How the panel treats a plugin

- It runs in a WebAssembly sandbox: no files, no processes, no network except through
  `panel.http.fetch`.
- The operator sees what it asks for before installing and can read `main.js` in
  the panel.
- It runs on the main server only.
- Its database is a file of its own (`plugins/<id>.db` in the data directory) and goes
  into the panel's backups.
