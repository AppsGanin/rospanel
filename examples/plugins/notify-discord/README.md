# Discord notifications

Posts to a Discord channel: new users, payments (can be switched off), servers that
stopped answering and came back.

Permissions: `users.view` (user events), `billing.view` (payments). Host: `discord.com`.

Settings: the channel's webhook (Channel settings → Integrations → Webhooks), whether
to report payments (on by default), the message language.

Each event is posted once: its id is kept under `sent/<id>` with an `age/<time>/<id>`
key beside it, and ids older than a day are forgotten oldest first.

```sh
rospanel plugin test .
rospanel plugin pack .
```
