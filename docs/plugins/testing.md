# Testing and debugging

[Русская версия](testing-RU.md) · [Contents](README.md)

The tools are built into `rospanel`:

```sh
rospanel plugin test .        # run test.js
rospanel plugin dev .         # an interactive console that reloads on save
rospanel plugin validate .    # check the package the way the panel will
```

In tests and in `dev` the plugin runs in the same sandbox as in the panel: the same memory and
database limits, the same SQLite database with your migrations. Its database is created afresh on every `test`
or `dev` run.

## dev.config.json

The settings the plugin runs with in `test` and `dev` — what the operator types into the form:

```json
{"settings": {"token": "test-token", "mode": "test", "limit": "100"}}
```

Values are strings, as in `panel.config`. With a required field empty, the plugin does not
start: `fill in the settings first: token`.

## test.js

`test.js` runs in a sandbox of its own, beside the plugin. It calls the plugin through
`plugin.*`, and `mock.*` intercepts the plugin's requests.

```js
test("test name", () => {
  mock.http("https://api.example.com/", { status: 200, body: { ok: true } });
  const r = plugin.call("onAction", { key: "sync", user_ids: [] });
  assert.equal(r.ok, true);
});
```

Tests run in the order declared. **Mocks are reset before each test; the plugin's database
and `kv` are not**: what one test wrote, the next one sees.

### test and assert

| | |
|---|---|
| `test(name, fn)` | declare a test; an exception inside `fn` fails it |
| `assert.equal(actual, expected, msg?)` | a deep comparison through JSON; the order of object keys does not matter |
| `assert.ok(value, msg?)` | the value is truthy |
| `assert.throws(fn, msg?)` | `fn` throws |

### plugin — call the plugin

| | |
|---|---|
| `plugin.call(export, arg?)` | call an export and return its answer: `plugin.call("payment.create", {...})`, `plugin.call("userFields", 42)`, `plugin.call("bot.onCommand", {...})` |
| `plugin.event(name, data)` | deliver an event to `onEvent` as the panel does: `{id, event, created_at: now, data}` |
| `plugin.cron(name)` | run a scheduled job |
| `plugin.kv.get/set/delete/list` | read and seed the plugin's `kv` |
| `plugin.db.exec/query` | read and seed the plugin's database |

An exception in the plugin propagates to the test — `assert.throws(() => plugin.event(...))`
checks that the plugin failed.

### mock — answers to the plugin's requests

| | |
|---|---|
| `mock.http(prefix, {status?, headers?, body?})` | the answer to `panel.http.fetch` for URLs starting with `prefix`. `body` as a string or an object (becomes JSON), `status` 200 by default |
| `mock.api(method, path, {status?, body?})` | the answer to `panel.api`. `path` exact (`/v1/users/7`) or ending in `*` (`/v1/users/*`) |
| `mock.calls()` | what the plugin asked for: `[{kind: "http"\|"api", method, url, body}]` |
| `mock.reset()` | forget the mocks and the recorded calls |

- An unmocked `fetch` in a test throws (`no mock for GET https://… — add mock.http(…)`); an
  unmocked `panel.api` answers `501` with a hint. A test never goes to the network.
- **`net` holds for mocks too:** a request to a host not in the manifest is refused, as in the
  panel.
- A `form` or blob body is recorded in `mock.calls()` as text (the first MB) — check multipart
  fields with `includes`.

### crypto — sign what a test sends the plugin

Tests have `crypto.hash`, `crypto.hmac`, `crypto.sign`, `crypto.jwt` — the same functions as
`panel.crypto`. A test signs a callback "from the payment system" with them:

```js
test("signed webhook is accepted", () => {
  const body = JSON.stringify({ id: "inv-1", status: "paid", amount: 150 });
  const sign = crypto.hmac("sha256", "k3y", body);
  const r = plugin.call("payment.webhook", { body, headers: { "x-signature": sign } });
  assert.equal(r.status, "paid");
});
```

### An example

A plugin with a `sync` job that reads users through the API and remembers how many there are:

```js
test("sync counts the users", () => {
  mock.api("GET", "/v1/users*", { body: { data: [{ id: 7 }, { id: 8 }] } });
  plugin.cron("sync");
  assert.equal(plugin.kv.get("last_sync").count, 2);
  assert.equal(mock.calls()[0].url, "/v1/users?limit=1000");
});

test("kv from the previous test is still there", () => {
  assert.ok(plugin.kv.get("last_sync"));
});
```

More in the `test.js` of every plugin in [examples/plugins](../../examples/plugins).

The output:

```
  ✓ sync counts the users
  ✗ kv from the previous test is still there
      expected a truthy value, got undefined

1 passed, 1 failed
```

The exit code is non-zero when any test fails — `rospanel plugin test` fits a CI.

### What tests check, and what they do not

As in the panel: the manifest (the permissions points need included), the exports, `net` for
mocked requests too, the memory limit, the database quota and its refused statements, `kv` and
blob limits.

Not in tests — check these on a real panel:

- **`panel.api` permissions.** A mock answers whatever the plugin asks; with `--panel` the
  key's permissions apply, not the plugin's.
- **Read-only `panel.api`** in decisions, `bot.menu` and `transformSubscription`.
- **Call times.** Every `plugin.call` gets 30 s, whatever the export's own limit (0.3 s for a
  decision).
- **Pausing.** Failed calls never pause the plugin, and a failed surface is not skipped for
  30 s.
- **Event delivery.** `plugin.event` calls `onEvent` once: no retries, no loop detection, no
  `channel.send`.
- **`onHttp` answers** are returned as the plugin made them, without the panel's header rules.
- **Who sees** user fields, buttons and widgets in the admin panel.

## dev — the console

```sh
rospanel plugin dev .
```

```
my-plugin is running. Type help for commands.
> event user.created {"id": 1, "name": "Ann"}
> call userFields 1
{"email":"ann@example.com"}
> cron sync
> kv user/
> sql SELECT * FROM orders LIMIT 5
> mock api GET /v1/users/1 {"data": {"id": 1, "name": "Ann"}}
> mock http https://api.example.com/ {"ok": true}
> logs
> reload
> quit
```

| Command | |
|---|---|
| `event <name> [json]` | deliver an event to `onEvent` |
| `call <export> [json]` | call an export: `call payment.create {"order_id": 1, "amount_rub": 100}` |
| `cron <name>` | run a job |
| `kv [prefix]` | show `kv` |
| `sql <query>` | query the plugin's database |
| `mock api <METHOD> <path> <json>` | an answer for `panel.api` |
| `mock http <url-prefix> <json>` | an answer for `panel.http.fetch` |
| `logs` | the plugin's log |
| `reload` | reload now (files are watched anyway) |
| `help`, `quit` | |

How it differs from `test`:

- **The network is real.** An unmocked `fetch` goes out to the internet (only to hosts in
  `net`) — handy for trying a real service with a token from `dev.config.json`.
- **Reloads.** Saving any file reloads the plugin as an update: the database and `kv` stay,
  new migrations apply. If you edited a migration already applied, or `dev.config.json`,
  restart `dev`.
- **A real panel.** With `--panel` and `--key`, `panel.api` goes to it:

  ```sh
  rospanel plugin dev . --panel https://vpn.example.com/<API path> --key rp_…
  ```

  The API path and keys are in the panel's **Settings → API**. Use a key with only the
  permissions you need: changes through it are real.

## validate — before publishing

```sh
rospanel plugin validate .
rospanel plugin validate my-plugin-1.2.0.zip
```

```
ok: my-plugin 1.2.0 — 14 KB, exports onEvent, sync, userFields
```

`validate` checks the manifest, the package's contents and the theme's styles, loads the code,
checks the exports, and lists every problem.

## Debugging in the panel

- **Plugins → Log** — the last 1000 lines: your `panel.log`/`console`, failed calls with the
  reason, the plugin being paused.
- **Plugins → Code** — what is installed now.
- **The panel's journal** — what the plugin changed through `panel.api` (actor
  `plugin:<id>`), and presses of its buttons.
- A paused plugin's card says why and when the panel retries it; press **Start again** once
  the cause is fixed.

Common errors and what they mean are in [limits and errors](troubleshooting.md).
