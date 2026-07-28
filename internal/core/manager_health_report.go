package core

import (
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/connguard"
	"github.com/AppsGanin/rospanel/internal/geo"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
	"github.com/AppsGanin/rospanel/internal/tuning"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Health check severities. worstStatus ranks error > warn > ok; info is advisory
// and never worsens the overall verdict.
const (
	healthOK    = "ok"
	healthWarn  = "warn"
	healthError = "error"
	healthInfo  = "info"
)

// HealthCheck is one diagnostic line shown on the Health page.
//
// The wording lives in the PANEL, not here: this carries dictionary keys plus the
// values to interpolate, and the SPA renders them against the language the admin
// picked in their browser. The server has no way to know that language — it is a
// per-browser choice, not a stored setting — so anything it worded itself would be
// stuck in one language on a bilingual page.
//
// Detail is the exception and stays free-form text: some details are not a sentence
// we wrote but something the outside world said (Xray's own config error, an ACME
// failure). Those pass through verbatim; DetailKey is empty for them.
type HealthCheck struct {
	Key      string `json:"key"`
	LabelKey string `json:"label_key"`
	Status   string `json:"status"` // ok | warn | error | info

	DetailKey string         `json:"detail_key,omitempty"`
	Detail    string         `json:"detail,omitempty"` // verbatim text when DetailKey is empty
	Args      map[string]any `json:"args,omitempty"`   // interpolated into DetailKey

	HintKey string `json:"hint_key,omitempty"` // shown when the check isn't ok
}

// HealthReport aggregates the per-component checks plus the worst overall status.
type HealthReport struct {
	Status string        `json:"status"`
	Checks []HealthCheck `json:"checks"`
}

// Health gathers the panel's self-diagnostics: the Xray process, last config
// apply, TLS certificate, disk/RAM headroom, geo databases, and any enabled
// helper egress lane. Every signal is read from memory/disk — no extra network
// calls — so the page is cheap to poll.
func (m *Manager) Health() *HealthReport {
	set, _ := m.store.GetSettings()
	checks := []HealthCheck{m.xrayHealth(), m.configHealth(set), m.tlsHealth()}

	if m.sys != nil {
		s := m.sys.Read()
		checks = append(checks, diskHealth(s.DiskUsed, s.DiskTotal), memHealth(s.MemUsed, s.MemTotal))
	}
	checks = append(checks, m.geoHealth())

	checks = append(checks, m.connGuardHealth(), bbrHealth())

	if nc := m.nodesHealth(); nc != nil {
		checks = append(checks, *nc)
	}

	if set != nil && set.OperaEnabled {
		if m.OperaHealthy() {
			checks = append(checks, HealthCheck{Key: "opera", LabelKey: "health.opera", Status: healthOK,
				DetailKey: "health.operaUp"})
		} else {
			checks = append(checks, HealthCheck{Key: "opera", LabelKey: "health.opera", Status: healthWarn,
				DetailKey: "health.operaDown", HintKey: "health.operaHint"})
		}
	}
	return &HealthReport{Status: worstStatus(checks), Checks: checks}
}

// nodesHealth summarizes the remote nodes: how many are online, and a warning
// when some enabled node is offline or on a stale Xray version. Returns nil when
// no nodes are configured (single-server install), so the check doesn't appear.
func (m *Manager) nodesHealth() *HealthCheck {
	nodes, err := m.store.ListNodes()
	if err != nil || len(nodes) == 0 {
		return nil
	}
	now := time.Now().Unix()
	var online, offline, stale int
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled {
			continue
		}
		if n.Online(now) {
			online++
		} else if n.Joined() {
			offline++
		}
		// Through the helper, never a raw compare: PinnedVersion carries a leading "v"
		// and `xray version` does not, so == reports every node as stale forever — the
		// warning survived any number of updates, and the Nodes tab (which does use the
		// helper) disagreed with the dashboard about the very same node.
		if n.XrayVersion != "" && !xray.VersionMatchesPinned(n.XrayVersion) {
			stale++
		}
	}
	total := online + offline
	switch {
	case offline > 0:
		return &HealthCheck{Key: "nodes", LabelKey: "health.nodes", Status: healthWarn,
			DetailKey: "health.nodesOffline", HintKey: "health.nodesOfflineHint",
			Args: map[string]any{"online": online, "total": total, "offline": offline}}
	case stale > 0:
		return &HealthCheck{Key: "nodes", LabelKey: "health.nodes", Status: healthWarn,
			DetailKey: "health.nodesStale", HintKey: "health.nodesStaleHint",
			Args: map[string]any{"online": online, "stale": stale}}
	case total == 0:
		return nil // only disabled nodes → nothing to report
	default:
		return &HealthCheck{Key: "nodes", LabelKey: "health.nodes", Status: healthOK,
			DetailKey: "health.nodesAllOnline", Args: map[string]any{"online": online}}
	}
}

func (m *Manager) xrayHealth() HealthCheck {
	if !m.sup.Running() {
		return HealthCheck{Key: "xray", LabelKey: "health.xray", Status: healthError,
			DetailKey: "health.xrayDown", HintKey: "health.xrayDownHint"}
	}
	ver := m.sup.Version()
	if ver == "" {
		ver = "?"
	}
	return HealthCheck{Key: "xray", LabelKey: "health.xray", Status: healthOK,
		DetailKey: "health.xrayUp",
		Args:      map[string]any{"version": ver, "uptime": m.sup.UptimeSeconds()}}
}

func (m *Manager) configHealth(set *model.Settings) HealthCheck {
	if set != nil && strings.TrimSpace(set.LastConfigError) != "" {
		// Xray's own message, passed through verbatim — we did not word it.
		return HealthCheck{Key: "config", LabelKey: "health.config", Status: healthError,
			Detail: set.LastConfigError, HintKey: "health.configHint"}
	}
	var rev int64
	if set != nil {
		rev = set.ConfigRevision
	}
	return HealthCheck{Key: "config", LabelKey: "health.config", Status: healthOK,
		DetailKey: "health.configOK", Args: map[string]any{"revision": rev}}
}

func (m *Manager) tlsHealth() HealthCheck {
	const label = "health.tls"
	info, err := tlsutil.ReadCertInfo(m.tls.CertPath)
	if err != nil || info == nil {
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthError,
			DetailKey: "health.tlsMissing", HintKey: "health.tlsMissingHint"}
	}
	if !time.Now().Before(info.NotAfter) {
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthError,
			DetailKey: "health.tlsExpired", HintKey: "health.tlsExpiredHint",
			Args: map[string]any{"date": info.NotAfter.Format("02.01.2006")}}
	}
	if info.Issuer == "" || info.Issuer == info.Subject { // self-signed fallback
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthWarn,
			DetailKey: "health.tlsSelfSigned", HintKey: "health.tlsSelfSignedHint"}
	}
	// Renewal runs in the last third of the cert's lifetime, so the "expiring
	// soon" floor must scale with that lifetime. A Let's Encrypt IP cert lives
	// only ~6 days and is perfectly healthy at 5 days left; a 90-day domain cert
	// at 5 days left means renewal is failing. Without scaling, every IP install
	// would sit in a permanent false warning.
	lifeDays := int(info.NotAfter.Sub(info.NotBefore).Hours() / 24)
	args := map[string]any{
		"days":       info.DaysLeft,
		"issuer":     info.Issuer,
		"shortLived": lifeDays > 0 && lifeDays <= 10,
	}
	if info.DaysLeft < certWarnThreshold(lifeDays) {
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthWarn,
			DetailKey: "health.tlsExpiring", HintKey: "health.tlsExpiringHint", Args: args}
	}
	return HealthCheck{Key: "tls", LabelKey: label, Status: healthOK,
		DetailKey: "health.tlsOK", Args: args}
}

// certWarnThreshold is the "days left" floor below which a certificate is flagged
// as expiring soon. It scales with the cert's own lifetime — renewal happens in
// the last third of life — so a short-lived (~6-day LE IP) cert isn't perpetually
// warned, while a 90-day domain cert still warns at 14 days. Never below 1.
func certWarnThreshold(lifeDays int) int {
	t := 14
	if lifeDays > 0 && lifeDays/3 < t {
		t = lifeDays / 3
	}
	if t < 1 {
		t = 1
	}
	return t
}

// connGuardHealth reports whether the per-IP connection limits are actually in
// force. connguard.Ensure degrades to a no-op when nft is missing or the panel
// isn't root, and only logs — so an operator who believes they're protected can be
// running with no guard at all and never know. That silent gap is the whole reason
// this check exists.
func (m *Manager) connGuardHealth() HealthCheck {
	const label = "health.connguard"
	if !m.connGuardWanted.Load() {
		return HealthCheck{Key: "connguard", LabelKey: label, Status: healthInfo,
			DetailKey: "health.connguardOff"}
	}
	if connguard.Active() {
		return HealthCheck{Key: "connguard", LabelKey: label, Status: healthOK,
			DetailKey: "health.connguardOK"}
	}
	return HealthCheck{Key: "connguard", LabelKey: label, Status: healthWarn,
		DetailKey: "health.connguardMissing", HintKey: "health.connguardHint"}
}

// bbrHealth reports the congestion-control algorithm. Informational, not a warning:
// BBR is a throughput optimization, and plenty of healthy kernels (and every non-
// Linux dev box) simply don't offer it — flagging that as a problem would be noise.
func bbrHealth() HealthCheck {
	const label = "health.bbr"
	if tuning.Active() {
		return HealthCheck{Key: "bbr", LabelKey: label, Status: healthOK, DetailKey: "health.bbrOn"}
	}
	return HealthCheck{Key: "bbr", LabelKey: label, Status: healthInfo,
		DetailKey: "health.bbrOff", HintKey: "health.bbrHint"}
}

func (m *Manager) geoHealth() HealthCheck {
	const label = "health.geo"
	files := geo.Status(m.sup.AssetDir())
	var missing []string
	var oldest int64
	now := time.Now().Unix()
	for _, f := range files {
		if !f.Present {
			missing = append(missing, f.Name)
			continue
		}
		if age := (now - f.ModifiedAt) / 86400; age > oldest {
			oldest = age
		}
	}
	if len(missing) > 0 {
		return HealthCheck{Key: "geo", LabelKey: label, Status: healthError,
			DetailKey: "health.geoMissing", HintKey: "health.geoHint",
			Args: map[string]any{"files": strings.Join(missing, ", ")}}
	}
	if oldest > 60 {
		return HealthCheck{Key: "geo", LabelKey: label, Status: healthWarn,
			DetailKey: "health.geoStale", HintKey: "health.geoHint",
			Args: map[string]any{"days": oldest},
		}
	}
	return HealthCheck{Key: "geo", LabelKey: label, Status: healthOK,
		DetailKey: "health.geoOK", Args: map[string]any{"days": oldest}}
}

func diskHealth(used, total int64) HealthCheck {
	const label = "health.disk"
	if total <= 0 {
		return HealthCheck{Key: "disk", LabelKey: label, Status: healthInfo, DetailKey: "health.noData"}
	}
	freePct := float64(total-used) / float64(total) * 100
	args := map[string]any{"used": humanBytes(used), "total": humanBytes(total), "freePct": int(freePct)}
	switch {
	case freePct < 5:
		return HealthCheck{Key: "disk", LabelKey: label, Status: healthError,
			DetailKey: "health.diskUsage", Args: args, HintKey: "health.diskCritHint"}
	case freePct < 15:
		return HealthCheck{Key: "disk", LabelKey: label, Status: healthWarn,
			DetailKey: "health.diskUsage", Args: args, HintKey: "health.diskLowHint"}
	default:
		return HealthCheck{Key: "disk", LabelKey: label, Status: healthOK,
			DetailKey: "health.diskUsage", Args: args}
	}
}

func memHealth(used, total int64) HealthCheck {
	const label = "health.mem"
	if total <= 0 {
		return HealthCheck{Key: "mem", LabelKey: label, Status: healthInfo, DetailKey: "health.noData"}
	}
	usedPct := float64(used) / float64(total) * 100
	args := map[string]any{"used": humanBytes(used), "total": humanBytes(total), "usedPct": int(usedPct)}
	if usedPct > 92 {
		return HealthCheck{Key: "mem", LabelKey: label, Status: healthWarn,
			DetailKey: "health.memUsage", Args: args, HintKey: "health.memHint"}
	}
	return HealthCheck{Key: "mem", LabelKey: label, Status: healthOK,
		DetailKey: "health.memUsage", Args: args}
}

// worstStatus returns the most severe status among the checks (error > warn > ok),
// ignoring purely informational rows. Empty → ok.
func worstStatus(checks []HealthCheck) string {
	rank := map[string]int{healthOK: 0, healthWarn: 1, healthError: 2}
	worst := healthOK
	for _, c := range checks {
		if rank[c.Status] > rank[worst] {
			worst = c.Status
		}
	}
	return worst
}

// humanBytes renders a byte count as a compact KB/MB/GB/TB string.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit && exp < len(units)-1; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), units[exp])
}

// humanDuration renders a second count as a coarse "Nd Nh" / "Nh Nm" / "Nm" string.
func humanDuration(sec int64) string {
	if sec <= 0 {
		return "—"
	}
	d := time.Duration(sec) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}
