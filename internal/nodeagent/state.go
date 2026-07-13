package nodeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
)

// persistState is the agent's durable state (state.json): the last config it
// applied, so a reboot re-applies it without the panel. Kept separate from the
// identity (node.json) so credentials and volatile state don't share a file.
type persistState struct {
	LastConfig *nodeapi.NodeState `json:"last_config,omitempty"`
}

func statePath(dataDir string) string { return filepath.Join(dataDir, "state.json") }

func loadState(dataDir string) *persistState {
	b, err := os.ReadFile(statePath(dataDir))
	if err != nil {
		return &persistState{}
	}
	var s persistState
	if err := json.Unmarshal(b, &s); err != nil {
		return &persistState{}
	}
	return &s
}

// setLastConfig records the applied desired state and persists it atomically.
func (a *Agent) setLastConfig(st *nodeapi.NodeState) {
	a.stateMu.Lock()
	a.state.LastConfig = st
	snapshot := *a.state
	a.stateMu.Unlock()

	b, err := json.MarshalIndent(&snapshot, "", "  ")
	if err != nil {
		return
	}
	tmp := statePath(a.dataDir) + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		slog.Warn("node: persist state failed", "err", err)
		return
	}
	_ = os.Rename(tmp, statePath(a.dataDir))
}

// ackReport clears the in-flight batch once the panel confirms it ingested it
// (ackReport >= the batch's report id). Traffic accumulated since (in `pending`)
// is untouched and goes out as the next batch.
func (a *Agent) ackReport(ackReport int64) {
	if ackReport <= 0 {
		return
	}
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if a.inflightID != 0 && ackReport >= a.inflightID {
		a.inflight = nil
		a.inflightID = 0
	}
}

// statsLoop samples Xray's per-user traffic counters and accumulates deltas into
// the pending buffer (sent on the next sync). It mirrors the panel's PollStats
// reset-handling: a counter that dropped means Xray restarted, so the new value is
// the delta from zero.
func (a *Agent) statsLoop(ctx context.Context) {
	t := time.NewTicker(statsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sampleStats()
		}
	}
}

func (a *Agent) sampleStats() {
	if !a.sup.Running() {
		return
	}
	stats, err := a.sup.QueryStats(a.sup.APIAddr())
	if err != nil {
		return
	}
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	for email, cur := range stats {
		uid, ok := userIDFromEmail(email)
		if !ok {
			continue
		}
		prev := a.lastCounters[email]
		addUp, addDown := cur.Up-prev.Up, cur.Down-prev.Down
		if cur.Up < prev.Up { // Xray restarted → counter reset
			addUp = cur.Up
		}
		if cur.Down < prev.Down {
			addDown = cur.Down
		}
		a.lastCounters[email] = cur
		if addUp <= 0 && addDown <= 0 {
			continue
		}
		d := a.pending[uid]
		if d == nil {
			d = &nodeapi.TrafficDelta{UserID: uid}
			a.pending[uid] = d
		}
		if addUp > 0 {
			d.Up += addUp
		}
		if addDown > 0 {
			d.Down += addDown
		}
	}
}

// userIDFromEmail parses an Xray client email "u<id>" back to the user id.
func userIDFromEmail(email string) (int64, bool) {
	if !strings.HasPrefix(email, "u") {
		return 0, false
	}
	id, err := strconv.ParseInt(email[1:], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

// Status reports the node's local view for `rospanel node status`.
func Status(dataDir string) (string, error) {
	ident, err := LoadIdentity(dataDir)
	if err != nil {
		return "", err
	}
	st := loadState(dataDir)
	var b strings.Builder
	fmt.Fprintf(&b, "Node ID   : %d\n", ident.NodeID)
	fmt.Fprintf(&b, "Panel     : %s\n", ident.PanelURL)
	applied := "none"
	if st.LastConfig != nil {
		applied = short(st.LastConfig.Hash)
	}
	fmt.Fprintf(&b, "Config    : %s\n", applied)
	// Cert freshness, if any.
	certPath := filepath.Join(dataDir, "certs", "cert.pem")
	if ci, err := tlsutil.ReadCertInfo(certPath); err == nil {
		kind := "CA-issued"
		if ci.Issuer == "" || ci.Issuer == ci.Subject {
			kind = "self-signed"
		}
		fmt.Fprintf(&b, "TLS cert  : %s, %d days left\n", kind, ci.DaysLeft)
	} else {
		fmt.Fprintf(&b, "TLS cert  : none yet\n")
	}
	return b.String(), nil
}
