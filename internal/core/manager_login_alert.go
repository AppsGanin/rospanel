package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/actor"
	"github.com/AppsGanin/rospanel/internal/model"
)

// A sign-in to the panel from an address the admin has not used before, told to
// the admin chats with a button that ends every session of that account. The
// alert is the cheapest second factor there is: it costs the operator nothing
// until the day a password leaks, and on that day it is the difference between
// finding out from the bot and finding out from the users.
//
// "Not used before" is judged against the audit trail — every successful sign-in
// leaves a row with its address — over its retention window, so a laptop taken to
// a café alerts once, and the office address never does.

// LoginAlert is one such sign-in, as handed to the bot.
type LoginAlert struct {
	AdminID  int64
	Username string
	IP       string
	Country  string // ISO-2 from the geo table, "" when unknown
	Org      string // the network's name, "" when unknown
	Client   string // browser · OS, as ClientLabel words it
	At       int64
}

// SetAdminLoginNotifier registers the admin bot's hook for LoginAlert. Nil clears it.
func (m *Manager) SetAdminLoginNotifier(fn func(LoginAlert)) {
	m.notifyMu.Lock()
	m.adminLogin = fn
	m.notifyMu.Unlock()
}

// NotifyAdminLogin fills in where the address is and hands the alert to the bot,
// when the category is on. Safe to call from a goroutine: the login handler must
// not wait on Telegram.
func (m *Manager) NotifyAdminLogin(a LoginAlert) {
	m.notifyMu.Lock()
	fn := m.adminLogin
	m.notifyMu.Unlock()
	if fn == nil {
		return
	}
	set, err := m.store.GetSettings()
	if err != nil || !set.AdminEventEnabled(model.AdminEventLogin) {
		return
	}
	a.Country = m.CountryOfIP(a.IP)
	_, a.Org = m.ASNOfIP(a.IP)
	if a.At == 0 {
		a.At = time.Now().Unix()
	}
	fn(a)
}

// AdminLoginIsNew reports whether an admin has signed in from ip within the audit
// window before now. Called BEFORE the sign-in's own audit row is written, or it
// would always find itself.
func (m *Manager) AdminLoginIsNew(username, ip string) bool {
	since := time.Now().AddDate(0, 0, -model.AdminAuditRetentionDays).Unix()
	seen, err := m.store.AdminLoginSeenFrom(username, ip, since)
	if err != nil {
		// Unknown is not "new": a query error must not turn into an alert storm.
		logErr("login alert: could not read the sign-in history", "err", err)
		return false
	}
	return !seen
}

// RevokeAdminSessions ends every session of an admin — the bot's "that was not
// me" — and records who pressed the button. Returns the account's name and how
// many sessions went.
func (m *Manager) RevokeAdminSessions(ctx context.Context, adminID int64) (string, int64, error) {
	adm, err := m.store.GetAdmin(adminID)
	if err != nil {
		return "", 0, err
	}
	n, err := m.store.DeleteAllAdminSessions(adminID)
	if err != nil {
		return adm.Username, 0, err
	}
	who := actor.From(ctx)
	m.AddAdminAudit(model.AdminAudit{
		Action: model.AuditSessionRevoked, Target: adm.Username,
		ActorKind: who.Kind, ActorName: who.Name,
		Details: map[string]any{"all": true, "count": n, "reason": "login_alert"},
	})
	logInfo("login alert: all sessions revoked from the bot", "admin", adm.Username, "sessions", n, "by", who.Name)
	return adm.Username, n, nil
}

// ClientLabel names the browser and the system from a User-Agent, the way the
// sessions screen does — the two words a person recognises their own device by.
// Falls back to the header's first token so an unknown client is still named.
func ClientLabel(ua string) string {
	has := func(s string) bool { return strings.Contains(ua, s) }
	browser := ""
	switch {
	case has("Edg/"):
		browser = "Edge"
	case has("OPR/"), has("Opera"):
		browser = "Opera"
	case has("YaBrowser"):
		browser = "Yandex"
	case has("Firefox/"):
		browser = "Firefox"
	case has("Chrome/"), has("CriOS/"):
		browser = "Chrome"
	case has("Safari/"):
		browser = "Safari"
	}
	os := ""
	switch {
	case has("iPad"):
		os = "iPad"
	case has("iPhone"):
		os = "iPhone"
	case has("Android"):
		os = "Android"
	case has("Windows"):
		os = "Windows"
	case has("Mac OS X"), has("Macintosh"):
		os = "macOS"
	case has("CrOS"):
		os = "ChromeOS"
	case has("Linux"):
		os = "Linux"
	}
	switch {
	case browser != "" && os != "":
		return fmt.Sprintf("%s · %s", browser, os)
	case browser != "":
		return browser
	case os != "":
		return os
	}
	if f := strings.FieldsFunc(ua, func(r rune) bool { return r == ' ' || r == '/' }); len(f) > 0 {
		return f[0]
	}
	return ""
}
