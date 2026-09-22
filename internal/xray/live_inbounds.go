package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Adding, editing or removing a custom inbound restarted Xray, and every user on every
// lane lost their connection for the ~7 seconds that takes. The running Xray can add
// and remove inbounds through the API just as it adds users, so a config that differs
// from the running one only in its custom inbounds (and users) is applied that way:
// the inbounds that went away or changed are taken down, the new and changed ones are
// brought up whole, and nobody else's connection is touched.
//
// Only VLESS, Trojan and Shadowsocks inbounds go this way. Hysteria2 and WireGuard
// inbounds carry state the supervisor keeps per process (users, cut-off rules, peers,
// the TURN relay), and a change to one of them still restarts.

// customTagPrefix marks the operator's own inbounds (model.Inbound.Tag).
const customTagPrefix = "custom-"

// inboundOps is what the running Xray needs to go from one set of custom inbounds to
// another.
type inboundOps struct {
	remove []string         // tags to take down: removed, or rebuilt below
	add    []map[string]any // inbounds to bring up, whole: new, or rebuilt
}

func (o inboundOps) empty() bool { return len(o.remove) == 0 && len(o.add) == 0 }

// liveInbound reports whether an inbound can be added or removed through the API.
func liveInbound(in map[string]any) bool {
	switch in["protocol"] {
	case "vless", "trojan", "shadowsocks":
		return true
	}
	return false
}

// planInboundChanges compares the running config with a new one. ok is false unless
// custom inbounds changed and everything else differs, at most, in users — those go
// through the returned user changes, as planUserChanges would plan them.
func planInboundChanges(cur, next []byte) (ops inboundOps, changes []userChange, ok bool) {
	curCfg, err := decodeRaw(cur)
	if err != nil {
		return inboundOps{}, nil, false
	}
	nextCfg, err := decodeRaw(next)
	if err != nil {
		return inboundOps{}, nil, false
	}
	curByTag := map[string]map[string]any{}
	curIn, _ := curCfg["inbounds"].([]any)
	for _, raw := range curIn {
		in, tag := asInbound(raw)
		if tag == "" || curByTag[tag] != nil {
			return inboundOps{}, nil, false
		}
		curByTag[tag] = in
	}

	// The running config rebuilt in the new one's order: every inbound the new config
	// keeps as it was, with the users it runs with now; every new or changed custom
	// inbound as the new config has it. What is left between that and the new config
	// has to be users alone.
	nextIn, _ := nextCfg["inbounds"].([]any)
	mid := make([]any, 0, len(nextIn))
	seen := map[string]bool{}
	for _, raw := range nextIn {
		in, tag := asInbound(raw)
		if tag == "" || seen[tag] {
			return inboundOps{}, nil, false
		}
		seen[tag] = true
		old, had := curByTag[tag]
		switch {
		case !strings.HasPrefix(tag, customTagPrefix):
			if !had {
				return inboundOps{}, nil, false
			}
			mid = append(mid, old)
		case had && sameShape(old, in):
			mid = append(mid, old)
		default:
			if !liveInbound(in) || had && !liveInbound(old) {
				return inboundOps{}, nil, false
			}
			if had {
				ops.remove = append(ops.remove, tag)
			}
			ops.add = append(ops.add, in)
			mid = append(mid, in)
		}
	}
	for _, raw := range curIn {
		in, tag := asInbound(raw)
		if seen[tag] {
			continue
		}
		if !strings.HasPrefix(tag, customTagPrefix) || !liveInbound(in) {
			return inboundOps{}, nil, false
		}
		ops.remove = append(ops.remove, tag)
	}
	if ops.empty() {
		return inboundOps{}, nil, false
	}
	curCfg["inbounds"] = mid
	midJSON, err := json.Marshal(curCfg)
	if err != nil {
		return inboundOps{}, nil, false
	}
	changes, ok = planUserChanges(midJSON, next)
	if !ok {
		return inboundOps{}, nil, false
	}
	// Relay users changed as well: this path does not carry them, a restart does.
	if _, changed, ok := relayRulesChanged(cur, next); !ok || changed {
		return inboundOps{}, nil, false
	}
	return ops, changes, true
}

func asInbound(raw any) (map[string]any, string) {
	in, isMap := raw.(map[string]any)
	if !isMap {
		return nil, ""
	}
	tag, _ := in["tag"].(string)
	return in, tag
}

// sameShape reports whether two inbounds differ in nothing but their users.
func sameShape(a, b map[string]any) bool {
	strip := func(in map[string]any) ([]byte, bool) {
		raw, err := json.Marshal(in)
		if err != nil {
			return nil, false
		}
		c, err := decodeRaw(raw)
		if err != nil {
			return nil, false
		}
		if _, ok := takeUsers(c); !ok {
			return nil, false
		}
		out, err := json.Marshal(c)
		return out, err == nil
	}
	x, okA := strip(a)
	y, okB := strip(b)
	return okA && okB && bytes.Equal(x, y)
}

// applyInboundOps takes the old inbounds down — closing the connections still open on
// them, which a removed inbound's listener going away does not end — and brings the new
// ones up. Caller holds runMu.
func (s *Supervisor) applyInboundOps(apiAddr string, ops inboundOps) error {
	for _, tag := range ops.remove {
		if _, err := s.runXrayAPI(statsTimeout, "api", "rmi", "--server="+apiAddr, tag); err != nil {
			return fmt.Errorf("api rmi tag=%s: %w", tag, err)
		}
	}
	s.cutInboundConns(ops.remove)
	if len(ops.add) == 0 {
		return nil
	}
	adds := make([]any, len(ops.add))
	for i, in := range ops.add {
		adds[i] = in
	}
	out, err := s.runXrayFile(statsTimeout, "xray-adi-*.json", map[string]any{"inbounds": adds}, "api", "adi", "--server="+apiAddr)
	if err != nil {
		return fmt.Errorf("api adi: %w", err)
	}
	// Exit 0 whatever happened: the output is the only evidence (see replaceInbound).
	if bytes.Contains(out, []byte("failed to")) {
		return fmt.Errorf("api adi: %s", bytes.TrimSpace(out))
	}
	return nil
}

// tryLiveInbounds puts data into effect through the API when it differs from the config
// on disk only in custom inbounds and users. live reports that it did; err is why an
// attempt failed part way, which leaves the running Xray for a restart to put right.
// Caller holds runMu.
func (s *Supervisor) tryLiveInbounds(apiAddr string, data []byte) (live bool, err error) {
	cur, err := os.ReadFile(s.configPath)
	if err != nil {
		return false, nil
	}
	ops, changes, ok := planInboundChanges(cur, data)
	if !ok {
		return false, nil
	}
	if err := s.applyInboundOps(apiAddr, ops); err != nil {
		return false, err
	}
	if err := s.applyUserChanges(apiAddr, changes); err != nil {
		return false, err
	}
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	if p != nil {
		p.conns.setPorts(tcpUserPorts(data))
	}
	return true, nil
}
