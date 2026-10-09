# Plugin API: `panel.*`

[Русская версия](api-RU.md) · [Contents](README.md)

`panel` is the global object a plugin reaches the panel through. The same types, with
completion, are in `rospanel.d.ts` (`rospanel plugin new` creates it).

**Every call is synchronous:** the result comes back right away, without `await` or
callbacks. `async`/`await` work in the code, but there is nothing to wait for: there are no
timers (`setTimeout`). An error of any call is an ordinary JavaScript exception you can catch
with `try/catch`.

**Data.** Strings travel as UTF-8. Bytes as `{base64: "…"}` (in `crypto`, `db`, `blob.from`).

---

## panel.plugin and panel.config

```js
panel.plugin   // {id: "my-plugin", version: "1.2.0"}
panel.config   // {"token": "…", "mode": "live", "limit": "100", "enabled_log": "true"}
```

`panel.config` is the operator's settings, decrypted, defaults included. **Every value is a
string**: a `bool` is always `"true"` or `"false"` (unset reads `"false"`), a `number` comes as
`"100"`. An optional field left
empty without a `default` is absent. The object is read-only; after a settings change the
plugin restarts with the new one.

```js
const limit = Number(panel.config.limit || 100);
const verbose = panel.config.enabled_log === "true";
```

## panel.log and console

```js
panel.log.info("sync", { users: 12 });
panel.log.warn("the service answered 429");
panel.log.error(new Error("…").message);
console.log("the same as panel.log.info", { a: 1 });
```

Every line shows in the panel (**Plugins → Log**, the last 1000). Warnings and errors are also
copied into the server's log tagged `plugin=<id>`, up to 20 a minute; `info` lines go there at
debug level only. A line is up to 4 KB. Do not log secrets.

## panel.kv — simple values

```js
panel.kv.set("last_sync", { at: Date.now(), count: 12 });
panel.kv.get("last_sync");            // {at: …, count: 12} or undefined
panel.kv.delete("last_sync");
panel.kv.list("user/");               // [{key: "user/1", value: …}, …] ordered by key
panel.kv.list("user/", { after: "user/500", limit: 100 });
```

| | |
|---|---|
| Key | a string, up to 512 bytes |
| Value | any JSON value, up to 64 KB serialized; `undefined` is an error (`delete` removes) |
| `list` | up to 1000 entries at a time (`limit`), paged with `after` |

`kv` lives in the plugin's database (the `_kv` table), so it goes into backups and rolls back
with it.

## panel.db — its own SQLite database

The plugin has its own SQLite file (`plugins/<id>.db` in the panel's data directory).
Migrations create the tables — `migrations/NNNN_name.sql` files in the package. They run in
name order when the plugin is switched on, each once. The panel remembers a shipped
migration's hash and does not let an update change it.

```js
const { changes, last_insert_id } = panel.db.exec(
  "INSERT INTO orders(user_id, amount) VALUES (?, ?)", 42, 199);

const rows = panel.db.query("SELECT id, amount FROM orders WHERE user_id = ? ORDER BY id DESC LIMIT 10", 42);
// [{id: 3, amount: 199}, …]

panel.db.tx(() => {
  panel.db.exec("UPDATE balance SET rub = rub - ? WHERE user_id = ?", 100, 42);
  panel.db.exec("INSERT INTO history(user_id, rub) VALUES (?, ?)", 42, -100);
}); // an exception inside rolls everything back
```

- **`?` arguments:** a string, a number (a whole one is written as INTEGER), `true`/`false`,
  `null`, `{base64: "…"}` for a BLOB.
- **`query`'s result** is an array of objects "column → value"; a BLOB comes back as
  `{base64}`. At most 10,000 rows and 4 MB — page big reads with `LIMIT`/`OFFSET`.
- **`exec`** returns `{changes, last_insert_id}`.
- **`tx(fn)`** runs `fn` in a transaction. If `fn` returns a value, the transaction commits
  and `tx` returns it. If it throws — rollback, and the exception goes on. `tx` does not nest.
  A transaction still open at the end of a call is rolled back by the panel.
- **Refused:** `PRAGMA`, `ATTACH`, `DETACH`, `VACUUM`, `EXPLAIN`, `BEGIN`, `COMMIT`, `END`,
  `ROLLBACK`, `SAVEPOINT`, `RELEASE` — in code and in migrations. Transactions go only
  through `tx`. Triggers, indexes, views, `WITH`, `RETURNING`, `UPSERT` (`ON CONFLICT`) work.
- **Quota:** `db_quota_mb` (100 MB by default). A write past it throws an error with
  `over its quota`. If the database is still 90% full or more after that call, the plugin is
  paused until the operator decides; a call that frees room keeps it running. Pages of deleted
  rows are reused, and the write-ahead log is trimmed back to 8 MB after checkpoints.
- **Sizes:** one value up to 8 MB, one statement's text up to 1 MB.

Change the schema with a new migration (`0002_add_email.sql` with `ALTER TABLE …`). Before
every update the panel snapshots the database, and **Roll back** restores it.

## panel.api — the panel's REST API

```js
const r = panel.api("GET", "/v1/users/42");
// r = {status: 200, body: {data: {id: 42, name: "ann", …}}}

const upd = panel.api("PATCH", "/v1/users/42", { data_limit: 10737418240 });
if (upd.status !== 200) throw new Error(upd.body.error.message);

const page = panel.api("GET", "/v1/users?limit=500&offset=0").body.data;
```

- **Routes.** The same as the external `/v1` (see the panel's `/v1/docs` and
  [docs/api.md](../api.md)), except `/v1/mcp`. Methods: `GET`, `POST`, `PUT`, `PATCH`,
  `DELETE`.
- **Permissions** — those the plugin asked for in `permissions` and the operator approved.
  Without one — `status: 403`.
- **API errors do not throw:** look at `status` and `body.error` (`{code, message, key,
  args}`). Only a wrong path or method, or a limit exceeded, throws.
- **The answer's `body`** is parsed JSON, or the text when the answer is not JSON.
- **Limits:** request body up to 1 MB, answer up to 4 MB. Take lists page by page (`limit`
  up to 1000, `offset`). An answer to be sent somewhere whole can go into a blob:
  `panel.api("GET", path, null, {blob: true})`.
- **The journal.** Changes are recorded in the panel's journal under the plugin's name
  (`plugin:<id>`), and the events they cause reach other plugins too.
- **Read-only** in decisions (`beforeSignup`, `beforeDeviceBind`, `quotePrice`), `bot.menu`
  and `transformSubscription`: only `GET` is allowed there.
- **Not into itself.** A request that would call the plugin itself back (an order paid with
  its own payment method) fails: `plugin: a call into itself (through the panel) is refused`.

Shortcuts:

```js
panel.users.get(42)                        // = panel.api("GET", "/v1/users/42")
panel.users.list("status=active&limit=50") // = panel.api("GET", "/v1/users?status=active&limit=50")
panel.users.update(42, { note: "VIP" })    // = panel.api("PATCH", "/v1/users/42", {note: "VIP"})
```

## panel.http.fetch — requests to the internet

```js
const r = panel.http.fetch("https://api.example.com/v1/items?limit=10", {
  method: "POST",                                   // GET by default
  headers: { Authorization: "Bearer " + panel.config.token },
  body: { name: "test" },                           // an object → JSON with Content-Type
  timeout_ms: 5000,                                 // up to 30,000
});
// r = {status: 201, headers: {"content-type": "application/json"}, body: "{\"id\": 1}"}
const item = JSON.parse(r.body);
```

- **Where.** Only hosts in the manifest's `net`; an entry without a port allows ports 80 and
  443. The panel's own addresses and private, loopback, CGNAT, NAT64, 6to4 and site-local ones
  are never reachable, even when an allowed name resolves to one.
- **The body.** A string is sent as is, anything else as JSON (`Content-Type:
  application/json` is set unless you set one). A blob is streamed. `form: {…}` sends
  multipart/form-data (see blobs below).
- **The answer.** `body` is always text — parse JSON yourself; header names are lower-case;
  `{blob: true}` puts the answer in a blob.
- **Errors.** A 4xx/5xx answer does not throw — check `status`. A network error, a timeout, a
  host not in `net`, or a limit exceeded throws. The message names only `scheme://host`, never
  the path or query.
- **Limits.** A string body up to 1 MB, an answer up to 4 MB, up to 30 s, up to 5 redirects,
  and only to hosts in `net`. `User-Agent: RosPanel-Plugin/1` unless you set your own.

## panel.blob — big data

A blob is data the panel keeps in a temporary file outside the plugin's memory, for the
length of one call.

```js
const file = panel.blob.from("id,name\n1,Ann\n");          // from text or {base64}
panel.blob.hash(file, "sha256");                            // hex (or "base64")
panel.blob.text(file);                                      // back to a string, up to 4 MB
panel.blob.text(file, "base64");                            // or base64

// sources
const dump = panel.api("GET", "/v1/users?limit=1000", null, { blob: true }).body;
const img = panel.http.fetch("https://cdn.example.com/a.png", { blob: true }).body;

// sinks
panel.http.fetch("https://s3.example.com/bucket/a.png", { method: "PUT", body: img });
panel.http.fetch("https://api.telegram.org/bot…/sendDocument", {
  method: "POST",
  form: { chat_id: "42", caption: "report", document: { blob: file, filename: "report.csv", type: "text/csv" } },
});
```

- **The handle.** A blob is `{blob: "b…", size: 123}`; pass it on within the call. A handle
  kept in `kv` is dead after the call: the files go when the call ends.
- **`form` fields.** A string or a number is a text field. A blob is a file (named after the
  field). `{blob, filename, type}` is a file with its own name and type.
- **Limits.** Up to 256 MB a blob, 16 blobs and 512 MB a call. A transfer is still bound by
  the call's time (60 s for cron, for one).

## panel.crypto

```js
panel.crypto.hash("sha256", "text")                       // hex
panel.crypto.hash("md5", { base64: "AAEC" }, "base64")
panel.crypto.hmac("sha256", panel.config.secret, body)    // a webhook signature
panel.crypto.sign("RS256", privatePem, data)              // base64
panel.crypto.verify("Ed25519", publicPem, data, sigBase64) // true/false
panel.crypto.jwt("RS256", privatePem, { iss: "…", exp: Math.floor(Date.now() / 1000) + 3600 })
panel.crypto.randomHex(16)                                // 32 hex characters
panel.crypto.randomUUID()
```

| Function | |
|---|---|
| `hash(alg, data, enc?)` | `alg`: `md5`, `sha1`, `sha256`, `sha512`; `enc`: `hex` (default), `base64`, `base64url` |
| `hmac(alg, key, data, enc?)` | the same `alg` and `enc` |
| `sign(alg, pem, data)` | `RS256`, `RS512`, `ES256` (signature `r‖s`, as JWS wants), `Ed25519`; base64 |
| `verify(alg, pem, data, sig)` | `sig` in base64; the key — public, private or a certificate |
| `jwt(alg, pem, claims, header?)` | a ready `header.claims.signature`; `typ: JWT` and `alg` (`EdDSA` for Ed25519) are set for you, the rest of `header` is added |
| `randomHex(n)` | `n` random bytes (1–1024) in hex |
| `randomUUID()` | a v4 UUID |

`data` and `key` are a string or `{base64}`. Keys in PEM: `PRIVATE KEY` (PKCS#8),
`RSA PRIVATE KEY`, `EC PRIVATE KEY`, `PUBLIC KEY`, `RSA PUBLIC KEY`, `CERTIFICATE`.

To check a webhook signature: compute `hmac` over the body exactly as it came (the `body`
string, without re-serializing the JSON) and compare it with the header in full.

## panel.time — the panel's timezone

```js
const at = e.data.expire_at;                          // unix seconds
const local = new Date((at + panel.time.offset(at)) * 1000);
local.getUTCHours();                                  // the hour as the panel shows it
```

`offset(unix)` is how many seconds the panel's timezone (Settings → General) is ahead of UTC at
that moment; without an argument, now. The sandbox has no timezone database of its own.

## panel.t — translations

```js
// i18n/en.json: {"hello": "Hello, {name}!"}
panel.t("hello", { name: "Ann" }, "en")    // "Hello, Ann!"
panel.t("hello", { name: "Ann" })          // the call's language, else en, else ru
panel.t("no such key")                     // the key itself
```

Looks the key up in `i18n/<lang>.json`, then `en`, then `ru`, and returns the key itself when
none has it. `{name}` is filled from `params` — pass values as strings. Without `lang`, the
call's language is used: for decisions and the bot, the user's.
