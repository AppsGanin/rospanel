# The plugin editor in the panel

[Русская версия](editor-RU.md)

A plugin can be written in the panel itself: **Settings → Plugins → Plugin editor → New plugin**.
Tests, a trial run, install and download are there too. A draft changes nothing in the panel
until it is installed. The editor needs the `plugins.manage` permission.

## Three ways to start

| | What you get |
|---|---|
| **Rule builder** | "When an event comes → if a condition holds → do something", no code. The panel builds an ordinary plugin from the rules. |
| **Code** | The `plugin new` template: `main.js`, `plugin.json`, a migration, `test.js`, translations. |
| **From a .zip** | A plugin package, or sources downloaded from the editor. |

An installed plugin opens with **Open in the editor** on its card: a draft with its code, which
installs as an update.

## The rule builder

A rule is:

- **When:** a panel event (the same as webhooks have) or a schedule.
- **If:** conditions on the event's fields — `equals`, `does not equal`, `contains`,
  `greater than`, `less than`, `is empty`, `is not empty`; all of them or any.
- **Then:** one or more actions:

| Action | What it does |
|---|---|
| Message in Telegram | To the chat in the plugin's settings: the ID of a chat, group or channel (or an `@name`) and the bot token are set after install. |
| Message in Discord | To the webhook from the plugin's settings. |
| HTTP request | To your address; without a body the whole event goes as JSON. An `Authorization` header from the settings. |
| Extend the subscription | By N days. |
| Enable / disable the user | |
| Add / remove a tag | |
| Write to the plugin's log | |

Texts, the chat, the address and the body take placeholders: `{{user.name}}`, `{{user.id}}`,
`{{user.telegram_id}}`, `{{data.<field>}}` — any field of the event, `{{event}}`, `{{now}}`.
In an address only the path and query take them: the host is fixed, and the operator sees it at
install.

The plugin is granted exactly what the rules need: events bring `users.view` (money events also
`billing.view`), actions on the user `users.manage`; hosts are `api.telegram.org`,
`discord.com` and the hosts of your HTTP requests.

Each action runs once per event: when a delivery is retried after a failure, the actions already
done are not repeated. A scheduled rule cannot change a user — it has none.

The **Code** tab shows the `main.js` the rules made: the rules sit in it as JSON, with the code
that runs them below. **Edit the code by hand** switches the draft to code for good — the rules
stay in `main.js`, and the builder closes for this draft.

## Code

Files on the left, the editor on the right: highlighting, `Tab`, search (`Ctrl/⌘+F`), `panel.*`
completion. Everything saves by itself. Files outside the package (`test.js`,
`dev.config.json`, `rospanel.d.ts`) stay in the draft and in the downloaded sources.

## Tests

**Run the tests** runs `test.js` in the panel's sandbox, like `rospanel plugin test`: the
internet and `panel.api` are mocked (`mock.http`, `mock.api`), settings come from
`dev.config.json`. What tests do not check is in [testing and debugging](testing.md).

## Trial run

One event to `onEvent` with your data, or a call of any exported function with an argument. The
result, the log and every request the plugin made are shown.

By default nothing goes out: the internet answers "no mock", `panel.api` a hint. Two checkboxes
change that:

- **Real requests to the internet** — only to the hosts in `net`, through the same guarded fetch
  an installed plugin has. Messages and webhooks really go out.
- **Read the panel's data** — `panel.api` `GET` only, with the permissions both the plugin and
  you hold.

There is one sandbox per panel: a second run waits a few seconds or answers "the sandbox is
busy". Tests are bounded by 60 seconds, a trial run by 30.

## Install and download

- **Check** — the package as the install will read it: permissions, network, functions, size, or
  the list of problems.
- **Install** — the ordinary consent screen with the password; a plugin with the same ID is
  updated (with a database snapshot and rollback).
- **Download** — the package, to install on another panel, or the sources with tests, to carry
  on in your own editor with `rospanel plugin test | dev | pack`. The sources open in the panel
  again through **From a .zip**; a builder draft stays a builder draft (the rules are in
  `rules.json`).

Drafts are kept in the panel's database encrypted, like plugin settings.
