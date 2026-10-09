# Limits, security and common errors

[Русская версия](troubleshooting-RU.md) · [Contents](README.md)

## How the sandbox works

- **WebAssembly.** A plugin runs in the QuickJS engine compiled to WebAssembly, inside the
  panel's process. It has no file system, no processes, no environment variables and no
  network — only what `panel.*` gives it.
- **Network** — only `panel.http.fetch`, only to hosts in `net`. The panel's own, private and
  local addresses are unreachable whatever the DNS says, redirects included.
- **The panel's API** — only with the permissions the operator approved. Keys, admins, updates
  and other plugins are never within reach.
- **Data** — its own SQLite database with a quota. The panel's database and other plugins'
  are out of reach.
- **Where it runs.** On the main server only, not on nodes. Its database goes into the panel's
  backups.
- **What the operator sees.** Before switching it on: the permissions, hosts, points and code
  (the pencil on the card opens it in the editor). The plugin's actions: in the panel's journal as `plugin:<id>`.

## Limits

| | Limit | Past it |
|---|---|---|
| JS memory | `memory_mb`, 32 MB (up to 128) | the call throws `InternalError: out of memory`, the VM is replaced |
| Call time | see the table in [extension points](exports.md): 0.3 s – 60 s | `InternalError: interrupted` |
| Database | `db_quota_mb`, 100 MB (up to 1024) | the write is refused; the plugin is paused if the database stays 90% full |
| A database query | 10,000 rows, 4 MB of answer; one value up to 8 MB, the statement text up to 1 MB | an error — use `LIMIT`/`OFFSET` |
| `kv` | a 64 KB value, a 512-byte key, `list` up to 1000 | an error |
| `panel.api` | 1 MB request, 4 MB answer | an error — page through, or use a blob |
| `panel.http.fetch` | 1 MB body, 4 MB answer, 30 s, 5 redirects | an error — big data through blobs |
| blob | 256 MB each, 16 and 512 MB per call | an error |
| `onHttp` | 1 MB bodies each way | an error |
| Package | 5 MB zipped, 12 MB unpacked, `main.js` 2 MB, 200 files | the install is refused |
| Log | 1000 lines, a line up to 4 KB | old lines drop off |

QuickJS in WebAssembly runs some 20–30 times slower than native code. Plenty for
integrations, but keep heavy computation (big loops, crypto in JS) out of plugins — hashes and
signatures are in `panel.crypto`.

## When the panel pauses a plugin

The plugin goes to "paused" (`paused`), and admins are told in the admin bot.

**Failures.** After 10 failed calls in a row (an exception or a timeout) the panel pauses the
plugin and retries it itself: in 5 minutes, in 15, then every hour. Events already waiting for
it are kept. After a retry, 3 failures in a row pause it again. A plugin that failed to start
with the panel is retried the same way. About failures admins are told at most once every
6 hours. Failures of `onHttp` and the payment `webhook` are logged but not counted.

**The quota and loops.** These pauses wait for the operator, and the events waiting for the
plugin are dropped:

- **the database outgrew its quota** — a write was refused and the database is still 90% full
  after the call;
- **a loop** — three minutes in a row of over 300 events a minute and over 100 changes a minute
  by the plugin itself through `panel.api`: it changes a user on an event that the change fires
  again.

A paused plugin gets no calls, and events that happen meanwhile do not reach it. Its card says
why it stopped and when the panel retries it. The operator presses **Start again** once the
cause is fixed. Orders of its payment method wait: the panel does not cancel them by age while
the plugin is installed.

A failed `subBlocks`, `bot.menu` or `transformSubscription` is not called again for 30 s; the
plugin keeps running.

## Common errors

| Message | Cause and what to do |
|---|---|
| `InternalError: interrupted` | the call ran out of time. Move long work to cron, split it, keep progress in `kv` |
| `InternalError: out of memory` | `memory_mb` was not enough. Do not hold big arrays whole: read the database page by page, take big answers into blobs, raise `memory_mb` |
| `fetch: X is not in the plugin's net allowlist` | the host is not in the manifest's `net`, or the port is not 80/443 for an entry without one. Add it, with `:port` if needed (a new consent for the operator) |
| `plugin fetch: … is a private or local address` | the host resolves to a private or local address, or one of the panel's own — a plugin never goes there |
| `fetch: redirect to …, not in the allowlist` | the service redirected to a host not in `net` |
| `fetch: body over 1024 KB — send a blob` | send a big body as a blob |
| `fetch: answer over 4 MB` | take the answer into a blob: `{blob: true}` |
| `panel.api: path "…" is not a /v1 route` | the path must start with `/v1/` |
| `panel.api: only GET inside a decision hook` | decisions (`beforeSignup`, `quotePrice`, …), `bot.menu` and `transformSubscription` may only read |
| `plugin: a call into itself (through the panel) is refused` | a `panel.api` request led back into the same plugin (an order paid with its own method) |
| `plugin: busy` | the plugin was busy with another call longer than this call's time — keep cron jobs short |
| `panel.api: answer over 4 MB — page with limit/offset` | use `limit`/`offset`, or `{blob: true}` |
| `status: 403` from `panel.api` | the plugin lacks the route's permission — add it to `permissions` |
| `pdb: statement not allowed: PRAGMA …` | a refused statement; transactions go through `panel.db.tx` |
| `pdb: database is over its quota` | the database reached `db_quota_mb`. Delete old rows (their room is reused) or raise the quota |
| `kv value over 65536 bytes` | a value over 64 KB — keep it in a table |
| `blob "…": not a blob of this call` | a handle from another call; a blob lives one call |
| `main.js does not export what plugin.json declares: …` | `provides` declares what `main.js` does not export (check the name and `export`) |
| `plugin: fill in the settings first: …` | required settings are empty |
| `provides.X needs the "…" permission: …` | the point needs a permission — add it to `permissions` |
| `provides.X: not available in this panel version yet` | the point came in a newer panel — raise `panel` in the manifest and update the panel |
| `provides.X is experimental: add "X" to "experimental"` | add the point to `experimental` |
| `a paid payment must report amount_kopecks and currency` | `payment.status`/`webhook` returned `paid` without the amount |
| `payment.create must return provider_id and an http(s) pay_url` | an incomplete `create` answer |
| `migrations: 0001_init.sql was changed — add a new migration instead` | a shipped migration was edited — restore it and add a new one |

## Questions

**npm packages, TypeScript?** Yes, built into one ES module `main.js` (esbuild:
`esbuild src/index.ts --bundle --format=esm --outfile=main.js`). Packages needing Node (`fs`,
`net`, `process`, `Buffer`) will not work. The network is only `panel.http.fetch`.

**What JavaScript is there?** The whole language, recent standards included: classes,
`async`/`await`, `Map`, `Set`, `BigInt`, `WeakRef`, `Array.prototype.at`/`findLast`/`toSorted`,
`Object.groupBy`, `JSON`, `Date`, `Math`, `RegExp`, and `atob`/`btoa`. Not what a browser or
Node adds: `setTimeout`/`setInterval`, `fetch` (use `panel.http.fetch`), `URL` and
`URLSearchParams` (build the string with `encodeURIComponent`), `TextEncoder`/`TextDecoder`,
`Intl` (`toLocaleString` does not format by language), `structuredClone`, `require`/`import`
of other files.

**How do I keep data past a call?** In `panel.kv` or `panel.db`. Globals are lost when the VM
restarts.

**How do I do something later, or regularly?** Declare a `cron` job and keep the queue in your
database.

**Does a plugin see other plugins' data?** No. Its database, settings and `kv` are its own.

**What happens on uninstall?** The plugin and its database are removed. The operator can
uninstall keeping the database (**Remove → keep the data**): installing the same plugin later
picks it up.

**Does a plugin run on nodes?** No, on the main server only. It sees nodes through
`panel.api` (`/v1/nodes`, permission `servers.view`).

**Can the `id` change?** No. A new `id` is a new plugin with an empty database.
