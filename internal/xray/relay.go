package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/AppsGanin/rospanel/internal/extsub"
	"github.com/AppsGanin/rospanel/internal/model"
)

// External servers relayed through this server (model.ExtSubscription.RelayLane).
//
// A user reaches one through a lane of ours they already hold — the TCP-TLS or the
// REALITY lane — with their own UUID, two bytes of it changed: the 7th and 8th, which
// Xray leaves out when it checks the UUID and hands to routing as vlessRoute. The
// route is the external server's (model.ExtRoute). Their email is their own, so the
// traffic is counted, capped and cut off exactly as on the lane itself.
//
// Each relayed server is one outbound built from its link and one rule: that route on
// that lane, for the users its groups grant it, goes out through it. A user it is not
// granted to who sets the route anyway matches nothing and leaves the ordinary way.
// The rule sits after the block rules and before every egress lane, so a relayed
// connection goes to the partner whole, bar what the operator blocks for everyone.
//
// Who is granted changes with the users, and the list is in the rule; the running
// rules are replaced through the API when only that changed (see relayRulesChanged).

// Relay is one external server this server carries traffic on to.
type Relay struct {
	ExtID int64
	Lane  string // model.LaneVLESS or model.LaneReality
	Link  string
}

// relayRulePrefix starts the tags of the relay rules: their users may change in the
// running process without a restart.
const relayRulePrefix = "relay-"

// relayNobody stands in for an empty user list. A rule with none has no user
// condition at all, so it would take the route for everyone; no user's email is this.
const relayNobody = "~"

func relayOutboundTag(extID int64) string { return fmt.Sprintf("ext-%d", extID) }

// relayRouting builds the outbounds and rules for opts.Relays. vless and reality are
// the clients of the two lanes as generated, so a user off a lane is off its relays.
func relayRouting(opts Options, vless, reality []VLESSClient) ([]Outbound, []RouteRule) {
	relays := slices.Clone(opts.Relays)
	slices.SortFunc(relays, func(a, b Relay) int { return int(a.ExtID - b.ExtID) })
	var outs []Outbound
	var rules []RouteRule
	for _, r := range relays {
		route, ok := model.ExtRoute(r.ExtID)
		if !ok {
			continue
		}
		var tag string
		var clients []VLESSClient
		switch r.Lane {
		case model.LaneVLESS:
			tag, clients = TagVLESS, vless
		case model.LaneReality:
			tag, clients = TagReality, reality
		default:
			continue
		}
		outTag := relayOutboundTag(r.ExtID)
		o, ok := extsub.XrayOutbound(r.Link, outTag)
		if !ok {
			continue // a link the panel read but cannot express is left out, not guessed at
		}
		protocol, _ := o["protocol"].(string)
		outs = append(outs, Outbound{Tag: outTag, Protocol: protocol, Settings: o["settings"], StreamSettings: o["streamSettings"]})

		var users []string
		for _, c := range clients {
			id, ok := model.UserIDOfEmail(c.Email)
			if !ok || !model.AccessOf(opts.Access, id).AllowsExt(r.ExtID) {
				continue
			}
			// A UUID that carries the route as it is cannot be told from its relayed
			// form: such a user's own traffic would go to the partner. Left out; the
			// subscription leaves them out too.
			if own, ok := model.UUIDRoute(c.ID); !ok || own == route {
				continue
			}
			users = append(users, c.Email)
		}
		if len(users) == 0 {
			users = []string{relayNobody}
		}
		slices.Sort(users)
		rules = append(rules, RouteRule{
			Type:        "field",
			InboundTag:  []string{tag},
			VlessRoute:  strconv.Itoa(int(route)),
			User:        users,
			OutboundTag: outTag,
			RuleTag:     relayRulePrefix + strconv.FormatInt(r.ExtID, 10),
		})
	}
	return outs, rules
}

// isRelayRule reports whether a rule tag is a relay rule's.
func isRelayRule(tag string) bool { return strings.HasPrefix(tag, relayRulePrefix) }

// Changing who a relay rule lets through, in the running process.

// ruleList is a config's routing rules as the routing API is handed them.
type ruleList = []map[string]json.RawMessage

// configRules reads the routing rules out of a config file.
func configRules(cfg []byte) (ruleList, error) {
	var doc struct {
		Routing *struct {
			Rules ruleList `json:"rules"`
		} `json:"routing"`
	}
	if err := json.Unmarshal(cfg, &doc); err != nil {
		return nil, err
	}
	if doc.Routing == nil {
		return nil, nil
	}
	return doc.Routing.Rules, nil
}

// relayUsersDiffer compares two rule lists. ok is false when they differ in anything
// but the users of relay rules; changed reports that those users differ.
func relayUsersDiffer(cur, next ruleList) (changed, ok bool) {
	if len(cur) != len(next) {
		return false, false
	}
	for i := range cur {
		a, aUsers, err := ruleWithoutRelayUsers(cur[i])
		if err != nil {
			return false, false
		}
		b, bUsers, err := ruleWithoutRelayUsers(next[i])
		if err != nil || !bytes.Equal(a, b) {
			return false, false
		}
		if !bytes.Equal(aUsers, bUsers) {
			changed = true
		}
	}
	return changed, true
}

// ruleWithoutRelayUsers is a rule, compacted, with a relay rule's users set apart.
// Marshalling compacts every value, so a rule read from an indented file and the same
// rule marshalled in one line compare equal.
func ruleWithoutRelayUsers(r map[string]json.RawMessage) (rest, users []byte, err error) {
	var tag string
	if raw, ok := r["ruleTag"]; ok {
		_ = json.Unmarshal(raw, &tag)
	}
	if !isRelayRule(tag) {
		rest, err = json.Marshal(r)
		return rest, nil, err
	}
	c := make(map[string]json.RawMessage, len(r))
	for k, v := range r {
		if k != "user" {
			c[k] = v
		}
	}
	if rest, err = json.Marshal(c); err != nil {
		return nil, nil, err
	}
	users, err = json.Marshal(r["user"])
	return rest, users, err
}

// relayRulesChanged compares the routing of two config files the way relayUsersDiffer
// does, and returns the new rules when only relay users changed.
func relayRulesChanged(cur, next []byte) (ruleList, bool, bool) {
	a, err := configRules(cur)
	if err != nil {
		return nil, false, false
	}
	b, err := configRules(next)
	if err != nil {
		return nil, false, false
	}
	changed, ok := relayUsersDiffer(a, b)
	if !ok || !changed {
		return nil, changed, ok
	}
	return b, true, true
}

// stripRelayUsers drops the users of relay rules from a decoded config, so two configs
// that differ in them alone compare equal (planUserChanges). The caller then applies
// them itself (relayRulesChanged).
func stripRelayUsers(cfg map[string]any) {
	routing, _ := cfg["routing"].(map[string]any)
	rules, _ := routing["rules"].([]any)
	for _, raw := range rules {
		r, _ := raw.(map[string]any)
		if tag, _ := r["ruleTag"].(string); isRelayRule(tag) {
			delete(r, "user")
		}
	}
}

// runningRulesLocked is the rules process p routes by now: as the panel last replaced
// them, or as it started. Caller holds runMu.
func runningRulesLocked(p *proc) (ruleList, error) {
	if p.hysteria != nil && p.hysteria.routable {
		return p.hysteria.rules, nil
	}
	return configRules(p.cfg)
}

// replaceOwnRulesLocked puts rules in place of the running process's own, through the
// routing API and without a gap (replaceRulesLocked); the Hysteria2 cut-off rules stay
// in front of them. Caller holds runMu.
func (s *Supervisor) replaceOwnRulesLocked(apiAddr string, rules ruleList) (err error) {
	if s.bin == "" {
		return errors.New("xray binary unavailable")
	}
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	if p == nil {
		return errors.New("xray is not running")
	}
	if p.hysteriaLost {
		return errors.New("an earlier change to the running routing failed part way")
	}
	st := p.hysteria
	if st == nil {
		if st, err = readHysteriaLive(p.cfg); err != nil {
			return err
		}
		p.hysteria = st
	}
	if !st.routable {
		return errors.New("the running routing cannot be replaced rule by rule")
	}
	prev := st.rules
	st.rules = rules
	if err := s.replaceRulesLocked(apiAddr, p, st); err != nil {
		// Part of the way, maybe: what runs is no config's. Marked, so the next Apply
		// restarts rather than finding it current.
		st.rules = prev
		p.hysteriaLost = true
		s.mu.Lock()
		if s.cur == p {
			s.appliedCfg = nil
		}
		s.mu.Unlock()
		return err
	}
	return nil
}

// SyncRelayRules brings the running process's relay rules to the users cfg gives them,
// with no restart. The master calls it after its live user sync. An error means the
// running routing differs from cfg in more than that, or the change failed: the full
// reload that follows puts it right.
//
// It runs on every user sync, so a panel that relays nothing pays for no more than a
// look: only the routing is marshalled, and a running config with no relay rule in it
// is not read at all.
func (s *Supervisor) SyncRelayRules(apiAddr string, cfg *Config) error {
	if cfg.Routing == nil {
		return nil
	}
	wantRelay := false
	for _, r := range cfg.Routing.Rules {
		if isRelayRule(r.RuleTag) {
			wantRelay = true
			break
		}
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	if p == nil {
		return errors.New("xray is not running")
	}
	replaced := p.hysteria != nil && p.hysteria.routable
	if !wantRelay && !replaced && !bytes.Contains(p.cfg, []byte(`"`+relayRulePrefix)) {
		return nil // no relay rule running, none wanted
	}
	data, err := json.Marshal(map[string]any{"routing": cfg.Routing})
	if err != nil {
		return err
	}
	next, err := configRules(data)
	if err != nil {
		return err
	}
	cur, err := runningRulesLocked(p)
	if err != nil {
		return err
	}
	changed, ok := relayUsersDiffer(cur, next)
	switch {
	case !ok:
		return errors.New("the running routing differs from the config in more than the relay users")
	case !changed:
		return nil
	}
	return s.replaceOwnRulesLocked(apiAddr, next)
}
