<div align="center">

<img src="docs/img/logo.svg" alt="RosPanel" width="120" height="120">

# RosPanel

**Self-hosted VPN control panel built on Xray-core — from a single personal server to a network of nodes.**

![Release](https://img.shields.io/github/v/release/AppsGanin/rospanel?label=release&sort=semver&color=2ea44f)
![Downloads](https://img.shields.io/github/downloads/AppsGanin/rospanel/total?label=downloads&color=6f42c1)
![Go](https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white)
![Xray-core](https://img.shields.io/badge/Xray--core-v26.7.28-2b2b2b)
![React](https://img.shields.io/badge/UI-React%20%2B%20Vite%20%2B%20Tailwind-61DAFB?logo=react&logoColor=white)
![Platform](https://img.shields.io/badge/platform-Linux-555?logo=linux&logoColor=white)
![Deploy](https://img.shields.io/badge/deploy-single%20binary%20%7C%20Docker-2496ED?logo=docker&logoColor=white)

**English** · [Русский](README-RU.md)

[What it is](#what-it-is) · [Where to get a server](#️-where-to-get-a-server) · [Quick start](#-quick-start) · [Features](#-features) · [CLI](#️-cli) · [Architecture](#-architecture) · [Disclaimer](#️-disclaimer) · [Development](#-development) · [Support](#-support-the-project)

</div>

---

## What it is

**RosPanel** is a self-hosted VPN control panel built on
[Xray-core](https://github.com/XTLS/Xray-core). A single process serves several protocols at
once, and the panel gives you a web interface for users, subscriptions, plans and payments,
routing, statistics, backups and branding — no hand-editing of config files. When one server
is no longer enough, the same panel grows into a **network of servers**: add nodes and every
user is served by all of them.

**Self-contained:** one static binary — no nginx, no certbot, no third-party scripts. The
panel issues its own TLS via ACME, and **a domain is optional**: the certificate will be
issued for a bare IP too. The panel is reachable at its own address right after install — no
DNS, no reverse proxy, no SSH tunnels. The Xray config is generated from the panel's state
(you never touch JSON by hand), users are added and removed on the fly without dropping
anyone else's connections, and if the database gets corrupted the panel brings itself back up
from the last backup.

> [!NOTE]
> This is a **control plane**, not a VPN client. It configures and operates your own server.
> The project is intended for educational and research use (see [Disclaimer](#️-disclaimer)).

---

## 🖥️ Where to get a server

The cheapest VPS is enough: 1 vCPU, 1 GB RAM, any Linux — that covers both the panel and
Xray for dozens of users.

**If you haven't picked a host yet, sign up through the links below.** It's a free way to
support the project: the price is the same for you, and a share of the rent goes into the
panel's development.

[VDSina](https://www.vdsina.com/?partner=nmzki7z7tu) ·
[Aeza](https://aeza.net/?ref=375522) ·
[NetGrid](https://netgrid.host/ru?from=3491) ·
[Serv.host](https://serv.host/?from=36809) ·
[u1Host](https://u1host.com/?from=7702) ·
[Waicore](https://waicore.net/?from=35607)

Thanks to everyone who signs up through them 🙏

---

## 🚀 Quick start

### Option 1 — install script (recommended)

One command: downloads the release, installs a systemd service, starts it and prints the login.

```bash
curl -Ls https://raw.githubusercontent.com/AppsGanin/rospanel/main/install.sh | sudo bash
```

**A domain is optional.** The script will ask for one: if you have a domain, enter it; if you
don't, just press Enter and the panel comes up on the server's IP. It gets a Let's Encrypt
certificate either way — certificates are issued for IP addresses as well, so no browser
warnings.

You can set the domain up front and skip the question:
`curl -Ls … | sudo ROSPANEL_HOST=vpn.example.com bash`.

### Option 2 — binary + systemd by hand

```bash
# download the latest release (replace amd64 with arm64 for ARM servers)
curl -fsSL -o rospanel \
  https://github.com/AppsGanin/rospanel/releases/latest/download/rospanel-linux-amd64
chmod +x rospanel

# install as a service (copies the binary to /usr/local/bin, writes a systemd unit, starts it)
sudo ./rospanel install
#   with a domain right away:  sudo ROSPANEL_HOST=vpn.example.com ./rospanel install

# the login and the secret path are printed ONCE:
journalctl -u rospanel | grep -A6 FIRST-RUN
```

### Option 3 — Docker

```bash
docker run -d --name rospanel \
  --network host \
  --cap-add NET_ADMIN \
  --device /dev/net/tun \
  -v rospanel-data:/data \
  ghcr.io/appsganin/rospanel:latest

docker logs rospanel | grep -A6 FIRST-RUN
```

> [!NOTE]
> `--network host` is required so Xray can listen on 443/TCP, 80/TCP and the Hysteria2 UDP
> ports directly; `NET_ADMIN` lets the panel manage firewall rules: port hopping and
> connection limits and brute-force bans (nftables). `--device /dev/net/tun` is what the
> AmneziaWG lane needs to create its tunnel — a container gets no TUN device without it,
> and the capability alone is not enough. Drop the flag only if you never enable that lane.

### 🔑 Default login

| Field       | Value        |
| ----------- | ------------ |
| Username    | `admin`      |
| Password    | `admin`      |
| Panel path  | `/rospanel/` |

Right after install the panel is available at `https://<domain-or-IP>/rospanel/`. The exact
link is also in the log (`journalctl -u rospanel | grep -A6 FIRST-RUN` or
`docker logs rospanel | grep -A6 FIRST-RUN`).

On first login the setup wizard **forces a password change** and offers to **replace the panel
path with a random secret** — the default `admin/admin` and `/rospanel/` only work up to that
step.

> [!IMPORTANT]
> After the change the panel is reachable **only via the secret path** — the root serves a
> decoy page. Without knowing `/<secret>/` there is no login form to find.

### 🌐 Adding a node

A node is a **clean Ubuntu server** running the same binary in node mode: the panel generates
its config, there is nothing to set up separately. Everything starts in the UI:
**Servers → Add node**, where you enter a name and the node's domain or IP. From there you
have two options.

**Option 1 — install command.** The panel shows a ready one-liner; run it on the node's server
as root:

```bash
curl -Ls https://raw.githubusercontent.com/AppsGanin/rospanel/main/install.sh \
  | sudo bash -s -- --join 'https://<panel>/<node-api-path>/v1/join#<token>'
```

Copy the command **whole, from the dialog** — both the address and the token are filled in
automatically. The token is shown **once** and lives for 24 hours; `<node-api-path>` is a
separate unguessable segment for node sync (neither the panel path nor the REST API path:
changing either one won't detach your nodes). If the panel runs on a bare IP (certificate not
from a public CA), the panel adds `--insecure` to the command itself.

**Option 1b — Docker.** Same image as the panel, run in node mode. `node install` writes a
systemd unit a container has nowhere to put, so the join goes in the environment instead: it
is used only when the volume has no `node.json` yet, so the container can be recreated at
will and a spent token in the compose file changes nothing.

```yaml
services:
  node:
    image: ghcr.io/appsganin/rospanel:latest
    command: node run
    network_mode: host          # Xray binds 443/TCP, 80/TCP and the Hysteria2 UDP ports
    cap_add: [NET_ADMIN]        # nftables: per-IP limits, port hopping
    devices: [/dev/net/tun]     # AmneziaWG's tunnel; omit if that lane stays off
    environment:
      ROSPANEL_JOIN: 'https://<panel>/<node-api-path>/v1/join#<token>'
      # ROSPANEL_JOIN_INSECURE: "1"   # only if the panel is still on a self-signed cert
    volumes: [rospanel-node:/data]
    restart: unless-stopped
volumes:
  rospanel-node:
```

The panel's **Update node** button swaps the binary inside the container and exits, so the
restart brings back the image's version — update a Docker node with `docker pull` and a fresh
container instead. The volume keeps the identity, so it does not re-join.

**Option 2 — install over SSH.** The "Install over SSH" tab in the same dialog: the panel logs
into the server itself (address, port, user + password **or** a PEM private key), uploads
**its own** binary and installs the agent — with a live install log. The node version is
guaranteed to match the panel's. **SSH credentials are never stored.**

A few seconds after install the node shows up in the list as online: it reaches out to the
panel over outbound HTTPS, so the panel needs no inbound access to it and there is nothing to
forward.

```bash
systemctl status rospanel-node        # node service
journalctl -u rospanel-node -f        # agent log
rospanel node status                  # local status
```

> [!NOTE]
> One server is **either** a panel **or** a node: they share port 443. If the panel service is
> already on the machine, `node install` will disable it.

---

## ✨ Features

#### 🔐 Protocols, masquerading, TLS

One config, one set of credentials: **VLESS-Vision** (TCP/443 + uTLS), **VLESS-XHTTP-REALITY**
(masquerading as someone else's TLS), **Hysteria2** (UDP + port hopping). On top of that —
**custom inbounds** on every server: VLESS / Trojan / Hysteria2 over any transport (TCP,
WebSocket, XHTTP, gRPC, HTTPUpgrade) with their own port, REALITY keys, hop range and
**fine-grained transport tuning** (XHTTP `extra`, HTTP masquerading, sockopt, extra TLS
fields) — as individual fields or raw JSON; the config is validated on the target machine
itself (`xray -test` + port bind) before saving, and combinations a client can't handle are
silently kept out of Clash/sing-box subscriptions. A **"Reset to factory defaults"** button in
the connections editor puts protocols, ports, donor, fingerprints and anti-DPI back to what a
fresh install has and removes the server's custom inbounds (REALITY/AmneziaWG keys stay; the
master snapshots its config first). The panel hides behind a **secret path**;
any other path serves a decoy site (11 templates). **Probe detection** notices an IP that
scans for the hidden panel — one that requests many distinct paths the decoy doesn't have —
and records it for the operator to review; the reply never changes, so a scanner still sees
only the decoy and the masquerade holds. Optionally it also **drops the IP at the firewall**
(nftables) and/or sends a **daily digest** of new scanners — both off by default, recording alone
is the safe baseline. TLS out of the box — **ACME** (Let's
Encrypt / ZeroSSL) with auto-renewal; the certificate can also be issued **for a bare IP, with
no domain and no DNS**.

**AmneziaWG** is the fourth built-in lane: WireGuard whose handshake hides behind junk packets
and random-looking headers, for the AmneziaVPN and AmneziaWG apps. The protocol engine
(amneziawg-go) runs inside the panel process — no daemon, no extra binary — one tunnel per
server (master and every node), each with its own
keypair and obfuscation parameters the panel generates; a user is a peer on every server they
are allowed on, with one keypair and one tunnel address everywhere. Switch it on under
*Connections* like any other lane (the UDP port is picked high and random rather than 51820,
which needs no handshake to spot); users get the config as a file or a QR on the subscription
page and in their card, and the access groups, device limit, traffic accounting and online
status treat it exactly like the Xray lanes. Needs `/dev/net/tun` and `CAP_NET_ADMIN`, which
the installed service has; in Docker pass them yourself (`--device /dev/net/tun --cap-add
NET_ADMIN`), as a container is given no TUN device by default. `nftables` for the tunnel's NAT.

#### 👤 Users

Traffic and time limits with auto-disable and quota auto-reset (day/week/month/year), a
**device limit** (see *Device binding* below for exactly what it counts) and a per-user
**speed cap**. Traffic accounting via Xray Stats, online status, connection
list; expired users can be auto-deleted. Every user carries an operator's **note** (where they
came from, what was agreed — panel and API only, never shown to the client) and **tags** (`vip`,
`reseller-a`) that the list filters on and the search covers, along with the Xray client id
(`u12`) shown next to the name so a log line maps to an account at a glance. **Import and
export**: the users page reads a **Marzban** database or `GET /api/users` dump, a **3x-ui**
`x-ui.db`, and this panel's **own export** — and writes that export with one button. Users arrive
with the **same UUIDs and passwords**, limits, expiry, used traffic, notes and tags, so nobody
re-adds a server in their app; the subscription token comes across too where the source has one
(3x-ui's `subId`, Marzban's dump, and always from this panel's own export), so moving a domain to
a new install keeps every existing subscription link working. A user already here (same UUID) is
skipped, so running the file twice doubles nobody, a token another user holds is replaced rather
than refused, and an uploaded file is deleted the moment it has been read. Search and filters
stay fast with hundreds of users,
and **bulk operations** (enable/disable/reset/extend/delete) go through a single Xray reload.
The dashboard shows CPU / RAM / swap / disk and VPN traffic in real time. A **connection
map** breaks down where clients connect from — distinct source IPs per **country** (from the
same geoip database Xray routes with) and per **network operator / ASN** (from a free
iptoasn table the panel fetches itself); no external service.

**The device limit — what it counts.** A user's device limit (or the one their plan sets) is
a **single number enforced two ways**. On its own it caps **concurrent unique source IPs**: a
user connecting from more distinct addresses than the limit within the online window is
dropped from the tunnels until they fall back under it — counted across **every server**
(master and nodes), not per-server. `0` means no IP cap.

**The cut is not immediate.** The limit has to stay exceeded for two and a half minutes, a
little longer than the online window. That is because the two commonest ways to exceed it
are not sharing at all: a phone changing network abandons its old address while that address
keeps a fresh sighting until the window drops it, and a mobile carrier rotates the public
address inside its own pool with no user action whatsoever (one live account shows seven
addresses in a single `176.15.0.0/16` over a month). The only way to tell either from a
second device is to watch whether the old address keeps being used — which means not cutting
the user, because cutting is what stops the traffic being judged. So the limit waits, and an
abandoned address leaves the window on its own. Addresses in genuine simultaneous use are
all still there when the wait is over, and the cut lands as before.

*Settings → Subscriptions → Count devices by* → **HWID only** drops the address count
altogether — at the price of the only thing that caps how many places one link is used at
*once*, since HWID caps who may fetch the subscription, not who may connect. The default
counts both.

**Device binding (HWID).** Turn this on and the **same number** also caps distinct **installs**.
Clients that follow the subscription-header convention (Happ, v2RayTun) send a stable install
id; the panel binds it to the account on first fetch and counts it against the limit. Once the
limit is full a NEW install is refused the subscription while the bound ones keep updating — the
check and the insert are one transaction, so two clients cannot both take the last slot. (When a
user's own limit is `0`, HWID uses a panel-wide fallback limit instead.) The devices are listed
in the user card, in the **client bot** (as separate *by IP* and *by HWID* lines when both apply),
and **on the subscription page**, where the owner can release one themselves instead of writing to
support; an idle device frees its slot after a configurable TTL, and rotating the subscription
link releases them all. Off by default (*Settings → Subscriptions*); once on, a client that sends
**no** id gets no subscription at all — a cap you can dodge by switching to a quieter app is not a
cap — with a switch to serve those clients anyway (counted by address, as before) if some of your
users are on them.

**Speed limit.** A per-user cap in kbit/s, set by hand or by the tariff. Xray has no
per-user bandwidth limit, so it is enforced below it — the kernel's own scheduler (HTB),
keyed on the addresses that user is currently connected from, in both directions. That
means it covers every protocol at once, that everyone behind one NAT address shares a cap,
and that for Hysteria2 (whose congestion control ignores loss by design) hitting the cap
looks like packet loss rather than a smooth slowdown. Nodes shape their own traffic from
their own view of who is connected.

**Access groups** decide which connections a user gets: built-in lanes and custom inbounds are
ticked per server, a user with no group gets everything, a user in several groups gets the
union of their connections. The restriction is **server-side** — the account simply isn't
added to the forbidden Xray inbounds (rather than being hidden in the UI), so both the
subscription and a hand-crafted link only ever hand out what's allowed. Membership is editable
from both sides — in the user's card and in the group itself — and a user's groups are visible
in their card and in the list.

#### 📲 Subscriptions

`/<path>/<token>` — a base64 list plus a page with a QR code, deep links and import into
popular clients (auto-routing headers for Happ / INCY / Mihomo), with your own node names. The
link can be **reset** (token rotation) without changing UUIDs and passwords. An
**announcement** inside the client (Happ, v2RayTun) puts a short text right in the app.

The page carries what the account holder needs and nothing they shouldn't hand out: the
**individual per-lane configs** card can be switched off, and with device binding on it lists **their own bound devices** with a
button to release one — so a full device roster is self-service rather than a support ticket.

**Response rules** override the automatic format detection: an ordered list of operator rules
matched against the request (User-Agent or an HWID header) — force a specific format for a given
client / OS / version (contains, equals, prefix, regex), or **block** a client entirely (it is
served the decoy). The first matching rule wins; no match falls through to normal detection.

**A server that stops reporting** stays in the subscription by default — a node bounces on every
update and certificate renewal, and a client whose refresh lands in that window would lose an
entry for a server that is already back, while one that keeps the entry fails over on its own.
*Settings → Subscriptions → hide a server while it is offline* switches that round for operators
who would rather hide a dead server.

**Server order** (*Settings → Subscriptions*) decides which server a client sees first — and
whether a full one is shown at all. Every server carries a manual **weight** and a **capacity**
in users, set on its card under *Servers*. Two modes: manual (weight, then the list) and **least
loaded** first (online users against capacity). A server marked *hide when full*
drops out of the subscription while it is at capacity — never the last one, since an empty
subscription strands every client. Load is counted per server from the same sightings that feed
the device limit, and the servers page shows the live number next to each capacity.

**DPI evasion on the client** (*Settings → Subscriptions*) is what the subscription tells the
app to do with the TLS handshake before a DPI box sees it — the server is untouched. For
Xray-core apps (Happ, v2rayNG, v2rayN, Streisand) there is an **Xray JSON** subscription format:
one full config per lane, derived from the very share link the panel already builds, with a
`freedom` outbound the proxy dials through that carries **fragment** (split the ClientHello:
`tlshello` / first packets, piece length, interval) and **noise** (random, string or base64
packets ahead of the data). It is served to those apps automatically once switched on, and is
always reachable through `?format=xray-json` or a response rule. For sing-box, **record fragment**
splits the ClientHello into several TLS records on top of the packet-level fragmentation set
under Connections.

**Maintenance mode** — one switch puts the public surfaces (subscription page, status page,
decoy) on a "temporarily unavailable" page while the panel, API, node sync and the tunnels
themselves keep running, so existing connections are untouched and the operator can still sign in.

#### 🧭 Routing and egress

**block / direct / WARP / Opera** categories with priority, **geosite/geoip** presets with
automatic database downloads, egress through **Cloudflare WARP** (WireGuard) and the free
**Opera VPN** with region selection. **Proxy lanes** are independent egresses, each with its
own upstreams and zone rules, balanced across whatever is alive (Observatory). An upstream is a
socks5/http proxy **or somebody else's VPN server**: a vless:// / trojan:// / ss:// / vmess:// /
hysteria2:// link, or a whole subscription (https://…, `happ://crypt…`, base64) — the panel
decrypts it and keeps re-reading it.
**Config snapshots** (a *Snapshots* tab in the server settings) give an undo history for the
**whole server config** — protocols, ports, REALITY, routing, egress, DNS, decoy and inbounds:
save a restore point by hand, and roll back to it if an edit breaks something. A rollback
re-validates through `xray -test` and auto-snapshots the current state first (so it's itself
undoable), and it deliberately leaves the **certificate and domain** untouched, so an undo
never risks live access.

**External subscriptions** (a card on the *Servers* page) are the other side of the same
coin: another provider's servers, read from their subscription, **handed to your users** beside
your own. The source is a link, a `happ://crypt…` link or a pasted list; a link is re-read every
hour, every server switches on and off on its own, and who gets them is decided by the same
**access groups** as your own lanes. The panel holds nothing on those servers — it only decides
who is told about them.

#### 🌐 Server network (multi-node)

A single panel manages the **master** and any number of remote **nodes**. A server is added
from the UI: copy **one command** for a clean Ubuntu box, or let the panel **log in over SSH**
and install the agent — with a live install log. **The node reaches out to the panel** itself
(outbound HTTPS long-poll), so the panel needs no access to the nodes, and moving the panel
doesn't detach them. Users, limits and plans roll out to every node; traffic and devices count
against **shared** limits, while statistics and the user card show **how much traffic went
through each server**. Each node also has a **traffic multiplier** — a coefficient that scales
how fast traffic through it spends a user's quota (2× on an expensive location, 0.5× on a promo
one); it bends only the quota, never the per-node byte statistics.

Every server is configured separately (protocols, egress, DNS, REALITY keys, domain and TLS,
geo databases, decoy). A node is **the same binary** in node mode: the panel generates its
config, a local `xray -test` with rollback guards against version mismatches, and updates run
from the UI with SHA256 verification.

#### 💳 Plans and payments (optional)

**Plans**: price, duration, traffic and device limits; price 0 makes a free plan. A plan can
carry its own **traffic refill cycle** (daily / weekly / monthly / yearly) for tariffs like
"100 GB a month, paid for a year"; left blank, a paid plan's quota covers the whole term and
a free plan refills every term. There's a trial period, a free fallback plan for expired
users, renewals and user migration between plans. **Payment acceptance** — pick a provider:
**YooKassa**, **PayPalych**, **RioPay**, **RollyPay**, **SeverPay**, **Platega**, **PayPear**,
**AuraPay** (cards, SBP, ₽), **CryptoBot** and **Heleket** (crypto). The client pays in the
bot or on the subscription page, and the plan **activates itself**. A webhook confirms it
(signature verified), polling covers the case where the webhook never arrives; processing is
idempotent and the amount is checked against the order. With no provider configured, an admin
confirms payments manually.

> [!WARNING]
> **Payment providers have not yet been verified against live accounts.** If you've connected
> one of them, please [open an issue](https://github.com/AppsGanin/rospanel/issues) and say
> whether it works (which provider, what worked, what broke). That's what lets verified
> providers be marked as such and the rest get fixed.

#### 👥 Access, roles and audit

Roles: **owner** (can do everything, exactly one, cannot be deleted), **administrator**
(everything except the admin list), **operator** (users, statistics, activity log). Permissions
are checked server-side on every request; a new admin gets a temporary password that must be
changed on first login. **Two-factor authentication** (TOTP): each admin turns it on for
themselves — a code from an authenticator app (Google Authenticator, Aegis, 1Password) on top
of the password, the secret encrypted in the database and never handed back out after setup;
for a lost phone, `rospanel totp reset <login>` on the server; a fuller **`rospanel rescue`**
resets a forgotten password, clears a second factor, or recreates an owner when no admin can
sign in at all. Each admin also sees their own **active sessions** in the account dialog —
browser and OS, the address it was last used from, when it signed in — and can end any one of
them or **sign out everywhere else** in one click; a session ended this way stops working on
its next request, and the panel log records who ended what. A sign-in from an address this
admin has not used before is **reported to Telegram** (the "Sign-in from a new address"
category in the bot settings): who, from where (IP, country, network), on which browser. Under
the message is a **"Not me"** button — it ends every session of that admin in one tap, leaving
only the password to change. The **user log** records what was
done to them and by whom (admin, API key, bot, the user themselves, the system) and survives
their deletion. The **panel log** (visible to the owner) covers logins and **failed attempts
with IPs**, second factors switched on and off, settings changes and backups; only successful
actions are written, request bodies never are. The panel log is **searchable** (free text over
action, target, administrator and IP) and filterable by category and date range, and the
current view **exports to CSV** in one click. Both logs are kept for 90 days.

#### 🤖 Integrations

**Admin bot** in Telegram: user management, plans, subscription QR codes, scheduled backups,
event notifications (signups, expirations, Xray failures, payments, certificates).

**Client bot**: self-signup, a personal menu with the subscription and statistics, plan
purchase — plus **personal notifications** to the user: subscription ending, traffic running
out, payment received.

**Support bot** — a third bot: a person writes to it in a DM and the conversation lands in your
forum group, **one topic per person**; you reply straight from there and the answer goes back
to them in the bot.

**Broadcasts** — nine audience slices (everyone, with and without an account, active, expired,
expiring soon, long-inactive, never connected) with an image and a button. Delivery is driven
by a recipient table, so an interrupted broadcast **resumes instead of restarting**, and nobody
gets the message twice.

**REST API** with named keys (`Authorization: Bearer`), **OpenAPI generated from the code** and
Swagger UI. It covers both halves of the panel: the users, plans, orders and servers you
operate, and the configuration behind them — settings, per-server routing and DNS, config
save-points with rollback. Administrators, API keys and the panel's secret path are
deliberately not exposed. **Webhooks** send HMAC-SHA256 signed events with retries. **Prometheus metrics**
at `/<api-path>/v1/metrics` behind the same key — users, traffic, throughput, host stats and
one series per node. An **MCP server** hands the same API to an AI assistant, with the tool
list generated from that OpenAPI document: paste `…/v1/mcp/<key>` into an assistant that takes
a URL and there is nothing to install anywhere. Write operations are off unless you ask for
them (the `/write` address). More in [docs/api.md](docs/api.md).

**Connecting an assistant** takes one URL and no local install. Create a key in
*Settings → API*, take the base address from the same page, and paste one of:

```text
https://vpn.example.com/<api-path>/v1/mcp/<key>          read-only
https://vpn.example.com/<api-path>/v1/mcp/<key>/write    plus everything that changes state
```

The address is the credential — as secret as the key inside it, and dead the moment that key
is revoked. The two differ only in the toolbox they offer: the short one cannot delete a user
even though the key behind it could, which is what makes handing an assistant the read-only
URL a real decision rather than a hope.

**Status page** — an optional public page (*Settings → General*) showing which
servers are up and 90 days of uptime history. Names and availability only: no addresses, no
users, no traffic, and no page at all until you switch it on.

#### 🌍 Language (RU / EN)

| Surface                   | Language comes from                                            |
| ------------------------- | -------------------------------------------------------------- |
| Panel                     | the admin's own pick (per browser), otherwise the browser's languages |
| Subscription page         | `Accept-Language`                                              |
| Client and support bots   | each person's Telegram language                                |
| Admin bot                 | a panel-wide setting (*Settings → Telegram*) |
| CLI                       | English                                                        |

#### 🎨 Branding and theme

Your own name and logo instead of "RosPanel" — in the panel and on the subscription page. An
accent color repaints the whole interface, and **dark mode** adapts text, statuses and charts
on its own.

#### 🛡️ Abuse detection

The panel checks **destination IP addresses** from the Xray access log against a list of
malicious networks and records **matches only** — ordinary traffic is never stored. It exists
for exactly one purpose: when an abuse complaint arrives about the server's address, you can
tell whose traffic it was.

The list is **FireHOL level 1**: botnet command-and-control servers, attackers and spam
networks. A curated level with minimal false positives, without CDNs or shared hosting.
Alongside it there's **your own list** (IP/CIDR), which is checked first. Matches show up in
the statistics and in the user's card, attributed to the **server** the traffic left from; when
the daily threshold is exceeded a Telegram notification goes out. Categories, the custom list,
the threshold and updates live in *Settings → Blocklists*.

The alert is not the only response. The same tab configures **automatic measures** — a ladder
of three steps, each with its own matches-per-day threshold (0 switches the step off): **warn**
the user through their Telegram bot (once a day), **cap their speed** to a given value, **switch
access off**. The cap and the switch-off hold for a set time (an hour to 30 days), after which
the panel restores the previous speed or switches access back on by itself and tells the user;
the user and the operator hear about every step, and every step lands in the journal. Enabling
the user or changing their speed by hand overrules the measure — the operator's decision is not
"lifted" by the panel later. Node traffic counts too: matches are tallied on the master, and
speed caps and the user set reach the nodes through the usual sync.

Checks run against addresses, not domains, and that isn't a simplification. Modern clients
resolve DNS outside the tunnel and encrypt SNI (ECH), so all that reaches the server is a bare
IP.

Matches are kept for **14 days** — enough to handle a complaint.

**Where clients may connect from** (*Settings → General*). A country rule — only these
countries, or everywhere except these — plus a list of **networks (ASN)** that may never
connect, which is how a resold account is usually spotted: it appears from a hosting provider
rather than a home line. Both are checked against what the panel already records for every
connection, on the master and on every node, so the rule covers every protocol including the
ones Xray does not carry. **The address is dropped, not the account**: the offender's IP goes
into an nftables set on every server (with a length the operator sets, self-expiring), while the
account keeps working from wherever the policy does allow. Enforcement is off until switched on —
until then a violation is only recorded, in the user's journal and in a list the operator can
read before letting the rule cut anything, and any block can be lifted by hand. An address the
geo table cannot place is never refused: that table is incomplete, and cutting real users off a
working service is the one failure this must not have.

#### 🧰 Operations and security

**Diagnostics** in one click: the Xray process, config application, TLS expiry, disk space, geo
database freshness, egress health — every check with a hint. A separate **connection self-test**
connects to each protocol as a real client and confirms traffic actually goes out — catching
credential, TLS or ALPN drift before a user does. **Backup / restore** and reset are available
from the panel and the CLI.

**Updates** in one command: the panel verifies SHA256, runs the binary dry, takes a backup and
only then replaces itself, keeping the previous version next to it. **"What's new"** in the
profile menu shows the release history built into the binary itself, with the running version
marked. The Xray core is pinned to
an exact release, and a panel update carries it: on the next start the panel and every node
compare the Xray they have with the pinned one and replace it if it differs — checksum first,
and a box that can't reach GitHub keeps the release it already runs. The supervisor restarts
Xray if it crashes. A **watchdog** covers the harder case a crash
handler can't see — a process that stays alive but stops serving: it probes Xray's API and, if
it goes unresponsive for several checks in a row, restarts it (with a cooldown against restart
storms) and alerts the operator. Runs on the master and every node.

The subscription page's own actions (cancel a plan, pay, release a device) are accepted only
from that page: the token in the link proves the account, not who is asking, so a leaked link
cannot be acted on by somebody else's site.

**Secrets in the database are encrypted** (AES-GCM). Session tokens and API keys are stored as
hashes only — even with table access you can't reuse someone's session. On a key change the
panel re-wraps everything it keeps closed: user passwords and keys, node keys (REALITY, WARP,
AmneziaWG), webhook secrets, custom-inbound keys, system-proxy passwords, admin second factors
and payment-provider configs. Payment confirmation
and admin management require **re-entering the password**. Outbound requests are protected
against SSRF, brute force on inbounds is banned via nftables (a timed set entry the kernel
expires on its own), and the number of connections per IP is limited via nftables.

---

## 🛠️ CLI

```text
rospanel                     run the panel (usually via systemd)
rospanel install             install the systemd service and start it (root)
rospanel uninstall [-y]      remove the service (data is kept)
rospanel start|stop|restart  service control
rospanel status              service status
rospanel update [-y]         update to the latest GitHub release
rospanel backup [file]       .tar.gz snapshot (DB + encryption key + certificates + Xray config)
rospanel restore [-y] <file> restore from a snapshot (applied on start)
rospanel host [-y] [domain|IP] show/change the address (reissues TLS)
rospanel path                show the panel URL and check secrets.key / the DB
rospanel totp reset <login>  remove an admin's two-factor auth (lost phone); bare totp lists
rospanel rescue <sub>        regain locked-out access: list | password | unlock | owner
rospanel reset [-y]          factory reset (wipes the DB)
rospanel version             version
rospanel help                full help
```

Destructive commands (`reset`, `restore`, `host`, `uninstall`) ask for confirmation; the `-y`
flag skips it.

### Node mode

The same binary can run as a panel-managed node. The `install` command is generated by the
"Add node" dialog in the panel — you normally never type it by hand.

```text
rospanel node install --join '<url>'   join the panel and install the service (root)
rospanel node run                       node agent (systemd entry point)
rospanel node set-panel <url>           point the node at a new panel address
rospanel node status                    local node status
rospanel node uninstall [-y]            remove the node service (data is kept)
```

`--join` comes from the add-node dialog; the token in it is single-use and lives for 24 hours.
For the full walkthrough see [🌐 Adding a node](#-adding-a-node).

---

## 🧱 Architecture

The single source of truth is **SQLite**; the Xray config is always generated from it and
applied by the supervisor. The web panel is embedded in the binary.

**Stack:** Go 1.26 · Xray-core · SQLite (modernc, CGO-free) · React + Vite + Tailwind.

---

## ⚠️ Disclaimer

The software is provided "as is", without warranties of any kind.

**The project is intended for educational and research use:** studying network protocols, TLS
and proxy technologies, **CTF** and network security lab work, **authorized** penetration
testing, and managing **your own** infrastructure. The project is **not intended** for
circumventing lawful restrictions or for any other unlawful activity; the masquerading
mechanisms exist to study the technology and to test service resilience within sanctioned
testing.

Responsibility for installing, configuring and operating the software, and for complying with
the laws of your jurisdiction, lies with the **server operator**. The authors and contributors
are not responsible for how third parties use the project.

---

## 🧑‍💻 Development

```bash
# frontend (after changes in web/)
npm --prefix web install
npm --prefix web run build      # → web/dist (embedded into the binary)

# binary
go build -o rospanel ./cmd/rospanel
./rospanel
```

**Localisation.** The panel's dictionaries are `web/src/i18n/ru.ts` and `en.ts`, typed against
each other: a key present in one and missing in the other is a build error, not a silent fallback.
What Go renders itself — the bots and the subscription page — lives in `internal/i18n/`; the
backend hands the panel a key plus arguments and never rendered prose. Adding a third language
means one more dictionary on each side.

Useful environment variables (all optional): `ROSPANEL_DATA` (data directory),
`ROSPANEL_ADMIN_ADDR` (the panel's loopback address, `127.0.0.1:8080` by default), `XRAY_BIN`,
`ROSPANEL_HOST`, `ROSPANEL_ACME_EMAIL`.

Flood protection (nftables limits on public TCP ports, see "Operations") is configured through
the environment too — handy when a whole office or a CGNAT carrier sits behind one IP and
clients hit the defaults:

| Variable | What it does |
| --- | --- |
| `ROSPANEL_CONNLIMIT=off` | Disables the limits entirely (nftables rules are removed) |
| `ROSPANEL_CONNLIMIT_MAX` | Maximum concurrent TCP connections from a single IP |
| `ROSPANEL_CONNLIMIT_RATE` | Maximum new connections per second from a single IP |

The current state is visible in **Dashboard → Management → Diagnostics**: if nftables isn't
installed or the panel isn't running as root, the rules silently won't apply — diagnostics will
say so.

PRs and issues are welcome. Commits follow
[Conventional Commits](https://www.conventionalcommits.org/): release-please uses them to cut
releases and publish the binary and the Docker image to GHCR.

---

## 💝 Support the project

RosPanel is developed in spare time and distributed for free. If the panel turned out to be
useful, the easiest way to support it is
**[DonationAlerts](https://www.donationalerts.com/r/dmitryapp)** or
**[Boosty](https://boosty.to/githubapps)** (RU cards, one-off or recurring). Thank you! 🙏


<b>Crypto</b>


**USDT.** Pick a network and copy the address **carefully** — the sender's and the receiver's
network must match. Send **USDT** only, and only on the network listed: a transfer on the wrong
network cannot be recovered. The cheapest fees are on **TRON (TRC20)** and **TON**.

| Network          | Token | Address                                            |
| ---------------- | ----- | -------------------------------------------------- |
| TRC20 (Tron)     | USDT  | `TJwyrPVEZVZ1YrcmDiZTyFjLo3Q2DmEGzs`               |
| ERC20 (Ethereum) | USDT  | `0xf9d663146ce902da91911b214c71cc73a5269d1d`       |
| Solana           | USDT  | `2qAZRTbaUMTfYuZbD1dCYHjkYgxkw4dUYE9XY3JhC2Cs`     |
| TON              | USDT  | `UQDoat731MLYuIw8ayL3Vhhw7zTBbLvRaQFmDvab--CNNI7e` |

**Bybit.** If you're on Bybit too — transfer to UID `136462734`: instant and free.

**Can't support financially?** You can still help — rent servers through the referral links in
[Where to get a server](#️-where-to-get-a-server). It costs you nothing and helps the project.

---

## 📄 License

[GNU Affero General Public License v3.0](LICENSE) (AGPL-3.0).

The project is free to use, modify and self-host. If you provide network access to a modified
version of the panel (including as a service), you must release the source of your changes
under the same terms.
