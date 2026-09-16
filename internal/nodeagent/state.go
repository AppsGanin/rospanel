package nodeagent

import (
	"bytes"
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
	"github.com/AppsGanin/rospanel/internal/nodestate"
	"github.com/AppsGanin/rospanel/internal/tlsutil"
	"github.com/AppsGanin/rospanel/internal/version"
)

// persistState is the agent's durable state (state.json): the last config it
// applied, so a reboot re-applies it without the panel. Kept separate from the
// identity (node.json) so credentials and volatile state don't share a file.
type persistState struct {
	LastConfig *nodeapi.NodeState `json:"last_config,omitempty"`
	// Split is the state as last sent in parts (see split.go). When it is set,
	// LastConfig is put together from it on load and not written: the parts are the
	// state, and a second copy of the users would double every write.
	Split *nodestate.Held `json:"split,omitempty"`
	// LastReportID is the highest traffic-report id this node has sent. The panel's
	// per-node watermark is forward-only, so a report id that regressed after a
	// restart (self-update, reboot, crash) would be dropped as a stale duplicate and
	// the node's traffic would silently stop counting. Persisting it keeps report ids
	// monotonic across restarts, honouring the wire contract in nodeapi.SyncRequest.
	LastReportID int64 `json:"last_report_id,omitempty"`
	// Revoked records that the panel has switched this node off, so a reboot doesn't
	// undo it. Without it the agent re-applies LastConfig on every boot and serves
	// again — with the users and credentials the panel withdrew — until its first
	// successful sync says otherwise. If the panel happens to be unreachable from
	// this node, "until" is forever, which makes disabling a node no guarantee at all.
	Revoked bool `json:"revoked,omitempty"`
	// AgentVersion is the binary that wrote this file.
	//
	// It exists to make an agent upgrade re-fetch the whole desired state. The state
	// is persisted by re-marshalling it through THIS binary's structs, so any field a
	// newer panel added is silently dropped by an older agent — while the HASH it
	// keeps reporting is the panel's, computed over the complete state. The two then
	// agree forever ("your config is current") over a config the node never actually
	// had: exactly how a fleet ends up with a panel that pushed per-user speed caps
	// and nodes that never applied any.
	AgentVersion string `json:"agent_version,omitempty"`
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
	// A binary change means the struct that wrote this file may not be the struct
	// reading it. The config itself is kept — Xray must keep serving across an
	// upgrade — but the hash is dropped, so the very first sync disagrees with the
	// panel and pulls the complete, current state down again. One extra push per
	// upgrade, in exchange for never running a config with fields quietly missing.
	if (s.LastConfig != nil || s.Split != nil) && s.AgentVersion != version.Version {
		slog.Info("node: agent version changed — re-fetching the full config",
			"was", s.AgentVersion, "now", version.Version)
		if s.LastConfig != nil {
			s.LastConfig.Hash = ""
		}
		if s.Split != nil {
			s.Split.Hash, s.Split.Tag = "", ""
		}
	}
	if s.Split != nil {
		// The parts are hashed as the panel sent them: undo any formatting of the file.
		compact := func(raw json.RawMessage) json.RawMessage {
			var b bytes.Buffer
			if err := json.Compact(&b, raw); err != nil {
				return raw
			}
			return b.Bytes()
		}
		s.Split.Skeleton, s.Split.Blocked = compact(s.Split.Skeleton), compact(s.Split.Blocked)
		for i := range s.Split.Rows {
			s.Split.Rows[i] = compact(s.Split.Rows[i])
		}
	}
	s.AgentVersion = version.Version
	return &s
}

// writeState persists the current durable state atomically (write-temp + rename).
func (a *Agent) writeState() {
	a.stateMu.Lock()
	snapshot := *a.state
	a.stateMu.Unlock()

	var b []byte
	var err error
	if snapshot.Split != nil {
		snapshot.LastConfig = nil // put together from the parts on load
		b, err = json.Marshal(&snapshot)
	} else {
		b, err = json.MarshalIndent(&snapshot, "", "  ")
	}
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

// setLastConfig records the applied desired state and persists it atomically.
func (a *Agent) setLastConfig(st *nodeapi.NodeState) {
	a.stateMu.Lock()
	a.state.LastConfig = st
	a.state.Split = nil
	a.state.AgentVersion = version.Version
	a.stateMu.Unlock()
	a.parts = nil
	a.writeState()
}

// setSplit records a state applied from parts: the parts, and what was put together
// from them.
func (a *Agent) setSplit(held *nodestate.Held, st *nodeapi.NodeState) {
	a.stateMu.Lock()
	a.state.LastConfig = st
	a.state.Split = held
	a.state.AgentVersion = version.Version
	a.stateMu.Unlock()
	a.writeState()
}

// renameSplit records the name the panel now gives the state the node holds.
func (a *Agent) renameSplit(held *nodestate.Held) {
	a.stateMu.Lock()
	a.state.Split = held
	a.stateMu.Unlock()
	a.writeState()
}

// forgetSplitTag drops the name of the state the node holds, keeping the state: the
// next sync then says only what the node has, by its hash.
func (a *Agent) forgetSplitTag() {
	a.stateMu.Lock()
	if a.state.Split != nil {
		held := *a.state.Split
		held.Tag = ""
		a.state.Split = &held
		if a.parts != nil {
			parts := *a.parts
			parts.Held = &held
			a.parts = &parts
		}
	}
	a.stateMu.Unlock()
	a.writeState()
}

// splitTag is the name of the split state the node holds, "" for none.
func (a *Agent) splitTag() string {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.state.Split == nil {
		return ""
	}
	return a.state.Split.Tag
}

// setRevoked records whether the panel currently has this node switched off, so the
// answer survives a reboot. Writes only on a change — this is called on every sync
// that reports a revocation, and the state file is not worth rewriting every minute.
func (a *Agent) setRevoked(revoked bool) {
	a.stateMu.Lock()
	if a.state.Revoked == revoked {
		a.stateMu.Unlock()
		return
	}
	a.state.Revoked = revoked
	a.stateMu.Unlock()
	a.writeState()
}

// wasRevoked reports the revocation remembered from before this process started.
func (a *Agent) wasRevoked() bool {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	return a.state.Revoked
}

// noteReportID persists the newest traffic-report id so it survives a restart. Called
// (outside statsMu) whenever a fresh batch is promoted, keeping ids monotonic across
// process restarts — otherwise the panel drops the post-restart batch as a duplicate.
func (a *Agent) noteReportID(rid int64) {
	a.stateMu.Lock()
	if rid <= a.state.LastReportID {
		a.stateMu.Unlock()
		return
	}
	a.state.LastReportID = rid
	a.stateMu.Unlock()
	a.writeState()
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
//
// A ticker would fire on an exact 60s grid forever. The sample itself is local, but
// what it produces rides out on the next sync, so a fixed grid pushes a periodic
// bump into the panel↔node traffic on top of the poll's own rhythm. Re-arming a
// timer per iteration keeps the average cadence and drops the period.
func (a *Agent) statsLoop(ctx context.Context) {
	t := time.NewTimer(jitter(statsInterval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sampleStats()
			t.Reset(jitter(statsInterval))
		}
	}
}

func (a *Agent) sampleStats() {
	a.sampleAWG()
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
	// Prune counters for users no longer in Xray's output so the map can't grow
	// unbounded across user churn on a long-lived node.
	for email := range a.lastCounters {
		if _, ok := stats[email]; !ok {
			delete(a.lastCounters, email)
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
	} else if st.Split != nil {
		applied = short(st.Split.Hash) + fmt.Sprintf(" (%d users)", len(st.Split.Rows))
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
