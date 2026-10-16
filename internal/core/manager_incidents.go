package core

import (
	"context"
	"html"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/status"
)

// Incidents and maintenance windows on the public status page. The operator writes
// them; the page shows what is going on, what is planned and what happened lately,
// and the user bot can tell everyone with an active subscription.

// incidentListMax bounds the panel's list. Old entries stay in the table (the page
// reads only recent ones); the editor has no use for years of them.
const incidentListMax = 200

// IncidentServer is a server an incident can name: the ones the status page shows.
type IncidentServer struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListIncidents returns the newest incidents first.
func (m *Manager) ListIncidents() ([]model.Incident, error) {
	return m.store.ListIncidents(incidentListMax)
}

// IncidentServers lists the servers the status page shows, in its order.
func (m *Manager) IncidentServers() ([]IncidentServer, error) {
	set, err := m.store.GetSettings()
	if err != nil {
		return nil, err
	}
	nodes, err := m.store.ListNodes()
	if err != nil {
		return nil, err
	}
	out := []IncidentServer{{ID: model.LocalNodeID, Name: masterStatusName(set)}}
	slices.SortFunc(nodes, func(a, b model.Node) int { return int(a.ID - b.ID) })
	for i := range nodes {
		if statusShows(&nodes[i]) {
			out = append(out, IncidentServer{ID: nodes[i].ID, Name: nodes[i].Name})
		}
	}
	return out, nil
}

// SaveIncident creates an incident (ID 0) or rewrites one, returning what was stored.
// Whether it is resolved is not set here: ResolveIncident does that.
func (m *Manager) SaveIncident(in model.Incident) (*model.Incident, error) {
	now := time.Now().Unix()
	in.Kind = strings.TrimSpace(in.Kind)
	in.Title = strings.TrimSpace(in.Title)
	in.Body = strings.TrimSpace(strings.ReplaceAll(in.Body, "\r\n", "\n"))
	var old *model.Incident
	if in.ID != 0 {
		var err error
		if old, err = m.store.GetIncident(in.ID); err != nil {
			return nil, err
		}
		if old == nil {
			return nil, invalidCode("err.incidentNotFound", "запись не найдена")
		}
	}
	var kept []int64
	if old != nil {
		kept = old.NodeIDs
	}
	if err := m.validateIncident(&in, now, kept); err != nil {
		return nil, err
	}
	in.UpdatedAt = now
	if old == nil {
		in.CreatedAt = now
		in.ResolvedAt = 0
		if err := m.store.CreateIncident(&in); err != nil {
			return nil, err
		}
		return &in, nil
	}
	if ok, err := m.store.UpdateIncident(&in); err != nil {
		return nil, err
	} else if !ok {
		return nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	// Read back rather than assemble: whether it is resolved is the store's, and a
	// resolve may have landed while this edit was being made.
	return m.store.GetIncident(in.ID)
}

// validateIncident checks and normalises what the operator wrote. kept are the
// servers the incident already named: one that has since left the status page
// (switched off, deleted) stays acceptable, or the entry could never be saved again.
func (m *Manager) validateIncident(in *model.Incident, now int64, kept []int64) error {
	if !model.ValidIncidentKind(in.Kind) {
		return invalidCode("err.incidentKind", "неизвестный тип записи")
	}
	if in.Title == "" {
		return invalidCode("err.incidentTitle", "укажите заголовок")
	}
	if n := utf8.RuneCountInString(in.Title); n > model.IncidentTitleMax {
		return invalidCode("err.incidentTitleLong", "заголовок длиннее {{max}} символов", map[string]any{"max": model.IncidentTitleMax})
	}
	if n := utf8.RuneCountInString(in.Body); n > model.IncidentBodyMax {
		return invalidCode("err.incidentBodyLong", "текст длиннее {{max}} символов", map[string]any{"max": model.IncidentBodyMax})
	}
	if in.StartsAt <= 0 {
		in.StartsAt = now
	}
	if in.Kind == model.IncidentKindIncident {
		// An incident is something that already happened; one dated ahead would sit
		// on the page as current before it began.
		in.StartsAt = min(in.StartsAt, now)
		in.EndsAt = 0
	} else if in.EndsAt != 0 && in.EndsAt <= in.StartsAt {
		return invalidCode("err.incidentWindow", "конец работ должен быть позже начала")
	}
	servers, err := m.IncidentServers()
	if err != nil {
		return err
	}
	known := make(map[int64]bool, len(servers))
	for _, s := range servers {
		known[s.ID] = true
	}
	ids := make([]int64, 0, len(in.NodeIDs))
	for _, id := range in.NodeIDs {
		if !known[id] && !slices.Contains(kept, id) {
			return invalidCode("err.incidentServer", "такого сервера нет на странице статуса")
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	in.NodeIDs = ids
	return nil
}

// ResolveIncident marks an incident resolved, or a maintenance window finished,
// now, and reports whether this call did it. One that is already over — resolved
// before, or a window that closed by itself — is left as it is: its end is history.
// Maintenance that has not begun cannot be finished; it is moved or deleted.
func (m *Manager) ResolveIncident(id int64) (*model.Incident, bool, error) {
	in, err := m.store.GetIncident(id)
	if err != nil {
		return nil, false, err
	}
	if in == nil {
		return nil, false, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	now := time.Now().Unix()
	switch in.Phase(now) {
	case model.IncidentDone:
		return in, false, nil
	case model.IncidentScheduled:
		return nil, false, invalidCode("err.incidentNotStarted", "работы ещё не начались — перенесите их или удалите запись")
	}
	ok, err := m.store.ResolveIncident(id, now)
	if err != nil {
		return nil, false, err
	}
	if in, err = m.store.GetIncident(id); err != nil {
		return nil, false, err
	}
	if in == nil {
		return nil, false, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	return in, ok, nil
}

// DeleteIncident removes an incident from the page and the history.
func (m *Manager) DeleteIncident(id int64) error {
	ok, err := m.store.DeleteIncident(id)
	if err != nil {
		return err
	}
	if !ok {
		return invalidCode("err.incidentNotFound", "запись не найдена")
	}
	return nil
}

// NotifyIncident sends the incident to every user with an active subscription
// through the user bot, worded in lang (the operator's own text is sent as typed).
// resolved announces that it is over instead.
func (m *Manager) NotifyIncident(ctx context.Context, in *model.Incident, lang i18n.Lang, resolved bool) error {
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	if !resolved && in.Phase(time.Now().Unix()) == model.IncidentDone {
		// Announcing an entry that is over would read to every user as current.
		return invalidCode("err.incidentOver", "запись уже завершена — сообщать не о чем")
	}
	text := m.incidentMessage(in, set, lang, resolved)
	b, err := m.CreateBroadcast(ctx, &model.Broadcast{Text: text, Audience: model.AudienceActive})
	if err != nil {
		return err
	}
	// CreateBroadcast leaves it paused; one that cannot start must not linger in the
	// list for someone to resume later with a stale notice.
	if err := m.StartBroadcast(b.ID); err != nil {
		m.AbandonBroadcast(b.ID)
		return err
	}
	return nil
}

// incidentMessage is the Telegram HTML the users get, kept within a message's
// length: escaping can grow the operator's text several times over (every & is five
// characters), so the text is shortened rather than the notice refused.
func (m *Manager) incidentMessage(in *model.Incident, set *model.Settings, lang i18n.Lang, resolved bool) string {
	msg := m.incidentHTML(in, in.Body, set, lang, resolved)
	if utf8.RuneCountInString(msg) <= broadcastTextMax {
		return msg
	}
	// The longest start of the text that fits with an ellipsis: the message grows
	// with the prefix, so a binary search finds it.
	body := []rune(in.Body)
	fits := func(n int) (string, bool) {
		text := ""
		if n > 0 {
			text = strings.TrimSpace(string(body[:n])) + "…"
		}
		out := m.incidentHTML(in, text, set, lang, resolved)
		return out, utf8.RuneCountInString(out) <= broadcastTextMax
	}
	lo, hi := 0, len(body)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if _, ok := fits(mid); ok {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	msg, _ = fits(lo)
	return msg
}

func (m *Manager) incidentHTML(in *model.Incident, bodyText string, set *model.Settings, lang i18n.Lang, resolved bool) string {
	var head, icon string
	switch {
	case resolved && in.Kind == model.IncidentKindMaintenance:
		icon, head = "✅", i18n.T(lang, "status.msgMaintenanceDone")
	case resolved:
		icon, head = "✅", i18n.T(lang, "status.msgResolved")
	case in.Kind == model.IncidentKindMaintenance:
		icon, head = "🛠", i18n.T(lang, "status.kindMaintenance")
	default:
		icon, head = "⚠️", i18n.T(lang, "status.kindIncident")
	}
	var sb strings.Builder
	sb.WriteString(icon + " <b>" + html.EscapeString(head) + ": " + html.EscapeString(in.Title) + "</b>")
	if bodyText != "" && !resolved {
		sb.WriteString("\n\n" + html.EscapeString(bodyText))
	}
	var facts []string
	if in.Kind == model.IncidentKindMaintenance && !resolved {
		facts = append(facts, "🕐 "+html.EscapeString(status.Window(lang, m.loc(), in.StartsAt, in.EndsAt)))
	}
	if names := m.incidentServerNames(in); names != "" {
		facts = append(facts, "📍 "+html.EscapeString(names))
	}
	if len(facts) > 0 {
		sb.WriteString("\n\n" + strings.Join(facts, "\n"))
	}
	if set.StatusEnabled && set.Host != "" {
		sb.WriteString("\n\n" + html.EscapeString("https://"+set.Host+"/"+set.StatusPathOr()))
	}
	return sb.String()
}

// incidentServerNames is the servers an incident names, "" when it is the whole
// service (or every server it named is gone).
func (m *Manager) incidentServerNames(in *model.Incident) string {
	if len(in.NodeIDs) == 0 {
		return ""
	}
	servers, err := m.IncidentServers()
	if err != nil {
		return ""
	}
	var names []string
	for _, s := range servers {
		if in.Concerns(s.ID) {
			names = append(names, s.Name)
		}
	}
	return strings.Join(names, ", ")
}
