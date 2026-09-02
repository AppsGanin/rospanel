// Package importer reads the users out of another panel's data — a Marzban
// database or API dump, a 3x-ui database — and turns them into candidates this
// panel can create: the same UUIDs and passwords, so nobody has to re-add a
// server in their app, plus the limits, expiry, traffic and notes that came with
// them.
//
// It only reads. Deciding what to do with a candidate (skip a duplicate, rename,
// tag) is the caller's; see core.ImportUsers.
package importer

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Source names the panel a file came from.
type Source string

const (
	SourceMarzban  Source = "marzban"  // Marzban's SQLite database, or a GET /api/users dump
	SourceXUI      Source = "3x-ui"    // 3x-ui / x-ui: x-ui.db
	SourceRosPanel Source = "rospanel" // this panel's own export (see Export)
)

// Format and FormatVersion mark this panel's own export file. The marker is what
// Parse recognises, and the version is what a future reader checks before trusting
// the fields — a file from a newer panel is refused rather than half-read.
const (
	Format        = "rospanel-users"
	FormatVersion = 1
)

// Export is the file this panel writes and reads back: its own users, with the
// credentials that make a move invisible to them (the same UUID, password,
// subscription token and tunnel key), their limits, their usage so far and the
// operator's own annotations. Plans and access groups are NOT in it — they are
// objects of the panel that owns them, and an id from one install means nothing
// in another.
type Export struct {
	Format     string      `json:"format"`
	Version    int         `json:"version"`
	ExportedAt int64       `json:"exported_at"`
	Panel      string      `json:"panel,omitempty"` // the source panel's host, for the operator
	Users      []Candidate `json:"users"`
}

// Issue flags something about a candidate the operator should know before
// importing. Keys, not text: the panel renders them in its own language.
const (
	IssueUUIDGenerated     = "uuid_generated"     // the source had no usable UUID; a fresh one was minted
	IssuePasswordGenerated = "password_generated" // no Trojan/Shadowsocks password; a fresh one was minted
	IssueExpiryRelative    = "expiry_relative"    // 3x-ui "N days after first use": no date to carry over
	IssueNameEmpty         = "name_empty"         // no username; named after its UUID
)

// Candidate is one user as read from the source, in this panel's terms. It is
// also what this panel's own export writes, so a round trip through Export loses
// nothing: the fields a foreign panel has no equivalent for are simply empty when
// the source is Marzban or 3x-ui.
type Candidate struct {
	Name        string   `json:"name"`
	UUID        string   `json:"uuid"`
	Password    string   `json:"password"`
	DataLimit   int64    `json:"data_limit"` // bytes, 0 = unlimited
	ExpireAt    int64    `json:"expire_at"`  // unix seconds, 0 = never
	UsedUp      int64    `json:"used_up"`
	UsedDown    int64    `json:"used_down"`
	DeviceLimit int      `json:"device_limit"`
	Enabled     bool     `json:"enabled"`
	Note        string   `json:"note"`
	Issues      []string `json:"issues"`

	// Everything below is this panel's own (see Export); a foreign source leaves
	// it blank and the import simply has nothing to apply.
	SubToken    string   `json:"sub_token,omitempty"`      // keeps the subscription URL working
	WGPrivate   string   `json:"wg_private_key,omitempty"` // keeps AmneziaWG configs working
	SpeedLimit  int      `json:"speed_limit,omitempty"`    // kbit/s
	ResetPeriod string   `json:"reset_period,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ErrUnknownFormat is returned when the file is neither a database nor a JSON
// dump of a panel this package knows.
var ErrUnknownFormat = errors.New("importer: unknown file format")

// MaxJSON is the largest JSON dump Parse will read into memory. A Marzban user is
// under a kilobyte; this is tens of thousands of them.
const MaxJSON = 64 << 20

// Parse detects what the file is and reads its users. The file is opened
// read-only and never modified.
func Parse(path string) (Source, []Candidate, error) {
	head, err := readHead(path, 16)
	if err != nil {
		return "", nil, err
	}
	if bytes.HasPrefix(head, []byte("SQLite format 3\x00")) {
		return parseSQLite(path)
	}
	trimmed := bytes.TrimLeft(head, " \t\r\n\xef\xbb\xbf")
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		raw, err := readJSON(path)
		if err != nil {
			return "", nil, err
		}
		// This panel's own export first: it is a JSON object too, and its marker is
		// the only thing that tells the two apart.
		if users, ok, err := parseRosPanel(raw); ok {
			return SourceRosPanel, users, err
		}
		users, err := parseMarzbanJSON(raw)
		return SourceMarzban, users, err
	}
	return "", nil, ErrUnknownFormat
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	k, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:k], nil
}

func parseSQLite(path string) (Source, []Candidate, error) {
	// mode=ro so a foreign database is never written to — not even a journal —
	// and immutable=1 so a file the panel cannot lock (a copy on a read-only
	// mount) still opens.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return "", nil, err
	}
	defer db.Close()
	tables, err := tableSet(db)
	if err != nil {
		return "", nil, fmt.Errorf("importer: not a readable SQLite database: %w", err)
	}
	switch {
	case tables["proxies"] && tables["users"]:
		users, err := parseMarzbanDB(db)
		return SourceMarzban, users, err
	case tables["inbounds"] && tables["client_traffics"]:
		users, err := parseXUIDB(db)
		return SourceXUI, users, err
	}
	return "", nil, ErrUnknownFormat
}

func tableSet(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

// ---- Marzban ---------------------------------------------------------------

// marzbanProxies is the per-protocol credential map Marzban keeps: the "proxies"
// table has one row per protocol with a settings JSON, and the API dump nests the
// same thing under "proxies".
type marzbanProxies map[string]map[string]any

// credentials picks the one UUID and one password this panel needs out of what
// Marzban had: VLESS first, VMess as a fallback for the UUID; Trojan first,
// Shadowsocks as a fallback for the password.
func (p marzbanProxies) credentials() (id, password string) {
	get := func(proto, key string) string {
		for k, v := range p {
			if !strings.EqualFold(k, proto) {
				continue
			}
			if s, ok := v[key].(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	id = get("vless", "id")
	if id == "" {
		id = get("vmess", "id")
	}
	password = get("trojan", "password")
	if password == "" {
		password = get("shadowsocks", "password")
	}
	return id, password
}

type marzbanUser struct {
	Username    string         `json:"username"`
	SubURL      string         `json:"subscription_url"` // "/sub/<token>", API dumps only
	Status      string         `json:"status"`
	UsedTraffic int64          `json:"used_traffic"`
	DataLimit   *int64         `json:"data_limit"`
	Expire      *int64         `json:"expire"`
	Note        *string        `json:"note"`
	Proxies     marzbanProxies `json:"proxies"`
}

func (u marzbanUser) candidate() Candidate {
	c := Candidate{
		Name: strings.TrimSpace(u.Username),
		// Marzban's "disabled" is the one operator-set state; limited / expired /
		// on_hold are derived there as they are here, and come back on their own
		// from the limits and the expiry.
		Enabled:  !strings.EqualFold(u.Status, "disabled"),
		UsedDown: max(u.UsedTraffic, 0), // one counter there; all of it counts as usage here
	}
	if u.DataLimit != nil && *u.DataLimit > 0 {
		c.DataLimit = *u.DataLimit
	}
	if u.Expire != nil && *u.Expire > 0 {
		c.ExpireAt = *u.Expire
	}
	if u.Note != nil {
		c.Note = strings.TrimSpace(*u.Note)
	}
	// Marzban signs its subscription token rather than storing one, so a database
	// import has nothing to carry; an API dump spells the finished URL out.
	c.SubToken = subToken(lastPathSegment(u.SubURL))
	id, password := u.Proxies.credentials()
	c.setCredentials(id, password)
	return c
}

func parseMarzbanDB(db *sql.DB) ([]Candidate, error) {
	// Columns Marzban has had since long before 1.0; note arrived later and is
	// read only if present, so an old database still imports.
	cols, err := columnSet(db, "users")
	if err != nil {
		return nil, err
	}
	noteExpr := "''"
	if cols["note"] {
		noteExpr = "COALESCE(note, '')"
	}
	rows, err := db.Query(`SELECT id, username, COALESCE(status, 'active'), COALESCE(used_traffic, 0),
		data_limit, expire, ` + noteExpr + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("importer: marzban users: %w", err)
	}
	defer rows.Close()
	byID := map[int64]*marzbanUser{}
	var order []int64
	for rows.Next() {
		var id int64
		var u marzbanUser
		var dataLimit, expire sql.NullInt64
		var note string
		if err := rows.Scan(&id, &u.Username, &u.Status, &u.UsedTraffic, &dataLimit, &expire, &note); err != nil {
			return nil, err
		}
		if dataLimit.Valid {
			v := dataLimit.Int64
			u.DataLimit = &v
		}
		if expire.Valid {
			v := expire.Int64
			u.Expire = &v
		}
		u.Note = &note
		u.Proxies = marzbanProxies{}
		byID[id] = &u
		order = append(order, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	prows, err := db.Query(`SELECT user_id, type, COALESCE(settings, '{}') FROM proxies`)
	if err != nil {
		return nil, fmt.Errorf("importer: marzban proxies: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var userID int64
		var typ, settings string
		if err := prows.Scan(&userID, &typ, &settings); err != nil {
			return nil, err
		}
		u, ok := byID[userID]
		if !ok {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(settings), &m) == nil {
			u.Proxies[strings.ToLower(typ)] = m
		}
	}
	if err := prows.Err(); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(order))
	for _, id := range order {
		out = append(out, byID[id].candidate())
	}
	return out, nil
}

func columnSet(db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

// readJSON slurps a JSON file, refusing one larger than MaxJSON.
func readJSON(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxJSON+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxJSON {
		return nil, fmt.Errorf("importer: JSON dump larger than %d bytes", MaxJSON)
	}
	return raw, nil
}

// parseRosPanel reads this panel's own export. The middle return says whether the
// file IS one — a JSON object without the marker is somebody else's and the caller
// tries the other readers.
func parseRosPanel(raw []byte) ([]Candidate, bool, error) {
	var f Export
	if err := json.Unmarshal(raw, &f); err != nil || f.Format != Format {
		return nil, false, nil
	}
	if f.Version > FormatVersion {
		return nil, true, fmt.Errorf("importer: export version %d is newer than this panel understands (%d)", f.Version, FormatVersion)
	}
	out := make([]Candidate, 0, len(f.Users))
	for _, c := range f.Users {
		// The credentials are re-validated exactly as a foreign panel's are: a file
		// can be hand-edited, and a broken UUID must be flagged, not stored.
		c.setCredentials(c.UUID, c.Password)
		out = append(out, c)
	}
	return out, true, nil
}

// parseMarzbanJSON reads what GET /api/users answers with ({"users": [...]}) or a
// bare array of the same objects.
func parseMarzbanJSON(raw []byte) ([]Candidate, error) {
	var list []marzbanUser
	var wrapped struct {
		Users []marzbanUser `json:"users"`
	}
	trimmed := bytes.TrimLeft(raw, " \t\r\n\xef\xbb\xbf")
	if len(trimmed) > 0 && trimmed[0] == '[' {
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, ErrUnknownFormat
		}
	} else {
		if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Users == nil {
			return nil, ErrUnknownFormat
		}
		list = wrapped.Users
	}
	out := make([]Candidate, 0, len(list))
	for _, u := range list {
		if strings.TrimSpace(u.Username) == "" && len(u.Proxies) == 0 {
			continue // not a user object at all
		}
		out = append(out, u.candidate())
	}
	if len(out) == 0 && len(list) > 0 {
		return nil, ErrUnknownFormat
	}
	return out, nil
}

// ---- 3x-ui -----------------------------------------------------------------

// xuiClient is one entry of an inbound's settings.clients. The same person
// appears once per inbound they were added to, keyed by email, so the parser
// folds them: the UUID from a VLESS/VMess inbound, the password from a
// Trojan/Shadowsocks one, limits from whichever row states them.
type xuiClient struct {
	ID         string `json:"id"`       // vless / vmess
	Password   string `json:"password"` // trojan / shadowsocks
	Email      string `json:"email"`
	SubID      string `json:"subId"` // their /sub/<subId> token
	LimitIP    int    `json:"limitIp"`
	TotalGB    int64  `json:"totalGB"` // bytes, despite the name
	ExpiryTime int64  `json:"expiryTime"`
	Enable     *bool  `json:"enable"`
	Comment    string `json:"comment"`
}

func parseXUIDB(db *sql.DB) ([]Candidate, error) {
	rows, err := db.Query(`SELECT id, protocol, COALESCE(settings, '{}') FROM inbounds ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("importer: 3x-ui inbounds: %w", err)
	}
	defer rows.Close()
	type acc struct {
		c     Candidate
		order int
	}
	byEmail := map[string]*acc{}
	n := 0
	get := func(email string) *acc {
		a, ok := byEmail[email]
		if !ok {
			a = &acc{order: n, c: Candidate{Name: email, Enabled: true}}
			n++
			byEmail[email] = a
		}
		return a
	}
	for rows.Next() {
		var id int64
		var protocol, settings string
		if err := rows.Scan(&id, &protocol, &settings); err != nil {
			return nil, err
		}
		var s struct {
			Clients []xuiClient `json:"clients"`
		}
		if json.Unmarshal([]byte(settings), &s) != nil {
			continue
		}
		for _, cl := range s.Clients {
			email := strings.TrimSpace(cl.Email)
			if email == "" {
				email = strings.TrimSpace(cl.ID)
			}
			if email == "" {
				continue
			}
			a := get(email)
			switch strings.ToLower(protocol) {
			case "vless", "vmess":
				if a.c.UUID == "" {
					a.c.UUID = strings.TrimSpace(cl.ID)
				}
			case "trojan", "shadowsocks":
				if a.c.Password == "" {
					a.c.Password = strings.TrimSpace(cl.Password)
				}
			}
			if cl.TotalGB > 0 && a.c.DataLimit == 0 {
				a.c.DataLimit = cl.TotalGB
			}
			// Milliseconds; a negative value is "N days after first connection",
			// which has no date to carry over.
			if cl.ExpiryTime > 0 && a.c.ExpireAt == 0 {
				a.c.ExpireAt = cl.ExpiryTime / 1000
			} else if cl.ExpiryTime < 0 {
				a.c.Issues = appendIssue(a.c.Issues, IssueExpiryRelative)
			}
			if cl.LimitIP > 0 && a.c.DeviceLimit == 0 {
				a.c.DeviceLimit = cl.LimitIP
			}
			if cl.Enable != nil && !*cl.Enable {
				a.c.Enabled = false
			}
			if cl.Comment != "" && a.c.Note == "" {
				a.c.Note = strings.TrimSpace(cl.Comment)
			}
			if a.c.SubToken == "" {
				a.c.SubToken = subToken(cl.SubID)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Traffic lives in client_traffics, one row per (inbound, email); the person's
	// usage is the sum. Its expiry/total duplicate the client entry and are only
	// used when the settings JSON said nothing.
	trows, err := db.Query(`SELECT email, COALESCE(up, 0), COALESCE(down, 0), COALESCE(total, 0),
		COALESCE(expiry_time, 0), COALESCE(enable, 1) FROM client_traffics`)
	if err != nil {
		return nil, fmt.Errorf("importer: 3x-ui client_traffics: %w", err)
	}
	defer trows.Close()
	for trows.Next() {
		var email string
		var up, down, total, expiry int64
		var enable int
		if err := trows.Scan(&email, &up, &down, &total, &expiry, &enable); err != nil {
			return nil, err
		}
		a, ok := byEmail[strings.TrimSpace(email)]
		if !ok {
			continue
		}
		a.c.UsedUp += max(up, 0)
		a.c.UsedDown += max(down, 0)
		if a.c.DataLimit == 0 && total > 0 {
			a.c.DataLimit = total
		}
		if a.c.ExpireAt == 0 && expiry > 0 {
			a.c.ExpireAt = expiry / 1000
		}
		if enable == 0 {
			a.c.Enabled = false
		}
	}
	if err := trows.Err(); err != nil {
		return nil, err
	}
	list := make([]*acc, 0, len(byEmail))
	for _, a := range byEmail {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].order < list[j].order })
	out := make([]Candidate, 0, len(list))
	for _, a := range list {
		c := a.c
		c.setCredentials(c.UUID, c.Password)
		out = append(out, c)
	}
	return out, nil
}

// ---- shared ----------------------------------------------------------------

// subToken keeps a foreign panel's subscription token when it is usable as one of
// ours: the URL path is /<sub_path>/<token>, so anything with a separator, a query
// or whitespace in it is dropped rather than producing a link nobody can fetch. An
// absurdly long one is dropped too — it would be a parsing accident, not a token.
func subToken(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" || len(t) > 256 || strings.ContainsAny(t, "/?#&= \t\r\n") {
		return ""
	}
	return t
}

// lastPathSegment is the token half of a subscription URL ("/sub/<token>", or the
// absolute form), or "" when there is nothing after the last slash.
func lastPathSegment(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return ""
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		return u[i+1:]
	}
	return u
}

// setCredentials installs the UUID and password a source offered, minting what is
// missing or unusable and flagging that it did. An id that is not a UUID cannot be
// carried over: this panel's links and Xray config are built around the UUID form.
func (c *Candidate) setCredentials(id, password string) {
	if parsed, err := uuid.Parse(id); err == nil {
		c.UUID = parsed.String()
	} else {
		c.UUID = uuid.NewString()
		c.Issues = appendIssue(c.Issues, IssueUUIDGenerated)
	}
	if password != "" {
		c.Password = password
	} else {
		// 128 random bits as hex — the same entropy createUser's password carries,
		// minted here to keep this package free of the panel's auth code.
		c.Password = strings.ReplaceAll(uuid.NewString(), "-", "")
		c.Issues = appendIssue(c.Issues, IssuePasswordGenerated)
	}
	if c.Name == "" {
		c.Name = c.UUID[:8]
		c.Issues = appendIssue(c.Issues, IssueNameEmpty)
	}
	if c.Issues == nil {
		c.Issues = []string{}
	}
}

func appendIssue(issues []string, issue string) []string {
	for _, i := range issues {
		if i == issue {
			return issues
		}
	}
	return append(issues, issue)
}
