package core

import (
	"context"
	"html"
	"slices"
	"strings"
	"sync"
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

// subNoticeAhead is how far ahead the subscription page announces maintenance.
const subNoticeAhead = 3 * 24 * time.Hour

// subIncidentCache holds SubPageIncidents for subIncidentTTL: every subscription
// page opened asks, and the answer changes only when the operator writes or a
// window opens — half a minute late at worst, like the status page.
type subIncidentCache struct {
	mu   sync.Mutex
	at   time.Time
	list []model.Incident
}

const subIncidentTTL = 30 * time.Second

// snapshotIncidents records who the incidents under way hit, as they begin: one
// posted now at once, a maintenance window on the poll after it opens.
func (m *Manager) snapshotIncidents(now int64) {
	if err := m.store.SnapshotIncidents(now); err != nil {
		logErr("incidents: recording who they hit failed", "err", err)
	}
}

// incidentsChanged drops the cached answer: the operator expects the page to say
// what they just posted.
func (m *Manager) incidentsChanged() {
	m.subIncidents.mu.Lock()
	m.subIncidents.at = time.Time{}
	m.subIncidents.mu.Unlock()
}

// SubPageIncidents is what the subscription page tells its reader: what is going
// on now, then maintenance due within three days, soonest first. An entry whose
// servers have all left the status page is about nothing the reader can be on.
// The slice is shared: callers read it, never change it.
func (m *Manager) SubPageIncidents() ([]model.Incident, error) {
	c := &m.subIncidents
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < subIncidentTTL {
		return c.list, nil
	}
	list, err := m.subPageIncidents()
	if err != nil {
		return nil, err
	}
	c.list, c.at = list, time.Now()
	return list, nil
}

func (m *Manager) subPageIncidents() ([]model.Incident, error) {
	now := time.Now()
	list, err := m.store.IncidentsForPage(now.Unix(), now.Unix())
	if err != nil || len(list) == 0 {
		return nil, err
	}
	servers, err := m.IncidentServers()
	if err != nil {
		return nil, err
	}
	shown := func(in *model.Incident) bool {
		if len(in.NodeIDs) == 0 {
			return true
		}
		for _, s := range servers {
			if in.Concerns(s.ID) {
				return true
			}
		}
		return false
	}
	var active, ahead []model.Incident
	horizon := now.Add(subNoticeAhead).Unix()
	for i := range list {
		in := &list[i]
		if !shown(in) {
			continue
		}
		switch in.Phase(now.Unix()) {
		case model.IncidentActive:
			active = append(active, *in)
		case model.IncidentScheduled:
			if in.StartsAt <= horizon {
				ahead = append([]model.Incident{*in}, ahead...) // the store hands latest first
			}
		}
	}
	return append(active, ahead...), nil
}

// MaintenanceHiddenServers is the servers a maintenance window under way takes out
// of subscriptions (HideInSub, naming them). Read from the cached list the
// subscription page uses, the phase judged now: a window opens and closes on time.
func (m *Manager) MaintenanceHiddenServers() map[int64]bool {
	list, err := m.SubPageIncidents()
	if err != nil {
		return nil
	}
	now := time.Now().Unix()
	var out map[int64]bool
	for i := range list {
		in := &list[i]
		if !in.HideInSub || in.Kind != model.IncidentKindMaintenance || len(in.NodeIDs) == 0 ||
			in.Phase(now) != model.IncidentActive {
			continue
		}
		if out == nil {
			out = map[int64]bool{}
		}
		for _, id := range in.NodeIDs {
			out[id] = true
		}
	}
	return out
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
		// The kind is chosen once: the stages of its course depend on it, and an
		// incident turned into maintenance would carry steps maintenance cannot have.
		if model.ValidIncidentKind(in.Kind) && in.Kind != old.Kind {
			return nil, invalidCode("err.incidentKindLocked", "тип записи после создания не меняется — удалите её и создайте новую")
		}
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
		in.Updates = []model.IncidentUpdate{}
		m.snapshotIncidents(now)
		// Read back: the record of who it hits is the store's.
		if saved, err := m.store.GetIncident(in.ID); err == nil && saved != nil {
			in = *saved
		}
		m.incidentsChanged()
		m.emitIncident(model.WebhookIncidentCreated, &in, nil)
		return &in, nil
	}
	if ok, err := m.store.UpdateIncident(&in); err != nil {
		return nil, err
	} else if !ok {
		return nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	// Read back rather than assemble: whether it is resolved is the store's, and a
	// resolve may have landed while this edit was being made.
	m.incidentsChanged()
	saved, err := m.store.GetIncident(in.ID)
	if err != nil {
		return nil, err
	}
	if saved == nil { // deleted in the meantime
		return nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	m.emitIncident(model.WebhookIncidentUpdated, saved, nil)
	return saved, nil
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
		in.HideInSub = false
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
// Maintenance that has not begun cannot be finished; it is moved or deleted. note,
// when given, closes its course (stage resolved).
func (m *Manager) ResolveIncident(id int64, note string) (*model.Incident, bool, error) {
	note = strings.TrimSpace(strings.ReplaceAll(note, "\r\n", "\n"))
	if n := utf8.RuneCountInString(note); n > model.IncidentBodyMax {
		return nil, false, invalidCode("err.incidentBodyLong", "текст длиннее {{max}} символов", map[string]any{"max": model.IncidentBodyMax})
	}
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
	ok, err := m.store.ResolveIncident(id, now, note)
	if err != nil {
		return nil, false, err
	}
	m.incidentsChanged()
	if in, err = m.store.GetIncident(id); err != nil {
		return nil, false, err
	}
	if in == nil {
		return nil, false, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	if ok {
		m.emitIncident(model.WebhookIncidentResolved, in, nil)
	}
	return in, ok, nil
}

// AddIncidentUpdate posts a step in an incident's course. A course goes on after
// the end too (what was found afterwards), so a finished incident takes updates.
func (m *Manager) AddIncidentUpdate(id int64, stage, body string) (*model.Incident, *model.IncidentUpdate, error) {
	in, err := m.store.GetIncident(id)
	if err != nil {
		return nil, nil, err
	}
	if in == nil {
		return nil, nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	stage = strings.TrimSpace(stage)
	if stage == "" {
		stage = model.IncidentStageUpdate
	}
	if !model.ValidIncidentStage(in.Kind, stage) {
		return nil, nil, invalidCode("err.incidentStage", "такой этап не подходит для этой записи")
	}
	body = strings.TrimSpace(strings.ReplaceAll(body, "\r\n", "\n"))
	if body == "" {
		return nil, nil, invalidCode("err.incidentUpdateEmpty", "напишите, что изменилось")
	}
	if n := utf8.RuneCountInString(body); n > model.IncidentBodyMax {
		return nil, nil, invalidCode("err.incidentBodyLong", "текст длиннее {{max}} символов", map[string]any{"max": model.IncidentBodyMax})
	}
	u := model.IncidentUpdate{IncidentID: id, Stage: stage, Body: body, CreatedAt: time.Now().Unix()}
	if ok, err := m.store.AddIncidentUpdate(&u); err != nil {
		return nil, nil, err
	} else if !ok {
		return nil, nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	m.incidentsChanged()
	if in, err = m.store.GetIncident(id); err != nil {
		return nil, nil, err
	}
	if in == nil { // deleted in the meantime
		return nil, nil, invalidCode("err.incidentNotFound", "запись не найдена")
	}
	m.emitIncident(model.WebhookIncidentUpdated, in, &u)
	return in, &u, nil
}

// DeleteIncidentUpdate takes back one step of a course — posted by mistake.
func (m *Manager) DeleteIncidentUpdate(id int64) error {
	incident, err := m.store.DeleteIncidentUpdate(id)
	if err != nil {
		return err
	}
	if incident == 0 {
		return invalidCode("err.incidentUpdateNotFound", "обновление не найдено")
	}
	m.incidentsChanged()
	// A copy of the course kept elsewhere has to drop the step too.
	if in, err := m.store.GetIncident(incident); err == nil && in != nil {
		m.emitIncident(model.WebhookIncidentUpdated, in, nil)
	}
	return nil
}

// emitIncident tells webhooks and plugins about an incident. update is the step
// that was just posted, for incident.updated.
func (m *Manager) emitIncident(event string, in *model.Incident, update *model.IncidentUpdate) {
	if !m.webhookWanted(event) {
		return
	}
	set, err := m.store.GetSettings()
	if err != nil {
		return
	}
	nodes := in.NodeIDs
	if nodes == nil {
		nodes = []int64{}
	}
	updates := in.Updates
	if updates == nil {
		updates = []model.IncidentUpdate{}
	}
	d := map[string]any{
		"id": in.ID, "kind": in.Kind, "title": in.Title, "body": in.Body,
		"node_ids": nodes, "servers": m.incidentServerNames(in),
		"starts_at": in.StartsAt, "ends_at": in.EndsAt, "resolved_at": in.ResolvedAt,
		"hide_in_sub": in.HideInSub,
		"phase":       in.Phase(time.Now().Unix()), "updates": updates,
		"status_url": statusPageURL(set),
	}
	if update != nil {
		d["update"] = update
	}
	m.EmitWebhook(event, d)
}

// DeleteIncident removes an incident from the page and the history.
func (m *Manager) DeleteIncident(id int64) error {
	in, err := m.store.GetIncident(id)
	if err != nil {
		return err
	}
	if in == nil {
		return invalidCode("err.incidentNotFound", "запись не найдена")
	}
	ok, err := m.store.DeleteIncident(id)
	if err != nil {
		return err
	}
	if !ok {
		return invalidCode("err.incidentNotFound", "запись не найдена")
	}
	m.incidentsChanged()
	m.emitIncident(model.WebhookIncidentDeleted, in, nil)
	return nil
}

// NotifyIncident sends the incident to every user with an active subscription
// through the user bot, worded in lang (the operator's own text is sent as typed).
// resolved announces that it is over instead, with the closing note if one was
// given.
func (m *Manager) NotifyIncident(ctx context.Context, in *model.Incident, lang i18n.Lang, resolved bool) error {
	if !resolved && in.Phase(time.Now().Unix()) == model.IncidentDone {
		// Announcing an entry that is over would read to every user as current.
		return invalidCode("err.incidentOver", "запись уже завершена — сообщать не о чем")
	}
	return m.sendIncidentNotice(ctx, in, incidentNoticeFor(in, lang, resolved), lang)
}

// incidentNoticeFor is the message announcing an incident, or its end.
func incidentNoticeFor(in *model.Incident, lang i18n.Lang, resolved bool) incidentNotice {
	n := incidentNotice{icon: "⚠️", head: i18n.T(lang, "status.kindIncident"), text: in.Body, window: true}
	switch {
	case resolved:
		n = incidentNotice{icon: "✅", head: i18n.T(lang, "status.msgResolved")}
		if in.Kind == model.IncidentKindMaintenance {
			n.head = i18n.T(lang, "status.msgMaintenanceDone")
		}
		if k := len(in.Updates); k > 0 && in.Updates[k-1].Stage == model.IncidentStageResolved {
			n.text = in.Updates[k-1].Body
		}
	case in.Kind == model.IncidentKindMaintenance:
		n.icon, n.head = "🛠", i18n.T(lang, "status.kindMaintenance")
	}
	return n
}

// NotifyIncidentUpdate sends one step of an incident's course to the same users.
func (m *Manager) NotifyIncidentUpdate(ctx context.Context, in *model.Incident, u *model.IncidentUpdate, lang i18n.Lang) error {
	if in.Phase(time.Now().Unix()) == model.IncidentDone {
		return invalidCode("err.incidentOver", "запись уже завершена — сообщать не о чем")
	}
	return m.sendIncidentNotice(ctx, in, incidentNotice{
		icon: "🔄", head: status.StageLabel(lang, in.Kind, u.Stage), text: u.Body,
	}, lang)
}

// incidentNotice is one message about an incident: its icon and heading word, the
// text under the title, and whether to state the maintenance window.
type incidentNotice struct {
	icon, head, text string
	window           bool
}

func (m *Manager) sendIncidentNotice(ctx context.Context, in *model.Incident, n incidentNotice, lang i18n.Lang) error {
	set, err := m.store.GetSettings()
	if err != nil {
		return err
	}
	text := m.incidentMessage(in, n, set, lang)
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
func (m *Manager) incidentMessage(in *model.Incident, n incidentNotice, set *model.Settings, lang i18n.Lang) string {
	msg := m.incidentHTML(in, n, n.text, set, lang)
	if utf8.RuneCountInString(msg) <= broadcastTextMax {
		return msg
	}
	// The longest start of the text that fits with an ellipsis: the message grows
	// with the prefix, so a binary search finds it.
	body := []rune(n.text)
	fits := func(k int) (string, bool) {
		text := ""
		if k > 0 {
			text = strings.TrimSpace(string(body[:k])) + "…"
		}
		out := m.incidentHTML(in, n, text, set, lang)
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

func (m *Manager) incidentHTML(in *model.Incident, n incidentNotice, text string, set *model.Settings, lang i18n.Lang) string {
	var sb strings.Builder
	sb.WriteString(n.icon + " <b>" + html.EscapeString(n.head) + ": " + html.EscapeString(in.Title) + "</b>")
	if text != "" {
		sb.WriteString("\n\n" + html.EscapeString(text))
	}
	var facts []string
	if n.window && in.Kind == model.IncidentKindMaintenance {
		facts = append(facts, "🕐 "+html.EscapeString(status.Window(lang, m.loc(), in.StartsAt, in.EndsAt)))
	}
	if names := m.incidentServerNames(in); names != "" {
		facts = append(facts, "📍 "+html.EscapeString(names))
	}
	if len(facts) > 0 {
		sb.WriteString("\n\n" + strings.Join(facts, "\n"))
	}
	if url := statusPageURL(set); url != "" {
		sb.WriteString("\n\n" + html.EscapeString(url))
	}
	return sb.String()
}

// statusPageURL is the public status page's address, "" while it is off.
func statusPageURL(set *model.Settings) string {
	if !set.StatusEnabled || set.Host == "" {
		return ""
	}
	return "https://" + set.Host + "/" + set.StatusPathOr()
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
