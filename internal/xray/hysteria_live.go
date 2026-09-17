package xray

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Hysteria2 users are changed in the running Xray without touching anyone else's
// connection.
//
// They used to be changed by rebuilding the whole inbound — removed and added back with
// the new users — because the CLI cannot add a user to a QUIC inbound. Removing the
// inbound closes every QUIC connection on it without telling the clients, and each one
// sat out its idle timeout before dialling again: everyone on the lane lost the tunnel
// for ten to thirty seconds whenever one user was added or removed, on the master and
// on every node.
//
// Users now go in and out through the API (see xrayAPI). That alone does not end a
// removed user's session: a Hysteria2 client proves who it is once per QUIC connection
// and opens every stream inside it, so removing them only refuses the next connection,
// and a client that keeps its connection busy keeps it for good. Their streams are
// ended by routing instead — a rule ahead of every other sends whatever the inbound
// hands over for a removed user to the block outbound. Nothing tells when such a
// connection has closed, so the list lasts as long as the process, less the users who
// come back.
//
// Xray's routing API can only append rules or replace them all, and replacing them all
// leaves the router matching against a half-built list for as long as the rebuild takes
// — traffic meant for WARP, a proxy lane or a block list goes out directly meanwhile. So
// the new list is appended whole behind the old one, where it decides nothing, and the
// old rules are then removed from the last to the first. At every step a connection's
// first matching rule is the one it had or that rule's copy; only the cut-off can take
// effect early. That needs every rule to carry a tag of its own, which the generator
// gives them (see compileRouting).

// hysteriaCutMax bounds how many removed users one inbound's cut-off rule names: the
// rule is checked against every stream that inbound opens, name by name. Past it the
// inbound is rebuilt instead, which ends every connection on it and so leaves nobody to
// cut off.
const hysteriaCutMax = 2000

// liveRulePrefix starts the tags of the routing rules put in place here. The generated
// rules must not use it (see readHysteriaLive).
const liveRulePrefix = "live"

// routingTimeout bounds an `xray api adrules` / `rmrules` call. The CLI's own deadline
// (-timeout) covers the connection and every call in it, and defaults to three seconds;
// building the rules of a config with large domain lists takes a good part of that on
// a small server.
const (
	routingCLITimeout = 10 * time.Second
	routingTimeout    = routingCLITimeout + 5*time.Second
)

// hysteriaInbound is one QUIC inbound as it should be: its users, and the whole inbound
// for when it has to be rebuilt.
type hysteriaInbound struct {
	tag   string
	users []HysteriaClient
	whole any
}

// hysteriaLive is what one running process's QUIC inbounds and routing hold, as the panel
// has changed them since the process read its config.
type hysteriaLive struct {
	users map[string]map[string]string   // inbound tag → email → auth
	cut   map[string]map[string]struct{} // inbound tag → removed users whose streams are blocked

	// routable: the process can take cut-off rules — its API serves routing, it has a
	// block outbound, and every routing rule of its own carries a tag of its own.
	routable bool
	block    string
	rules    []map[string]json.RawMessage // the process's own routing rules, in order
	gen      int                          // how many times the rules have been replaced
	ruleTags []string                     // tags of the rules routing everything now, in order
	cutTags  []string                     // tags of the cut-off rules in place now
}

// readHysteriaLive reads the QUIC inbounds and routing a process started with.
func readHysteriaLive(cfg []byte) (*hysteriaLive, error) {
	var doc struct {
		API *struct {
			Services []string `json:"services"`
		} `json:"api"`
		Inbounds []struct {
			Tag      string          `json:"tag"`
			Protocol string          `json:"protocol"`
			Settings json.RawMessage `json:"settings"`
		} `json:"inbounds"`
		Outbounds []struct {
			Tag      string `json:"tag"`
			Protocol string `json:"protocol"`
		} `json:"outbounds"`
		Routing *struct {
			Rules []map[string]json.RawMessage `json:"rules"`
		} `json:"routing"`
	}
	if len(cfg) == 0 {
		return nil, errors.New("the config the process started with is unknown")
	}
	if err := json.Unmarshal(cfg, &doc); err != nil {
		return nil, fmt.Errorf("read the running config: %w", err)
	}
	st := &hysteriaLive{users: map[string]map[string]string{}, cut: map[string]map[string]struct{}{}}
	for _, in := range doc.Inbounds {
		if in.Protocol != "hysteria" || in.Tag == "" {
			continue
		}
		var settings struct {
			Users []HysteriaClient `json:"users"`
		}
		if len(in.Settings) > 0 {
			if err := json.Unmarshal(in.Settings, &settings); err != nil {
				return nil, fmt.Errorf("read the users of %s: %w", in.Tag, err)
			}
		}
		users := make(map[string]string, len(settings.Users))
		for _, u := range settings.Users {
			if _, dup := users[u.Email]; dup || u.Email == "" {
				return nil, fmt.Errorf("%s holds a user with no email or a shared one (%q)", in.Tag, u.Email)
			}
			users[u.Email] = u.Auth
		}
		st.users[in.Tag] = users
	}

	if doc.API == nil || !slices.Contains(doc.API.Services, "RoutingService") || doc.Routing == nil {
		return st, nil
	}
	for _, out := range doc.Outbounds {
		if out.Protocol == "blackhole" && out.Tag != "" {
			st.block = out.Tag
			break
		}
	}
	if st.block == "" {
		return st, nil
	}
	tags := make([]string, 0, len(doc.Routing.Rules))
	for _, r := range doc.Routing.Rules {
		var tag string
		if raw, ok := r["ruleTag"]; !ok || json.Unmarshal(raw, &tag) != nil ||
			tag == "" || slices.Contains(tags, tag) || strings.HasPrefix(tag, liveRulePrefix) {
			return st, nil
		}
		tags = append(tags, tag)
	}
	st.rules, st.ruleTags, st.routable = doc.Routing.Rules, tags, true
	return st, nil
}

// SyncHysteria brings the running Xray's Hysteria2 inbounds to the users the generated
// config gives them, without dropping anyone who stays (see the top of this file).
func (s *Supervisor) SyncHysteria(apiAddr string, inbounds []Inbound) error {
	want := make([]hysteriaInbound, 0, len(inbounds))
	for _, in := range inbounds {
		settings, ok := in.Settings.(HysteriaInboundSettings)
		if !ok {
			return fmt.Errorf("inbound %s carries no hysteria settings", in.Tag)
		}
		want = append(want, hysteriaInbound{tag: in.Tag, users: settings.Users, whole: in})
	}
	s.runMu.Lock()
	defer s.runMu.Unlock()
	return s.syncHysteriaLocked(apiAddr, want)
}

// syncHysteriaLocked brings each QUIC inbound named in want to its users, and leaves
// every other inbound alone. Caller holds runMu.
//
// A user who joins is added. A user who leaves is removed and their open connections cut
// off. When that cannot be done — the user's auth changed, so their old connection
// carries the same email as the new; the cut-off list would pass hysteriaCutMax; or the
// process cannot take cut-off rules — the inbound is rebuilt, which ends every
// connection on it.
//
// A failure leaves the process holding something between the two, which no config
// describes: it is marked so, and the next Apply restarts it rather than finding it
// already current.
func (s *Supervisor) syncHysteriaLocked(apiAddr string, want []hysteriaInbound) (err error) {
	if len(want) == 0 {
		return nil
	}
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
		return errors.New("an earlier change to the running hysteria inbounds failed part way")
	}
	defer func() {
		if err != nil {
			p.hysteriaLost = true
			s.mu.Lock()
			if s.cur == p {
				s.appliedCfg = nil
			}
			s.mu.Unlock()
		}
	}()
	st := p.hysteria
	if st == nil {
		if st, err = readHysteriaLive(p.cfg); err != nil {
			return err
		}
		p.hysteria = st
	}

	api := newXrayAPI(apiAddr, s.waitFn)
	defer api.close()
	cutChanged := false
	for _, in := range want {
		have, running := st.users[in.tag]
		if !running {
			// An inbound the config gained after this process started: the restart
			// that brings it up brings its users.
			slog.Warn("xray: a hysteria inbound to update is not in the running process", "tag", in.tag)
			continue
		}
		next := make(map[string]string, len(in.users))
		all := make(map[string]string, len(in.users)) // next and the keyless: what a rebuild puts in
		keyless := 0
		for _, u := range in.users {
			if _, dup := all[u.Email]; dup || u.Email == "" {
				return fmt.Errorf("%s: a user with no email or a shared one (%q)", in.tag, u.Email)
			}
			all[u.Email] = u.Auth
			if u.Auth == "" {
				// An empty auth is a key every client holds. It is what a password that
				// failed to decrypt reads as; such a user is kept out, not let in.
				keyless++
				continue
			}
			next[u.Email] = u.Auth
		}
		if keyless > 0 {
			slog.Warn("xray: hysteria users with an empty auth are kept off the inbound", "tag", in.tag, "users", keyless)
		}
		var gone, joined []string
		rekeyed := false
		for email, auth := range have {
			switch now, kept := next[email]; {
			case !kept:
				gone = append(gone, email)
			case now != auth:
				rekeyed = true
			}
		}
		for email := range next {
			if _, had := have[email]; !had {
				joined = append(joined, email)
			}
		}
		if len(gone) == 0 && len(joined) == 0 && !rekeyed {
			continue
		}
		cut := st.cut[in.tag]
		if rekeyed || (len(gone) > 0 && !st.routable) || len(cut)+len(gone) > hysteriaCutMax {
			if err := s.replaceInbound(apiAddr, in.tag, in.whole); err != nil {
				return err
			}
			// The rebuilt inbound holds everyone given, keyless users too; the next
			// change finds those gone and takes them off.
			st.users[in.tag] = all
			if len(cut) > 0 {
				delete(st.cut, in.tag)
				cutChanged = true
			}
			continue
		}
		slices.Sort(gone)
		slices.Sort(joined)
		for _, email := range gone {
			if err := api.removeUser(in.tag, email); err != nil {
				return err
			}
			delete(have, email)
		}
		for _, email := range joined {
			if err := api.addHysteriaUser(in.tag, email, next[email]); err != nil {
				return err
			}
			have[email] = next[email]
		}
		for _, email := range joined {
			if _, was := cut[email]; was {
				delete(cut, email)
				cutChanged = true
			}
		}
		if len(gone) > 0 {
			if cut == nil {
				cut = make(map[string]struct{}, len(gone))
				st.cut[in.tag] = cut
			}
			for _, email := range gone {
				cut[email] = struct{}{}
			}
			cutChanged = true
		}
		if len(cut) == 0 {
			delete(st.cut, in.tag)
		}
	}
	if cutChanged && st.routable {
		return s.replaceRulesLocked(apiAddr, p, st)
	}
	return nil
}

// replaceRulesLocked puts the cut-off rules for st.cut in front of process p's own rules:
// the new list is appended behind the old, and the old is removed from its last rule to
// its first, then the cut-off rules it had. Caller holds runMu.
func (s *Supervisor) replaceRulesLocked(apiAddr string, p *proc, st *hysteriaLive) error {
	gen := st.gen + 1
	var rules []map[string]any
	var cutTags, ruleTags []string
	inbounds := slices.Sorted(maps.Keys(st.cut))
	for i, tag := range inbounds {
		ruleTag := fmt.Sprintf("%s%d-cut-%d", liveRulePrefix, gen, i)
		rules = append(rules, map[string]any{
			"type":        "field",
			"ruleTag":     ruleTag,
			"inboundTag":  []string{tag},
			"user":        slices.Sorted(maps.Keys(st.cut[tag])),
			"outboundTag": st.block,
		})
		cutTags = append(cutTags, ruleTag)
	}
	for i, r := range st.rules {
		ruleTag := fmt.Sprintf("%s%d-%d", liveRulePrefix, gen, i)
		c := make(map[string]any, len(r))
		for k, v := range r {
			c[k] = v
		}
		c["ruleTag"] = ruleTag
		rules = append(rules, c)
		ruleTags = append(ruleTags, ruleTag)
	}
	timeout := "-timeout=" + strconv.Itoa(int(routingCLITimeout/time.Second))
	if _, err := s.runXrayFile(routingTimeout, "xray-rules-*.json", map[string]any{"routing": map[string]any{"rules": rules}},
		"api", "adrules", "-append", timeout, "--server="+apiAddr); err != nil {
		return fmt.Errorf("api adrules: %w", err)
	}
	old := make([]string, 0, len(st.ruleTags)+len(st.cutTags))
	for i := len(st.ruleTags) - 1; i >= 0; i-- {
		old = append(old, st.ruleTags[i])
	}
	old = append(old, st.cutTags...)
	if len(old) > 0 {
		if _, err := s.runXrayAPI(routingTimeout, append([]string{"api", "rmrules", timeout, "--server=" + apiAddr}, old...)...); err != nil {
			return fmt.Errorf("api rmrules: %w", err)
		}
	}
	st.gen, st.ruleTags, st.cutTags = gen, ruleTags, cutTags
	if len(st.cut) == 0 {
		p.cutOff.Store(nil)
	} else {
		view := &cutOffView{block: st.block, users: map[string]struct{}{}}
		for tag, users := range st.cut {
			for email := range users {
				view.users[tag+"\x00"+email] = struct{}{}
			}
		}
		p.cutOff.Store(view)
	}
	return nil
}

// cutOffView is who a process's routing cuts off, as the access-log tap reads it.
type cutOffView struct {
	block string
	users map[string]struct{} // inbound tag, NUL, email
}

// cuts reports whether an access-log line is a stream the routing cut off:
//
//	... accepted tcp:host:443 [<inbound> -> <block>] email: <user>
//
// for a user cut off on that inbound. "->" is a stream a rule sent there; ">>" one that
// matched no rule and went to the default outbound.
func (v *cutOffView) cuts(line string) bool {
	const marker = "] email: "
	e := strings.LastIndex(line, marker)
	if e < 0 {
		return false
	}
	open := strings.LastIndexByte(line[:e], '[')
	if open < 0 {
		return false
	}
	in, out, ok := strings.Cut(line[open+1:e], " -> ")
	if !ok || out != v.block {
		return false
	}
	_, cut := v.users[in+"\x00"+strings.TrimSpace(line[e+len(marker):])]
	return cut
}
