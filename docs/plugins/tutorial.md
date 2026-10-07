# Your first plugin, step by step

[Русская версия](tutorial-RU.md) · [Contents](README.md)

We will write a plugin that tells the operator's Telegram about every new user and sends a
summary once a day. Along the way we meet the manifest, events, scheduled jobs, settings, the
plugin's own database, requests to the internet, translations and tests — nearly everything
any plugin is made of.

The finished result is in [examples/plugins/new-users](../../examples/plugins/new-users).

All you need is the `rospanel` binary (the one on the server, or one built for your machine).
No Node.js.

## 1. Create the skeleton

```sh
rospanel plugin new new-users
cd new-users
```

```
new-users/
  plugin.json             the manifest: who the plugin is, what it may do, what it answers
  main.js                 the code
  migrations/0001_init.sql  the tables of its own database
  i18n/ru.json, en.json   strings for panel.t()
  test.js                 tests (not packed)
  dev.config.json         settings for tests and dev (not packed)
  rospanel.d.ts           editor types (not packed)
  jsconfig.json           wires the types into VS Code
  README.md
```

The skeleton already works: `rospanel plugin test .` passes. Open the folder in VS Code and
`panel.*` completion appears by itself, thanks to `rospanel.d.ts`.

## 2. The manifest

`plugin.json` describes the plugin. Rewrite it like this:

```json
{
  "id": "new-users",
  "version": "1.0.0",
  "api": 1,
  "panel": ">=4.4.0",
  "name": {"ru": "Новые пользователи — в Telegram", "en": "New users to Telegram"},
  "description": {
    "ru": "Пишет в ваш чат Telegram о каждом новом пользователе и раз в день присылает сводку.",
    "en": "Tells your Telegram chat about every new user and sends a summary once a day."
  },
  "author": "you",
  "license": "MIT",
  "permissions": ["users.view"],
  "net": ["api.telegram.org"],
  "settings": [
    {"key": "bot_token", "kind": "secret", "label": {"ru": "Токен бота", "en": "Bot token"},
     "help": {"ru": "Создайте бота у @BotFather", "en": "Create a bot with @BotFather"}},
    {"key": "chat_id", "kind": "text", "label": {"ru": "ID чата", "en": "Chat ID"}, "placeholder": "-1001234567890"},
    {"key": "lang", "kind": "select", "label": {"ru": "Язык сообщений", "en": "Message language"}, "default": "ru",
     "options": [{"value": "ru", "label": "Русский"}, {"value": "en", "label": "English"}]}
  ],
  "provides": {
    "events": ["user.created", "user.registered"],
    "cron": [{"name": "summary", "schedule": "0 9 * * *"}]
  }
}
```

What matters here:

- **`id`** never changes: it names the plugin's database and its entries in the panel's
  journal.
- **`permissions`** — what the plugin may do through the panel's API. This one needs none,
  `users.view` stays for the example; the operator sees this list before installing.
- **`net`** — the only hosts the plugin may reach. Without `api.telegram.org`, the request to
  Telegram is refused.
- **`settings`** — the form the operator fills in after installing. A `secret` is stored
  encrypted and never shown to anyone again.
- **`provides`** — what the plugin answers: two events and one job, every day at 9:00 in the
  panel's time zone.

Every field is in the [manifest reference](manifest.md).

## 3. Its own database

`migrations/0001_init.sql`:

```sql
-- One row per user the plugin announced: what the daily summary counts, and what
-- keeps a repeated delivery of the same event from being announced twice.
CREATE TABLE announced (
    user_id INTEGER PRIMARY KEY,
    name    TEXT    NOT NULL,
    source  TEXT    NOT NULL,
    at      INTEGER NOT NULL
);
```

Migrations run in name order when the plugin is switched on. A migration that shipped is
never edited — changes go in a new one (`0002_….sql`).

## 4. Strings

`i18n/en.json`:

```json
{
  "new_user": "🆕 New user: {name} (#{id})",
  "self_registered": "signed up by themselves",
  "created": "created in the panel",
  "summary": "📊 New users in the last day: {count}"
}
```

And the same in Russian in `i18n/ru.json`. `panel.t("new_user", {name, id}, "en")` fills in
`{name}` and `{id}`.

## 5. The code

`main.js` is one ES module. The panel calls what it exports:

```js
// Tells the operator's Telegram chat about new users, and once a day how many came.

/** Sends a message through the operator's bot; throws when Telegram refuses it. */
function send(text) {
  const res = panel.http.fetch(`https://api.telegram.org/bot${panel.config.bot_token}/sendMessage`, {
    method: "POST",
    body: { chat_id: panel.config.chat_id, text },
  });
  if (res.status !== 200) throw new Error(`Telegram answered ${res.status}: ${res.body.slice(0, 200)}`);
}

/** @param {PanelEvent<{id: number, name: string}>} e */
export function onEvent(e) {
  const lang = panel.config.lang;
  const user = e.data;
  // One transaction: if Telegram fails, the row is rolled back too and the panel's
  // retry of this event sends it again. An event that comes twice finds the row
  // (the primary key) and sends nothing.
  panel.db.tx(() => {
    const { changes } = panel.db.exec(
      "INSERT OR IGNORE INTO announced(user_id, name, source, at) VALUES (?, ?, ?, ?)",
      user.id, user.name, e.event, e.created_at,
    );
    if (changes === 0) return;
    const how = panel.t(e.event === "user.registered" ? "self_registered" : "created", {}, lang);
    send(`${panel.t("new_user", { name: user.name, id: String(user.id) }, lang)}\n${how}`);
  });
}

export function summary() {
  const since = Math.floor(Date.now() / 1000) - 86400;
  const [{ n }] = panel.db.query("SELECT count(*) AS n FROM announced WHERE at >= ?", since);
  send(panel.t("summary", { count: String(n) }, panel.config.lang));
}
```

Three things to understand about how the panel calls code:

1. **Everything is synchronous.** `panel.http.fetch` returns the answer right away — no
   `await`, callbacks or timers. You cannot wait for time (`setTimeout`).
2. **An event may come more than once.** Delivery is at-least-once: after a failure the
   panel repeats it. So the plugin remembers whom it already announced.
3. **An error means a retry.** If `onEvent` throws, the panel delivers the event again after
   10 s, 30 s, 2 min and 10 min. The transaction above is there for exactly that: the row
   about a user appears only together with the message sent.

`panel.*` is in the [API reference](api.md); events and the other points in the
[extension points reference](exports.md).

## 6. Tests

`test.js` runs beside the plugin; the network and the panel's API are never real in it —
mocks answer everything:

```js
const tg = "https://api.telegram.org/bot123:abc/sendMessage";

test("a new user is announced once", () => {
  mock.http(tg, { body: { ok: true } });

  plugin.event("user.registered", { id: 7, name: "Ann" });
  plugin.event("user.registered", { id: 7, name: "Ann" }); // the same event again

  const sent = mock.calls().filter((c) => c.kind === "http");
  assert.equal(sent.length, 1);
  assert.equal(JSON.parse(sent[0].body), {
    chat_id: "-100500",
    text: "🆕 Новый пользователь: Ann (#7)\nзарегистрировался сам",
  });
});

test("after a Telegram error the retry sends it", () => {
  mock.http(tg, { status: 502, body: "bad gateway" });
  assert.throws(() => plugin.event("user.created", { id: 9, name: "Bob" }));

  mock.reset();
  mock.http(tg, { body: { ok: true } });
  plugin.event("user.created", { id: 9, name: "Bob" }); // the panel delivers it again
  assert.equal(mock.calls().length, 1);
});
```

The settings tests run with are in `dev.config.json`:

```json
{"settings": {"bot_token": "123:abc", "chat_id": "-100500", "lang": "ru"}}
```

```sh
rospanel plugin test .
```

```
  ✓ a new user is announced once
  ✓ after a Telegram error the retry sends it

2 passed, 0 failed
```

Everything tests can do is in the [testing reference](testing.md).

## 7. Try it by hand

```sh
rospanel plugin dev .
```

```
new-users is running. Type help for commands.
> mock http https://api.telegram.org/ {"ok": true}
> event user.created {"id": 1, "name": "Ann"}
> sql SELECT * FROM announced
> logs
```

`dev` reloads the plugin whenever a file is saved. Requests that are not mocked go out to the
internet for real — so you can try a real bot by putting its token in `dev.config.json`.

## 8. Package and install

```sh
rospanel plugin pack .
```

```
new-users-1.0.0.zip — 2 KB, sha256 …
not packed: dev.config.json, jsconfig.json, rospanel.d.ts, test.js
```

In the panel: **Settings → Plugins → Install** → choose the zip. The panel shows the consent
screen: permissions, hosts, what the plugin does. After installing, the plugin is off — fill
in its settings (token, chat) and switch it on. Create a user — a message arrives in the
chat. The plugin's **Log** shows what it did and where it failed.

## What next

- Buttons on a user's card, widgets, blocks on the subscription page, your own payment
  method, buttons in the bot — [extension points](exports.md).
- Updates, migrations and publishing to the catalog — [publishing](publishing.md).
- Limits and common errors explained — [limits and errors](troubleshooting.md).
- More finished plugins — [examples/plugins](../../examples/plugins).
