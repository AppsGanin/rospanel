package server

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/version"
)

// Prometheus exposition, served at GET /<apiPath>/v1/metrics behind the same API key
// as the rest of the external surface (Prometheus sends it as `authorization:
// credentials:` in a scrape config).
//
// It lives under the API path rather than on a port of its own for the reason the
// liveness probe does: a second listener is another thing to firewall, and a
// /metrics answering at the root would fingerprint the panel in one request. The
// segment is also stable across secret rotation, so a scrape config keeps working.
//
// The format is written by hand instead of pulling in the client library. What is
// needed here is a few dozen lines of "name{labels} value" — the library brings
// registries, collectors and a dependency this binary otherwise doesn't have.
//
// Deliberately no per-user series. A panel with a thousand users would turn into a
// thousand time series per metric, which is how a small Prometheus falls over; the
// per-user numbers live in the API and the panel, where they are asked for one at a
// time.

// metricsWriter accumulates the exposition text.
type metricsWriter struct {
	b    strings.Builder
	seen map[string]bool // families already given their HELP/TYPE header
}

func newMetricsWriter() *metricsWriter {
	return &metricsWriter{seen: map[string]bool{}}
}

// gauge writes one sample, emitting the family header on first use. labels are
// alternating key/value pairs.
func (w *metricsWriter) gauge(name, help string, v float64, labels ...string) {
	w.metric("gauge", name, help, v, labels...)
}

// counter is for monotonically increasing totals (bytes served, and nothing that
// can go backwards without a restart).
func (w *metricsWriter) counter(name, help string, v float64, labels ...string) {
	w.metric("counter", name, help, v, labels...)
}

func (w *metricsWriter) metric(typ, name, help string, v float64, labels ...string) {
	if !w.seen[name] {
		w.seen[name] = true
		fmt.Fprintf(&w.b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}
	w.b.WriteString(name)
	if len(labels) > 1 {
		w.b.WriteByte('{')
		for i := 0; i+1 < len(labels); i += 2 {
			if i > 0 {
				w.b.WriteByte(',')
			}
			w.b.WriteString(labels[i])
			w.b.WriteString(`="`)
			w.b.WriteString(escapeLabel(labels[i+1]))
			w.b.WriteString(`"`)
		}
		w.b.WriteByte('}')
	}
	// 'f', not 'g': Prometheus accepts scientific notation, but a lifetime byte
	// counter rendered as 2.3405591506e+10 is unreadable to the operator running curl
	// against this endpoint. -1 precision still round-trips exactly.
	fmt.Fprintf(&w.b, " %s\n", strconv.FormatFloat(v, 'f', -1, 64))
}

func (w *metricsWriter) bool(name, help string, v bool, labels ...string) {
	n := 0.0
	if v {
		n = 1
	}
	w.gauge(name, help, n, labels...)
}

// escapeLabel escapes a label VALUE per the exposition format. Node names are
// operator-supplied text, so this is not theoretical: a quote or a newline in one
// would otherwise produce a file Prometheus refuses wholesale.
func escapeLabel(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}

// apiMetrics renders the panel's state as Prometheus metrics.
func (rt *Router) apiMetrics(w http.ResponseWriter, _ *http.Request) {
	m := newMetricsWriter()

	m.gauge("rospanel_build_info", "Panel build, always 1 — the version is in the label.",
		1, "version", version.Version)

	status, err := rt.mgr.SystemStatus()
	if err != nil {
		// The scrape is worth more than any single family: hand over what is already
		// written rather than 500, so the "is the panel up" signal survives a
		// database hiccup that costs us the user counts.
		writeMetrics(w, m.b.String())
		return
	}

	m.gauge("rospanel_users_total", "Users known to the panel.", float64(status.Users))
	m.gauge("rospanel_users_active", "Users able to connect right now (enabled, unexpired, within quota).",
		float64(status.EnabledUsers))
	m.gauge("rospanel_users_online", "Users carrying traffic right now, anywhere in the fleet.",
		float64(status.OnlineUsers))
	m.counter("rospanel_traffic_bytes_total", "Lifetime VPN traffic, by direction.",
		float64(status.TotalUp), "direction", "up")
	m.counter("rospanel_traffic_bytes_total", "Lifetime VPN traffic, by direction.",
		float64(status.TotalDown), "direction", "down")
	m.gauge("rospanel_traffic_today_bytes", "Traffic on the operator's current calendar day.",
		float64(status.TrafficToday))
	m.gauge("rospanel_throughput_bytes_per_second", "Live VPN throughput, by direction.",
		float64(status.VPNUp), "direction", "up")
	m.gauge("rospanel_throughput_bytes_per_second", "Live VPN throughput, by direction.",
		float64(status.VPNDown), "direction", "down")

	m.bool("rospanel_xray_running", "1 when the local Xray process is up.", status.XrayRunning)
	m.gauge("rospanel_xray_uptime_seconds", "Seconds since the local Xray last started.",
		float64(status.XrayUptime))
	m.gauge("rospanel_cert_days_left", "Days until the panel's TLS certificate expires.",
		float64(status.CertDaysLeft))

	// Host metrics. A node_exporter next to the panel would report the same numbers,
	// but most installs are a single 1-vCPU box where the panel is the only thing
	// running — publishing them here means one scrape target and no second agent.
	m.gauge("rospanel_cpu_percent", "Host CPU utilisation, percent.", status.CPUPercent)
	m.gauge("rospanel_memory_bytes", "Host memory, by state.", float64(status.MemUsed), "state", "used")
	m.gauge("rospanel_memory_bytes", "Host memory, by state.", float64(status.MemTotal), "state", "total")
	m.gauge("rospanel_swap_bytes", "Host swap, by state.", float64(status.SwapUsed), "state", "used")
	m.gauge("rospanel_swap_bytes", "Host swap, by state.", float64(status.SwapTotal), "state", "total")
	m.gauge("rospanel_disk_bytes", "Panel filesystem, by state.", float64(status.DiskUsed), "state", "used")
	m.gauge("rospanel_disk_bytes", "Panel filesystem, by state.", float64(status.DiskTotal), "state", "total")
	m.gauge("rospanel_host_uptime_seconds", "Seconds since the host booted.", float64(status.HostUptime))
	m.gauge("rospanel_process_memory_bytes", "Resident memory of the panel process.", float64(status.ProcMem))
	m.gauge("rospanel_goroutines", "Goroutines in the panel process.", float64(status.Goroutines))

	// How many accounts carry a speed cap the panel would enforce right now. It is
	// the number an operator checks when a cap "doesn't work": zero here means the
	// panel isn't capping anyone, which is a different problem from a cap that is
	// installed but not biting.
	m.gauge("rospanel_users_speed_capped", "Users with a speed cap in force.",
		float64(len(rt.mgr.SpeedLimits())))

	rt.metricsNodes(m)
	rt.metricsDevices(m)
	writeMetrics(w, m.b.String())
}

// metricsNodes publishes one series per server, master included, so a fleet graph
// shows which box is down rather than only that something is.
func (rt *Router) metricsNodes(m *metricsWriter) {
	nodes, err := rt.mgr.NodeViews()
	if err != nil {
		return
	}
	// Stable order so a diff of two scrapes is readable; Prometheus itself doesn't
	// care about ordering.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	for _, n := range nodes {
		id := strconv.FormatInt(n.ID, 10)
		lbl := []string{"node", n.Name, "node_id", id}
		m.bool("rospanel_node_online", "1 when the node is reporting in (the master is always 1).",
			n.Online, lbl...)
		m.bool("rospanel_node_enabled", "1 when the node is switched on in the panel.",
			n.Enabled, lbl...)
		m.bool("rospanel_node_xray_running", "1 when the node's Xray process is up.",
			n.XrayRunning, lbl...)
		m.gauge("rospanel_node_last_seen_seconds", "Seconds since the node last reported (0 = never).",
			nodeSilence(n.LastSeen), lbl...)

		// Traffic per server, so a fleet graph can answer "which node is carrying
		// this" — the aggregate above cannot, and pulling it from /v1/stats/nodes
		// means a second, differently-shaped source for the same question.
		m.gauge("rospanel_node_traffic_today_bytes", "Traffic through this server today, by direction.",
			float64(n.TrafficUp), append(lbl, "direction", "up")...)
		m.gauge("rospanel_node_traffic_today_bytes", "Traffic through this server today, by direction.",
			float64(n.TrafficDown), append(lbl, "direction", "down")...)

		// The machine itself. A node reports these on every sync; a node that has
		// never reported (or an agent too old to send them) simply has no samples,
		// which is what a gap in a graph should mean.
		if !n.HasHostStats {
			continue
		}
		m.gauge("rospanel_node_cpu_percent", "Node CPU utilisation, percent.", n.CPUPercent, lbl...)
		m.gauge("rospanel_node_memory_bytes", "Node memory, by state.",
			float64(n.MemUsed), append(lbl, "state", "used")...)
		m.gauge("rospanel_node_memory_bytes", "Node memory, by state.",
			float64(n.MemTotal), append(lbl, "state", "total")...)
		m.gauge("rospanel_node_disk_bytes", "Node filesystem, by state.",
			float64(n.DiskUsed), append(lbl, "state", "used")...)
		m.gauge("rospanel_node_disk_bytes", "Node filesystem, by state.",
			float64(n.DiskTotal), append(lbl, "state", "total")...)
		m.gauge("rospanel_node_host_uptime_seconds", "Seconds since the node's host booted.",
			float64(n.HostUptime), lbl...)
	}
}

// nodeSilence is how long ago a node last checked in. A node that has never
// reported gets 0 rather than "seconds since 1970", which would otherwise dwarf
// every real value on a graph.
func nodeSilence(lastSeen int64) float64 {
	if lastSeen <= 0 {
		return 0
	}
	return float64(time.Now().Unix() - lastSeen)
}

// metricsDevices publishes the HWID roster size — the number an operator watches
// after switching device binding on, to see whether the cap is biting.
func (rt *Router) metricsDevices(m *metricsWriter) {
	set, err := rt.mgr.Settings()
	if err != nil {
		return
	}
	m.bool("rospanel_device_binding_enabled", "1 when HWID device binding is switched on.",
		set.HWIDEnabled)
	if !set.HWIDEnabled {
		return
	}
	n, err := rt.mgr.Store().CountAllDevices()
	if err != nil {
		return
	}
	m.gauge("rospanel_devices_bound", "Client installs currently holding a device slot.", float64(n))
}

func writeMetrics(w http.ResponseWriter, body string) {
	// The 0.0.4 content type is what every Prometheus version accepts; the newer
	// OpenMetrics one would also require an "# EOF" trailer.
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(body))
}
