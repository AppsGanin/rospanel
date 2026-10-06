package core

import (
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// RegistrationInfo is what the operator sees of a sign-up request before deciding
// it: who is asking — a name alone is ambiguous — and where they came from.
type RegistrationInfo struct {
	Username  string `json:"username,omitempty"`   // Telegram @username, without the @
	Lang      string `json:"lang,omitempty"`       // Telegram interface language
	StartedAt int64  `json:"started_at,omitempty"` // first /start
	Source    string `json:"source,omitempty"`     // the tag the request came with
	// Referrer is who invited them: their account, as the operator would find it.
	ReferrerID   int64  `json:"referrer_id,omitempty"`
	ReferrerName string `json:"referrer_name,omitempty"`
	// Blacklisted: the Telegram is on the shared blacklist, for this reason.
	Blacklisted     bool   `json:"blacklisted,omitempty"`
	BlacklistReason string `json:"blacklist_reason,omitempty"`
}

// RegistrationInfoOf gathers it for one request.
func (m *Manager) RegistrationInfoOf(r model.RegistrationRequest) RegistrationInfo {
	var info RegistrationInfo
	source, ref := r.Source, r.ReferrerID
	if r.ChatID != 0 {
		if sub, err := m.store.SubscriberByChat(r.ChatID); err == nil && sub != nil {
			info.Username, info.Lang, info.StartedAt = sub.Username, sub.Lang, sub.StartedAt
		}
		source, ref = m.store.SubscriberOrigin(r.ChatID)
		info.BlacklistReason, info.Blacklisted = m.Blacklisted(r.ChatID)
	}
	info.Source = source
	if ref != 0 {
		info.ReferrerID = ref
		if u, err := m.store.GetUser(ref); err == nil {
			info.ReferrerName = u.Name
		}
	}
	return info
}

// moderationPrompt is the admin bot's text for a request: who (HTML) and the lines
// that tell them apart — source, inviter, blacklist.
func (m *Manager) moderationPrompt(r model.RegistrationRequest) (who, details string) {
	lang := m.botLang()
	if r.ChatID != 0 {
		who = m.adminChat(r.Name, r.ChatID)
	} else {
		who = escHTML(r.Name) + " (" + escHTML(r.ExternalID) + ")"
	}
	info := m.RegistrationInfoOf(r)
	var lines []string
	if info.Source != "" {
		lines = append(lines, i18n.T(lang, "admin.regSource", escHTML(info.Source)))
	}
	if info.ReferrerID != 0 {
		lines = append(lines, i18n.T(lang, "admin.regReferrer", m.adminUserByID(info.ReferrerID, info.ReferrerName)))
	}
	if info.StartedAt > 0 {
		lines = append(lines, i18n.T(lang, "admin.regFirstSeen", time.Unix(info.StartedAt, 0).In(m.loc()).Format("02.01.2006 15:04")))
	}
	if info.Blacklisted {
		lines = append(lines, i18n.T(lang, "admin.regBlacklisted", escHTML(info.BlacklistReason)))
	}
	return who, strings.Join(lines, "\n")
}
