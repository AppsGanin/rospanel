# Capacity bench

Two measurements live here: the SQLite write ceiling (a small Go program in this
directory) and the end-to-end panel load test (a netns stand driven by `zzload`).
They answer different questions and must not be mixed: the first is what the disk
does, the second is what a user waits for.

Results of the 2026-11-03 run: [docs/benchmarks.md](../../docs/benchmarks.md).

## Write ceiling — `sqlitebench`

```sh
go run ./tools/bench/sqlitebench -n 1000 -dir /tmp/bench
```

It reproduces the panel's store pragmas (WAL, `synchronous=FULL` by default, one
connection) and reports:

- the filesystem's `fsync` rate;
- SQLite autocommit inserts — one commit and one fsync per row;
- the same rows in one transaction;
- autocommit with `synchronous=NORMAL` (what batching deliberately did not take).

Run it on the machine whose disk you are judging. Numbers from a laptop's APFS mean
nothing for a VPS. The tool writes only to `-dir` and removes its scratch database.

## End-to-end load — the netns stand

The generator (`zzload`) is an internal test tool and is not part of this repository;
the commands below document the stand it drives so a future round starts from the
same shape instead of rediscovering it.

### Why a netns

The panel must not touch the host that runs the real one: in a network namespace the
nftables tables, `:80`/`:443`, the AmneziaWG tun and `tc` are all netns-local. The
stand lives in `/root/lt` on the test master and is removed between rounds.

1. **Max memory a 1 GB box gives the panel.** A transient unit, not `systemd-run
   --scope` (a scope dies with the ssh session):

   ```sh
   systemd-run --unit=lt-panel --service-type=exec -q \
     -p MemoryMax=620M -p MemorySwapMax=0 -p Restart=no \
     -p Environment=GOMEMLIMIT=300MiB \
     -p Environment=ROSPANEL_DATA=/root/lt/data \
     -p Environment=ROSPANEL_ADMIN_ADDR=127.0.0.1:18080 \
     -p Environment=ROSPANEL_HOST=10.250.0.2 \
     ip netns exec lt /root/lt/rospanel
   ```

   `GOMEMLIMIT` must leave room for Xray: it shares the unit's cgroup. 300 MiB holds
   at 20k; 380 MiB was killed by the cap at 50k.

2. **Relay inside the netns.** The panel accepts a PROXY header only from a loopback
   peer; a relay lets the generator claim per-node and per-client addresses:

   ```sh
   ip netns exec lt /root/lt/zzload fwd -listen 10.250.0.2:18080 -to 127.0.0.1:18080
   ```

3. **Generator on another machine.** Keep it off the target: on a 1 vCPU box a local
   generator competes with the panel. In the 2026-11 run it lived on the test node
   and reached the panel through an ssh tunnel to `10.250.0.2:18080`; the WAN makes
   the 9–16 MB config pushes look slow, so report those separately from p50/p95.

4. **Seed and run.**

   ```sh
   ./zzload seed -data /root/lt/data -users 20000 -nodes 5 -out /root/lt/lt.json
   ./zzload run -cfg lt.json -addr 127.0.0.1:18081 -aaddr 127.0.0.1:18081 \
     -nodes 5 -users 20000 -subrps 11 -pagerps 1 -cardrps 1 \
     -updrps 0.3 -createrps 0.05 -dash 2 -warm 90s -dur 4m
   ```

   The `=== FINAL` block is the report: per-operation p50/p95/p99, error counts and
   bytes. `sample.sh 5` prints panel/Xray CPU and RSS every 5 s; read the panel and
   host columns only — the sshd column is bots and noise.

### Traps

- `pkill -f /root/lt/...` over ssh matches the ssh command itself; use `[/]root/lt/`.
- `zzload seed` appends nodes to an existing `lt.json`; delete it with the database
  before reseeding, or the tokens and the DB disagree.
- A fresh data directory needs `secrets.key`, `decoy.seed`, `bin/`, `geo/`, `abuse/`
  from an existing install; seed cannot create the first settings row itself.
- `-users` only sets active users; the node state still carries every seeded user, so
  a "5k run" on a 50k database is not a 5k run.
- 10 nodes at 50k need `-nodes 10` and their tokens; five tokens cannot emulate ten.

## Throughput, not just a mix

A mix says how the panel feels; it does not say where it gives up. For that second
question, drive **one operation only** and raise it until the box stops keeping up —
with a plain keep-alive client rather than `zzload`, whose own pacing becomes the
limit long before the panel's: a 40-line Go client over `net/http`, `-c` workers and a
list of subscription tokens was enough to find the 2026-11 ceiling.

- Subscriptions: `<panel>/sub/<token>` over 1..256 tokens, 20 s per level, measuring
  rps + p50/p95/p99 + errors with the panel's CPU sampled beside it. On the 2026-11
  stand throughput was still rising at 16 clients and plateaued at ~900/s with the
  panel at 17% of a core — a serialized wait inside the panel, not CPU, and two
  orders of magnitude above a real 20k panel's subscription rate.
- Creates: one level per rate (0.1, 0.25, 0.5, 1/s), nodes emulated, because every
  create rebuilds state and pushes it to each node.

Take the tokens from the database — `select sub_token from users where enabled=1 and
(expire_at=0 or expire_at > strftime('%s','now')) limit 500` — an expired token answers
with a page, and a number measured on those means nothing.

## What to report

Always state: users, mix (subs/pages/cards/edits/creates/dashboards), nodes, window,
panel build and Xray pin, cgroup and `GOMEMLIMIT`, whether another panel shared the
host, and where the generator ran. Without the mix, a capacity number is not
comparable — the users page and creates dominate it at 50k.
