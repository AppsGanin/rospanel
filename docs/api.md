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

## Endpoints

Base URL below is written as `$BASE` (e.g. `https://vpn.example.com/ab12cd34/v1`).

### Health

```
GET $BASE/health → { "data": { "status": "ok" } }
```

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
| `GET` | `/v1/users/{id}/connections` | List the user's recent source IPs / devices. |

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
(0 = never). The response `data` is the full user object, including `sub_url`,
`vless`, `trojan`, `hysteria2`, and `reality` share links.

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
| `GET` | `/v1/billing/plans?include_disabled=true` | List tariff plans. |
| `POST` | `/v1/billing/plans` | Create (no `id`) or update (`id` set) a plan. |
| `DELETE` | `/v1/billing/plans/{id}` | Delete a plan. |
| `GET` | `/v1/billing/orders?status=pending` | List payment orders (`status` optional). |
| `POST` | `/v1/billing/orders` | Open an order for a user+plan. |
| `POST` | `/v1/billing/orders/{id}/confirm` | Mark an order paid (activates the plan). |
| `POST` | `/v1/billing/orders/{id}/cancel` | Cancel an order. |

**Create order** — body `{ "user_id": 5, "plan_id": 2 }`. The response carries the
order and, when a payment provider is configured, a hosted `pay_url` to send the
user to:

```json
{ "data": { "order": { ... }, "pay_url": "https://..." } }
```

A manual order returns an empty `pay_url` and waits for `/confirm`.

### Stats

```
GET $BASE/v1/stats/series?user_id=5&from=2026-01-01&to=2026-01-31   → daily traffic points
GET $BASE/v1/stats/users?from=2026-01-01&to=2026-01-31              → per-user totals
```

`user_id` is optional on `series` (omit for a panel-wide series). `from`/`to` are
`YYYY-MM-DD` (in the panel's configured timezone).

### Monitoring

```
GET $BASE/v1/summary          → users / online / traffic totals / xray + cert status
GET $BASE/v1/system           → live CPU / RAM / disk / network / VPN throughput
GET $BASE/v1/health/report    → full self-diagnostics (xray, config, TLS, geo, egress lanes)
```

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
Вебхуки**: add a receiver URL and tick the events you want (tick none = all).

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
