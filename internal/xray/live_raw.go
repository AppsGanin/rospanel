package xray

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// A node receives its config as finished JSON and used to apply every change with a
// restart — so each user who expired, ran out of traffic or went over their device
// limit dropped every connection on every node. Users are the one part of a config
// the running Xray can change in place, the way the master's live user sync does, so
// a pushed config that differs from the running one in users alone is applied that
// way. Anything else still restarts.

// RawApply says how ApplyRawLive put a config into effect.
type RawApply int

const (
	RawUnchanged RawApply = iota // the config was already running
	RawLive                      // users changed in the running process, no restart
	RawRestarted                 // written, validated and restarted
)

// liveUserKey names, per protocol, the settings field that carries an inbound's users.
// A protocol missing here has no users to change: every difference in it is structural.
var liveUserKey = map[string]string{
	"vless":       "clients",
	"trojan":      "clients",
	"shadowsocks": "users",
	"hysteria":    "users",
	"wireguard":   "peers",
}

// liveUserChangesMax bounds how many user entries one live update adds and removes.
// The API takes ~60µs an entry, so a change this size is a few seconds on a small
// server; past it — a group granting a server to half the fleet — a restart is the
// quicker way to the same config and keeps the CLI well inside its timeout.
const liveUserChangesMax = 5000

// userChange is what one inbound needs to go from the running config to the pushed
// one. Removals are emails; additions are the pushed client objects, verbatim.
type userChange struct {
	tag       string
	remove    []string
	add       []any
	hysteria  bool           // a QUIC inbound: its users go through syncHysteriaLocked
	wireGuard bool           // a WireGuard inbound: its users go through syncWireGuardLocked
	inbound   map[string]any // the pushed inbound, whole
	key       string         // its users field
}

// planUserChanges compares the running config with a pushed one. ok is false when they
// differ in anything but inbound users, or when a user cannot be told apart from
// another — only a restart applies those.
func planUserChanges(cur, next []byte) (changes []userChange, ok bool) {
	curCfg, err := decodeRaw(cur)
	if err != nil {
		return nil, false
	}
	nextCfg, err := decodeRaw(next)
	if err != nil {
		return nil, false
	}
	curIn, _ := curCfg["inbounds"].([]any)
	nextIn, _ := nextCfg["inbounds"].([]any)
	if len(curIn) != len(nextIn) {
		return nil, false
	}
	curUsers := make([][]any, len(curIn))
	nextUsers := make([][]any, len(nextIn))
	for i := range curIn {
		if curUsers[i], ok = takeUsers(curIn[i]); !ok {
			return nil, false
		}
		if nextUsers[i], ok = takeUsers(nextIn[i]); !ok {
			return nil, false
		}
	}
	// With the users lifted out, the two configs must be the same document.
	a, errA := json.Marshal(curCfg)
	b, errB := json.Marshal(nextCfg)
	if errA != nil || errB != nil || !bytes.Equal(a, b) {
		return nil, false
	}

	total := 0
	for i := range nextIn {
		inbound, _ := nextIn[i].(map[string]any)
		protocol, _ := inbound["protocol"].(string)
		key := liveUserKey[protocol]
		if key == "" {
			continue
		}
		c, ok := diffUsers(curUsers[i], nextUsers[i])
		if !ok {
			return nil, false
		}
		if len(c.remove) == 0 && len(c.add) == 0 {
			continue
		}
		// Shadowsocks-2022 with no users is a single-user server whose key is the one
		// in every link it ever issued. The generator never emits that, and a live
		// update must not be the way it reaches the file a restart would load.
		if protocol == "shadowsocks" && len(nextUsers[i]) == 0 {
			return nil, false
		}
		tag, _ := inbound["tag"].(string)
		if tag == "" {
			return nil, false
		}
		// Put the users back into the pushed inbound: an addition is parsed as a full
		// inbound, and a QUIC inbound that has to be rebuilt is re-added whole.
		settings, isMap := inbound["settings"].(map[string]any)
		if !isMap {
			return nil, false
		}
		settings[key] = nextUsers[i]
		c.tag, c.key, c.inbound = tag, key, inbound
		c.hysteria = protocol == "hysteria"
		c.wireGuard = protocol == "wireguard"
		total += len(c.remove) + len(c.add)
		changes = append(changes, c)
	}
	if total > liveUserChangesMax {
		return nil, false
	}
	return changes, true
}

func decodeRaw(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// takeUsers removes an inbound's users from it and returns them. ok is false for an
// inbound whose users field is not a list.
func takeUsers(raw any) ([]any, bool) {
	inbound, isMap := raw.(map[string]any)
	if !isMap {
		return nil, false
	}
	protocol, _ := inbound["protocol"].(string)
	key := liveUserKey[protocol]
	if key == "" {
		return nil, true
	}
	settings, isMap := inbound["settings"].(map[string]any)
	if !isMap {
		return nil, true
	}
	v, present := settings[key]
	delete(settings, key)
	if !present || v == nil {
		return nil, true
	}
	users, isList := v.([]any)
	return users, isList
}

// diffUsers matches users by email. A user present on both sides with any field changed
// (a rotated key) is removed and added again. ok is false when a user has no email or
// shares one, since removal goes by email.
func diffUsers(cur, next []any) (userChange, bool) {
	index := func(users []any) (map[string]string, []string, bool) {
		byEmail := make(map[string]string, len(users))
		order := make([]string, 0, len(users))
		for _, u := range users {
			m, isMap := u.(map[string]any)
			if !isMap {
				return nil, nil, false
			}
			email, _ := m["email"].(string)
			if email == "" {
				return nil, nil, false
			}
			if _, dup := byEmail[email]; dup {
				return nil, nil, false
			}
			b, err := json.Marshal(m)
			if err != nil {
				return nil, nil, false
			}
			byEmail[email] = string(b)
			order = append(order, email)
		}
		return byEmail, order, true
	}
	curBy, curOrder, ok := index(cur)
	if !ok {
		return userChange{}, false
	}
	nextBy, nextOrder, ok := index(next)
	if !ok {
		return userChange{}, false
	}
	var c userChange
	for _, email := range curOrder {
		if nb, kept := nextBy[email]; !kept || nb != curBy[email] {
			c.remove = append(c.remove, email)
		}
	}
	for i, email := range nextOrder {
		if cb, had := curBy[email]; !had || cb != nextBy[email] {
			c.add = append(c.add, next[i])
		}
	}
	return c, true
}

// ApplyRawLive applies a node's pushed config with as little disruption as it allows.
//
// A config identical to the one on disk is left alone: the node's desired state
// carries more than the Xray config — certificates, hop ranges, the connection guard,
// per-user speed caps — and any change to it re-runs the apply, so routing an unchanged
// config through ApplyRaw would restart Xray over one user's speed limit. That
// shortcut, like the live one, needs Xray running: a stopped process with a matching
// config still has to be started.
//
// A config that differs in users alone goes to the running Xray through the API at
// apiAddr — removals first, so a revoked user is out before anyone is let in — and is
// recorded on disk for the next start. Anything else, or any failure on the way, goes
// through ApplyRaw: validated, and Xray restarted with it, which is always the right
// end state.
func (s *Supervisor) ApplyRawLive(apiAddr string, data []byte) (RawApply, error) {
	attempted := false
	if s.Running() && s.bin != "" {
		live, done, err := s.tryLiveUsers(apiAddr, data)
		if done {
			return live, nil
		}
		if err != nil {
			attempted = true
			slog.Warn("xray: live user update failed, restarting with the new config", "err", err)
		}
	}
	if err := s.ApplyRaw(data); err != nil {
		// A live update that got part of the way has already changed who is on the
		// running Xray, and the config that would have finished the job was refused. Go
		// back to the config on disk, so what runs is at least what the file says.
		if attempted {
			if rerr := s.Restart(); rerr != nil {
				slog.Error("xray: could not restore the running config after a failed update", "err", rerr)
			}
		}
		return RawRestarted, err
	}
	return RawRestarted, nil
}

// tryLiveUsers applies data without a restart when it can. done reports that it did
// (or that nothing needed doing); err is why a live update that was attempted failed.
func (s *Supervisor) tryLiveUsers(apiAddr string, data []byte) (RawApply, bool, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	cur, err := os.ReadFile(s.configPath)
	if err != nil {
		return RawRestarted, false, nil
	}
	if bytes.Equal(cur, data) {
		return RawUnchanged, true, nil
	}
	changes, ok := planUserChanges(cur, data)
	if !ok {
		return RawRestarted, false, nil
	}
	if err := s.applyUserChanges(apiAddr, changes); err != nil {
		return RawRestarted, false, err
	}
	if err := writeFileAtomic(s.configPath, data); err != nil {
		return RawRestarted, false, err
	}
	return RawLive, true, nil
}

// applyUserChanges runs a plan against the running Xray. Caller holds runMu.
func (s *Supervisor) applyUserChanges(apiAddr string, changes []userChange) error {
	for _, c := range changes {
		if c.hysteria || c.wireGuard || len(c.remove) == 0 {
			continue
		}
		// A user already gone is the state asked for, so the count is not checked.
		if _, err := s.runXrayAPI(statsTimeout, append([]string{"api", "rmu", "--server=" + apiAddr, "-tag=" + c.tag}, c.remove...)...); err != nil {
			return fmt.Errorf("api rmu tag=%s: %w", c.tag, err)
		}
	}

	var stubs []any
	want := 0
	for _, c := range changes {
		if c.hysteria || c.wireGuard || len(c.add) == 0 {
			continue
		}
		stub := make(map[string]any, len(c.inbound))
		for k, v := range c.inbound {
			stub[k] = v
		}
		settings := make(map[string]any)
		if orig, isMap := c.inbound["settings"].(map[string]any); isMap {
			for k, v := range orig {
				settings[k] = v
			}
		}
		settings[c.key] = c.add
		stub["settings"] = settings
		stubs = append(stubs, stub)
		want += len(c.add)
	}
	if len(stubs) > 0 {
		out, err := s.runXrayFile(statsTimeout, "xray-adu-*.json", map[string]any{"inbounds": stubs}, "api", "adu", "--server="+apiAddr)
		if err != nil {
			return fmt.Errorf("api adu: %w", err)
		}
		// Exit 0 with users left out is how adu reports a refusal; the count is the
		// only reliable signal (see AddUsers).
		if got := reportedAdded(out); got != want {
			return fmt.Errorf("api adu added %d of %d user entries: %s", got, want, bytes.TrimSpace(out))
		}
	}

	var wireGuard []wireGuardInbound
	for _, c := range changes {
		if !c.wireGuard {
			continue
		}
		// The pushed peers, whole, re-read as the typed entries the sync compares.
		settings, _ := c.inbound["settings"].(map[string]any)
		raw, err := json.Marshal(settings[c.key])
		if err != nil {
			return err
		}
		var peers []WireGuardInboundPeer
		if err := json.Unmarshal(raw, &peers); err != nil {
			return fmt.Errorf("read the peers of %s: %w", c.tag, err)
		}
		wireGuard = append(wireGuard, wireGuardInbound{tag: c.tag, peers: peers})
	}
	if err := s.syncWireGuardLocked(apiAddr, wireGuard); err != nil {
		return err
	}

	var hysteria []hysteriaInbound
	for _, c := range changes {
		if !c.hysteria {
			continue
		}
		in := hysteriaInbound{tag: c.tag, whole: c.inbound}
		settings, _ := c.inbound["settings"].(map[string]any)
		users, _ := settings[c.key].([]any)
		for _, raw := range users {
			u, _ := raw.(map[string]any)
			auth, _ := u["auth"].(string)
			email, _ := u["email"].(string)
			in.users = append(in.users, HysteriaClient{Auth: auth, Email: email})
		}
		hysteria = append(hysteria, in)
	}
	return s.syncHysteriaLocked(apiAddr, hysteria)
}

// runXrayFile writes body as JSON to a temp file and runs `xray <args...> <file>`: an
// api call that changes the running Xray, asked again while it cannot reach it (see
// runXrayAPI).
func (s *Supervisor) runXrayFile(timeout time.Duration, pattern string, body any, args ...string) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return s.runXrayAPI(timeout, append(args, f.Name())...)
}

// writeFileAtomic replaces path with data through a temp file and a rename.
func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
