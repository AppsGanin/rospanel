package core

import (
	"sort"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/nodeapi"
	"github.com/AppsGanin/rospanel/internal/store"
	"github.com/AppsGanin/rospanel/internal/xray"
)

// Who uses what: the apps a user fetches the subscription with, and the minutes they
// spend on each protocol. Both are buffered here and written by FlushUsage on the
// access flush's cadence, so neither the subscription handler nor the access-log
// reader touches the database.

// usageLaneKey is one user on one lane.
type usageLaneKey struct {
	userID int64
	lane   string
}

// usageClientKey is one install of one user.
type usageClientKey struct {
	userID int64
	key    string
}

// usage buffers between flushes. Its own lock: the access-log reader takes it on
// every connection line, and it must not contend with the device path's accMu.
type usage struct {
	mu      sync.Mutex
	clients map[usageClientKey]store.SubClient
	// laneLast is the minute (unix/60) each user+lane was last counted in; a line in
	// the same minute adds nothing.
	laneLast      map[usageLaneKey]int64
	laneLastSwept int64
	// laneMinutes is what the next flush books.
	laneMinutes map[usageLaneKey]int64
}

const (
	// usageClientsMax and usageLanesMax bound the buffers for a flush that keeps
	// failing; past them new keys are dropped, existing ones keep counting.
	usageClientsMax = 1 << 14
	usageLanesMax   = 1 << 17
	// subClientRetention is how long an install that stopped fetching stays listed.
	subClientRetention = 180 * 24 * time.Hour
)

func newUsage() *usage {
	return &usage{
		clients:     map[usageClientKey]store.SubClient{},
		laneLast:    map[usageLaneKey]int64{},
		laneMinutes: map[usageLaneKey]int64{},
	}
}

// RecordSubClient notes a served subscription fetch. ip is the address it came from:
// a hint for the card, since an install may fetch over Wi-Fi and tunnel over mobile.
func (m *Manager) RecordSubClient(userID int64, userAgent, hwid, deviceOS, deviceModel, ip string, at time.Time) {
	// The header is attacker-chosen and up to a megabyte: parsing it as sent costs
	// milliseconds per request. The stored copy is bounded anyway (128 bytes).
	userAgent = model.CleanDeviceApp(userAgent)
	info := model.ParseSubClient(userAgent, deviceOS)
	key := model.SubClientKey(hwid, info)
	c := store.SubClient{
		UserID: userID, Key: key, App: info.App, Version: info.Version, Platform: info.Platform,
		Model: deviceModel, IP: ip, UA: userAgent,
		FirstSeen: at.Unix(), LastSeen: at.Unix(), Fetches: 1,
	}
	u := m.usage
	if u == nil { // a Manager built bare in a test
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	k := usageClientKey{userID: userID, key: key}
	if prev, ok := u.clients[k]; ok {
		c.FirstSeen, c.Fetches = prev.FirstSeen, prev.Fetches+1
		if c.Model == "" {
			c.Model = prev.Model
		}
		// The newest address wins; a fetch that names none (a client sending no
		// address the panel can read) keeps the one the row already has.
		if c.IP == "" {
			c.IP = prev.IP
		}
	} else if len(u.clients) >= usageClientsMax {
		return
	}
	u.clients[k] = c
}

// RecordLaneActivity counts the minute `at` falls in toward a user's time on a lane,
// once per minute however many connections it opened in it.
func (m *Manager) RecordLaneActivity(userID int64, lane string, at time.Time) {
	if lane == "" || userID <= 0 {
		return
	}
	minute := at.Unix() / 60
	k := usageLaneKey{userID: userID, lane: lane}
	u := m.usage
	if u == nil { // a Manager built bare in a test
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.laneLast[k] == minute {
		return
	}
	if _, pending := u.laneMinutes[k]; !pending && len(u.laneMinutes) >= usageLanesMax {
		return
	}
	u.laneLast[k] = minute
	u.laneMinutes[k]++
	// Forget pairs idle for more than a minute, at most once a minute: the map is then
	// bounded by who is active now rather than by everyone ever seen.
	if len(u.laneLast) > 4096 && minute > u.laneLastSwept {
		u.laneLastSwept = minute
		for key, last := range u.laneLast {
			if last < minute-1 {
				delete(u.laneLast, key)
			}
		}
	}
}

// RecordLocalLane is the master's lane hook: one access-log line on its own Xray.
func (m *Manager) RecordLocalLane(email, lane string) {
	if _, bridge := xray.BridgeLaneEntry(lane); bridge {
		return // another server's traffic passing through, not a user's lane
	}
	if id, ok := userIDFromEmail(email); ok {
		m.RecordLaneActivity(id, lane, time.Now())
	}
}

// userLaneReportMax bounds one row of a node's report: a sync is seconds apart, and an
// older node's report from after an outage still does not carry an hour per row.
const userLaneReportMax = 60

// nodeUserLanesMax bounds the rows taken from one report (a node sends at most 4000).
const nodeUserLanesMax = 8000

// ingestNodeLaneMinutes adds a node's per-user lane minutes, keeping only rows for
// users the node is believed to serve and lanes that node can have.
func (m *Manager) ingestNodeLaneMinutes(nodeID int64, rows []nodeapi.UserLaneSample, believed func(int64) bool) {
	if len(rows) == 0 {
		return
	}
	if len(rows) > nodeUserLanesMax {
		rows = rows[:nodeUserLanesMax]
	}
	lanes := map[string]bool{}
	asDeltas := make([]nodeapi.LaneDelta, 0, len(rows))
	for _, r := range rows {
		if !lanes[r.Lane] {
			lanes[r.Lane] = true
			asDeltas = append(asDeltas, nodeapi.LaneDelta{Lane: r.Lane})
		}
	}
	kept, err := m.nodeLanes(nodeID, "", asDeltas)
	if err != nil {
		return
	}
	valid := make(map[string]bool, len(kept))
	for _, l := range kept {
		if _, bridge := xray.BridgeLaneEntry(l.Lane); !bridge {
			valid[l.Lane] = true
		}
	}
	u := m.usage
	if u == nil { // a Manager built bare in a test
		return
	}
	// Row by row first: the cap is per user and lane, not per line of the report, or a
	// node could name one pair a thousand times and book an hour per line.
	perPair := make(map[usageLaneKey]int64, len(rows))
	for _, r := range rows {
		if !valid[r.Lane] || r.Minutes <= 0 || !believed(r.UserID) {
			continue
		}
		perPair[usageLaneKey{userID: r.UserID, lane: r.Lane}] += min(r.Minutes, userLaneReportMax)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	for k, n := range perPair {
		if _, pending := u.laneMinutes[k]; !pending && len(u.laneMinutes) >= usageLanesMax {
			continue
		}
		u.laneMinutes[k] += min(n, userLaneReportMax)
	}
}

// FlushUsage writes the buffered installs and lane minutes. A failed write keeps its
// batch for the next flush.
func (m *Manager) FlushUsage() {
	u := m.usage
	if u == nil { // a Manager built bare in a test
		return
	}
	u.mu.Lock()
	clients := make([]store.SubClient, 0, len(u.clients))
	for _, c := range u.clients {
		clients = append(clients, c)
	}
	clear(u.clients)
	lanes := make(map[usageLaneKey]int64, len(u.laneMinutes))
	for k, n := range u.laneMinutes {
		lanes[k] = n
	}
	clear(u.laneMinutes)
	u.mu.Unlock()

	if err := m.store.RecordSubClients(clients); err != nil {
		logErr("usage: sub clients failed", "err", err)
		u.mu.Lock()
		for _, c := range clients {
			k := usageClientKey{userID: c.UserID, key: c.Key}
			if cur, ok := u.clients[k]; ok {
				cur.FirstSeen, cur.Fetches = min(cur.FirstSeen, c.FirstSeen), cur.Fetches+c.Fetches
				if cur.IP == "" {
					cur.IP = c.IP
				}
				u.clients[k] = cur
			} else {
				u.clients[k] = c
			}
		}
		u.mu.Unlock()
	}
	if len(lanes) == 0 {
		return
	}
	day := time.Now().In(m.loc()).Format("2006-01-02")
	rows := make([]store.LaneMinutes, 0, len(lanes))
	for k, n := range lanes {
		rows = append(rows, store.LaneMinutes{UserID: k.userID, Day: day, Lane: k.lane, Minutes: n})
	}
	if err := m.store.AddLaneMinutes(rows); err != nil {
		logErr("usage: lane minutes failed", "err", err)
		u.mu.Lock()
		for k, n := range lanes {
			u.laneMinutes[k] += n
		}
		u.mu.Unlock()
	}
}

// PurgeOldSubClients drops installs that stopped fetching long ago.
func (m *Manager) PurgeOldSubClients() {
	if _, err := m.store.PurgeSubClients(time.Now().Add(-subClientRetention).Unix()); err != nil {
		logErr("usage: purge sub clients failed", "err", err)
	}
}

// UserSubClients is a user's installs, most recently seen first.
func (m *Manager) UserSubClients(userID int64) ([]store.SubClient, error) {
	return m.store.UserSubClients(userID)
}

// UserLane is one protocol of one user over a period, named for the screen.
type UserLane struct {
	Lane    string `json:"lane"`
	Name    string `json:"name"`
	Minutes int64  `json:"minutes"`
	Days    int64  `json:"days"`
	LastDay string `json:"last_day"`
}

// UserLaneBreakdown is a user's time on each protocol between from and to, most used
// first. Lanes of different servers that share a name are summed.
func (m *Manager) UserLaneBreakdown(userID int64, from, to string) ([]UserLane, error) {
	rows, err := m.store.UserLaneUsage(userID, from, to)
	if err != nil {
		return nil, err
	}
	inbounds := map[int64]string{}
	if all, err := m.store.AllInbounds(); err == nil {
		for _, list := range all {
			for _, in := range list {
				inbounds[in.ID] = in.Name
			}
		}
	}
	out := make([]UserLane, 0, len(rows))
	for _, r := range rows {
		out = append(out, UserLane{Lane: r.Lane, Name: laneName(r.Lane, inbounds),
			Minutes: r.Minutes, Days: r.Days, LastDay: r.LastDay})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Minutes != out[j].Minutes {
			return out[i].Minutes > out[j].Minutes
		}
		return out[i].Lane < out[j].Lane
	})
	return out, nil
}

// ClientShares is how many users fetched with each app and from each platform over the
// days from..to (operator-local, both inclusive).
func (m *Manager) ClientShares(from, to string) (apps, platforms []store.ClientShare, users int64, err error) {
	start, err := time.ParseInLocation("2006-01-02", from, m.loc())
	if err != nil {
		return nil, nil, 0, err
	}
	end, err := time.ParseInLocation("2006-01-02", to, m.loc())
	if err != nil {
		return nil, nil, 0, err
	}
	return m.store.ClientShares(start.Unix(), end.AddDate(0, 0, 1).Unix())
}
