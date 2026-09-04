package model

import (
	"fmt"
	"strings"
	"time"
)

// Connection names can carry variables. The operator writes them into a lane's name
// or a custom inbound's name; the panel expands them when it renders a share link, a
// Clash proxy name or a sing-box tag — so what the client shows can say where the
// server is and how much of the user's quota is left, per user, without the operator
// maintaining one name per person.
//
// Deliberately a fixed list rather than a template language. These names are embedded
// into a Clash document, a sing-box tag, a link fragment and an HTML page, and every
// one of those has its own escaping rules; a fixed vocabulary of values the panel
// itself produces is the only version of this that cannot be turned into an injection
// by an operator who pastes something clever into a name field.
//
// An unknown placeholder is left exactly as typed. It is the operator's own text and
// every surface escapes it, so it renders as a curiosity rather than as a break.
const (
	NameVarFlag    = "{flag}"    // 🇳🇱 — the server's country as an emoji flag
	NameVarCountry = "{country}" // NL
	NameVarServer  = "{server}"  // the server's display name (see NameVars.Server)
	NameVarUser    = "{user}"    // the account name
	NameVarUsed    = "{used}"    // traffic spent, e.g. "12.4 GB"
	NameVarLeft    = "{left}"    // traffic remaining, or ∞ on an unlimited plan
	NameVarTotal   = "{total}"   // the quota itself, or ∞
	NameVarExpire  = "{expire}"  // the expiry date, YYYY-MM-DD, or ∞
	NameVarDays    = "{days}"    // whole days until expiry, or ∞
)

// NameVars are the values a connection-name template is rendered against.
//
// User is optional: the panel also renders these names where no particular user is in
// hand (the access-group editor lists lanes as things to tick, not as one person's
// connections). Those renders fill the user-dependent variables with NameUnknown
// rather than dropping them, so the operator can still see the shape of the name they
// wrote instead of a mysteriously shorter one.
type NameVars struct {
	Server  string
	Country string
	User    *User
	Loc     *time.Location
}

// NameUnknown is what a variable renders as when the value is not available in this
// context — a user-dependent one rendered without a user, or an unset country.
const NameUnknown = "—"

// NameVarList is every variable, in the order the UI offers them.
var NameVarList = []string{
	NameVarFlag, NameVarCountry, NameVarServer, NameVarUser,
	NameVarUsed, NameVarLeft, NameVarTotal, NameVarExpire, NameVarDays,
}

// HasNameVar reports whether a name uses a particular variable. The panel asks this
// about {server}: a name that places the server itself takes over the automatic
// "<server> · <lane>" prefix rather than being prefixed on top of its own answer.
func HasNameVar(name, v string) bool { return strings.Contains(name, v) }

// UsesNameVars reports whether a name carries any variable at all — the cheap check
// that keeps the ordinary case (a plain name) from touching the renderer.
func UsesNameVars(name string) bool {
	if !strings.ContainsRune(name, '{') {
		return false
	}
	for _, v := range NameVarList {
		if strings.Contains(name, v) {
			return true
		}
	}
	return false
}

// RenderName expands the variables in a connection name. A name with no variables is
// returned untouched, which is every name that existed before this feature.
func RenderName(name string, v NameVars) string {
	if !UsesNameVars(name) {
		return name
	}
	r := strings.NewReplacer(
		NameVarFlag, orUnknown(CountryFlag(v.Country)),
		NameVarCountry, orUnknown(strings.ToUpper(strings.TrimSpace(v.Country))),
		NameVarServer, orUnknown(v.Server),
		NameVarUser, orUnknown(userField(v.User, func(u *User) string { return u.Name })),
		NameVarUsed, orUnknown(userField(v.User, func(u *User) string { return NameBytes(u.UsedUp + u.UsedDown) })),
		NameVarLeft, orUnknown(userField(v.User, quotaLeft)),
		NameVarTotal, orUnknown(userField(v.User, func(u *User) string { return quotaOf(u.DataLimit) })),
		NameVarExpire, orUnknown(userField(v.User, func(u *User) string { return expireOn(u, v.Loc) })),
		NameVarDays, orUnknown(userField(v.User, daysLeft)),
	)
	// Collapsing runs of spaces is what keeps "{flag} {country}" from leaving a gap
	// when one of the two is empty rather than unknown.
	return strings.TrimSpace(strings.Join(strings.Fields(r.Replace(name)), " "))
}

func orUnknown(s string) string {
	if s == "" {
		return NameUnknown
	}
	return s
}

func userField(u *User, f func(*User) string) string {
	if u == nil {
		return ""
	}
	return f(u)
}

// NameInfinite is how an absent limit reads. The glyph rather than a word because the
// name has to stay short enough to fit a client's server list.
const NameInfinite = "∞"

func quotaOf(limit int64) string {
	if limit <= 0 {
		return NameInfinite
	}
	return NameBytes(limit)
}

func quotaLeft(u *User) string {
	if u.DataLimit <= 0 {
		return NameInfinite
	}
	left := u.DataLimit - (u.UsedUp + u.UsedDown)
	if left < 0 {
		left = 0
	}
	return NameBytes(left)
}

func expireOn(u *User, loc *time.Location) string {
	if u.ExpireAt <= 0 {
		return NameInfinite
	}
	if loc == nil {
		loc = time.UTC
	}
	return time.Unix(u.ExpireAt, 0).In(loc).Format("2006-01-02")
}

func daysLeft(u *User) string {
	if u.ExpireAt <= 0 {
		return NameInfinite
	}
	// Rounded down and floored at zero: "0" on the last day is the honest answer, and
	// a negative count on an already-expired account would only confuse.
	d := (u.ExpireAt - time.Now().Unix()) / 86400
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d", d)
}

// NameBytes renders a byte count the way a connection name wants it: short, at most
// one decimal, and never the bare "0 B" that would read as a broken value.
func NameBytes(n int64) string {
	if n <= 0 {
		return "0"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v, i := float64(n), 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 && i > 0 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

// CountryFlag renders a two-letter country code as its emoji flag, or "" for anything
// that is not one. The glyph is two regional-indicator runes: 'A' maps to U+1F1E6, and
// a pair of them is what every platform draws as a flag.
//
// A copy of geo.Flag rather than a call to it: model is the bottom of the import graph
// and nothing above it may be pulled in here. The algorithm is four lines of the
// Unicode spec and has no version to drift with.
func CountryFlag(code string) string {
	code = strings.TrimSpace(code)
	if len(code) != 2 {
		return ""
	}
	out := make([]rune, 0, 2)
	for i := 0; i < 2; i++ {
		c := code[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c < 'A' || c > 'Z' {
			return ""
		}
		out = append(out, rune(c-'A')+0x1F1E6)
	}
	return string(out)
}
