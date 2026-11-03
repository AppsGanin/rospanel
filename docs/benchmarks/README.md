# Panel capacity

Measured on **2026-11-03** on the test master: **1 vCPU, 961 MB RAM**, Ubuntu 24,
RosPanel **5.13.0**, Xray-core **26.10.21**. The panel ran in its own netns capped at
`MemoryMax=620M` with `MemorySwapMax=0` and `GOMEMLIMIT=300MiB`, while the live panel
kept running on the host beside it — the box was busier and tighter on memory than a
production one, so these numbers are the conservative side. The load generator lived
on another machine, so its CPU is not part of the panel's.

## Latency at 20,000 users

Full mix over a four-minute window after warmup, **0 errors**: 11 subscriptions/s
(about 2,600 in the window), 1 users page/s and 1 card/s (240 each), 0.3 limit
edits/s (71), 0.05 creates/s (11), 2 dashboards.

![Latency at 20,000 users](../img/bench/latency-20k-en.svg)

| Operation | p50 | p95 | p99 |
|---|---:|---:|---:|
| Subscription | 26 ms | 447 ms | 535 ms |
| Users page | 45 ms | 277 ms | 487 ms |
| User card | 28 ms | 277 ms | 488 ms |
| Limit edits | 29 ms | 223 ms | 228 ms |
| Creates | 41 ms | 59 ms | 59 ms |

## Throughput: subscriptions

The same 20,000 users under a subscription-only load — no admin work, no node sync, a
keep-alive client on another machine:

| Concurrent clients | Subscriptions/s | p50 | p95 | p99 | Errors |
|---|---:|---:|---:|---:|---:|
| 4 | 577 | 7 ms | 9 ms | 13 ms | 0 |
| 8 | 750 | 11 ms | 14 ms | 19 ms | 0 |
| 16 | 877 | 16 ms | 32 ms | 37 ms | 0 |
| 64 | 887 | 36 ms | 41 ms | 46 ms | 0 |
| 256 | 902 | 210 ms | 531 ms | 1 s | 0 |

About 900 subscriptions per second on one vCPU, with the panel at **17% of a core**. A
20,000-user panel sees ~11 subscriptions/s at a 30-minute refresh interval, so the
headroom is two orders of magnitude: CPU is not what a box of this size runs out of.

## 50,000 users

| Scenario | p50 | p95 | Errors | Panel RSS |
|---|---:|---:|---:|---:|
| 50k, subscriptions 14/s | 26 ms | 437 ms | 0 | — |
| 50k, node sync | 56 ms | 134 ms | 0 | 300–346 MB |

The read path does not get slower with the user base: a subscription answers in 26 ms
at p50 on both 20k and 50k, and node sync stays at 56 ms p50 / 134 ms p95 with no
errors.

## Memory: how to size a box

20k users with the full mix is 336 MB of panel and 160 MB of Xray. 50k in subscription
or node-sync mode is 346 + 247 MB. Size by RAM, not CPU: about 1 GB per 20k users with
admin work, or 50k users who mostly fetch subscriptions and sync nodes.

## What it means

- **One vCPU is not the wall:** 20% average load with the full mix at 20k, 17% while
  serving 900 subscriptions/s. The spikes to 88% come from config builds on creates,
  not from serving users.
- **Memory sets the size of the box:** 1 GB carries ~20k with the full mix, or 50k
  subscriptions and node sync.
- **The disk is not the limit either:** the panel batches its writes, and the measured
  ceiling of the disk it ran on sits far above what the workload asks (below).

## SQLite write ceiling

![SQLite write ceiling](../img/bench/write-en.svg)

Measured on the same disk, on its own, so the panel's batching can be judged against
it: WAL, `synchronous=FULL`, one connection, 256-byte rows — `fsync` 2,638/s, 1,381
autocommit writes/s, 63,386 rows/s in one transaction, 20,701 writes/s with
`synchronous=NORMAL`.

How to repeat: [tools/bench/README.md](https://github.com/AppsGanin/rospanel/blob/main/tools/bench/README.md).
