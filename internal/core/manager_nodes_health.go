package core

import (
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/version"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// NodeHealth is the diagnostics for one server, so the Nodes page can show the
// same report next to every card. Node 0 is the panel's own server and reuses the
// full local report (Xray, config, TLS, disk/RAM, geo, connguard, BBR).
//
// A remote node is diagnosed entirely from what it last reported — the panel never
// dials it — so the report is honest about staleness instead of pretending to
// probe: everything is qualified by "as the node reported it" through the connection
// check, which leads and says how fresh that picture is.
func (m *Manager) NodeHealth(id int64) (*HealthReport, error) {
	if id == model.LocalNodeID {
		rep := m.Health()
		// Drop the fleet summary ("nodes: N online"): it is not a fact about THIS
		// server, and the page it now appears on already lists every node's state one
		// card below. Health() keeps it for /v1/health, where a monitor has no such
		// list — so the status is recomputed here without it.
		kept := rep.Checks[:0]
		for _, c := range rep.Checks {
			if c.Key != "nodes" {
				kept = append(kept, c)
			}
		}
		rep.Checks = kept
		rep.Status = worstStatus(rep.Checks)
		return rep, nil
	}
	n, err := m.store.GetNode(id)
	if err != nil {
		return nil, err
	}
	if n == nil {
		return nil, &ValidationError{Msg: "node not found"}
	}
	now := time.Now().Unix()
	online := n.Online(now)
	checks := []HealthCheck{m.nodeLinkHealth(n, now, online)}
	// Everything below describes the node's last report. When it has never
	// connected there is nothing to describe, so the link check stands alone.
	if n.Joined() {
		checks = append(checks,
			nodeXrayHealth(n),
			m.nodeConfigHealth(n, online),
			nodeCertHealth(n),
		)
		// The machine itself, as the node reported it. An agent older than this
		// feature sends nothing, so the rows are omitted rather than shown as zeros.
		if h, ok := m.NodeHostStats(n.ID); ok {
			checks = append(checks,
				diskHealth(h.DiskUsed, h.DiskTotal),
				memHealth(h.MemUsed, h.MemTotal),
				nodeConnGuardHealth(h),
				nodeBBRHealth(h),
			)
		}
		checks = append(checks,
			m.nodeGeoHealth(n),
			nodeAgentHealth(n),
		)
	}
	return &HealthReport{Status: worstStatus(checks), Checks: checks}, nil
}

// nodeLinkHealth reports the node↔panel link: the one check that says how much the
// rest of the report can be trusted.
func (m *Manager) nodeLinkHealth(n *model.Node, now int64, online bool) HealthCheck {
	const label = "health.nodeLink"
	switch {
	case !n.Enabled:
		return HealthCheck{Key: "link", LabelKey: label, Status: healthInfo,
			DetailKey: "health.nodeDisabled"}
	case !n.Joined():
		return HealthCheck{Key: "link", LabelKey: label, Status: healthWarn,
			DetailKey: "health.nodeNeverJoined", HintKey: "health.nodeNeverJoinedHint"}
	case online:
		return HealthCheck{Key: "link", LabelKey: label, Status: healthOK,
			DetailKey: "health.nodeOnline", Args: map[string]any{"ago": humanDuration(now - n.LastSeen)}}
	default:
		return HealthCheck{Key: "link", LabelKey: label, Status: healthError,
			DetailKey: "health.nodeOffline", HintKey: "health.nodeOfflineHint",
			Args: map[string]any{"ago": humanDuration(now - n.LastSeen)}}
	}
}

func nodeXrayHealth(n *model.Node) HealthCheck {
	const label = "health.xray"
	if !n.XrayRunning {
		return HealthCheck{Key: "xray", LabelKey: label, Status: healthError,
			DetailKey: "health.nodeXrayDown", HintKey: "health.nodeXrayDownHint"}
	}
	ver := n.XrayVersion
	if ver == "" {
		ver = "?"
	}
	if n.XrayVersion != "" && !xray.VersionMatchesPinned(n.XrayVersion) {
		return HealthCheck{Key: "xray", LabelKey: label, Status: healthWarn,
			DetailKey: "health.nodeXrayStale", HintKey: "health.nodeUpdateHint",
			Args: map[string]any{"version": ver, "want": xray.PinnedVersion}}
	}
	return HealthCheck{Key: "xray", LabelKey: label, Status: healthOK,
		DetailKey: "health.nodeXrayOK", Args: map[string]any{"version": ver}}
}

// nodeConfigHealth compares what the node last applied against what the panel
// would push now. A mismatch on a live node means the push is stuck; on an offline
// one it is simply the pending change it will pick up when it returns.
func (m *Manager) nodeConfigHealth(n *model.Node, online bool) HealthCheck {
	const label = "health.config"
	state, err := m.NodeDesiredState(n)
	if err != nil {
		return HealthCheck{Key: "config", LabelKey: label, Status: healthError,
			DetailKey: "health.nodeConfigBuildFailed", HintKey: "health.nodeConfigHint",
			Args: map[string]any{"err": err.Error()}}
	}
	if state.Hash == n.ConfigHash {
		return HealthCheck{Key: "config", LabelKey: label, Status: healthOK,
			DetailKey: "health.nodeConfigCurrent"}
	}
	if !online {
		return HealthCheck{Key: "config", LabelKey: label, Status: healthInfo,
			DetailKey: "health.nodeConfigPendingOffline"}
	}
	return HealthCheck{Key: "config", LabelKey: label, Status: healthWarn,
		DetailKey: "health.nodeConfigPending", HintKey: "health.nodeConfigPendingHint"}
}

// nodeCertWarnDays is how close to expiry a node's cert must be before it reads as
// a problem rather than normal renewal churn.
const nodeCertWarnDays = 2

func nodeCertHealth(n *model.Node) HealthCheck {
	const label = "health.tls"
	if n.CertExpiresAt == 0 && n.CertIssuer == "" && !n.CertSelfSigned {
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthInfo,
			DetailKey: "health.nodeCertUnknown"}
	}
	daysLeft := int(time.Until(time.Unix(n.CertExpiresAt, 0)).Hours() / 24)
	if n.CertExpiresAt > 0 && time.Now().After(time.Unix(n.CertExpiresAt, 0)) {
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthError,
			DetailKey: "health.tlsExpired", HintKey: "health.nodeCertExpiredHint",
			Args: map[string]any{"date": time.Unix(n.CertExpiresAt, 0).Format("02.01.2006")}}
	}
	if n.CertSelfSigned {
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthWarn,
			DetailKey: "health.nodeCertSelfSigned", HintKey: "health.nodeCertSelfSignedHint"}
	}
	// A low, fixed floor on purpose: the node reports only the expiry, not the
	// issue date, so we cannot scale the threshold to the cert's lifetime the way
	// tlsHealth does. The master's 14-day floor would warn forever on a node with a
	// ~6-day Let's Encrypt IP cert, which is perfectly healthy and renews itself.
	if daysLeft < nodeCertWarnDays {
		return HealthCheck{Key: "tls", LabelKey: label, Status: healthWarn,
			DetailKey: "health.nodeCertExpiring", HintKey: "health.nodeCertExpiringHint",
			Args: map[string]any{"days": daysLeft, "issuer": n.CertIssuer}}
	}
	return HealthCheck{Key: "tls", LabelKey: label, Status: healthOK,
		DetailKey: "health.nodeCertOK", Args: map[string]any{"days": daysLeft, "issuer": n.CertIssuer}}
}

// nodeGeoHealth reads the geo status the node reported (the panel can't stat the
// node's disk).
func (m *Manager) nodeGeoHealth(n *model.Node) HealthCheck {
	const label = "health.geo"
	files := m.NodeGeoFiles(n.ID)
	if len(files) == 0 {
		return HealthCheck{Key: "geo", LabelKey: label, Status: healthInfo,
			DetailKey: "health.nodeGeoUnknown"}
	}
	now := time.Now().Unix()
	var missing []string
	var oldest int64
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
			DetailKey: "health.geoMissing", HintKey: "health.nodeGeoHint",
			Args: map[string]any{"files": strings.Join(missing, ", ")}}
	}
	if oldest > 60 {
		return HealthCheck{Key: "geo", LabelKey: label, Status: healthWarn,
			DetailKey: "health.geoStale", HintKey: "health.nodeGeoHint",
			Args: map[string]any{"days": oldest}}
	}
	return HealthCheck{Key: "geo", LabelKey: label, Status: healthOK,
		DetailKey: "health.geoOK", Args: map[string]any{"days": oldest}}
}

// nodeConnGuardHealth mirrors the master's flood-guard check. A node always wants
// the guard (its agent installs it on every apply, with no opt-out env), so
// "not active" here means nftables or root was missing — the same silent gap the
// master's check exists to surface.
func nodeConnGuardHealth(h nodeapi.HostStats) HealthCheck {
	const label = "health.connguard"
	if h.ConnGuard {
		return HealthCheck{Key: "connguard", LabelKey: label, Status: healthOK,
			DetailKey: "health.connguardOK"}
	}
	return HealthCheck{Key: "connguard", LabelKey: label, Status: healthWarn,
		DetailKey: "health.nodeConnguardMissing", HintKey: "health.nodeConnguardHint"}
}

// nodeBBRHealth mirrors bbrHealth: informational, since BBR is a throughput
// optimization and a kernel without it is not a fault.
func nodeBBRHealth(h nodeapi.HostStats) HealthCheck {
	const label = "health.bbr"
	if h.BBR {
		return HealthCheck{Key: "bbr", LabelKey: label, Status: healthOK, DetailKey: "health.bbrOn"}
	}
	return HealthCheck{Key: "bbr", LabelKey: label, Status: healthInfo,
		DetailKey: "health.nodeBbrOff", HintKey: "health.bbrHint"}
}

// nodeAgentHealth flags a node running an older build than the panel: the two
// speak one protocol, and a drifting agent is what makes a new panel feature
// silently not work on that server.
func nodeAgentHealth(n *model.Node) HealthCheck {
	const label = "health.agent"
	if n.NodeVersion == "" {
		return HealthCheck{Key: "agent", LabelKey: label, Status: healthInfo,
			DetailKey: "health.agentUnknown"}
	}
	if n.NodeVersion != version.Version {
		return HealthCheck{Key: "agent", LabelKey: label, Status: healthWarn,
			DetailKey: "health.agentStale", HintKey: "health.nodeUpdateHint",
			Args: map[string]any{"version": n.NodeVersion, "panel": version.Version}}
	}
	return HealthCheck{Key: "agent", LabelKey: label, Status: healthOK,
		DetailKey: "health.agentOK", Args: map[string]any{"version": n.NodeVersion}}
}
