# The plugin editor in the panel

[Русская версия](editor-RU.md)

A plugin can be written in the panel itself: **Settings → Plugins → Add → Or make your own**.
Tests, a trial run, install and download are there too. A draft changes nothing in the panel
until it is installed. The editor needs the `plugins.manage` permission.

## Three ways to start

| | What you get |
|---|---|
| **Rule builder** | "When an event comes → if a condition holds → do something", no code. The panel builds an ordinary plugin from the rules. |
| **Code** | The `plugin new` template: `main.js`, `plugin.json`, a migration, `test.js`, translations. |
| **Sources (.zip)** | An archive downloaded from the editor — carry on where you left off. A plugin package opens too, as a code draft. (A zip dropped in the zone above is installed, not opened.) |

## Drafts in the plugin list

Drafts of plugins not installed yet are in the one plugin list, after the installed plugins,
with a gray dot by the name. The pencil opens a draft, the bin deletes it, **Install** installs
it.

An installed plugin opens in the editor with the pencil on its card. That makes a draft with its
code — and however many times it is opened, it is the same draft: a plugin has one. It has no
row of its own in the list: while it holds changes not applied yet, the plugin's card shows an
orange dot. Hovering the dot says what is up. It also tells of settings to fill in and of a
pause, and turns red when the plugin failed to start.

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
| HTTP request | To your address; without a body the whole event goes as JSON: `{id, event, created_at, data}`. An `Authorization` header from the settings. |
| Extend the subscription | By N days. |
| Enable / disable the user | |
| Add / remove a tag | |
| Write to the plugin's log | |

Texts, the address and the body take placeholders such as `{{user.name}}` or `{{data.days_left}}`.
Each rule's **Event data** lists what its event carries — the user's fields (`user.*`), the
event's own (`data.*`), and `event`, `event_id`, `created_at`, `now` — each described, with an
example and the whole event as a sample; a click puts the field where the cursor is. Fields marked
`?` are not on every such event. In an address only the path and query take placeholders:
the host is fixed, and the operator sees it at install.

A placeholder can show a value through a format — `{{user.expire_at|date}}`:

| Format | For | Shows |
|---|---|---|
| `date` | a time (unix seconds or `{{now}}`) | `01.02.2026`, in the panel's timezone; `—` when 0 |
| `datetime` | a time | `01.02.2026 03:00` |
| `days` | a time | days left until it: `3` |
| `gb` | bytes | gigabytes: `80` |
| `rub` | kopecks | roubles: `150.5` |

`user.*` works on every event about a user — `user.*`, `plan.*`, `balance.adjusted`, `referral.*`,
`promo.*`, and the payment events (from `data.user`). Server, broadcast and sign-up request events
have no user: their rules cannot act on one. Nor can a rule on `user.deleted`: the user's fields
are there, the user is gone. A rejected sign-up request names a user only when an account already
exists (`data.user_id`); otherwise the actions on a user are skipped.

The builder refuses rules that would feed themselves: an extension on `user.limits_changed`, or
enabling on `user.disabled` together with disabling on `user.enabled`.

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

There is one sandbox per panel, shared by tests, trial runs and the check: a second run waits a
few seconds or answers "the sandbox is busy". Tests are bounded by 60 seconds, a trial run and
the check by 30.

## Install and download

- **Check** — the package as the install will read it: permissions, network, functions, size, or
  the list of problems. The code is loaded as a start would load it, so an error in `main.js`
  shows at once.
- **Install** — the ordinary consent screen with the password.
- While the builder's rules have problems, **Check** lists them, and tests, trial runs, install
  and the package download refuse until they are fixed: otherwise the code of the previous rules
  would go, not the rules on the screen.
- A draft is a copy: saving it does not touch the running plugin. The header says whether the
  draft is the installed version. With the plugin installed the button is **Apply**: an update, with a database snapshot and rollback. A draft version not above
  the installed one is raised by itself (0.1.0 → 0.1.1).
- **Download** — the package, to install on another panel, or the sources with tests, to carry
  on in your own editor with `rospanel plugin test | dev | pack`. The sources open in the panel
  again through **Sources (.zip)**; a builder draft stays a builder draft (the rules are in
  `rules.json`). Secret settings in `dev.config.json` stay behind — their values are blanked
  in the archive.

Drafts are kept in the panel's database encrypted, like plugin settings.
