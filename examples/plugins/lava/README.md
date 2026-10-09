# Lava

Card and SBP payments through Lava Business (api.lava.ru).

Permission: `payments.manage` (a payment method decides which orders are paid). Host:
`api.lava.ru`.

Settings, all from the project in the Lava Business dashboard:

- **Project ID (shopId)** — the project's id.
- **Secret key** — signs the panel's requests to Lava.
- **Additional key** — Lava signs its callbacks with it; without it every callback is refused.
- **Invoice lifetime** — minutes, 60 by default.

Then switch Lava on in **Settings → Payments**. The callback address is sent with each
invoice, so nothing needs to go into the Lava dashboard; the card shows the same address.

```sh
rospanel plugin test .
rospanel plugin pack .
```
