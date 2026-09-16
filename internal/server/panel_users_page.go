package server

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/store"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The users page used to load every user at once — each with their share links, their
// subscription URL and their groups — and filter, sort and page in the browser. At
// ~2.5 KB a user that was ~50 MB for 20,000 users, fetched again by the dashboard every
// five minutes. The page now asks for one window of rows: the filtering, the chip
// counts and the tag list are worked out here, and the links are fetched only for the
// one user whose card is open.

const (
	usersPageDefault = 50
	usersPageMax     = 1000
	// expiringSoonDays is what the "expiring" chip means, on the users page and on the
	// dashboard tile alike.
	expiringSoonDays = 7
)

// userChips are the filters the users page offers, each counted over every user.
var userChips = []string{"active", "online", "expiring", "limited", "disabled", "expired"}

// userRow is a user as one line of the list: what the row shows and what the filters
// read, and nothing a row does not draw — no links, no subscription URL, no secrets.
type userRow struct {
	ID            int64            `json:"id"`
	Name          string           `json:"name"`
	SystemEmail   string           `json:"system_email"`
	Status        string           `json:"status"`
	Enabled       bool             `json:"enabled"`
	DataLimit     int64            `json:"data_limit"`
	ExpireAt      int64            `json:"expire_at"`
	HoldSeconds   int64            `json:"hold_seconds"`
	UsedUp        int64            `json:"used_up"`
	UsedDown      int64            `json:"used_down"`
	LastSeen      int64            `json:"last_seen"`
	DeviceLimit   int              `json:"device_limit"`
	ActiveDevices int              `json:"active_devices"`
	Tags          []string         `json:"tags"`
	Groups        []model.GroupRef `json:"groups"`
}

type usersPage struct {
	Users  []userRow      `json:"users"`
	Total  int            `json:"total"`  // users the filter matches
	All    int            `json:"all"`    // every user
	Counts map[string]int `json:"counts"` // per chip, over every user
	Tags   []tagCount     `json:"tags"`
	// IDs is every matching user's id, in list order, when asked for (?ids=1) — what
	// "select all" selects.
	IDs []int64 `json:"ids,omitempty"`
}

// listUsersPage answers GET /api/users/page:
//
//	?q=      search: name, note or tag containing it; id or Xray email equal to it
//	?filter= all | active | online | expiring | limited | disabled | expired
//	?tag=    only users carrying this tag
//	?sort=   new | name | traffic | expiry | online
//	?lang=   ru | en, the alphabet a name sort follows
//	?offset= ?limit=   the window (limit 0 returns counts only)
//	?ids=1   also every matching id
func (rt *Router) listUsersPage(w http.ResponseWriter, r *http.Request) {
	// Summaries, not whole users: the page reads everyone on every request, and nothing
	// it shows needs a credential. The index is what every request over them works out
	// alike, worked out once for the lot.
	users, idx, err := rt.indexedUsers() // newest first, which is also the tie order
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	now := time.Now().Unix()
	page := buildUsersPage(users, idx, r.URL.Query(), now, pageLookups{
		groups: func() map[int64][]model.GroupRef {
			groups, _ := rt.mgr.GroupsForAllUsers()
			return groups
		},
		devices: func(ids []int64) map[int64]int {
			counts, _ := rt.mgr.Store().ActiveDeviceCountsOf(ids, now-model.DeviceOnlineWindow)
			return counts
		},
	})
	writeJSON(w, http.StatusOK, page)
}

// usersSnapshotTTL is how long requests share one read of every user's summary.
//
// The page reads everyone on every request — its filters and chip counts are over
// everyone — and an operator typing a search or scrolling asks several times a second.
// With 50,000 users each read was ~0.3 s of CPU on a 1-vCPU box, and an operator
// browsing kept a third of the core busy. Shared for a few seconds, a burst costs one
// read. Usage and online status on the page can lag by that much; nothing an operator
// does through the panel or the API does, because a request that changes something
// makes the next read fresh (see notingWrites).
const usersSnapshotTTL = 3 * time.Second

// usersSnapshot is the shared read, with what was worked out over it.
type usersSnapshot struct {
	mu     sync.Mutex
	users  []store.UserSummary
	idx    *usersIndex // nil until a page asks for one
	writes uint64      // the write count it was read under
	at     time.Time
}

// userSummaries returns every user's summary, read afresh unless the last read is
// younger than usersSnapshotTTL and no request has changed anything since. The slice is
// shared: callers must not modify it. Callers asking at once share one read.
func (rt *Router) userSummaries() ([]store.UserSummary, error) {
	users, _, err := rt.snapshot(false)
	return users, err
}

// indexedUsers returns the shared read together with what a page works out over every
// user: the chips they fall under, the counts and the tags. Built at most once per
// read, by the first page to ask. Neither is to be modified.
func (rt *Router) indexedUsers() ([]store.UserSummary, *usersIndex, error) {
	return rt.snapshot(true)
}

// snapshot is the body of both, reading the users when the shared read has run out and
// building the index for the caller that wants one.
func (rt *Router) snapshot(index bool) ([]store.UserSummary, *usersIndex, error) {
	// Taken before the read, so a write landing during it leaves the snapshot looking
	// older than it is — the next caller reads again, never the other way round.
	writes := rt.writes.Load()
	c := &rt.usersSnap
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.users == nil || c.writes != writes || time.Since(c.at) >= usersSnapshotTTL {
		users, err := rt.mgr.Store().ListUserSummaries()
		if err != nil {
			return nil, nil, err
		}
		if users == nil {
			users = []store.UserSummary{} // no users is an answer worth sharing too
		}
		c.users, c.idx, c.writes, c.at = users, nil, writes, time.Now()
	}
	if index && c.idx == nil {
		c.idx = newUsersIndex(c.users, time.Now().Unix())
	}
	return c.users, c.idx, nil
}

// usersIndex is what every request over a snapshot works out the same way: which chips
// each user falls under, how many fall under each, and the tags they carry between
// them — a pass over everyone, which was repeated for every keystroke of a search.
//
// It is worked out at the moment the users are read, so the chips that depend on the
// time — online, expiring — are as fresh as the read they belong to and no fresher.
// Both are measured in minutes or days, and the snapshot lives seconds.
type usersIndex struct {
	chips  []uint32 // per user, a bit per chip in userChips
	counts map[string]int
	tags   []tagCount

	mu    sync.Mutex
	names map[string][]nameKey // per alphabet, made when a name sort first asks
	lower *loweredText         // made when a search first asks
}

// loweredText is every user's name and note folded to lower case, which is what a
// search compares against. Folding them per keystroke allocated a copy of every name
// in the panel for each character typed.
type loweredText struct{ names, notes []string }

func newUsersIndex(users []store.UserSummary, now int64) *usersIndex {
	idx := &usersIndex{chips: make([]uint32, len(users)), counts: make(map[string]int, len(userChips))}
	counts := make([]int, len(userChips))
	tags := map[string]int{}
	for i := range users {
		for c, chip := range userChips {
			if userInChip(&users[i], chip, now) {
				idx.chips[i] |= 1 << c
				counts[c]++
			}
		}
		for _, t := range users[i].Tags {
			tags[t]++
		}
	}
	for c, chip := range userChips {
		idx.counts[chip] = counts[c] // every chip is counted, including at zero
	}
	idx.tags = tagCounts(tags)
	return idx
}

// chipBit is the bit a filter reads, 0 for one that excludes nobody.
func chipBit(filter string) uint32 {
	if c := slices.Index(userChips, filter); c >= 0 {
		return 1 << c
	}
	return 0
}

// searchText folds every name and note to lower case, once per snapshot. Tags are
// stored folded already (see model.NormalizeTags), so they are read as they are.
func (idx *usersIndex) searchText(users []store.UserSummary) *loweredText {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.lower != nil {
		return idx.lower
	}
	low := &loweredText{names: make([]string, len(users)), notes: make([]string, len(users))}
	for i := range users {
		low.names[i] = strings.ToLower(users[i].Name)
		low.notes[i] = strings.ToLower(users[i].Note)
	}
	idx.lower = low
	return low
}

// nameKeys are the collation keys for the alphabet asked for, made once per snapshot:
// collating 50,000 names is most of what a name sort costs, and an operator paging
// through them asks for the same order again and again.
func (idx *usersIndex) nameKeys(users []store.UserSummary, lang string) []nameKey {
	if lang != "en" {
		lang = "ru"
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if keys, ok := idx.names[lang]; ok {
		return keys
	}
	keys := collationKeys(users, lang)
	if idx.names == nil {
		idx.names = map[string][]nameKey{}
	}
	idx.names[lang] = keys
	return keys
}

// notingWrites counts every request that may change something once it has been
// handled — before its response is flushed — so a users snapshot read before it is not
// served after it: an operator who edits a user and reloads the list sees the edit.
func (rt *Router) notingWrites(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			rt.writes.Add(1)
		}
	})
}

// pageLookups are what the page reads only for the rows it shows.
type pageLookups struct {
	groups  func() map[int64][]model.GroupRef
	devices func(ids []int64) map[int64]int
}

// buildUsersPage is the page over a newest-first user list and the index worked out
// over it. It modifies neither: both are the shared snapshot.
//
// What the list is filtered and sorted into is positions in that snapshot, never
// copies of the users: a summary is a couple of hundred bytes, and a page of fifty
// rows used to move all fifty thousand of them twice.
func buildUsersPage(users []store.UserSummary, idx *usersIndex, q url.Values, now int64, look pageLookups) usersPage {
	chip := chipBit(q.Get("filter"))
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	tag := strings.TrimSpace(q.Get("tag"))

	page := usersPage{All: len(users), Counts: idx.counts, Tags: idx.tags, Users: []userRow{}}
	var low *loweredText
	var searchID int64
	if search != "" {
		low = idx.searchText(users)
		searchID = searchedID(search)
	}
	// nil is every user in the order they are already in, which is the order the page
	// shows unless it is asked for another: whole-list work no request needs.
	var matched []int32
	if chip != 0 || tag != "" || search != "" {
		matched = []int32{}
		for i := range users {
			if idx.chips[i]&chip == chip && (tag == "" || slices.Contains(users[i].Tags, tag)) &&
				(search == "" || userSearchMatch(&users[i], low.names[i], low.notes[i], search, searchID)) {
				matched = append(matched, int32(i))
			}
		}
		page.Total = len(matched)
	} else {
		page.Total = len(users)
	}

	offset := clampNonNeg(atoiOr(q.Get("offset"), 0))
	limit := min(clampNonNeg(atoiOr(q.Get("limit"), usersPageDefault)), usersPageMax)
	wantIDs := q.Get("ids") == "1"
	// The dashboard asks for counts alone (limit 0); an order nobody reads is not worth
	// a sort of the whole list — a name sort collates every name.
	if limit == 0 && !wantIDs {
		return page
	}
	if by := q.Get("sort"); slices.Contains(userSorts, by) {
		if matched == nil {
			matched = make([]int32, len(users))
			for i := range matched {
				matched[i] = int32(i)
			}
		}
		sortUserList(matched, users, idx, by, q.Get("lang"), now)
	}
	at := func(i int) *store.UserSummary {
		if matched == nil {
			return &users[i]
		}
		return &users[matched[i]]
	}

	offset = min(offset, page.Total)
	window := max(min(offset+limit, page.Total)-offset, 0)
	if window > 0 {
		ids := make([]int64, window)
		for i := range ids {
			ids[i] = at(offset + i).ID
		}
		groups, devices := look.groups(), look.devices(ids)
		page.Users = make([]userRow, window)
		for i := range page.Users {
			u := at(offset + i)
			page.Users[i] = makeUserRow(u, groups[u.ID], devices[u.ID])
		}
	}
	if wantIDs {
		page.IDs = make([]int64, page.Total)
		for i := range page.IDs {
			page.IDs[i] = at(i).ID
		}
	}
	return page
}

func makeUserRow(u *store.UserSummary, groups []model.GroupRef, devices int) userRow {
	if groups == nil {
		groups = []model.GroupRef{}
	}
	tags := u.Tags
	if tags == nil {
		tags = []string{}
	}
	return userRow{
		ID: u.ID, Name: u.Name, SystemEmail: model.UserEmail(u.ID), Status: u.Status,
		Enabled: u.Enabled, DataLimit: u.DataLimit, ExpireAt: u.ExpireAt, HoldSeconds: u.HoldSeconds,
		UsedUp: u.UsedUp, UsedDown: u.UsedDown, LastSeen: u.LastSeen,
		DeviceLimit: u.DeviceLimit, ActiveDevices: devices, Tags: tags, Groups: groups,
	}
}

// userInChip reports whether a user belongs under a filter chip. "online" and
// "expiring" are facts no status carries, which is why the chips are wider than it.
func userInChip(u *store.UserSummary, chip string, now int64) bool {
	switch chip {
	case "active":
		return u.Status == model.StatusActive
	case "online":
		return u.LastSeen > 0 && now-u.LastSeen < model.DeviceOnlineWindow
	case "expiring":
		return u.ExpireAt > now && u.ExpireAt-now <= expiringSoonDays*86400
	case "limited":
		return u.Status == model.StatusLimited || u.Status == model.StatusDeviceLimited
	case "disabled":
		return u.Status == model.StatusDisabled
	case "expired":
		return u.Status == model.StatusExpired
	}
	return true
}

// searchedID is the user a search names outright: an id, or an Xray email — so a log
// line's "u42" finds its account. 0 when the search names no one, which is no user's
// id. Read once per request: formatting every user's id to compare it as text
// allocated a string per user per keystroke.
func searchedID(q string) int64 {
	digits := strings.TrimPrefix(q, "u")
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != digits {
		return 0 // not a number, or not the plain way of writing one
	}
	return id
}

// userSearchMatch is the users page's search: a name, note or tag containing q, or the
// user the search names outright (see searchedID). q, name and note are already
// lower-cased.
func userSearchMatch(u *store.UserSummary, name, note, q string, id int64) bool {
	if strings.Contains(name, q) || strings.Contains(note, q) {
		return true
	}
	if id != 0 && u.ID == id {
		return true
	}
	for _, t := range u.Tags {
		if strings.Contains(t, q) {
			return true
		}
	}
	return false
}

// userSorts are the orders that are not the one the list is already in. "new" — and
// anything unknown — is newest first, which is how the users are read: a stable sort
// of fifty thousand rows into the order they already hold is work for nothing.
var userSorts = []string{"name", "traffic", "expiry", "online"}

// sortUserList orders positions into users, in place. Stable, over a newest-first
// list, so users that tie keep newest first.
func sortUserList(order []int32, users []store.UserSummary, idx *usersIndex, by, lang string, now int64) {
	switch by {
	case "name":
		keys := idx.nameKeys(users, lang)
		sort.SliceStable(order, func(a, b int) bool { return keys[order[a]].less(keys[order[b]]) })
	case "traffic":
		sort.SliceStable(order, func(a, b int) bool {
			x, y := &users[order[a]], &users[order[b]]
			return x.UsedUp+x.UsedDown > y.UsedUp+y.UsedDown
		})
	case "expiry":
		sort.SliceStable(order, func(a, b int) bool {
			return expiryKey(&users[order[a]], now) < expiryKey(&users[order[b]], now)
		})
	case "online":
		sort.SliceStable(order, func(a, b int) bool { return users[order[a]].LastSeen > users[order[b]].LastSeen })
	}
}

// expiryKey orders by soonest end. A term still waiting for its first connection
// cannot end sooner than its full length from now; no end at all sorts last.
func expiryKey(u *store.UserSummary, now int64) int64 {
	switch {
	case u.ExpireAt > 0:
		return u.ExpireAt
	case u.HoldSeconds > 0:
		return now + u.HoldSeconds
	}
	return int64(^uint64(0) >> 1)
}

// nameKey is where a name falls in the name sort: its group, then its collation key.
type nameKey struct {
	class int
	key   []byte
}

func (a nameKey) less(b nameKey) bool {
	if a.class != b.class {
		return a.class < b.class
	}
	return bytes.Compare(a.key, b.key) < 0
}

// collationKeys orders names the way the browser's list did: alphabetically with case and
// «ё» folded in, punctuation and digits first — and the operator's own alphabet before
// the other, which is where a plain collator differs from the browser's for Russian
// (it put every Latin name before the Cyrillic ones). Which group a name falls in is
// judged by its first character. Keys are made once per name, so a sort of tens of
// thousands does not collate each pair it compares.
func collationKeys(users []store.UserSummary, lang string) []nameKey {
	c := collate.New(language.Russian)
	own := unicode.Cyrillic
	if lang == "en" {
		c = collate.New(language.English)
		own = unicode.Latin
	}
	class := func(s string) int {
		r, _ := utf8.DecodeRuneInString(s)
		switch {
		case unicode.IsDigit(r):
			return 1
		case unicode.Is(own, r):
			return 2
		case unicode.IsLetter(r):
			return 3
		}
		return 0 // punctuation, symbols, an empty name
	}
	var buf collate.Buffer
	keys := make([]nameKey, len(users))
	for i, u := range users {
		keys[i] = nameKey{class: class(u.Name), key: append([]byte(nil), c.KeyFromString(&buf, u.Name)...)}
		buf.Reset()
	}
	return keys
}

// getUser answers GET /api/users/{id} with the whole user, links included — what the
// user's card needs and the list no longer carries.
func (rt *Router) getUser(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := rt.mgr.Store().GetUser(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErrCode(w, http.StatusNotFound, "err.userNotFound", "пользователь не найден")
		return
	}
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	set, err := rt.mgr.Store().GetSettings()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	rt.applyTLSHints(set)
	writeJSON(w, http.StatusOK, rt.userViewFor(*u, set, botUsername(r.Context(), set.TGUserBotToken, set.TelegramProxyURL())))
}

// userBrief is a user as a picker names them.
type userBrief struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// listUsersBrief answers GET /api/users/brief: every user's id, name and status, for
// choosing members — a few dozen bytes a user where a list row is a few hundred.
func (rt *Router) listUsersBrief(w http.ResponseWriter, _ *http.Request) {
	users, err := rt.userSummaries()
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	out := make([]userBrief, len(users))
	for i, u := range users {
		out[i] = userBrief{ID: u.ID, Name: u.Name, Status: u.Status}
	}
	writeJSON(w, http.StatusOK, out)
}
