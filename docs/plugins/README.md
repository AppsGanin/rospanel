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
| Blocks and a theme on the subscription page | `subBlocks`, `theme` | [announcement](../../examples/plugins/announcement), [theme-night](../../examples/plugins/theme-night) |
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

Install the zip in the panel: **Settings → Plugins → Install**.

```js
// main.js — the panel calls what is exported
export function onEvent(e) {
  if (e.event === "user.created") panel.log.info("new user", e.data.name);
}
```

## Documentation

1. **[Your first plugin, step by step](tutorial.md)** — from `plugin new` to installing it,
   with Telegram notifications as the example.
2. **[Manifest and package](manifest.md)** — all of `plugin.json`: permissions, hosts,
   settings, points, resources.
3. **[Extension points](exports.md)** — every export: when it is called, what it gets, what
   to return, time limits.
4. **[Plugin API](api.md)** — `panel.config`, `log`, `kv`, `db`, `api`, `http.fetch`,
   `blob`, `crypto`, `t`.
5. **[Testing and debugging](testing.md)** — `test.js`, mocks, `dev`, `validate`, the log in
   the panel.
6. **[Publishing and updates](publishing.md)** — versions, migrations, rollback, sharing a plugin.
7. **[Limits, security and errors](troubleshooting.md)** — the sandbox, limits, pausing,
   common errors explained, questions.

Editor types are in `rospanel.d.ts`, which `plugin new` puts in the folder. Events and their
fields are in [docs/api.md](../api.md#events), the API's routes in your panel's `/v1/docs`.

## The essentials

- **Synchronous code.** Every `panel.*` call is synchronous. There are no timers.
- **At-least-once delivery.** An event may come twice; handle it idempotently.
- **State in storage.** Keep it in `panel.kv`/`panel.db`, not in variables: the VM may be
  restarted between calls.
- **Errors and retries.** A thrown exception is an error: the event is delivered again, and
  after 10 errors in a row the plugin is paused until the panel retries it.
- **Least privilege.** Ask only for the permissions and hosts you need: the operator sees
  them before installing.
