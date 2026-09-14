package server

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/model"
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
	users, err := rt.mgr.Store().ListUsers() // newest first, which is also the tie order
	if err != nil {
		writeManagerErr(w, err)
		return
	}
	q := r.URL.Query()
	now := time.Now().Unix()
	filter := q.Get("filter")
	if !slices.Contains(userChips, filter) {
		filter = "all"
	}
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	tag := strings.TrimSpace(q.Get("tag"))

	page := usersPage{All: len(users), Counts: make(map[string]int, len(userChips))}
	tags := map[string]int{}
	matched := make([]model.User, 0, len(users))
	for _, u := range users {
		for _, c := range userChips {
			if userInChip(u, c, now) {
				page.Counts[c]++
			}
		}
		for _, t := range u.Tags {
			tags[t]++
		}
		if userInChip(u, filter, now) && (tag == "" || slices.Contains(u.Tags, tag)) &&
			(search == "" || userSearchMatch(u, search)) {
			matched = append(matched, u)
		}
	}
	page.Tags = tagCounts(tags)
	sortUserList(matched, q.Get("sort"), q.Get("lang"), now)
	page.Total = len(matched)

	offset := clampNonNeg(atoiOr(q.Get("offset"), 0))
	limit := min(clampNonNeg(atoiOr(q.Get("limit"), usersPageDefault)), usersPageMax)
	offset = min(offset, len(matched))
	window := matched[offset:min(offset+limit, len(matched))]

	groups, _ := rt.mgr.GroupsForAllUsers()
	page.Users = make([]userRow, 0, len(window))
	for _, u := range window {
		page.Users = append(page.Users, makeUserRow(u, groups[u.ID]))
	}
	if q.Get("ids") == "1" {
		page.IDs = make([]int64, len(matched))
		for i, u := range matched {
			page.IDs[i] = u.ID
		}
	}
	writeJSON(w, http.StatusOK, page)
}

func makeUserRow(u model.User, groups []model.GroupRef) userRow {
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
		DeviceLimit: u.DeviceLimit, ActiveDevices: u.ActiveDevices, Tags: tags, Groups: groups,
	}
}

// userInChip reports whether a user belongs under a filter chip. "online" and
// "expiring" are facts no status carries, which is why the chips are wider than it.
func userInChip(u model.User, chip string, now int64) bool {
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

// userSearchMatch is the users page's search: a name, note or tag containing q, or an
// id or Xray email equal to it (so a log line's "u42" finds its account). q is already
// lower-cased.
func userSearchMatch(u model.User, q string) bool {
	if strings.Contains(strings.ToLower(u.Name), q) || strings.Contains(strings.ToLower(u.Note), q) {
		return true
	}
	if strconv.FormatInt(u.ID, 10) == q || model.UserEmail(u.ID) == q {
		return true
	}
	for _, t := range u.Tags {
		if strings.Contains(t, q) {
			return true
		}
	}
	return false
}

// sortUserList orders the list in place. Stable, over a newest-first list, so users
// that tie keep newest first.
func sortUserList(users []model.User, by, lang string, now int64) {
	switch by {
	case "name":
		keys := nameKeys(users, lang)
		idx := make([]int, len(users))
		for i := range idx {
			idx[i] = i
		}
		sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]].less(keys[idx[b]]) })
		sorted := make([]model.User, len(users))
		for i, k := range idx {
			sorted[i] = users[k]
		}
		copy(users, sorted)
	case "traffic":
		sort.SliceStable(users, func(i, j int) bool {
			return users[i].UsedUp+users[i].UsedDown > users[j].UsedUp+users[j].UsedDown
		})
	case "expiry":
		sort.SliceStable(users, func(i, j int) bool { return expiryKey(users[i], now) < expiryKey(users[j], now) })
	case "online":
		sort.SliceStable(users, func(i, j int) bool { return users[i].LastSeen > users[j].LastSeen })
	default:
		sort.SliceStable(users, func(i, j int) bool { return users[i].ID > users[j].ID })
	}
}

// expiryKey orders by soonest end. A term still waiting for its first connection
// cannot end sooner than its full length from now; no end at all sorts last.
func expiryKey(u model.User, now int64) int64 {
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

// nameKeys orders names the way the browser's list did: alphabetically with case and
// «ё» folded in, punctuation and digits first — and the operator's own alphabet before
// the other, which is where a plain collator differs from the browser's for Russian
// (it put every Latin name before the Cyrillic ones). Which group a name falls in is
// judged by its first character. Keys are made once per name, so a sort of tens of
// thousands does not collate each pair it compares.
func nameKeys(users []model.User, lang string) []nameKey {
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
	users, err := rt.mgr.Store().ListUsers()
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
