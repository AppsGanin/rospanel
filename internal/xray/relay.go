package xray

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
// The rules sit after the block rules and before every egress lane, so a relayed
// connection goes to the partner whole, bar what the operator blocks for everyone —
// unless the subscription's own routing (model.ExtRouting) sends part of it elsewhere:
// each of its rules is one more rule on the same route and users, ahead of the one that
// takes the rest, and the partner itself may be dialled through WARP, Opera or a lane.
//
// Who is granted changes with the users, and the list is in the rule; the running
// rules are replaced through the API when only that changed (see relayRulesChanged).

// Relay is one external server this server carries traffic on to.
type Relay struct {
	ExtID   int64
	Lanes   []string // model.LaneVLESS and/or model.LaneReality
	Link    string
	Routing model.ExtRouting
}

// relayPlan is one relayed server as its rules need it: which connections are its
// (the route on the lane, for its users), its outbound and its routing.
type relayPlan struct {
	// tag names the plan's rules after the relay prefix: the external server's id.
	tag     string
	extID   int64
	inbound []string
	route   string
	users   []string
	outTag  string
	routing model.ExtRouting
}

// relayTargets is every target the relays' routing names, so the lanes, WARP and
// Opera they send traffic to are built even when nothing else routes to them.
func relayTargets(relays []Relay) map[string]bool {
	out := map[string]bool{}
	for _, r := range relays {
		for _, t := range r.Routing.Targets() {
			out[t] = true
		}
	}
	return out
}

// relayDialer is the outbound the relay server dials a partner through, "" for
// directly: the first member of WARP or of a lane (dialerProxy takes an outbound, not
// a balancer), or Opera's — which is HTTP and carries no UDP, so a partner reached over
// UDP is dialled directly instead.
func relayDialer(dial, protocol string, warpActive, operaOn bool, active map[string]bool) string {
	switch dial {
	case "":
		return ""
	case "warp":
		if warpActive {
			return warpMemberTag(0)
		}
	case "opera":
		if operaOn && !udpProtocol(protocol) {
			return "opera"
		}
	case model.ExtToPartner, "direct", "block":
	default:
		if active[dial] {
			return laneTagPrefix(dial) + "0"
		}
	}
	return ""
}

// udpProtocol reports whether an outbound protocol rides UDP.
func udpProtocol(protocol string) bool {
	return protocol == "hysteria2" || protocol == "hysteria" || protocol == "wireguard"
}

// withDialer sets an outbound's streamSettings.sockopt.dialerProxy.
func withDialer(stream any, tag string) any {
	if tag == "" {
		return stream
	}
	ss, _ := stream.(map[string]any)
	if ss == nil {
		ss = map[string]any{}
	}
	sock, _ := ss["sockopt"].(map[string]any)
	if sock == nil {
		sock = map[string]any{}
	}
	sock["dialerProxy"] = tag
	ss["sockopt"] = sock
	return ss
}

// relayRulePrefix starts the tags of the relay rules: their users may change in the
// running process without a restart.
const relayRulePrefix = "relay-"

// relayNobody stands in for an empty user list. A rule with none has no user
// condition at all, so it would take the route for everyone; no user's email is this.
const relayNobody = "~"

func relayOutboundTag(extID int64) string { return fmt.Sprintf("ext-%d", extID) }

// relayRouting builds the outbounds and the plans of opts.Relays' rules. vless and
// reality are the clients of the two lanes as generated, so a user off a lane is off
// its relays. dialer picks what a partner is dialled through (relayDialer).
func relayRouting(opts Options, vless, reality []VLESSClient, dialer func(dial, protocol string) string) ([]Outbound, []relayPlan) {
	relays := slices.Clone(opts.Relays)
	slices.SortFunc(relays, func(a, b Relay) int { return int(a.ExtID - b.ExtID) })
	var outs []Outbound
	var plans []relayPlan
	for _, r := range relays {
		route, ok := model.ExtRoute(r.ExtID)
		if !ok {
			continue
		}
		var tags []string
		var clients []VLESSClient
		for _, lane := range r.Lanes {
			switch lane {
			case model.LaneVLESS:
				tags, clients = append(tags, TagVLESS), append(clients, vless...)
			case model.LaneReality:
				tags, clients = append(tags, TagReality), append(clients, reality...)
			}
		}
		if len(tags) == 0 {
			continue
		}
		slices.Sort(tags)
		outTag := relayOutboundTag(r.ExtID)
		o, ok := linkOutbound(r.Link, outTag, opts.LegacyUDPHop)
		if !ok {
			continue // a link the panel read but cannot express is left out, not guessed at
		}
		protocol, _ := o["protocol"].(string)
		outs = append(outs, Outbound{Tag: outTag, Protocol: protocol, Settings: o["settings"],
			StreamSettings: withDialer(o["streamSettings"], dialer(r.Routing.Dial, protocol))})

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
			if !slices.Contains(users, c.Email) {
				users = append(users, c.Email)
			}
		}
		if len(users) == 0 {
			users = []string{relayNobody}
		}
		slices.Sort(users)
		plans = append(plans, relayPlan{
			tag: strconv.FormatInt(r.ExtID, 10), extID: r.ExtID, inbound: tags, route: strconv.Itoa(int(route)),
			users: users, outTag: outTag, routing: r.Routing,
		})
	}
	return outs, plans
}

// relayTarget is where one target of a relay's routing sends traffic: an outbound or
// a balancer. A target this config cannot build goes the way the server's own routing
// sends it (direct, or dropped under its StrictEgress).
type relayTarget func(target, partner string) (outbound, balancer string)

// relayRules compiles the plans: per relayed server its rules in order, domain and IP
// rules apart (a rule naming both would need both to match), and last the rule that
// takes the rest. Every rule carries the route and the users, and a tag of its own
// with the relay prefix, so a change of users alone is still applied live.
//
// IP rules and the rest: Xray resolves a name and matches IP rules again only when no
// rule matched the name (IPIfNonMatch), and a rest rule with no condition always does.
// So when the routing has IP rules, the rest takes every IP rather than everything: a
// connection by name matches nothing at first, is resolved, and meets the IP rules
// before the rest. One whose name does not resolve here then takes this server's own
// routing, as any unmatched connection does.
//
// Opera carries no UDP: a rule sent there takes TCP only, its UDP going on to the next
// rule, and a rest sent there drops UDP rather than let it leave from here.
func relayRules(plans []relayPlan, target relayTarget, expand func([]string, bool) []string) []RouteRule {
	var rules []RouteRule
	for _, p := range plans {
		base := func(tag string) RouteRule {
			return RouteRule{
				Type: "field", InboundTag: p.inbound, VlessRoute: p.route, User: p.users,
				RuleTag: relayRulePrefix + p.tag + tag,
			}
		}
		aim := func(r *RouteRule, t string) {
			r.OutboundTag, r.BalancerTag = target(t, p.outTag)
			if r.BalancerTag == operaBalancerTag {
				r.Network = "tcp"
			}
		}
		hasIP := false
		for i, x := range p.routing.Rules {
			if d := normDomains(expand(x.Domains, true)); len(d) > 0 {
				r := base(fmt.Sprintf("-%dd", i))
				r.Domain = d
				aim(&r, x.Target)
				rules = append(rules, r)
			}
			if ips := trimList(expand(x.IPs, false)); len(ips) > 0 {
				r := base(fmt.Sprintf("-%di", i))
				r.IP = ips
				aim(&r, x.Target)
				rules = append(rules, r)
				hasIP = true
			}
		}
		rest := base("")
		if hasIP {
			rest.IP = []string{"0.0.0.0/0", "::/0"}
		}
		aim(&rest, p.routing.RestTarget())
		rules = append(rules, rest)
		if rest.Network == "tcp" {
			udp := base("-udp")
			udp.Network, udp.OutboundTag = "udp", "block"
			udp.IP = rest.IP
			rules = append(rules, udp)
		}
	}
	return rules
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
