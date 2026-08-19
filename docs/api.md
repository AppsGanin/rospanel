# RosPanel REST API

The external REST API lets a surrounding system (a billing service, a Telegram
shop, a provisioning script) manage the panel over HTTP with an API key. It calls
the same internal logic the admin panel does, so the two never drift.

## Enabling the API

Open the panel → **Settings → API**. Creating your first key turns the surface on
and generates a stable, unguessable base URL:

```
https://<your-host>/<api_path>/v1
```

The `<api_path>` segment is separate from the hidden panel path, so rotating the
panel secret never breaks integrations. You can rotate or disable the API path
from the same page (rotating changes the base URL; keys keep working under the new
one).

## Interactive docs & machine-readable spec

The API publishes its own OpenAPI 3.0 spec, generated from the server code (the
schemas are reflected from the actual Go types, so they never drift):

```
GET $BASE/openapi.json    → the OpenAPI 3.0 document
GET $BASE/docs            → Swagger UI (try endpoints in the browser)
```

Both are served without a key (the base URL itself is the secret). Open `…/docs`,
click **Authorize**, paste a key, and call any endpoint live. Point Postman /
`openapi-generator` / any client generator at `…/openapi.json` to scaffold a
typed client. (The Swagger UI shell loads from a CDN; the spec it renders is
fully local.)

## Authentication

Every request must carry a key, created in **Settings → API**. The raw key is
shown **once** at creation — store it immediately; only its prefix is kept
afterwards. Send it as a bearer token:

```
Authorization: Bearer rp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

(`X-API-Key: <key>` is also accepted.) Revoked keys stop working immediately.

A missing or invalid key returns `401`. The surface is per-IP rate-limited.

## Response envelope

Success:

```json
{ "data": { ... } }
```

Error:

```json
{ "error": { "code": "bad_request", "message": "name is required" } }
```

Common codes: `bad_request` (400), `unauthorized` (401), `not_found` (404),
`unsupported_media_type` (415), `internal` (500).

Input the panel refuses carries two more fields — the reason it refused, and the
parameters of that reason:

```json
{ "error": {
    "code": "bad_request",
    "key": "err.planHasUsers",
    "message": "the plan is assigned to 12 users — move them to another plan first",
    "args": { "count": 12 }
} }
```

`code` stays the coarse class to branch on for retries; `key` is the specific,
stable reason to branch on for behaviour — never match on `message`, which is
free to be reworded. `message` is always English (the panel translates the same
codes in the browser, in whichever language the admin chose); `args` lets a client
render its own wording without parsing prose. Both are absent when the failure has
no code behind it (a malformed body, an unknown path).

## Request bodies

A field the endpoint does not define is an error, not something to ignore:

```json
{ "error": {
    "code": "bad_request",
    "message": "unknown field \"groups\" — see GET /v1/openapi.json for the fields this endpoint accepts"
} }
```

Accepting the body and quietly dropping the field is worse than refusing it: a
caller that sends `{"groups": [2,4]}` to an endpoint expecting `group_ids` gets a
success back and no membership change, and finds out whenever they next read the
value. So check the spelling against the spec — it is generated from the code, so
it cannot list a field the panel doesn't read.

Only fields the endpoint genuinely requires are marked `required` there. Everything
else may be omitted and keeps its documented default; you never have to send a full
object to change one thing.

### Concurrent edits

There is no version or `If-Match` on writes: two callers changing the same object
resolve last-write-wins, and neither is told. This matters most where a write carries
fields it did not intend to change — a node edit re-sends the whole node, so a routing
change written from here can overwrite a name someone set in the panel a second
earlier.

In practice it bites rarely, because the endpoints that patch (`PATCH /v1/settings`,
`PATCH /v1/users/{id}`) apply only the fields the body carries, so two callers touching
*different* fields do not collide. If you automate edits to the same object from more
than one place, read it back after writing rather than assuming your value stuck.

## Paging

Every list endpoint takes `?limit` and `?offset` and answers with a `meta` block:

```json
{ "data": [ … ], "meta": { "total": 1043, "offset": 0, "limit": 100 } }
```

`limit` defaults to **100** and is capped at **1000**; `limit=0` (or negative) means
"everything from `offset`", which is a deliberate ask rather than the default — an
unbounded default is how an integration works fine for a year and then times out on a
panel that grew. `total` counts the rows after filtering and before the window, so a
caller can page without guessing.

The journals (`/v1/events`, `/v1/admin-audit`) page differently, by cursor
(`?before=<oldest id held>`): their rows shift as new ones arrive, and an offset would
skip or repeat entries.

## Endpoints

Base URL below is written as `$BASE` (e.g. `https://vpn.example.com/ab12cd34/v1`).

### Health

```
GET $BASE/health → { "data": { "status": "ok" } }
```

#### Liveness probe (no API key)

`GET $BASE/healthz` is the one endpoint that needs no key — point an uptime monitor
or a load balancer at it. It answers **503** (not 200) when Xray isn't running: the
panel may be fine, but the node is carrying no VPN traffic, which is what you want
to be paged about.

```
GET $BASE/healthz
200 → { "data": { "status": "ok",       "xray": "running", "xray_started_at": 1752230400 } }
503 → { "data": { "status": "degraded", "xray": "down",    "xray_started_at": 0 } }
```

It lives under the API path rather than at the server root on purpose: an
unauthenticated `/healthz` on the root would answer JSON to any scanner and give the
panel away, defeating the decoy. The API path is stable across secret rotation, so a
monitor pointed here keeps working.

### Users

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/users` | List users (filter + paginate). |
| `POST` | `/v1/users` | Create a user. |
| `POST` | `/v1/users/bulk` | Apply one action to many users at once. |
| `GET` | `/v1/users/{id}` | Get one user. |
| `PATCH` | `/v1/users/{id}` | Update name / limits / expiry / device limit / speed limit / enabled. |
| `DELETE` | `/v1/users/{id}` | Delete a user. |
| `POST` | `/v1/users/{id}/reset` | Reset the user's traffic counters. |
| `POST` | `/v1/users/{id}/reset-period` | Set auto-reset period. |
| `POST` | `/v1/users/{id}/rotate-sub` | Issue a new subscription URL (old link dies). |
| `POST` | `/v1/users/{id}/plan` | Apply a tariff plan to the user. |
| `POST` | `/v1/users/{id}/plan/cancel` | Cancel a paid subscription now. |
| `GET` | `/v1/users/{id}/connections` | List the user's recent source IPs / devices. |
| `GET` | `/v1/users/{id}/devices` | List the installs bound by HWID, with the cap they count against. |
| `POST` | `/v1/users/{id}/devices/unbind` | Release one bound device (or all), freeing the slot. |
| `GET` | `/v1/users/{id}/events` | The user's own journal (paged). |
| `GET` | `/v1/users/{id}/abuse` | The user's blocklist matches (`limit`, default 20). |

**Create** — `name` is required; everything else is optional and applied to the fresh
account in the same call:

```json
{ "name": "alice", "data_limit": 0, "expire_at": 0,
  "device_limit": 3, "speed_limit": 20000, "plan_id": 2, "group_ids": [4] }
```

**Devices** are the installs bound by the `x-hwid` subscription header (see the panel's
*Settings → Subscriptions*). The list carries the cap alongside the roster, so one call
answers "is this user full":

```json
{ "data": { "devices": [ { "hwid": "…", "os": "iOS", "os_version": "26.5.2",
    "model": "iPhone 15 Pro Max", "app": "Happ/1.0", "ip": "203.0.113.7",
    "first_seen": 1786500000, "last_seen": 1786568000 } ],
  "limit": 3, "enabled": true } }
```

Unbinding takes either one id or the lot, and answers with how many slots were freed:

```json
{ "hwid": "…" }        →  { "data": { "removed": 1 } }
{ "all": true }        →  { "data": { "removed": 3 } }
```

A device that was never bound is a `404`, not a silent success. The freed slot is
immediately available: the next fetch from a new install is admitted.

`speed_limit` is a per-user bandwidth cap in **kbit/s** (0 = unlimited), applied in both
directions. It is enforced by the host kernel on the addresses the user is connected from,
not by Xray — so everyone behind one NAT address shares a cap, and with Hysteria2 the cap
manifests as packet loss rather than a smooth slowdown. A tariff plan carries its own
`speed_limit` and overwrites the user's when it is applied, exactly as it does for the
traffic and device limits.

`plan_id` rewrites `data_limit` and `expire_at` with the plan's own — a plan *is* the
limits — while an explicit `device_limit` is applied after it and wins. If one of the
extras fails, the account is NOT rolled back (credentials may already be in someone's
hands): the call returns the error and the user stays as far as it got. Responds `201`
with the user as it actually ended up.

**Cancel a subscription** — moves the user to the free plan immediately (losing the
remaining paid time), or ends their access when no free plan is configured. Distinct
from applying the free plan by hand: this is recorded as `plan.cancelled`, which is
the event a billing integration reacts to.

**List** — query params: `status` (`active` / `disabled` / `expired` / `limited`
/ `device_limited`), `search` (substring of the name), `limit`, `offset`
(`limit<=0` = all from `offset`). The response adds a `meta` block:

```json
{ "data": [ ... ], "meta": { "total": 42, "offset": 0, "limit": 20 } }
```

**Bulk** — body:

```json
{ "ids": [1, 2, 3], "action": "extend", "days": 30 }
```

`action` is one of `enable`, `disable`, `delete`, `reset`, `extend` (`days` is
required only for `extend`). Response: `{ "data": { "affected": 3 } }`.

**Reset period** — body: `{ "period": "monthly" }` (`none` / `daily` / `weekly`
/ `monthly` / `yearly`).

**Create** — body:

```json
{ "name": "alice", "data_limit": 0, "expire_at": 0 }
```

`data_limit` is bytes (0 = unlimited); `expire_at` is a Unix timestamp
(0 = never). The response `data` is the full user object, including `sub_url`, the
built-in lanes' `vless` / `reality` / `hysteria2` share links, and `links` — every
lane the user has on this server, custom inbounds included, each with the name the
client will display.

**Patch** — send only the fields you want to change:

```json
{ "name": "alice2", "data_limit": 107374182400, "expire_at": 1767225600, "device_limit": 3, "enabled": true }
```

**Apply plan** — body:

```json
{ "plan_id": 2, "extend_from_current": false }
```

### Billing

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/billing/providers` | List the enabled payment methods (what a client can pay with). |
| `GET` | `/v1/billing/plans?include_disabled=true` | List tariff plans. |
| `POST` | `/v1/billing/plans` | Create (no `id`) or update (`id` set) a plan. |
| `DELETE` | `/v1/billing/plans/{id}` | Delete a plan (refused while users are on it). |
| `POST` | `/v1/billing/plans/{id}/migrate` | Move every user on this plan to another one. |
| `GET` | `/v1/billing/orders?status=pending` | List payment orders (`status` optional). |
| `POST` | `/v1/billing/orders` | Open an order for a user+plan. |
| `GET` | `/v1/billing/orders/{id}` | Get one order (poll a payment's status). |
| `POST` | `/v1/billing/orders/{id}/confirm` | Mark an order paid (activates the plan). |
| `POST` | `/v1/billing/orders/{id}/cancel` | Cancel an order. |
| `GET` | `/v1/billing/settings` | Billing configuration. |
| `POST` | `/v1/billing/settings` | Replace it (whole object). |
| `GET` | `/v1/billing/stats` | Revenue totals, per-provider split, pending backlog. |
| `GET` | `/v1/payments` | Every payment provider with its settings form. |
| `POST` | `/v1/payments` | Configure one provider. |

A plan may name the access groups it grants (`"group_ids": [3]`): whoever is put on
the plan — by a paid order, by `POST /v1/users/{id}/plan`, or at registration — joins
those groups and leaves them when the plan changes. Memberships assigned directly
through `POST /v1/users/{id}/groups` are kept; only what the plan granted is taken
back. An empty list means the plan says nothing about access.

**Create order** — body `{ "user_id": 5, "plan_id": 2 }`. The response carries the
order and, when a payment provider is configured, a hosted `pay_url` to send the
user to:

```json
{ "data": { "order": { ... }, "pay_url": "https://..." } }
```

A manual order returns an empty `pay_url` and waits for `/confirm`. Creating an order
is **not** idempotent by key, but it does not stack duplicates either: a still-pending
order for the same user, plan and provider is reused instead of a second one being
opened, so a retried call returns the order that already exists.

**Billing settings** — the whole object, replaced on write (there is no partial
update: "no free plan" is a real state and must be distinguishable from "unspecified"):

```json
{ "enabled": true, "free_plan_id": 1, "trial_plan_id": 2, "payment_note": "card 1234" }
```

Designating a plan as the free or trial one also makes it free and re-applies it to
everyone already on it — the same rule the panel enforces.

**Migrate** — body `{ "to_plan_id": 3 }`, response `{ "data": { "migrated": 12 } }`.
Applies the target plan's limits, period and access groups to every user on the source
plan. This is the only way to empty a plan before deleting it.

**Providers** — `GET /v1/payments` returns each provider in the registry with its
settings form: the fields it takes, their current values, which secrets are set (never
their values) and the `webhook_url` to paste into the provider's dashboard. The field
list is per provider and generated from the registry, so this payload is deliberately
free-form. `POST /v1/payments` takes `{ "key": "yookassa", "enabled": true, "config":
{...} }`; a secret left empty keeps its stored value.

`GET /v1/billing/providers` is dynamic — it returns whatever payment methods you've
enabled in the panel (cards, SBP, crypto, …), each with a `key`. That `key` is what you
pass as `provider` when opening an order; omit it for a manual order.

### Nodes

Manage the server fleet. The local panel server is node `0`; the rest are remote
**nodes** that hold an outbound long-poll to the panel. Users and limits sync to every
enabled node automatically, and each server can be edited independently.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/nodes` | List servers (the local panel is node `0`) with status and today's traffic. |
| `POST` | `/v1/nodes` | Register a node — returns the one-line install command (join token shown once). |
| `GET` | `/v1/nodes/{id}` | Get one node. |
| `PATCH` | `/v1/nodes/{id}` | Edit name / host / protocol / routing / DNS overrides and WARP-Opera egress. |
| `DELETE` | `/v1/nodes/{id}` | Delete a node (it stops serving users). |
| `POST` | `/v1/nodes/{id}/enabled` | Enable or disable a node. |
| `POST` | `/v1/nodes/{id}/regen-join` | Issue a fresh install command (revokes the node's current token). |
| `POST` | `/v1/nodes/{id}/update` | Ask a node to self-update to the latest release. |
| `POST` | `/v1/nodes/update-all` | Ask every connected node to self-update (sequentially). |
| `POST` | `/v1/nodes/{id}/proxy` | Configure that server's system proxy (`id` 0 = the master). |
| `GET` | `/v1/nodes/{id}/health` | One server's self-diagnostics. |
| `GET` | `/v1/nodes/{id}/logs` | A node's recent log lines. |

**Register a node** — body `{ "name": "NL #1", "host": "nl1.example.com" }`. The
response carries the node id, the one-time join token and the ready-to-run install
command for a fresh Ubuntu server:

```json
{
  "data": {
    "id": 3,
    "join_token": "rpn_…",
    "install_command": "curl -Ls https://.../install.sh | sudo bash -s -- --join '…#rpn_…'"
  }
}
```

The join token is embedded once and expires in 24h; `/regen-join` issues a new one.

**System proxy** — a SOCKS5 and/or HTTP forward listener on that server, for traffic
that is not a VPN client: a scraper, a bot, another panel chaining its egress here.
No user credential opens it, no access group gates it, and it never appears in a
subscription. Traffic leaves under that server's routing, so WARP, Opera and the proxy
lanes apply exactly as they do for VPN clients.

```json
{ "socks_enabled": true, "socks_port": 1080,
  "http_enabled": true,  "http_port": 3128,
  "accounts": [ { "user": "scraper", "pass": "…" },
                { "user": "bot",     "pass": "…" } ] }
```

`accounts` is the full list every time — the way to delete one is to send the list
without it. Logins must be unique and carry no colon or space (a colon is the
separator in `user:pass`), and at least one complete account is required whenever
either listener is on: an open proxy on a public port is found by scanners within
hours, and is then somebody else's spam relay with this server's IP on it. A protocol
enabled without a port gets 1080 (SOCKS) / 3128 (HTTP). Each server has its OWN
accounts and ports — a node never inherits the master's, so a leaked login opens one
machine. The response is the stored configuration, so a caller that sent only
`{"socks_enabled": true}` learns which port it got.

### Custom inbounds

Operator-defined inbounds beyond the three built-in lanes, one set per server (server
`0` = the master). Each is a public listener, so a create/update is validated **on the
target machine** (`xray -test` + a port-bind probe) before it's saved.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/servers/{id}/inbounds` | List one server's custom inbounds (`id` = server id, `0` = master). |
| `POST` | `/v1/servers/{id}/inbounds` | Create a custom inbound on that server. |
| `POST` | `/v1/inbounds/{id}` | Update a custom inbound (keyed by the inbound's own id). |
| `DELETE` | `/v1/inbounds/{id}` | Delete a custom inbound. |

**Create / update** — the body mirrors the panel's inbound editor: `name`, `protocol`
(`vless` / `trojan` / `hysteria2` / `shadowsocks`), `transport` (`tcp` / `ws` / `xhttp` /
`grpc` / `httpupgrade`), `port`, `security` (`none` / `tls` / `reality`) with the matching
keys (REALITY dest & keys, fingerprint, path/host, Hysteria2 hop range), plus optional
advanced blocks (XHTTP `extra`, TCP HTTP masquerade, `sockopt`, extra TLS keys). The
full field list — and which combinations are valid — is in `openapi.json` / Swagger.
`shadowsocks` is Shadowsocks-2022 (`method` picks the AEAD; the server key is generated,
the per-user key derived from the UUID) — multi-user, so per-user stats and quotas work,
but only modern clients (sing-box, mihomo, v2rayN, Shadowrocket, Streisand) speak it.
Two formats the schema can only call `string`: `reality_dest` is a bare hostname
(`www.microsoft.com` — a `host:port` form is rejected), and `hop_interval` is a range in
seconds (`"5-10"`, not `"30"`).
The response `data` is the saved inbound; a rejected config (port already bound, invalid
combo, node offline) returns `400` with the reason. Inbounds a client can't represent are
silently dropped from Clash/sing-box subscriptions rather than emitted broken.

An inbound is addressable by an access group via the `inbound:<id>` grant token (see
**Groups**).

`GET /v1/inbounds/catalog` publishes what can be combined with what — every protocol ×
transport, the `security` modes each allows, which subscription formats cannot carry it,
and the enum values the advanced fields accept. It is the same table the panel editor
uses, so a client that builds inbounds from it cannot construct one the validator will
reject.

### Groups

Access groups gate which connections a user may use. A user in **no** group reaches
everything; a user in one or more groups reaches the **union** of their groups' grants.
Enforcement is **server-side** — a disallowed lane's credential is withheld from Xray,
not merely hidden — so the user object's `links`, the subscription, and a hand-built
link all expose only what's granted.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/groups` | List groups, each with its `grants`, `member_ids` and member count. |
| `POST` | `/v1/groups` | Create a group. |
| `POST` | `/v1/groups/{id}` | Update a group (name + grants). |
| `DELETE` | `/v1/groups/{id}` | Delete a group (members left in no group revert to unrestricted). |
| `POST` | `/v1/groups/{id}/members` | Replace the group's members. |
| `POST` | `/v1/users/{id}/groups` | Replace one user's group membership. |

**Create / update** — body:

```json
{ "name": "VIP", "grants": ["builtin:0:vless", "builtin:0:reality", "inbound:7"] }
```

A **grant token** names one connection:

- `builtin:<server_id>:<lane>` — a built-in lane, where `<lane>` is `vless`, `reality`
  or `hysteria2` and `<server_id>` is `0` for the master or a node id from `GET /v1/nodes`.
- `inbound:<id>` — a custom inbound, by the id from `GET /v1/servers/{id}/inbounds`.

An empty `grants` array is a real state, not a no-op: members of a group that grants
nothing reach nothing — that's how you revoke. The response `data` is the group.

**Set members** — body `{ "user_ids": [1, 2, 3] }`; replaces the whole member set.

**Set a user's groups** — body `{ "group_ids": [4, 5] }`; replaces the user's whole
membership. An empty array removes the user from every group (→ unrestricted).

Grants that reference a deleted inbound or node are swept automatically, and harmless
until then.

**Where the tokens come from** — `GET /v1/groups/targets` lists every server with the
connections a group can grant, each with its ready-made token:

```json
{ "data": [
  { "server_id": 0, "server_name": "Мастер",
    "lanes": [ { "lane": "vless", "label": "VLESS-TCP-TLS",
                 "token": "builtin:0:vless", "enabled": true } ],
    "inbounds": [ { "id": 7, "name": "WS резерв",
                    "token": "inbound:7", "enabled": true } ] }
] }
```

Disabled lanes and inbounds are included, so a grant can be prepared before the
connection is switched on. Assembling those tokens by hand works too — but a typo
grants nothing, silently, which is exactly what this endpoint prevents.

### Server configuration

The endpoints above operate a panel; these configure one. Everything here mirrors a
screen in the panel and calls the same code behind it, so a value the UI refuses is
refused here too.

Two things are deliberately absent. **Administrators and API keys** have no endpoints:
a key able to mint keys turns one leak into permanent, self-renewing access, and that
decision belongs behind a session with a password prompt. **The panel's secret path**
is never returned by any `/v1` route — it is the obscurity layer in front of the panel,
not a setting to read out.

```
GET   $BASE/v1/settings                        → the operational knobs
PATCH $BASE/v1/settings                        → change some of them
GET   $BASE/v1/servers/{id}/routing            → routing, DNS and egress backends
POST  $BASE/v1/servers/{id}/routing            → change them
POST  $BASE/v1/servers/{id}/xray-restart       → restart that server's Xray
GET   $BASE/v1/config/snapshots                → config save-points
POST  $BASE/v1/config/snapshots                → take one
POST  $BASE/v1/config/snapshots/{id}/rollback  → restore the server config from one
DELETE $BASE/v1/config/snapshots/{id}          → forget one
```

`PATCH /v1/settings` applies **only the fields the body carries**. Everything absent is
left as it is, which is what makes it safe to read the object, change one value and send
that one value back — re-posting the whole thing would also re-apply whatever another
operator changed in between. The grouped fields (`hwid_*`, `local_backup_*`) are stored
by a single write internally, so they overlay onto the current row rather than resetting
their siblings to zero.

```bash
curl -X PATCH $BASE/v1/settings -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' -d '{"maintenance_mode":true}'
```

```json
{ "data": {
  "xray_dns": "https://dns.example/dns-query\n1.1.1.1",
  "decoy_template": "coming-soon",
  "decoy_templates": ["coming-soon", "YouTube", "…"],
  "maintenance_mode": true,
  "probe_detect": true, "probe_block": false,
  "watchdog_enabled": true, "watchdog_restarts": 0,
  "user_autodelete_days": 0,
  "hwid_enabled": false, "hwid_require": true,
  "hwid_fallback_limit": 0, "hwid_ttl_days": 30,
  "local_backup_cron": "", "local_backup_keep": 7,
  "sub_path": "sub", "warp_enabled": false, "warp_registered": true
} }
```

Three of these have an effect the database alone does not carry — the masquerade
template, scanner detection and maintenance mode are consulted on every request — so the
API swaps them live exactly as the panel does. An unknown `decoy_template` is refused
before anything is written; a `xray_dns` entry that is not an IP, `host:port`, URL or
`localhost` is refused the same way the panel refuses it, because a DNS server Xray
cannot parse makes every later config regeneration fail.

**Routing** is per server, and server `0` is the panel's own machine — the same numbering
`/v1/servers/{id}/inbounds` uses. A `GET` and a `POST` speak the same shape:

```json
{ "data": {
  "routing": { "block_ads": true, "block_bittorrent": false, "lanes": [], "…": null },
  "xray_dns": "1.1.1.1",
  "warp_enabled": false,
  "opera_enabled": false,
  "opera_country": "EU"
} }
```

An omitted field is **left unchanged**; `routing`, when present, replaces the rule set
wholesale. The split is deliberate: merging rule *lists* has no meaning a caller could
predict (does a shorter list delete rules or leave them?), while silently switching an
egress backend off because a body did not mention it is how tunnelled traffic starts
leaving from the server's own IP. So send `routing` to change rules, and name only the
backends you actually mean to move.

**Snapshots** capture the whole server config — protocols, ports, REALITY, routing,
egress, DNS, decoy and the custom inbounds — as one save-point. A rollback restores all
of it *except* the certificate and domain, so an undo can never break the live cert, and
takes an automatic save-point of the current state first, so the rollback is itself
undoable. It regenerates and restarts Xray; because nodes inherit the master's fields,
that restart is fleet-wide and every live connection drops.

```bash
curl -X POST $BASE/v1/config/snapshots -H "Authorization: Bearer $KEY" \
     -H 'Content-Type: application/json' -d '{"label":"before the egress change"}'
```

The answer carries the save-point that call made, by id — not "the newest one", which
would be somebody else's snapshot if a create landed in between.

`xray-restart` is direct for server `0` and queued for a node, which picks it up on its
next sync — a `200` there means *accepted*, not *done*.

### Stats

```
GET $BASE/v1/stats/series?user_id=5&from=2026-01-01&to=2026-01-31        → daily traffic
GET $BASE/v1/stats/nodes?user_id=5&from=2026-01-01&to=2026-01-31         → totals per server
GET $BASE/v1/stats/nodes/series?user_id=5&from=2026-01-01&to=2026-01-31  → daily, per server
GET $BASE/v1/stats/users?from=2026-01-01&to=2026-01-31                   → per-user totals
```

`user_id` narrows `series`, `nodes` and `nodes/series` to one account; omit it for a
panel-wide figure. `users` has no `user_id` — it already answers per user.
`from`/`to` are `YYYY-MM-DD` in the panel's configured timezone, and both default to
the **last 30 days** — an omitted window is a window, not a request for nothing.
A malformed or reversed range is a 400 rather than an empty answer.

Every day in the range is present, including the ones that carried nothing:

```json
{ "data": [
  { "day": "2026-01-01", "up": 52711616, "down": 3901246326 },
  { "day": "2026-01-02", "up": 0, "down": 0 },
  { "day": "2026-01-03", "up": 46059475, "down": 1488367869 }
] }
```

A quiet day writes no row internally, so the stored history is a list of busy days
rather than a series; these endpoints fill the gaps, because a hole and a zero are
different shapes to anything computing a moving average.

`nodes` breaks the same traffic down by the server that carried it, busiest first —
`series` tells you how much, this tells you where. `node_id` is `0` for the panel's
own server; names are resolved for you, including servers deleted since (their
traffic rows outlive them).

```json
{ "data": [
  { "node_id": 2, "name": "NL", "up": 46059475, "down": 1488367869 },
  { "node_id": 0, "name": "Мастер", "up": 52711616, "down": 3901246326 }
] }
```

`nodes/series` is both dimensions at once — one row per day per server, so one call
plots a line per server:

```json
{ "data": [
  { "day": "2026-01-01", "node_id": 0, "name": "Мастер", "up": 52711616, "down": 3901246326 },
  { "day": "2026-01-01", "node_id": 2, "name": "NL",     "up": 46059475, "down": 1488367869 },
  { "day": "2026-01-02", "node_id": 0, "name": "Мастер", "up": 0,        "down": 0 },
  { "day": "2026-01-02", "node_id": 2, "name": "NL",     "up": 12000000, "down": 340000000 }
] }
```

Servers that carried nothing across the whole window are left out entirely; the ones
that appear have a row for every day.

Where the connections came from, rather than how much they carried:

```
GET $BASE/v1/stats/countries  → recent connections grouped by country
GET $BASE/v1/stats/asns       → the same, grouped by network operator
```

Both count **distinct source IPs** and their activity over the connection retention
window, busiest first. `asn` is `0` and `org` empty for an address no routing table
covers (a private or unrouted one):

```json
{ "data": [
  { "asn": 15169, "org": "GOOGLE", "ips": 23, "hits": 2548 },
  { "asn": 13335, "org": "CLOUDFLARENET", "ips": 4, "hits": 61 }
] }
```

Each answer is computed from the whole connections table, so both are memoized for
about half a minute — two calls a second get the same figures, and the panel keeps its
one database connection for everything else.

### Which totals mean what

Two families of counter exist and they answer different questions. Mixing them up
looks exactly like a double-counting bug:

| Number | Source | Reset by |
|---|---|---|
| `total_up` / `total_down` on `/v1/summary` | the users' quota counters | a traffic reset, or a plan rolling the period over |
| `/v1/stats/series`, `/v1/stats/nodes` | the daily traffic history | nothing — it accumulates |

So the summary's totals are **usage in the current quota period**, which is what a
data limit is measured against, and they are routinely *smaller* than the history
over the same span. That is not traffic going missing: it is one user's counters
having been reset while their history stayed. Bill from the quota counters, report
from the history, and don't expect the two to agree.

```
GET $BASE/v1/stats/abuse?limit=50   → recent blocklist matches across the fleet
```

### Journals

Two read-only trails. **Events** is what happened to users; **admin audit** is what
admins and API keys did to the panel — including everything done through this API,
attributed to the key's name.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/events` | User events, filterable. |
| `GET` | `/v1/events/catalog` | The event keys a row can carry. |
| `GET` | `/v1/admin-audit` | The admin trail. |
| `GET` | `/v1/admin-audit/catalog` | Its categories and the actions in each. |

Both page **backwards**: pass `before=<id of the oldest row you hold>` and read
`next_before` from the response — `0` means you have reached the end. Ids are
monotonic, so the cursor stays stable while new rows land at the top.

```json
{ "data": { "events": [ { "id": 812, "user_id": 5, "action": "plan.cancelled", … } ],
            "next_before": 780 } }
```

`/v1/events` takes `action` (one key from the catalog), `actor` (`admin` / `user` /
`system` / `api`), `user_id` and the paging pair. `/v1/admin-audit` takes `category`
(expands to the actions it holds), `action`, `actor` and the paging pair. An unknown
`action` or `category` is a `400`, not an empty page — a filter that quietly matches
nothing is indistinguishable from a quiet period.

### Webhooks

The push half of an integration, configurable from the integration itself.

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/webhooks` | List the configured endpoints. |
| `GET` | `/v1/webhooks/events` | The event keys a webhook can subscribe to. |
| `POST` | `/v1/webhooks` | Add an endpoint. |
| `POST` | `/v1/webhooks/{id}` | Update one (whole object). |
| `DELETE` | `/v1/webhooks/{id}` | Delete one. |
| `POST` | `/v1/webhooks/{id}/test` | Send a test delivery. |

Body: `{ "url": "https://…", "events": ["user.created"], "enabled": true }`. An empty
`events` array means **all** events. `enabled` is optional on create (defaults to on).
The delivery format, signature and retry policy are described under **Webhooks** below.

A test delivery answers `200` even when the endpoint fails — the call succeeded, the
delivery is the result: `{ "data": { "status": 502, "ok": false, "error": "…" } }`.

### Registrations

The moderated signup queue (only meaningful while the user bot is in moderation mode —
`moderation` says whether it is).

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/v1/registrations` | Pending signups. |
| `POST` | `/v1/registrations/{id}/approve` | Create the account and link its Telegram chat. |
| `POST` | `/v1/registrations/{id}/reject` | Drop the request. |

### Monitoring

```
GET $BASE/v1/summary          → users / online / traffic totals / xray + cert status
GET $BASE/v1/system           → live CPU / RAM / disk / network / VPN throughput
GET $BASE/v1/health/report    → full self-diagnostics (xray, config, TLS, geo, egress lanes)
GET $BASE/v1/nodes/{id}/health → one server's diagnostics (id 0 = the master)
GET $BASE/v1/nodes/{id}/logs   → a node's recent log lines
GET $BASE/v1/backup/info       → what a backup taken now would contain
GET $BASE/v1/backup            → download that backup (.tar.gz body, not JSON)
GET $BASE/v1/metrics           → Prometheus exposition (text, not JSON)
```

`/v1/metrics` is a scrape target for Prometheus, authenticated with the same API key:

```yaml
scrape_configs:
  - job_name: rospanel
    scheme: https
    metrics_path: /<api-path>/v1/metrics
    authorization:
      credentials: rp_...
    static_configs:
      - targets: ["vpn.example.com"]
```

It publishes user counts, lifetime and daily traffic, live throughput, Xray and certificate
state, and the panel host's own CPU/RAM/disk.

Every server also gets its own series, labelled `node` and `node_id` (the master is
`node_id="0"`): `rospanel_node_online`, `rospanel_node_enabled`,
`rospanel_node_xray_running`, `rospanel_node_last_seen_seconds`,
`rospanel_node_traffic_today_bytes{direction=…}`, plus the machine under it —
`rospanel_node_cpu_percent`, `rospanel_node_memory_bytes{state=…}`,
`rospanel_node_disk_bytes{state=…}` and `rospanel_node_host_uptime_seconds`. A node that
has never reported (or whose agent predates these fields) simply has no samples, which is
what a gap in a graph should mean — rather than a row of zeros that reads as an idle
machine.

There are deliberately **no per-user series**: a panel with a thousand users would produce
a thousand time series per metric.

`/v1/nodes/{id}/logs` is answered from the node's next long-poll, so a freshly-woken
node may return the previous batch — `at` is the unix time the lines were collected.

`/v1/backup` streams the archive itself (`Content-Type: application/gzip`), so it is
the one endpoint outside the `{"data": …}` envelope — point a scheduler at it to keep
copies off the box. **Restore is deliberately not exposed**: it is staged on disk and
applied at the next start, so over an API it would be a request that silently replaces
everything on the next restart.

## MCP (AI assistants)

The panel serves this API to an assistant over the Model Context Protocol itself. There is
nothing to install anywhere: an assistant that takes a URL:

```
$BASE/v1/mcp/<api-key>          read-only
$BASE/v1/mcp/<api-key>/write    plus everything that changes state
```

The key rides in the path because those dialogs accept a URL and nothing else: **the address
is the credential**, exactly as secret as the key inside it, and it stops working the moment
that key is revoked. Build it by hand from the two values *Settings → API* gives you — the
base address shown there, and a key at the moment you create it (it is never shown again).

The two addresses are the same server with a different toolbox. The short one cannot delete a
user even though the key behind it could — which is the point: an assistant acting on a
misread sentence should not be able to, and the operator chooses that by which URL they paste.

Transport is MCP's Streamable HTTP: one JSON-RPC message per `POST`, answered with
`application/json`; notifications get `202` and no body; `GET` is `405` (this endpoint has no
server-initiated stream). Calls are dispatched against the panel's own REST API in process, so
they carry the same permissions, the same audit trail and the same error shapes as any other
client of `/v1`.

### Response size

A list call with no `limit` gets **50 rows**, not the REST default of 100: an assistant pays
for every row in context. Ask for more explicitly and you get it, `limit=0` (everything)
included.

Above that sits a hard ceiling of **512 KB per call**. A response over it is shortened by
whole rows, and `meta` says so — `limit` becomes the count actually returned while `total`
still reports how many exist, so paging on `limit`/`offset` reaches the rest. The result
stays valid JSON.

**The backup surface is not offered as a tool at all.** `GET /v1/backup` because its body is
a tarball an assistant cannot read, and `GET /v1/backup/info` because knowing what a dump
would contain is only useful to somebody about to take one — an operator's job, done from the
panel where the file actually lands somewhere. Restoring is not in `/v1` in any form: it lives
in the panel behind a session and a re-entered password, so no key, and no assistant holding
one, can put a backup back.

The tool list is generated from the OpenAPI document above, so it never drifts from the API:
an endpoint added to `/v1` becomes a tool with no one remembering to register it, and a
removed one disappears. That includes the configuration half — an assistant on the `/write`
address can read and change settings, rewrite a server's routing, take and roll back config
save-points and restart Xray.

Each tool carries the annotations a client uses to decide whether to ask its human first
(`readOnlyHint`, `destructiveHint`). Which writes count as destructive is **declared by the
route**, not guessed from its wording — a rewritten sentence must not be able to turn a
warning off, and the calls that reroute traffic, restart Xray, roll the config back or take
the panel into maintenance are all flagged. Answers come back as text and, when the panel replied
with JSON, as `structuredContent` too; one answer is capped so a single call cannot fill the
assistant's context — ask for a smaller page (`limit`/`offset`) rather than the lot.

## Examples

Create a user:

```bash
curl -sS -X POST "$BASE/v1/users" \
  -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"alice","data_limit":0,"expire_at":0}'
```

Fetch a user's subscription URL:

```bash
curl -sS "$BASE/v1/users/5" -H "Authorization: Bearer $KEY" \
  | jq -r '.data.sub_url'
```

Delete a user:

```bash
curl -sS -X DELETE "$BASE/v1/users/5" -H "Authorization: Bearer $KEY"
```

---

# Webhooks

Instead of polling the API, you can have the panel **push** lifecycle events to
your own HTTP endpoint. Configure them in the panel → **Settings → API →
Webhooks** (add a receiver URL and tick the events you want — tick none = all), or
over the API itself: `POST /v1/webhooks`.

Webhook targets, unlike the API's outbound fetches, may be `http` **or** `https`
and **may point at a private/localhost host** — the receiver is often the
operator's own internal service, and each delivery is a blind POST (the response
body is never read).

## Events

| Event | Fires when |
| --- | --- |
| `user.created` | a user is created (panel or API) |
| `user.deleted` | a user is deleted |
| `user.registered` | a user self-registers via the Telegram user bot |
| `user.expired` | a subscription lapses |
| `user.limited` | a user exhausts their traffic quota |
| `user.device_limited` | a user exceeds their device limit |
| `payment.created` | a payment order is opened |
| `payment.paid` | an order is paid and the plan applied |
| `payment.cancelled` | an order is cancelled |

## Delivery format

Each delivery is an HTTP `POST` with a JSON body:

```json
{
  "id": "3f1c…",                 // unique delivery id
  "event": "user.created",
  "created_at": 1767225600,
  "data": { "id": 7, "name": "alice", "status": "active", "enabled": true,
            "expire_at": 0, "data_limit": 0, "plan_id": 0 }
}
```

`data` is the user object for `user.*` events and the payment order for
`payment.*` events.

Headers:

```
Content-Type: application/json
User-Agent: RosPanel-Webhook/1
X-RosPanel-Event: user.created
X-RosPanel-Signature: sha256=<hex HMAC-SHA256 of the raw body>
```

## Verifying the signature

Every webhook has a secret (shown in the panel). Recompute the HMAC over the
**raw request body** and compare in constant time:

```python
import hmac, hashlib

def verify(secret: str, body: bytes, header: str) -> bool:
    expected = "sha256=" + hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, header)
```

```js
import crypto from "node:crypto";

function verify(secret, body, header) {
  const expected = "sha256=" + crypto.createHmac("sha256", secret).update(body).digest("hex");
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(header));
}
```

## Retries & delivery

Return a `2xx` status to acknowledge. A non-2xx response or a connection error is
retried with a growing backoff (roughly 10s, 30s, 2m, 10m — up to 5 attempts),
then dropped. Deliveries can arrive **out of order** and, on retry, **more than
once** — treat the `id` field as an idempotency key. The **Test** button in the
panel sends a `ping` delivery so you can confirm reachability and signature
verification. The last delivery's status is shown next to each webhook.
