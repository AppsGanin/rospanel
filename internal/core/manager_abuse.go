package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/abuse"
	"github.com/AppsGanin/rospanel/internal/model"

	"github.com/AppsGanin/rospanel/internal/store"
)

// abusePendingKey identifies one buffered match. Keyed by day as well as domain so
// a batch that straddles midnight lands in the right rollups rather than stamping
// everything with the day the flush happened to run.
type abusePendingKey struct {
	userID int64
	nodeID int64
	domain string
	day    string
}

// abuseAlertKey identifies one already-sent alert. A SET of these, not a
// user→latest-day map: a user can be alerted for two adjacent days at once (a batch
// straddling midnight), and a single latest-day marker cannot remember both.
type abuseAlertKey struct {
	userID int64
	day    string
}

const (
	// abusePendingMax bounds the buffer, mirroring accPendingMax. A device hammering
	// a dead C2 can produce matches far faster than the flush drains them.
	abusePendingMax = 4096
	// abuseNodeMax bounds how many matches ONE node sync may add to the shared buffer,
	// so a hostile node cannot fill abusePendingMax with blocklisted domains (the feeds
	// are public) and starve the master's own locally-observed matches until the next
	// flush. Well below abusePendingMax so the master's hot path always has room.
	abuseNodeMax = 512
	// abuseAlertMin is how many matches a user must accrue in a day before the
	// operator is told. One hit is noise — an ad-adjacent CDN, a mistyped domain;
	// a pattern is not.
	abuseAlertMin = 20
	// maxAbuseCount clamps one node-reported match count. A node syncs every ~45s, so
	// a real count is far below this; the clamp stops a hostile count from overflowing
	// the int64 rollup negative.
	maxAbuseCount = 1 << 20
)

// recordAbuse checks one destination against the blocklists and buffers a match.
//
// Runs on the access-log hot path, so it does no I/O: a lookup in a sorted hash
// slice under a read lock, and on the rare hit a map update. A nil matcher (feeds
// never downloaded, or the feature off) matches nothing and costs one nil check.
func (m *Manager) recordAbuse(userID int64, dest string) {
	m.addAbuseMatch(0, userID, dest, 1)
}

// RecordNodeAbuse folds in a match a node reported, attributed to that node, and
// reports whether it matched — the caller bounds how many a single sync may add.
//
// Node abuse detection is weaker than the master's by design: the node ships only
// each user's busiest destinations (a truncated top-N), so a low-volume callback by
// a heavy browser can be trimmed out before the panel ever sees it — the exact
// low-volume case the master's unthrottled path catches. That is the cost of not
// shipping the feed to every node; the master's own traffic is matched in full.
func (m *Manager) RecordNodeAbuse(nodeID, userID int64, dest string, count int64) bool {
	return m.addAbuseMatch(nodeID, userID, dest, count)
}

// addAbuseMatch matches a destination against the blocklists and buffers a hit,
// returning whether it matched (independent of whether the buffer had room).
//
// Only addresses match (see package abuse), so the stored value is the address
// itself — there is no folding step and nothing to normalise.
func (m *Manager) addAbuseMatch(nodeID, userID int64, dest string, count int64) bool {
	if dest == "" || count <= 0 || m.abuse == nil {
		return false
	}
	cat, ok := m.abuse.Matcher().Match(dest)
	if !ok {
		return false
	}
	// Clamp a node-supplied count: it is remote input and an unbounded value would
	// overflow the int64 rollup to a negative number over repeated syncs. The local
	// path always passes 1.
	if count > maxAbuseCount {
		count = maxAbuseCount
	}
	now := time.Now()
	key := abusePendingKey{
		userID: userID, nodeID: nodeID, domain: dest,
		day: now.In(m.loc()).Format("2006-01-02"),
	}
	m.abuseMu.Lock()
	defer m.abuseMu.Unlock()
	h, buffered := m.abusePending[key]
	if !buffered && len(m.abusePending) >= abusePendingMax {
		// Shed rather than grow forever, as RecordAccess does — but note it: unlike the
		// connections buffer, nothing upstream throttles this, so a full buffer means
		// matches are being lost and the operator's view is undercounting.
		m.abuseDropLog()
		return true
	}
	h.UserID, h.NodeID = userID, nodeID
	h.Domain, h.Category, h.Day = dest, string(cat), key.day
	h.Count += count
	if ts := now.Unix(); ts > h.SeenAt {
		h.SeenAt = ts
	}
	m.abusePending[key] = h
	return true
}

// abuseDropLog warns at most once a minute that the match buffer is full, so a
// flood is visible without flooding the log in turn. Caller holds abuseMu.
func (m *Manager) abuseDropLog() {
	now := time.Now().Unix()
	if now-m.abuseDropAt < 60 {
		return
	}
	m.abuseDropAt = now
	logErr("abuse: match buffer full, dropping matches", "cap", abusePendingMax)
}

// FlushAbuse writes the buffered matches in one transaction and alerts on users who
// crossed the threshold. Driven by the same loop as FlushAccess.
func (m *Manager) FlushAbuse() {
	m.abuseMu.Lock()
	if len(m.abusePending) == 0 {
		m.abuseMu.Unlock()
		return
	}
	hits := make([]store.AbuseHit, 0, len(m.abusePending))
	for _, h := range m.abusePending {
		hits = append(hits, h)
	}
	clear(m.abusePending)
	m.abuseMu.Unlock()

	if err := m.store.AddAbuseMatches(hits); err != nil {
		// Requeue rather than drop, as FlushAccess does: these rows are the whole
		// product of the feature, and a full disk should not silently erase them.
		m.abuseMu.Lock()
		for _, h := range hits {
			key := abusePendingKey{
				userID: h.UserID, nodeID: h.NodeID, domain: h.Domain, day: h.Day,
			}
			cur, buffered := m.abusePending[key]
			if !buffered && len(m.abusePending) >= abusePendingMax {
				continue
			}
			cur.UserID, cur.NodeID = h.UserID, h.NodeID
			cur.Domain, cur.Category, cur.Day = h.Domain, h.Category, h.Day
			cur.Count += h.Count
			if h.SeenAt > cur.SeenAt {
				cur.SeenAt = h.SeenAt
			}
			m.abusePending[key] = cur
		}
		m.abuseMu.Unlock()
		logErr("abuse: flush failed, matches requeued", "matches", len(hits), "err", err)
		return
	}
	m.alertAbuse(hits)
}

// alertAbuse notifies the operator about users who crossed the daily threshold.
//
// Deduped per user per day: the point is "look at this account", and repeating it
// every five seconds for as long as the traffic continues would train the operator
// to ignore it.
func (m *Manager) alertAbuse(hits []store.AbuseHit) {
	// Key by (user, day): a batch straddling midnight can carry both days, and the
	// threshold is per-day. abuseAlerted is a SET of (user, day) rather than a
	// user→day map because a user can be alerted for two adjacent days at once — a
	// single "latest day" marker cannot remember that both were sent, so the earlier
	// day would look un-alerted forever and re-fire on every later flush.
	seen := make(map[abuseAlertKey]struct{}, len(hits))
	for _, h := range hits {
		seen[abuseAlertKey{h.UserID, h.Day}] = struct{}{}
	}
	threshold := m.abuseThreshold()

	// One counts query per distinct day in the batch (almost always one). On a query
	// error, skip that day rather than abandon the whole batch: the matches are
	// already persisted, so other days still alert and this day retries on the next
	// flush that carries a match for it. A day with no entry reads total 0 below.
	dayCounts := map[string]map[int64]int64{}
	for k := range seen {
		if _, ok := dayCounts[k.day]; ok {
			continue
		}
		c, err := m.store.AbuseUserCountsForDay(k.day)
		if err != nil {
			logErr("abuse: day count query failed, skipping alerts for day", "day", k.day, "err", err)
			continue
		}
		dayCounts[k.day] = c
	}

	for k := range seen {
		total := dayCounts[k.day][k.userID]
		if total < int64(threshold) {
			continue
		}
		// abuseAlerted is touched only here, and FlushAbuse (this call's only caller)
		// runs serially on one ticker — the lock keeps the invariant local if that ever
		// changes. Record BEFORE notifying, as notifyStatusTransitions does: a failed
		// send costs one missed alert (the matches are still visible in the UI), which
		// beats re-firing it every flush.
		m.abuseMu.Lock()
		_, alerted := m.abuseAlerted[k]
		if !alerted {
			m.abuseAlerted[k] = struct{}{}
		}
		m.abuseMu.Unlock()
		if alerted {
			continue
		}
		matches, err := m.store.AbuseByUser(k.userID, 5)
		if err != nil || len(matches) == 0 {
			continue
		}
		u, err := m.store.GetUser(k.userID)
		if err != nil || u == nil {
			continue
		}
		m.notifyAbuse(*u, total, matches)
	}

	// Prune alert markers so the set stays bounded — it would otherwise keep one entry
	// per user per day ever alerted.
	//
	// The window is the match retention window, NOT "yesterday". A marker has to
	// outlive every match that can still name its day: drop it earlier and the next
	// batch carrying that day finds no marker, alerts again, prunes again, and
	// re-alerts on every single flush from then on. Bounded at users × retention days.
	cutoff := time.Now().In(m.loc()).AddDate(0, 0, -model.AbuseRetentionDays).Format("2006-01-02")
	m.abuseMu.Lock()
	for k := range m.abuseAlerted {
		if k.day < cutoff {
			delete(m.abuseAlerted, k)
		}
	}
	m.abuseMu.Unlock()
}

// notifyAbuse tells the admin chats which account crossed the threshold and what
// it reached, with a few examples — the operator's next move is to look at that
// account, and a bare count would not tell them whether it is worth doing.
func (m *Manager) notifyAbuse(u model.User, total int64, matches []store.AbuseMatch) {
	var b strings.Builder
	fmt.Fprintf(&b, "🚨 <b>Подозрительный трафик</b>\nПользователь: %s\nСовпадений: %d",
		escHTML(u.Name), total)
	for _, mt := range matches {
		fmt.Fprintf(&b, "\n• %s — %s (%d)",
			escHTML(mt.Domain), escHTML(abuse.Category(mt.Category).Title()), mt.Count)
		// Name the reporting node when it is not the master. A match seen ONLY via a
		// node (never on the master's own traffic) is worth the operator's suspicion
		// both ways: real abuse on that node, or a misbehaving node fabricating it.
		if mt.NodeID != 0 {
			fmt.Fprintf(&b, " · нода %d", mt.NodeID)
		}
	}
	m.notifyAdminEvent(model.AdminEventAbuse, b.String())
}

// PurgeOldAbuse drops matches past the retention window.
func (m *Manager) PurgeOldAbuse() {
	cutoff := time.Now().In(m.loc()).AddDate(0, 0, -model.AbuseRetentionDays).Format("2006-01-02")
	n, err := m.store.PurgeAbuseMatches(cutoff)
	if err != nil {
		logErr("abuse: retention sweep failed", "err", err)
		return
	}
	if n > 0 {
		logInfo("abuse: retention sweep removed matches", "rows", n, "before", cutoff)
	}
}

// SetAbuse wires the blocklist store in and configures it from the persisted
// settings (which categories are active + the custom list), loading whatever is
// already cached — so a panel that has run before starts matching at boot rather
// than staying blind until the first download lands.
func (m *Manager) SetAbuse(s *abuse.Store) {
	m.abuse = s
	if s == nil {
		return
	}
	set, err := m.store.GetSettings()
	if err != nil {
		s.LoadCached() // no settings yet: default all-on
		return
	}
	s.Configure(m.abuseEnabledMap(set), set.AbuseCustom)
}

// abuseEnabledMap turns the persisted category bitmask into the per-category enabled
// set the abuse Store wants. Master off ⇒ nothing enabled. Custom is on whenever the
// master is (its content decides whether it actually matches anything).
func (m *Manager) abuseEnabledMap(set *model.Settings) map[abuse.Category]bool {
	out := map[abuse.Category]bool{}
	if !set.AbuseEnabled {
		return out
	}
	for _, c := range model.AbuseCategoryCatalog {
		out[abuse.Category(c.Key)] = set.AbuseCategoryEnabled(c.Bit)
	}
	out[abuse.CatCustom] = true
	return out
}

// AbuseConfig returns the current blocklist config for the settings UI.
func (m *Manager) AbuseConfig() (enabled bool, categories map[string]bool, custom string, alertMin int) {
	set, err := m.store.GetSettings()
	if err != nil {
		return true, nil, "", abuseAlertMin
	}
	categories = map[string]bool{}
	for _, c := range model.AbuseCategoryCatalog {
		categories[c.Key] = set.AbuseCategoryEnabled(c.Bit)
	}
	min := set.AbuseAlertMin
	if min < 1 {
		min = abuseAlertMin
	}
	return set.AbuseEnabled, categories, set.AbuseCustom, min
}

// SetAbuseConfig persists the blocklist config and reconfigures the live matcher.
func (m *Manager) SetAbuseConfig(enabled bool, categories map[string]bool, custom string, alertMin int) error {
	var mask int64
	for _, c := range model.AbuseCategoryCatalog {
		if categories[c.Key] {
			mask |= c.Bit
		}
	}
	if alertMin < 1 {
		alertMin = 1
	}
	if err := m.store.SetAbuseConfig(enabled, mask, custom, alertMin); err != nil {
		return err
	}
	if m.abuse != nil {
		set, err := m.store.GetSettings()
		if err == nil {
			m.abuse.Configure(m.abuseEnabledMap(set), custom)
		}
	}
	return nil
}

// abuseThreshold is the matches-per-day alert trigger, from settings (fallback const).
func (m *Manager) abuseThreshold() int {
	if set, err := m.store.GetSettings(); err == nil && set.AbuseAlertMin > 0 {
		return set.AbuseAlertMin
	}
	return abuseAlertMin
}

// RecentAbuse returns the fleet's recent matches for the panel's view.
func (m *Manager) RecentAbuse(limit int) ([]store.AbuseMatch, error) {
	return m.store.AbuseRecent(limit)
}

// UserAbuse returns one user's matches.
func (m *Manager) UserAbuse(userID int64, limit int) ([]store.AbuseMatch, error) {
	return m.store.AbuseByUser(userID, limit)
}

// RefreshAbuse forces an immediate re-download of the enabled feeds, in the
// background so the operator's request returns promptly.
func (m *Manager) RefreshAbuse() {
	if m.abuse == nil {
		return
	}
	go m.abuse.Refresh(context.Background(), true)
}

// AbuseStatus exposes the loaded feeds for the settings UI.
func (m *Manager) AbuseStatus() []abuse.FileInfo {
	if m.abuse == nil {
		return nil
	}
	return m.abuse.Status()
}
