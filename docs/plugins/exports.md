# Extension points

[Русская версия](exports-RU.md) · [Contents](README.md)

The panel calls functions `main.js` exports. Which ones it calls is decided by `provides` in
the manifest ([manifest](manifest.md)). This page describes when each export is called, what
it gets, what it must return, and what happens on an error.

Common rules for all of them:

- **Argument and answer are JSON.** A function gets one argument and may return any JSON
  value; `undefined` and functions in the answer are dropped.
- **One call at a time.** While the plugin runs one call, the next waits. So a heavy cron job
  delays this plugin's events — but decisions (sign-up, price) and the bot menu do not wait:
  a busy plugin counts as having no objection.
- **Time is limited.** Past the limit the call is cut with `interrupted`.
- **Errors are counted.** After 10 errors in a row the panel pauses the plugin and tells the
  operator.
- **Memory does not survive between calls.** Globals live as long as the VM, and the panel
  may replace it at any moment: after a settings change, when memory grows, after an error.
  Keep state in `panel.kv` or `panel.db`.

| Export | When | Time |
|---|---|---|
| `onEnable` | switched on, and at every panel start | 10 s |
| `onEvent` | a panel event | 10 s |
| a cron job | on schedule | 60 s |
| `payment.*` | a payment | 15 s |
| `onHttp` | a request to the plugin's address | 10 s |
| `userFields` | a user's card is opened | 0.3 s |
| `onAction` | an admin pressed the plugin's button | 30 s |
| `widget` | the dashboard is opened | 2 s |
| `subBlocks` | the subscription page is opened | 0.3 s |
| `channel.send` | a message the bot did not deliver | 10 s |
| `beforeSignup`, `beforeDeviceBind`, `quotePrice` | a panel decision | 0.3 s |
| `bot.menu` | the user bot's menu | 0.3 s |
| `bot.onCallback`, `bot.onCommand` | a press or a command in the bot | 3 s |
| `transformSubscription` | a client refreshes its subscription | 0.3 s |

## onEnable

```js
export function onEnable() {
  if (!panel.kv.get("installed_at")) panel.kv.set("installed_at", Date.now());
}
```

Optional. Called after the migrations when the plugin is switched on, and at every panel
start for plugins that are on. If `onEnable` throws, the plugin does not switch on and the
operator sees the error. A good place to check the settings (a token, by asking the service)
and prepare data.

## Events — onEvent

```json
"provides": {"events": ["user.created", "payment.paid"]}
```

```js
/** @param {PanelEvent} e */
export function onEvent(e) {
  // e = {id: "3f1c…", event: "payment.paid", created_at: 1767225600, data: {...}}
  if (e.event === "payment.paid") panel.log.info("payment", e.data.amount_rub);
}
```

- **Format and fields.** `e.data` is what the panel's webhooks carry. The full list of events
  and their fields is in "Events" of [docs/api.md](../api.md#events).
- **At-least-once delivery.** The same event may come twice. Use `e.id` or a unique key in
  your database, as in the [tutorial](tutorial.md).
- **Retries on error.** If `onEvent` throws, the event comes again after 10 s, 30 s, 2 min
  and 10 min — up to 5 attempts in all.
- **No guaranteed order** between different users' events or across retries.
- **Loops.** A plugin that, for three minutes in a row, receives over 300 events a minute and
  itself makes over 100 changes a minute through `panel.api` is paused: that is a plugin
  reacting to its own changes (changing limits on `user.limits_changed`). The panel's bulk
  events — a traffic reset for thousands of users — do not pause a plugin that only reads them.

Events often needed:

| Event | When |
|---|---|
| `user.created`, `user.registered` | a user was created in the panel, or signed up by themselves (bot, Mini App, website) |
| `user.expiring` | 14, 7, 3 and 1 days before the subscription ends (`data.stage`) |
| `user.expired`, `user.limited` | the subscription ended; the traffic ran out |
| `user.traffic_low` | 80% of the traffic used |
| `payment.created`, `payment.paid`, `payment.refunded` | an order opened, paid, refunded |
| `plan.changed`, `plan.cancelled` | the plan changed or was cancelled |
| `user.device_bound`, `user.device_limited` | a new device; the device limit reached |
| `user.telegram_linked` | a Telegram was linked to the account |
| `node.down`, `node.up`, `xray.down`, `xray.up` | a server or Xray went down and came back |
| `user.message`, `broadcast.sent` | a message or a broadcast from the operator |

## Scheduled jobs

```json
"provides": {"cron": [{"name": "sync", "schedule": "*/15 * * * *"}]}
```

```js
export function sync() { /* … */ }
```

- **Schedule** — five fields in the panel's time zone (see [manifest](manifest.md)).
- **No overlaps.** If the previous run is still going, the new one is skipped.
- **Errors.** An exception is logged and counts as an error; there is no retry — the job just
  runs at its next time.

## A payment method — payment

```json
"provides": {"payment": {"label": {"ru": "Оплата картой (Lava)", "en": "Card (Lava)"}, "note": "…"}}
```

```js
export const payment = {
  create({ amount_rub, order_id, description, return_url, webhook_url, email }) {
    // open an invoice with the payment system
    return { provider_id: "inv-123", pay_url: "https://pay.example/inv-123" };
  },
  status(provider_id) {
    return { status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
  webhook({ body, headers }) {
    // check the signature! header names are lower-case
    return { provider_id: "inv-123", status: "paid", amount_kopecks: 15000, currency: "RUB" };
  },
};
```

The method appears in **Settings → Payments** as `plugin.<id>`, and the operator switches it
on like any other.

| Function | Gets | Returns |
|---|---|---|
| `create` | `amount_rub` (whole roubles), `order_id`, `description`, `return_url` (where to send the payer back), `webhook_url` (where the payment system sends callbacks), `email` (may be empty) | `{provider_id, pay_url}`: the invoice id and an http(s) link to pay |
| `status` | `provider_id` (a string) | `{status, amount_kopecks, currency}` |
| `webhook` | `{body, headers}` — the callback's body as a string and its headers | `{provider_id, status, amount_kopecks, currency}` |

- **Statuses:** `paid`, `pending`, `cancelled`, `refunded`.
- **The amount is required.** `paid` and `refunded` must carry `amount_kopecks` and
  `currency`. The plan is granted only when the amount and currency match the order.
- **Checking callbacks.** `webhook` must make sure the callback is real
  (`panel.crypto.hmac`/`verify`). If the payment system signs nothing, call
  `payment.status(id)` and return its answer, not the body.
- **Missed callbacks.** If no callback comes, the panel itself polls `status` for open orders.
- **Errors.** An exception in `create` shows the payer an error. In `webhook` the callback is
  rejected and goes into the payment callback journal; the panel still answers the payment
  system `200 ok`, and polling `status` confirms the order later.

A complete example — [examples/plugins/lava](../../examples/plugins/lava).

## Requests from the internet — onHttp

```json
"provides": {"http": true}
```

```js
export function onHttp({ method, path, query, headers, body, ip }) {
  if (query.token !== panel.config.token) return { status: 403, body: "forbidden" };
  return { status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify({ ok: true }) };
}
```

- **Address:** `https://<panel>/<secret path>/x/<id>/<path>` — shown on the plugin's card
  once it is on. `path` starts with `/`, `query` is an object "parameter → value", `headers`
  have lower-case names, `body` is a string, `ip` the client's address.
- **Answer:** `{status, headers, body}`, `body` a string. Bodies up to 1 MB each way.
- **Safety:**
  - The answer is served with `Content-Security-Policy: sandbox`, `nosniff` and `no-store`:
    a plugin's page cannot run script as the panel.
  - `Set-Cookie` and security headers in the answer are dropped; the request's cookies are
    not passed to the plugin.
- **Checking the caller is the plugin's job:** a signature, a token from its settings.

An example — [examples/plugins/shop-webhook](../../examples/plugins/shop-webhook).

## A user's card — userFields

```json
"provides": {"user_fields": [{"key": "email", "label": "E-mail"}, {"key": "ltv", "label": {"ru": "Выручка", "en": "LTV"}}]}
```

```js
export function userFields(userId) {
  const row = panel.db.query("SELECT email, ltv FROM clients WHERE user_id = ?", userId)[0];
  return row ? { email: row.email, ltv: `${row.ltv} ₽` } : {};
}
```

Gets the user's id (a number) and returns `{key: value}`. Only keys from the manifest are
shown, values as text up to 1000 characters. If the plugin does not answer within 0.3 s or
fails, its block simply does not appear.

## Admin buttons — onAction

```json
"provides": {"actions": [
  {"key": "receipt", "label": "Send a receipt", "scope": "user", "confirm": true},
  {"key": "export", "label": "Export", "scope": "users", "perm": "users.view"},
  {"key": "sync", "label": "Sync", "scope": "global", "perm": "settings.manage"}
]}
```

```js
export function onAction({ key, user_ids }) {
  if (key === "receipt") { /* user_ids = [42] */ }
  return { ok: true, message: "Done" };
}
```

| `scope` | Where the button is | `user_ids` |
|---|---|---|
| `user` | on a user's card | one id |
| `users` | in the users list's bulk actions | 1 to 1000 ids |
| `global` | on the plugin's card | `[]` |

- **Who sees the button.** Only admins holding `perm` (`users.manage` by default).
- **Confirmation.** `confirm: true` asks "are you sure?" first.
- **The answer.** `message` is shown as a toast. `ok: false` shows it as an error, and so
  does an exception.
- **The journal.** Every press is recorded in the panel's journal, and changes made through
  `panel.api` under the plugin's name.

## Dashboard widgets — widget

```json
"provides": {"widgets": [{"key": "today", "label": "Payments today"}]}
```

```js
export function widget(key) {
  return { type: "stat", value: 12, hint: "+3 on yesterday" };
  // { type: "table", columns: ["Plan", "Count"], rows: [["Std", 10], ["Max", 2]] }
  // { type: "list", items: ["first", "second"] }
}
```

The answer is up to 64 KB and cached for a minute. If the plugin does not answer within 2 s,
the widget shows "no data".

## The subscription page — subBlocks

```json
"provides": {"sub_blocks": true}
```

```js
export function subBlocks({ user, lang }) {
  // user = {id, name, status, expire_at, plan_id, data_limit, used, telegram_id}
  return [
    { type: "notice", text: "20% off until Friday" },
    { type: "markdown", text: "**A new server** in the Netherlands" },
    { type: "button", label: "Renew", url: "https://vpn.example/pay" },
  ];
}
```

- **Blocks.** Up to 10: `text`, `notice` (highlighted), `markdown` (no raw HTML), `button`
  (`https://` only). Text up to 1000 characters.
- **Time and cache.** The answer is awaited for up to 0.3 s and cached for 5 minutes per user
  and language.
- **An own page.** An operator with their own page gets the same blocks as `blocks` in
  `GET /v1/users/{id}/subscription`.

## A delivery channel — channel (experimental)

```json
"provides": {"channel": {"label": "E-mail"}}, "experimental": ["channel"]
```

```js
export const channel = {
  send(msg) {
    // msg = {event_id, kind: "message"|"auto_message"|"broadcast"|"notice", notice, text, buttons, users: [...], data}
    for (const u of msg.users) { /* e-mail u.external_id */ }
  },
};
```

- **What comes in.** The channel gets messages, broadcasts and reminders (`user.expiring`,
  `user.traffic_low`) for those the panel's bot did not reach: the user has no Telegram, has
  blocked the bot, or the bot is off.
- **Format.** `text` is Telegram HTML.
- **Retries.** Delivery is retried like events. To send nothing twice, remember `event_id`
  together with the user's id.

The `ChannelMessage` type is in `rospanel.d.ts`; an example —
[examples/plugins/email](../../examples/plugins/email).

## Decisions — beforeSignup, beforeDeviceBind, quotePrice

```json
"provides": {"hooks": ["beforeSignup", "beforeDeviceBind"], "price": true}, "experimental": ["price"]
```

```js
export function beforeSignup(req) {
  // req = {channel: "web"|"telegram"|"miniapp"|"bot", telegram_id, username, external_id, ip, ref, source, lang}
  if (req.ip && panel.kv.get("ban/" + req.ip)) return { allow: false, reason: "banned" };
  return { allow: true };
}

export function beforeDeviceBind(req) {
  // req = {user_id, hwid, device_os, device_model, user_agent, ip, count, cap, lang}
  return req.device_os === "Windows" ? false : { allow: true };
}

export function quotePrice(req) {
  // req = {user_id, plan_id, plan, periods, devices, base_rub, lang}
  return req.plan === "Standard" ? { price_rub: req.base_rub - 50, note: "loyal" } : null;
}
```

Common rules:

- **No objection by default.** An error, a timeout (0.3 s) or a busy plugin means "no
  objection": a broken plugin never stops anyone signing up or paying.
- **Read-only.** Inside decisions `panel.api` only reads (GET).
- **Several plugins.** They are asked in order; the first refusal wins.
- **The reason.** `reason` (and `note` of a price) is a key of the plugin's `i18n/`, or plain
  text. A person reads it: a website gets `err.pluginDenied`, the bot and the Mini App show
  the text.

| Export | Asked | Answer |
|---|---|---|
| `beforeSignup` | before a new account from the bot, the Mini App or `POST /v1/signup`. Not asked about an existing account or one coming back after being unlinked | `{allow, reason}` or `false` |
| `beforeDeviceBind` | before a device new to the user takes a slot (the device limit on, and the client sent a HWID). A refusal of that device is kept for a minute | `{allow, reason}` or `false` |
| `quotePrice` | while pricing a plan or a renewal the user buys; `base_rub` is the price after the period discount, before a promo code. Auto-renewal from the balance charges the regular price | `{price_rub, note}` or `null` |

- **Price bounds.** A price counts from half of `base_rub` to `base_rub`. Any other is
  ignored, with a warning in the plugin's log.
- **Cache and promo codes.** A price is cached for a minute per user, plan, period and
  devices. A promo code applies on top of it. The order keeps the price it was opened with.

An example — [examples/plugins/channel-gate](../../examples/plugins/channel-gate).

## The user bot — bot

```json
"provides": {"bot": {"menu": true, "commands": [{"command": "bonus", "description": {"ru": "Бонус дня", "en": "Daily bonus"}}]}}
```

```js
export const bot = {
  menu({ user, lang }) {
    return [{ text: "🎁 Bonus", data: "bonus" }, { text: "Website", url: "https://vpn.example" }];
  },
  onCallback({ user, data, lang }) {
    return { text: "Bonus added", buttons: [[{ text: "Again", data: "bonus" }]] };
  },
  onCommand({ user, command, args, lang }) {
    return "Command /" + command + " " + args;
  },
};
```

- **`user`.** `{id, name, telegram_id, username}`; `id` is 0 when this Telegram has no
  account.
- **`menu`.** The buttons go under the built-in menu buttons, up to 6 per plugin. The answer
  is awaited for up to 0.3 s and cached for 5 minutes.
- **A press.** It comes to `onCallback` with the button's `data`. `px:<id>:<data>` must fit in
  64 bytes, so keep `data` short.
- **Commands.** They go into the bot's command menu next to `/start` and come to `onCommand`;
  `args` is the text after the command.
- **The answer.** A string or `{text, buttons}`: plain text (the panel escapes it), and
  `buttons` as rows (up to 8 rows of 3) or a flat list. A button with `data` comes back to
  `onCallback`, one with `url` (`https://` only) opens a link. The bot adds its own "Menu"
  button.
- **Failures.** 3 s per answer; if the plugin fails or is late, the person sees "This is not
  available right now. Try again later".

## Subscription configs — transformSubscription (experimental)

```json
"provides": {"subscription": true}, "experimental": ["subscription"]
```

```js
export function transformSubscription({ user, format, links, config }) {
  if (format === "links") return { links: links.map((l) => l.replace(/#.*/, "#" + encodeURIComponent("My VPN"))) };
  if (format === "singbox") { config.route.rules.unshift({ domain_suffix: ["example.ru"], outbound: "direct" }); return { config }; }
  return null; // as it is
}
```

- **Formats.** `links` — an array of share links, `clash` — `config` as a YAML string,
  `singbox` and `xray` — `config` as a JSON object (Xray's may be an array).
- **The answer.** The same shape, or `null`.
- **Checking the answer.**
  - Links must be proxy links (`vless://`, `hysteria2://`, `trojan://`, `ss://`, …), at most
    twice as many as given plus 50.
  - Clash stays a text with `proxies:`, sing-box has non-empty `outbounds`.
  - If the check fails, the client gets the panel's profile, and the plugin's log gets a
    warning.
- **Load.** Every subscription refresh passes here. The answer is cached for 5 minutes per
  user, format and input; refreshes with an unchanged input do not call the plugin.

## The subscription page theme — theme

```json
"provides": {"theme": true}
```

```
theme/theme.css    styles over the page's own
theme/bg.webp      images (png, jpg, webp, gif, svg) and fonts (woff2, woff)
```

```css
:root { --bg: #0b1020; --surface: #141a2e; --ink: #e8ecf8; --muted: #9aa3bf; }
body { background-image: url(bg.webp); }
.btn.alt { color: var(--ink); }
```

- **What a theme changes.** Only the look: the page and all it does (payments, devices,
  Telegram) stay the panel's.
- **Variables.** `--brand`, `--brand-dark`, `--on-brand`, `--accent-fg`, `--bg`, `--surface`,
  `--ink`, `--muted`, `--success-fg`, `--warning-fg`, `--danger-fg`. The page's classes
  (`.btn`, `.btn.alt` …) can be styled too, but they may change between versions.
- **Nothing from elsewhere.**
  - `url()` may name only a file of `theme/`, or `data:image/…`, `data:font/…`.
  - `@import`, `image-set(`, `expression(`, `javascript:`, addresses with a scheme (`://`)
    or `//`, and backslash escapes are refused.
  - The subscription page itself loads images and fonts only from the panel, too (CSP).
  - The reason: a page loading anything from a third-party host hangs where that host is
    blocked.
- **Sizes.** Up to 50 files, 1 MB each, 3 MB together.
- **Several themes.** With several switched on, the first in order applies.

A theme's `main.js` can be empty: `export {};`. An example —
[examples/plugins/theme-night](../../examples/plugins/theme-night).
