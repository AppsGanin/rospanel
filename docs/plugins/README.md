# RosPanel plugins

[Русская версия](README-RU.md)

A plugin is JavaScript that runs inside the panel. It reacts to events, runs on a schedule,
keeps its own data and calls the panel's API and the internet — only as far as the operator
agreed when installing it. Plugins are in beta.

## What a plugin can do

| Task | How | Example |
|---|---|---|
| React to events: sign-ups, payments, ending subscriptions, a server going down | `onEvent` | [new-users](../../examples/plugins/new-users), [notify-discord](../../examples/plugins/notify-discord) |
| Do something on a schedule | `cron` | [users-csv](../../examples/plugins/users-csv) |
| Add a payment method | `payment` | [lava](../../examples/plugins/lava) |
| Take requests from the internet: a shop, a form, OAuth | `onHttp` | [shop-webhook](../../examples/plugins/shop-webhook) |
| Show data on a user's card, buttons, dashboard widgets | `userFields`, `onAction`, `widget` | [email](../../examples/plugins/email) |
| Blocks on the subscription page | `subBlocks` | [announcement](../../examples/plugins/announcement) |
| A theme for the subscription page, or your own page from scratch | `theme` | [theme-night](../../examples/plugins/theme-night), [theme-hero](../../examples/plugins/theme-hero) |
| Deliver messages to those the bot does not reach (e-mail, SMS) | `channel` | [email](../../examples/plugins/email) |
| Decide who may sign up, which device may bind, what price to give | `beforeSignup`, `beforeDeviceBind`, `quotePrice` | [channel-gate](../../examples/plugins/channel-gate) |
| Buttons and commands in the user bot | `bot` | [channel-gate](../../examples/plugins/channel-gate) |
| Change the configs clients are served | `transformSubscription` | — |

## Quick start

Everything is built into `rospanel`; no Node.js needed.

```sh
rospanel plugin new my-plugin   # a working plugin from the template
cd my-plugin
rospanel plugin test .          # tests
rospanel plugin dev .           # a console that reloads on save
rospanel plugin pack .          # my-plugin-0.1.0.zip
```

Install the zip in the panel: **Settings → Plugins → Add**. Or write the plugin in the
panel itself — see [the editor](editor.md).

```js
// main.js — the panel calls what is exported
export function onEvent(e) {
  if (e.event === "user.created") panel.log.info("new user", e.data.name);
}
```

## Your own subscription page

A theme changes the subscription page with no code — files in `theme/`:

- **A look.** `theme/theme.css` over the panel's styles: colours, background, fonts. Example:
  [theme-night](../../examples/plugins/theme-night).
- **A page from scratch.** `theme/layout.html` and `theme/dialogs.html` are Go templates of
  the whole page and its dialogs; `"theme": {"panel_styles": false}` drops the panel's
  styles. Example: [theme-hero](../../examples/plugins/theme-hero).
- **Single parts.** `theme/<section>.html` replaces one section — the header, the QR card,
  the payment block; the rest stays the panel's. All 32 sections, described, are in the
  [extension points](exports.md) ("The page's sections").

```html
<!-- theme/layout.html -->
<main class="page">
  <h1>{{.Name}}</h1>
  <p>{{.Used}}{{if .HasLimit}} / {{.Limit}}{{end}}</p>
  <img src="{{.SubURL}}/qr.png" alt="QR" />
  <button type="button" data-action="copy" data-value="{{.SubURL}}">{{.L.CopyLink}}</button>
</main>
```

The page's data — the user, traffic, links, plans, devices, texts in the reader's
language — is in the [field reference](theme-data.md). Buttons work through `data-action`:
the panel's script pays, opens apps, runs the connect wizard and the rest, and a theme has
no script of its own. The panel checks a theme when it is installed: a misspelt field, a
`<script>` or an outside address is an error naming the file. In full: the
[extension points, "Your own page"](exports.md).

## Documentation

0. **[The plugin editor in the panel](editor.md)** — the rule builder, the code editor, tests and
   a trial run, without leaving the panel.
1. **[Your first plugin, step by step](tutorial.md)** — from `plugin new` to installing it,
   with Telegram notifications as the example.
2. **[Manifest and package](manifest.md)** — all of `plugin.json`: permissions, hosts,
   settings, points, resources.
3. **[Extension points](exports.md)** — every export: when it is called, what it gets, what
   to return, time limits.
   - [Subscription page fields](theme-data.md) — everything a theme template sees.
4. **[Plugin API](api.md)** — `panel.config`, `log`, `kv`, `db`, `api`, `http.fetch`,
   `blob`, `crypto`, `t`.
5. **[Testing and debugging](testing.md)** — `test.js`, mocks, `dev`, `validate`, the log in
   the panel.
6. **[Publishing and updates](publishing.md)** — versions, migrations, rollback, sharing a plugin.
7. **[Limits, security and errors](troubleshooting.md)** — the sandbox, limits, pausing,
   common errors explained, questions.

Editor types are in `rospanel.d.ts`, which `plugin new` puts in the folder. Events and their
fields are in [docs/api.md](../api/README.md#events), the API's routes in your panel's `/v1/docs`.

## The essentials

- **Synchronous code.** Every `panel.*` call is synchronous. There are no timers.
- **At-least-once delivery.** An event may come twice; handle it idempotently.
- **State in storage.** Keep it in `panel.kv`/`panel.db`, not in variables: the VM may be
  restarted between calls.
- **Errors and retries.** A thrown exception is an error: the event is delivered again, and
  after 10 errors in a row the plugin is paused until the panel retries it.
- **Least privilege.** Ask only for the permissions and hosts you need: the operator sees
  them before installing.
