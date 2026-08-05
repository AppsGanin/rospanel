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
| `PATCH` | `/v1/users/{id}` | Update name / limits / expiry / device limit / enabled. |
| `DELETE` | `/v1/users/{id}` | Delete a user. |
| `POST` | `/v1/users/{id}/reset` | Reset the user's traffic counters. |
| `POST` | `/v1/users/{id}/reset-period` | Set auto-reset period. |
| `POST` | `/v1/users/{id}/rotate-sub` | Issue a new subscription URL (old link dies). |
| `POST` | `/v1/users/{id}/plan` | Apply a tariff plan to the user. |
| `POST` | `/v1/users/{id}/plan/cancel` | Cancel a paid subscription now. |
| `GET` | `/v1/users/{id}/connections` | List the user's recent source IPs / devices. |
| `GET` | `/v1/users/{id}/events` | The user's own journal (paged). |
| `GET` | `/v1/users/{id}/abuse` | The user's blocklist matches (`limit`, default 20). |

**Create** — `name` is required; everything else is optional and applied to the fresh
account in the same call:

```json
{ "name": "alice", "data_limit": 0, "expire_at": 0,
  "device_limit": 3, "plan_id": 2, "group_ids": [4] }
```

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
(`vless` / `trojan` / `hysteria2`), `transport` (`tcp` / `ws` / `xhttp` / `grpc` /
`httpupgrade`), `port`, `security` (`none` / `tls` / `reality`) with the matching keys
(REALITY dest & keys, fingerprint, path/host, Hysteria2 hop range), plus optional
advanced blocks (XHTTP `extra`, TCP HTTP masquerade, `sockopt`, extra TLS keys). The
full field list — and which combinations are valid — is in `openapi.json` / Swagger.
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

### Stats

```
GET $BASE/v1/stats/series?user_id=5&from=2026-01-01&to=2026-01-31   → daily traffic points
GET $BASE/v1/stats/nodes?user_id=5&from=2026-01-01&to=2026-01-31    → traffic split by server
GET $BASE/v1/stats/users?from=2026-01-01&to=2026-01-31              → per-user totals
```

`user_id` is optional on `series` and `nodes` (omit for a panel-wide figure).
`from`/`to` are `YYYY-MM-DD` (in the panel's configured timezone).

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
```

`/v1/nodes/{id}/logs` is answered from the node's next long-poll, so a freshly-woken
node may return the previous batch — `at` is the unix time the lines were collected.

`/v1/backup` streams the archive itself (`Content-Type: application/gzip`), so it is
the one endpoint outside the `{"data": …}` envelope — point a scheduler at it to keep
copies off the box. **Restore is deliberately not exposed**: it is staged on disk and
applied at the next start, so over an API it would be a request that silently replaces
everything on the next restart.

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
