package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/reach"
)

// Reachability from other countries (internal/reach, Globalping).
//
// Every check here runs from the panel's side of the world; a server blocked in the
// country its users live in still answers the panel. Globalping has probes in those
// countries — Russia, China and Iran among them — and a TCP ping from them to the
// server's main port says whether the address gets through.
//
// The operator checks a server by hand from its card, and may have the panel check
// every server on a schedule and tell the admins when one stops answering from a
// country (two rounds in a row, so one bad round of a flaky probe network does not
// page anyone) and again when it answers.

const (
	reachTick = time.Minute
	// reachMinAnswered is how many probes must have said something before a country's
	// share is believed: one probe of one answering is no verdict on a whole country.
	reachMinAnswered = 3
	// reachBadRounds is how many rounds in a row a country must fail before admins are
	// told.
	reachBadRounds    = 2
	reachMaxCountries = 6
	reachMaxEvery     = 24 * 60
	reachMinEvery     = 15
)

// ReachReport is the last check of one server.
type ReachReport struct {
	ServerID  int64           `json:"server_id"`
	Host      string          `json:"host"`
	Port      int             `json:"port"`
	At        int64           `json:"at"`
	Countries []reach.Country `json:"countries"`
}

// ReachSummary is a check boiled down for the server list: per country, how many
// probes reached the server of how many answered.
type ReachSummary struct {
	At        int64              `json:"at"`
	Countries []ReachCountryLine `json:"countries"`
}

// ReachCountryLine is one country of a ReachSummary.
type ReachCountryLine struct {
	Code     string `json:"code"`
	OK       int    `json:"ok"`
	Answered int    `json:"answered"`
	Blocked  bool   `json:"blocked"` // below the threshold
}

// ReachConfig is the settings form: model.ReachSettings without the token itself.
type ReachConfig struct {
	Countries []string `json:"countries"`
	Probes    int      `json:"probes"`
	Every     int      `json:"every"`
	Threshold int      `json:"threshold"`
	HasToken  bool     `json:"has_token"`
}

// reachState is what the monitor knows: the last report per server, and per server
// and country how many rounds in a row it failed and whether admins were told.
type reachState struct {
	mu      sync.Mutex
	last    map[int64]ReachReport
	bad     map[reachKey]int
	alerted map[reachKey]bool
	roundAt time.Time
	running bool // a round or a manual check is talking to Globalping
}

type reachKey struct {
	server  int64
	country string
}

func (m *Manager) reachLocked() *reachState {
	if m.reach.last == nil {
		m.reach.last = map[int64]ReachReport{}
		m.reach.bad = map[reachKey]int{}
		m.reach.alerted = map[reachKey]bool{}
	}
	return &m.reach
}

// ReachConfig returns the reachability settings for the form.
func (m *Manager) ReachConfig() (ReachConfig, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return ReachConfig{}, err
	}
	r := set.Reach
	return ReachConfig{Countries: r.Countries, Probes: r.Probes, Every: r.Every,
		Threshold: r.Threshold, HasToken: r.Token != ""}, nil
}

// SetReachConfig validates and stores the reachability settings. token nil keeps the
// stored one; "" clears it.
func (m *Manager) SetReachConfig(c ReachConfig, token *string) error {
	seen := map[string]bool{}
	var countries []string
	for _, cc := range c.Countries {
		cc = strings.ToUpper(strings.TrimSpace(cc))
		if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
			return invalidCode("err.reachCountry", "неверный код страны: {{code}}", map[string]any{"code": cc})
		}
		if !seen[cc] {
			seen[cc] = true
			countries = append(countries, cc)
		}
	}
	// No countries is the check switched off.
	if len(countries) > reachMaxCountries {
		return invalidCode("err.reachCountries", "не больше {{max}} стран", map[string]any{"max": reachMaxCountries})
	}
	if c.Probes < 1 || c.Probes*len(countries) > reach.MaxProbes {
		return invalidCode("err.reachProbes", "зондов на все страны — не больше {{max}}", map[string]any{"max": reach.MaxProbes})
	}
	if c.Every != 0 && (c.Every < reachMinEvery || c.Every > reachMaxEvery) {
		return invalidCode("err.reachEvery", "интервал — от {{min}} минут до суток или выключен",
			map[string]any{"min": reachMinEvery})
	}
	if c.Threshold < 1 || c.Threshold > 100 {
		return invalidCode("err.reachThreshold", "порог — от 1 до 100%")
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	r := model.ReachSettings{Countries: countries, Probes: c.Probes, Every: c.Every,
		Threshold: c.Threshold, Token: set.Reach.Token}
	if token != nil {
		r.Token = strings.TrimSpace(*token)
	}
	if err := m.store.SetReach(r); err != nil {
		return err
	}
	m.pruneReach(countries)
	return nil
}

// pruneReach forgets the monitor's state for countries no longer watched, so taking
// a country off and putting it back starts from a clean slate.
func (m *Manager) pruneReach(countries []string) {
	keep := map[string]bool{}
	for _, c := range countries {
		keep[c] = true
	}
	m.reach.mu.Lock()
	defer m.reach.mu.Unlock()
	st := m.reachLocked()
	for k := range st.bad {
		if !keep[k.country] {
			delete(st.bad, k)
		}
	}
	for k := range st.alerted {
		if !keep[k.country] {
			delete(st.alerted, k)
		}
	}
}

// reachTarget is where a server is checked: its public host and main TCP port (the
// :443 front, which every server keeps listening).
type reachTarget struct {
	id   int64
	name string
	node *model.Node // nil for the master
	host string
	port int
}

func (m *Manager) reachTargets(set *model.Settings) ([]reachTarget, error) {
	var out []reachTarget
	if set.Host != "" {
		out = append(out, reachTarget{id: model.LocalNodeID, name: model.LocalNodeName, host: set.Host, port: set.VLESSPort})
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		n := &nodes[i]
		if !n.Enabled || !n.Joined() || n.Host == "" {
			continue
		}
		out = append(out, reachTarget{id: n.ID, name: n.Name, node: n, host: n.Host,
			port: nodeSettings(set, n).VLESSPort})
	}
	return out, nil
}

// CheckReach checks one server now and returns the report.
func (m *Manager) CheckReach(ctx context.Context, serverID int64) (*ReachReport, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	targets, err := m.reachTargets(set)
	if err != nil {
		return nil, err
	}
	if len(set.Reach.Countries) == 0 {
		return nil, invalidCode("err.reachOff", "проверка доступности выключена: в её настройках нет стран")
	}
	for _, t := range targets {
		if t.id == serverID {
			if !m.reachBegin() {
				return nil, invalidCode("err.reachBusy", "проверка уже идёт — дождитесь её")
			}
			defer m.reachEnd()
			rep, err := m.measureReach(ctx, set, t)
			if err != nil {
				return nil, err
			}
			m.recordReach(*rep)
			return rep, nil
		}
	}
	return nil, invalidCode("err.reachNoTarget", "у сервера нет публичного адреса или он не подключён")
}

// reachBegin claims Globalping for one caller: a manual check and a monitor round
// share one hourly budget, and two at once would only spend it twice.
func (m *Manager) reachBegin() bool {
	m.reach.mu.Lock()
	defer m.reach.mu.Unlock()
	if m.reach.running {
		return false
	}
	m.reach.running = true
	return true
}

func (m *Manager) reachEnd() {
	m.reach.mu.Lock()
	m.reach.running = false
	m.reach.mu.Unlock()
}

func (m *Manager) measureReach(ctx context.Context, set *model.Settings, t reachTarget) (*ReachReport, error) {
	r := set.Reach
	m.reach.mu.Lock()
	api := m.reachAPI
	m.reach.mu.Unlock()
	c := &reach.Client{Base: api, Token: r.Token}
	countries, err := c.TCP(ctx, t.host, t.port, r.Countries, r.Probes)
	if errors.Is(err, reach.ErrRateLimited) {
		return nil, invalidCode("err.reachRateLimited", "лимит Globalping на этот час исчерпан — попробуйте позже")
	}
	if err != nil {
		return nil, invalidCode("err.reachFailed", "Globalping не ответил: {{reason}}", map[string]any{"reason": err.Error()})
	}
	return &ReachReport{ServerID: t.id, Host: t.host, Port: t.port, At: time.Now().Unix(), Countries: countries}, nil
}

func (m *Manager) recordReach(rep ReachReport) {
	m.reach.mu.Lock()
	defer m.reach.mu.Unlock()
	m.reachLocked().last[rep.ServerID] = rep
}

// LastReach returns the last check of a server; At is 0 when it has not been checked
// since the panel started.
func (m *Manager) LastReach(serverID int64) ReachReport {
	m.reach.mu.Lock()
	defer m.reach.mu.Unlock()
	if rep, ok := m.reachLocked().last[serverID]; ok {
		return rep
	}
	return ReachReport{ServerID: serverID, Countries: []reach.Country{}}
}

// SetReachEndpoint points the reachability checks at another Globalping API — a
// stand-in for tests, which must not reach the internet.
func (m *Manager) SetReachEndpoint(url string) {
	m.reach.mu.Lock()
	defer m.reach.mu.Unlock()
	m.reachAPI = url
}

// reachSummaries boils the last checks down for the server list.
func (m *Manager) reachSummaries(threshold int) map[int64]*ReachSummary {
	m.reach.mu.Lock()
	defer m.reach.mu.Unlock()
	out := map[int64]*ReachSummary{}
	for id, rep := range m.reachLocked().last {
		s := &ReachSummary{At: rep.At, Countries: []ReachCountryLine{}}
		for _, c := range rep.Countries {
			s.Countries = append(s.Countries, ReachCountryLine{Code: c.Code, OK: c.OK, Answered: c.Answered(),
				Blocked: reachBlocked(c, threshold)})
		}
		out[id] = s
	}
	return out
}

// reachBlocked is a country's verdict: enough probes answered and too few got through.
func reachBlocked(c reach.Country, threshold int) bool {
	return c.Answered() >= reachMinAnswered && c.Percent() < threshold
}

// reachLoop runs the monitor: every Reach.Every minutes, each server once.
func (m *Manager) reachLoop() {
	t := time.NewTicker(reachTick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-m.done:
			return
		}
		set, err := m.store.GetSettings()
		if err != nil || set.Reach.Every <= 0 || len(set.Reach.Countries) == 0 {
			continue
		}
		m.reach.mu.Lock()
		due := time.Since(m.reach.roundAt) >= time.Duration(set.Reach.Every)*time.Minute
		m.reach.mu.Unlock()
		if due {
			m.reachRound(set)
		}
	}
}

// reachRound checks every server once and tells admins what changed.
func (m *Manager) reachRound(set *model.Settings) {
	if !m.reachBegin() {
		return // a manual check is running; the next tick tries again
	}
	defer m.reachEnd()
	m.reach.mu.Lock()
	m.reach.roundAt = time.Now()
	m.reach.mu.Unlock()

	targets, err := m.reachTargets(set)
	if err != nil {
		logErr("reach: cannot list servers", "err", err)
		return
	}
	for _, t := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		rep, err := m.measureReach(ctx, set, t)
		cancel()
		if err != nil {
			// Nothing is known about this server this round: its state stays as it was.
			logWarn("reach: check failed", "server", t.id, "err", err)
			var ve *ValidationError
			if errors.As(err, &ve) && ve.Code == "err.reachRateLimited" {
				return // the rest of the round would be refused too
			}
			continue
		}
		m.recordReach(*rep)
		for _, msg := range m.reachTransitions(t, *rep, set.Reach.Threshold) {
			m.notifyAdminEvent(model.AdminEventReach, msg)
		}
	}
}

// reachTransitions advances the monitor's state for one server's report and returns
// the messages to send.
func (m *Manager) reachTransitions(t reachTarget, rep ReachReport, threshold int) []string {
	lang := m.botLang()
	label := i18n.T(lang, "notify.serverIs", escHTML(t.name))
	if t.host != "" {
		label += " (" + escHTML(t.host) + ")"
	}
	m.reach.mu.Lock()
	defer m.reach.mu.Unlock()
	st := m.reachLocked()
	var out []string
	for _, c := range rep.Countries {
		k := reachKey{t.id, c.Code}
		if c.Answered() < reachMinAnswered {
			continue // too few probes said anything: no verdict, no change
		}
		if c.Percent() < threshold {
			st.bad[k]++
			if st.bad[k] >= reachBadRounds && !st.alerted[k] {
				st.alerted[k] = true
				out = append(out, fmt.Sprintf(i18n.T(lang, "notify.reachLost"),
					countryFlag(c.Code)+" "+c.Code, label, c.OK, c.Answered()))
			}
			continue
		}
		st.bad[k] = 0
		if st.alerted[k] {
			st.alerted[k] = false
			out = append(out, fmt.Sprintf(i18n.T(lang, "notify.reachBack"),
				countryFlag(c.Code)+" "+c.Code, label, c.OK, c.Answered()))
		}
	}
	return out
}

// countryFlag is the flag emoji of an ISO country code.
func countryFlag(cc string) string {
	if len(cc) != 2 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(cc) {
		if r < 'A' || r > 'Z' {
			return ""
		}
		b.WriteRune(0x1F1E6 + r - 'A')
	}
	return b.String()
}
