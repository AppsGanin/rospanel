# Grafana dashboard

`rospanel.json` is a ready dashboard for the panel's own metrics: users, traffic and
throughput, one row per server in the fleet, and the panel host itself. Import it in
Grafana under **Dashboards → New → Import**, upload the file, and pick your Prometheus
data source when asked.

## Pointing Prometheus at the panel

The metrics endpoint is `GET https://<panel>/<api-path>/v1/metrics`. It sits behind
the same API key as the rest of the external API, so the scrape has to carry one.
Create a key under **Settings → API**, then:

```yaml
scrape_configs:
  - job_name: rospanel
    scheme: https
    metrics_path: /<api-path>/v1/metrics
    authorization:
      credentials: rp_your_api_key_here
    static_configs:
      - targets: ['panel.example.com']
```

**The key is not read-only.** The panel has no read-only API keys: any valid key opens
the whole `/v1` surface, including creating and deleting users and servers. A
`prometheus.yml` is a file that tends to be world-readable and committed to a config
repo, so treat the key in it as the panel credential it is — restrict the file, keep it
out of version control, and give the scrape its own key so it can be revoked on its own.

## What the panels expect

Everything is scraped from one target, so no `instance` or `job` filtering is built
in. Two things are worth knowing when a panel looks empty rather than wrong:

- **Per-server CPU, memory and disk** are only reported by a node whose agent has
  checked in. A server that was added but never installed has no host stats at all,
  and drops out of those three panels instead of showing zero.
- **Devices bound** is only emitted while device binding (HWID) is switched on.

Traffic panels reset at local midnight in the operator's timezone, not on a rolling
24 hours — that is what the panel itself counts, so the dashboard follows it rather
than inventing a second definition.
