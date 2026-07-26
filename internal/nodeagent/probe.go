package nodeagent

import (
	"fmt"
	"net"
	"sync"

	"github.com/AppsGanin/rospanel/internal/hop"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
)

// hopRanges is the complete set of UDP funnels this node should install, taken from
// the panel's desired state. Falls back to the single built-in Hysteria2 range when
// the panel is too old to send the list, so a mixed-version fleet keeps working.
func hopRanges(m nodeapi.NodeMeta) []hop.Range {
	if len(m.HopRanges) > 0 {
		out := make([]hop.Range, 0, len(m.HopRanges))
		for _, r := range m.HopRanges {
			out = append(out, hop.Range{Start: r.Start, End: r.End, Target: r.Target})
		}
		return out
	}
	if !m.HysteriaEnabled {
		return nil
	}
	return []hop.Range{{Start: m.HopStart, End: m.HopEnd, Target: m.HysteriaPort}}
}

// stashConfigCheck holds a config verdict until the next sync request carries it.
func (a *Agent) stashConfigCheck(res nodeapi.ConfigCheckResult) {
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	a.configCheck = &res
}

// takeConfigCheck drains the pending verdict into the outgoing sync request.
func (a *Agent) takeConfigCheck() *nodeapi.ConfigCheckResult {
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	out := a.configCheck
	a.configCheck = nil
	return out
}

// runConfigCheck asks the local Xray whether it would accept a candidate config,
// WITHOUT applying it. The panel is holding an operator's save on the answer.
//
// The cert-path sentinels are substituted first, exactly as applyState does: the
// panel doesn't know this node's data directory, and leaving them in would make Xray
// reject a config that is actually fine for a reason that has nothing to do with what
// the operator changed.
func (a *Agent) runConfigCheck(req nodeapi.ConfigCheckRequest) nodeapi.ConfigCheckResult {
	res := nodeapi.ConfigCheckResult{ID: req.ID}
	body := substituteCertPaths(req.Config, a.certPath, a.keyPath)
	if err := a.sup.ValidateBytes(body); err != nil {
		res.Err = err.Error()
		return res
	}
	res.OK = true
	return res
}

// stashProbeResults holds the answers until the next sync request carries them.
func (a *Agent) stashProbeResults(res []nodeapi.PortProbeResult) {
	if len(res) == 0 {
		return
	}
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	a.probeResults = append(a.probeResults, res...)
}

// takeProbeResults drains the pending answers into the outgoing sync request.
func (a *Agent) takeProbeResults() []nodeapi.PortProbeResult {
	a.probeMu.Lock()
	defer a.probeMu.Unlock()
	out := a.probeResults
	a.probeResults = nil
	return out
}

// runPortProbes bind-tests each port the panel asked about and returns the answers.
//
// The panel validates a custom inbound's port before saving it and cannot bind a
// socket on this machine, so this is the only way it can find out. The test is the
// real thing — an actual listen, immediately closed — rather than an inspection of
// /proc, because what matters is whether Xray will be able to bind, and only a bind
// answers that (permissions, an existing listener on a different address family, and
// SO_REUSEPORT all show up here and nowhere else).
//
// A port our OWN Xray currently holds reads as busy, which is correct: the operator
// is trying to put a second listener on it.
func runPortProbes(probes []nodeapi.PortProbe) []nodeapi.PortProbeResult {
	if len(probes) == 0 {
		return nil
	}
	// Deduplicate: the panel may re-ask for the same port across round trips.
	seen := make(map[nodeapi.PortProbe]struct{}, len(probes))
	var wg sync.WaitGroup
	var mu sync.Mutex
	out := make([]nodeapi.PortProbeResult, 0, len(probes))

	for _, p := range probes {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		wg.Add(1)
		go func(p nodeapi.PortProbe) {
			defer wg.Done()
			res := probeOne(p)
			mu.Lock()
			out = append(out, res)
			mu.Unlock()
		}(p)
	}
	wg.Wait()
	return out
}

// probeOne binds and releases one port.
func probeOne(p nodeapi.PortProbe) nodeapi.PortProbeResult {
	res := nodeapi.PortProbeResult{Network: p.Network, Port: p.Port}
	addr := fmt.Sprintf(":%d", p.Port)
	if p.Network == "udp" {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			res.Err = err.Error()
			return res
		}
		_ = c.Close()
		res.Free = true
		return res
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		res.Err = err.Error()
		return res
	}
	_ = l.Close()
	res.Free = true
	return res
}
