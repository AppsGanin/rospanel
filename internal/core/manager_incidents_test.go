package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// joinedNode adds a node the status page lists: enabled, and heard from.
func joinedNode(t *testing.T, m *Manager, name string) int64 {
	t.Helper()
	n, err := m.store.CreateNode(name, name+".example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.UpdateNodeStatus(n.ID, model.NodeStatusUpdate{LastSeen: time.Now().Unix(), ConfigHash: "h"}); err != nil {
		t.Fatal(err)
	}
	return n.ID
}

func validationCode(err error) string {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve.Code
	}
	return ""
}

func TestSaveIncidentValidates(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	nl := joinedNode(t, m, "NL")
	hidden, err := m.store.CreateNode("never joined", "x.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	cases := []struct {
		name string
		in   model.Incident
		code string
	}{
		{"unknown kind", model.Incident{Kind: "outage", Title: "x"}, "err.incidentKind"},
		{"no title", model.Incident{Kind: model.IncidentKindIncident, Title: "  "}, "err.incidentTitle"},
		{"long title", model.Incident{Kind: model.IncidentKindIncident, Title: strings.Repeat("я", model.IncidentTitleMax+1)}, "err.incidentTitleLong"},
		{"long text", model.Incident{Kind: model.IncidentKindIncident, Title: "x", Body: strings.Repeat("я", model.IncidentBodyMax+1)}, "err.incidentBodyLong"},
		{"window backwards", model.Incident{Kind: model.IncidentKindMaintenance, Title: "x", StartsAt: now + 100, EndsAt: now + 100}, "err.incidentWindow"},
		{"server not on the page", model.Incident{Kind: model.IncidentKindIncident, Title: "x", NodeIDs: []int64{hidden.ID}}, "err.incidentServer"},
		{"edit of nothing", model.Incident{ID: 999, Kind: model.IncidentKindIncident, Title: "x"}, "err.incidentNotFound"},
	}
	for _, c := range cases {
		if _, err := m.SaveIncident(c.in); validationCode(err) != c.code {
			t.Errorf("%s: got %v, want %s", c.name, err, c.code)
		}
	}

	// An incident is dated no later than now and has no end; servers are deduplicated
	// and sorted; the text is trimmed.
	in, err := m.SaveIncident(model.Incident{
		Kind: model.IncidentKindIncident, Title: "  NL down ", Body: "line 1\r\nline 2 ",
		NodeIDs: []int64{nl, model.LocalNodeID, nl}, StartsAt: now + 3600, EndsAt: now + 7200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if in.Title != "NL down" || in.Body != "line 1\nline 2" || in.StartsAt > time.Now().Unix() || in.EndsAt != 0 ||
		!slices.Equal(in.NodeIDs, []int64{model.LocalNodeID, nl}) || in.CreatedAt == 0 {
		t.Errorf("saved %+v", in)
	}

	// An edit keeps when it was posted and whether it was resolved.
	if _, _, err := m.ResolveIncident(in.ID, ""); err != nil {
		t.Fatal(err)
	}
	resolved, _ := m.store.GetIncident(in.ID)
	edit := *resolved
	edit.Title, edit.ResolvedAt, edit.CreatedAt = "NL was down", 0, 1
	out, err := m.SaveIncident(edit)
	if err != nil {
		t.Fatal(err)
	}
	if out.Title != "NL was down" || out.ResolvedAt != resolved.ResolvedAt || out.CreatedAt != resolved.CreatedAt {
		t.Errorf("edit lost its history: %+v (was %+v)", out, resolved)
	}

	// The kind is chosen once.
	turned := *out
	turned.Kind = model.IncidentKindMaintenance
	if _, err := m.SaveIncident(turned); validationCode(err) != "err.incidentKindLocked" {
		t.Errorf("an incident turned into maintenance: %v", err)
	}

	// A maintenance window with no end is fine: it lasts until resolved.
	if _, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindMaintenance, Title: "x", StartsAt: now + 60}); err != nil {
		t.Errorf("open-ended maintenance refused: %v", err)
	}
}

func TestResolveIncidentKeepsTheFirstTime(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	in, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindIncident, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	first, changed, err := m.ResolveIncident(in.ID, "")
	if err != nil || !changed || first.ResolvedAt == 0 {
		t.Fatalf("resolve: %+v %v %v", first, changed, err)
	}
	again, changed, err := m.ResolveIncident(in.ID, "")
	if err != nil || changed || again.ResolvedAt != first.ResolvedAt {
		t.Errorf("resolving twice: changed=%v, %d → %d (%v)", changed, first.ResolvedAt, again.ResolvedAt, err)
	}
	if _, _, err := m.ResolveIncident(in.ID+100, ""); validationCode(err) != "err.incidentNotFound" {
		t.Errorf("resolving nothing: %v", err)
	}
	if err := m.DeleteIncident(in.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteIncident(in.ID); validationCode(err) != "err.incidentNotFound" {
		t.Errorf("deleting twice: %v", err)
	}
}

// Maintenance that has not begun is moved or deleted, not "finished"; one whose
// window already closed keeps the close as its end.
func TestResolveMaintenanceByPhase(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	now := time.Now().Unix()
	ahead, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindMaintenance, Title: "ahead", StartsAt: now + 3600, EndsAt: now + 7200})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.ResolveIncident(ahead.ID, ""); validationCode(err) != "err.incidentNotStarted" {
		t.Errorf("finishing maintenance before it began: %v", err)
	}
	past, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindMaintenance, Title: "past", StartsAt: now - 7200, EndsAt: now - 3600})
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := m.ResolveIncident(past.ID, "")
	if err != nil || changed || got.ResolvedAt != 0 {
		t.Errorf("finishing a closed window: changed=%v resolved_at=%d (%v)", changed, got.ResolvedAt, err)
	}
	if err := m.NotifyIncident(context.Background(), got, i18n.RU, false); validationCode(err) != "err.incidentOver" {
		t.Errorf("announcing a finished window as current: %v", err)
	}
}

// A server that left the status page after the incident named it does not lock the
// incident against edits; a new one still has to be on the page.
func TestSaveIncidentKeepsAServerThatLeft(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	nl := joinedNode(t, m, "NL")
	de := joinedNode(t, m, "DE")
	in, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindIncident, Title: "NL", NodeIDs: []int64{nl}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeEnabled(nl, false); err != nil {
		t.Fatal(err)
	}
	in.Title = "NL, still"
	if _, err := m.SaveIncident(*in); err != nil {
		t.Errorf("editing an incident whose server was switched off: %v", err)
	}
	if err := m.store.SetNodeEnabled(de, false); err != nil {
		t.Fatal(err)
	}
	in.NodeIDs = []int64{nl, de}
	if _, err := m.SaveIncident(*in); validationCode(err) != "err.incidentServer" {
		t.Errorf("adding a server that is not on the page: %v", err)
	}
}

// The message is Telegram HTML: the operator's text must not become markup.
func TestIncidentMessage(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	nl := joinedNode(t, m, "NL <1>")
	set, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	set.StatusEnabled, set.StatusPath, set.Host = true, "status", "vpn.example.com"
	start := time.Date(2026, 10, 16, 12, 0, 0, 0, time.Local).Unix()
	in := &model.Incident{
		Kind: model.IncidentKindMaintenance, Title: "Works <b>now</b>", Body: "a & b",
		NodeIDs: []int64{nl}, StartsAt: start, EndsAt: start + 3*3600,
	}

	msg := m.incidentMessage(in, incidentNoticeFor(in, i18n.EN, false), set, i18n.EN)
	for _, want := range []string{
		"🛠 <b>Maintenance: Works &lt;b&gt;now&lt;/b&gt;</b>",
		"a &amp; b",
		"🕐 Oct 16, 12:00–15:00",
		"📍 NL &lt;1&gt;",
		"https://vpn.example.com/status",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}

	done := m.incidentMessage(in, incidentNoticeFor(in, i18n.RU, true), set, i18n.RU)
	if !strings.HasPrefix(done, "✅ <b>Работы завершены: ") || strings.Contains(done, "a &amp; b") || strings.Contains(done, "🕐") {
		t.Errorf("finished message:\n%s", done)
	}
	in.Kind = model.IncidentKindIncident
	if got := m.incidentMessage(in, incidentNoticeFor(in, i18n.RU, true), set, i18n.RU); !strings.HasPrefix(got, "✅ <b>Решено: ") {
		t.Errorf("resolved message:\n%s", got)
	}

	// No public page: no link to one. The whole service: no server line.
	set.StatusEnabled = false
	in.NodeIDs = nil
	if got := m.incidentMessage(in, incidentNoticeFor(in, i18n.RU, false), set, i18n.RU); strings.Contains(got, "https://") || strings.Contains(got, "📍") {
		t.Errorf("message with the page off / all servers:\n%s", got)
	}
}

// Escaping can grow the text past a Telegram message; the text is shortened, not
// the notice refused.
func TestIncidentMessageFitsTelegram(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	set, err := m.store.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	in := &model.Incident{Kind: model.IncidentKindIncident, Title: "x", Body: strings.Repeat("&", model.IncidentBodyMax)}
	msg := m.incidentMessage(in, incidentNoticeFor(in, i18n.RU, false), set, i18n.RU)
	if n := utf8.RuneCountInString(msg); n > broadcastTextMax || !strings.Contains(msg, "&amp;…") {
		t.Errorf("message of %d runes (limit %d), ends %q", n, broadcastTextMax, msg[len(msg)-20:])
	}
	in.Body = "short"
	if msg := m.incidentMessage(in, incidentNoticeFor(in, i18n.RU, false), set, i18n.RU); !strings.Contains(msg, "\n\nshort") || strings.Contains(msg, "…") {
		t.Errorf("a short text was touched: %q", msg)
	}
}

// Nobody to tell is an error the caller reports beside the saved incident.
func TestNotifyIncidentNeedsRecipients(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	in, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindIncident, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.NotifyIncident(context.Background(), in, i18n.RU, false); validationCode(err) != "err.audienceEmpty" {
		t.Errorf("notify with no active subscribers: %v", err)
	}
}

// A course takes the stages its kind allows, needs words, and a resolve's note
// closes it.
func TestIncidentCourse(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	now := time.Now().Unix()
	works, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindMaintenance, Title: "works", StartsAt: now - 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.AddIncidentUpdate(works.ID, model.IncidentStageIdentified, "x"); validationCode(err) != "err.incidentStage" {
		t.Errorf("an incident's stage on maintenance: %v", err)
	}
	if _, _, err := m.AddIncidentUpdate(works.ID, "", "  "); validationCode(err) != "err.incidentUpdateEmpty" {
		t.Errorf("an empty update: %v", err)
	}
	in, u, err := m.AddIncidentUpdate(works.ID, "", "halfway")
	if err != nil || u.Stage != model.IncidentStageUpdate || len(in.Updates) != 1 {
		t.Fatalf("plain update: %+v %+v %v", in, u, err)
	}
	if _, _, err := m.AddIncidentUpdate(works.ID, model.IncidentStageResolved, "x"); validationCode(err) != "err.incidentStage" {
		t.Errorf("a resolved step by hand: %v", err)
	}

	in, changed, err := m.ResolveIncident(works.ID, "  all done \r\n ")
	if err != nil || !changed {
		t.Fatal(err)
	}
	if last := in.Updates[len(in.Updates)-1]; last.Stage != model.IncidentStageResolved || last.Body != "all done" {
		t.Errorf("closing note: %+v", in.Updates)
	}
	set, _ := m.store.GetSettings()
	if msg := m.incidentMessage(in, incidentNoticeFor(in, i18n.RU, true), set, i18n.RU); !strings.Contains(msg, "Работы завершены: works</b>\n\nall done") {
		t.Errorf("the resolve message lacks the note:\n%s", msg)
	}
	// Resolving again adds no second note.
	if again, _, _ := m.ResolveIncident(works.ID, "again"); len(again.Updates) != len(in.Updates) {
		t.Errorf("a repeat resolve wrote a note: %+v", again.Updates)
	}
	if err := m.NotifyIncidentUpdate(context.Background(), in, u, i18n.RU); validationCode(err) != "err.incidentOver" {
		t.Errorf("announcing a step of a finished entry: %v", err)
	}
	if err := m.DeleteIncidentUpdate(u.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteIncidentUpdate(u.ID); validationCode(err) != "err.incidentUpdateNotFound" {
		t.Errorf("deleting an update twice: %v", err)
	}
}

// The subscription page hears of what is going on and of maintenance due within
// three days — not of the past, the far future, or a server off the status page.
func TestSubPageIncidents(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	nl := joinedNode(t, m, "NL")
	now := time.Now().Unix()
	add := func(in model.Incident) {
		t.Helper()
		if _, err := m.SaveIncident(in); err != nil {
			t.Fatal(err)
		}
	}
	add(model.Incident{Kind: model.IncidentKindIncident, Title: "now", NodeIDs: []int64{nl}})
	add(model.Incident{Kind: model.IncidentKindMaintenance, Title: "in two days", StartsAt: now + 2*86400, EndsAt: now + 2*86400 + 3600})
	add(model.Incident{Kind: model.IncidentKindMaintenance, Title: "tomorrow", StartsAt: now + 86400, EndsAt: now + 86400 + 3600})
	add(model.Incident{Kind: model.IncidentKindMaintenance, Title: "next week", StartsAt: now + 7*86400})
	add(model.Incident{Kind: model.IncidentKindMaintenance, Title: "over", StartsAt: now - 7200, EndsAt: now - 3600})
	gone, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindIncident, Title: "gone server", NodeIDs: []int64{nl}})
	if err != nil {
		t.Fatal(err)
	}
	other := joinedNode(t, m, "DE")
	gone.NodeIDs = []int64{other}
	if _, err := m.SaveIncident(*gone); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetNodeEnabled(other, false); err != nil {
		t.Fatal(err)
	}

	list, err := m.SubPageIncidents()
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, in := range list {
		titles = append(titles, in.Title)
	}
	if got := strings.Join(titles, "|"); got != "now|tomorrow|in two days" {
		t.Errorf("sub page incidents %q", got)
	}
}

// Webhooks hear the whole life of an entry.
func TestIncidentWebhooks(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	m.webhookCh = make(chan webhookJob, 64)
	if _, err := m.store.CreateWebhook("https://hooks.example/status", []string{model.WebhookIncidentCreated,
		model.WebhookIncidentUpdated, model.WebhookIncidentResolved, model.WebhookIncidentDeleted}, true); err != nil {
		t.Fatal(err)
	}
	in, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindIncident, Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	in.Title = "y"
	if _, err := m.SaveIncident(*in); err != nil {
		t.Fatal(err)
	}
	_, step, err := m.AddIncidentUpdate(in.ID, model.IncidentStageIdentified, "cause")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteIncidentUpdate(step.ID); err != nil { // taken back: the copy must drop it
		t.Fatal(err)
	}
	if _, _, err := m.ResolveIncident(in.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.ResolveIncident(in.ID, ""); err != nil { // a repeat is not an event
		t.Fatal(err)
	}
	if err := m.DeleteIncident(in.ID); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, job := range takeWebhooks(t, m.store) {
		var p struct {
			Event string         `json:"event"`
			Data  map[string]any `json:"data"`
		}
		if err := json.Unmarshal(job.Body, &p); err != nil {
			t.Fatal(err)
		}
		line := p.Event + " " + p.Data["title"].(string) + " " + p.Data["phase"].(string)
		if u, ok := p.Data["update"].(map[string]any); ok {
			line += " " + u["stage"].(string)
		}
		got = append(got, line)
	}
	want := "incident.created x active|incident.updated y active|incident.updated y active identified|" +
		"incident.updated y active|incident.resolved y done|incident.deleted y done"
	if strings.Join(got, "|") != want {
		t.Errorf("events:\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
}

func TestCompensateIncident(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	nl := joinedNode(t, m, "NL")
	de := joinedNode(t, m, "DE")
	now := time.Now().Unix()
	mk := func(name string, expire int64) int64 {
		t.Helper()
		u, err := m.store.CreateUser(name, "uuid-"+name, "pw", "tok-"+name, 0, expire, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.store.SetUserCreatedAt(u.ID, now-30*86400); err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	setExpire := func(id, at int64) {
		t.Helper()
		if err := m.store.ExecForTest(`UPDATE users SET expire_at = ? WHERE id = ?`, at, id); err != nil {
			t.Fatal(err)
		}
	}
	running := mk("running", now+10*86400)
	lapsed := mk("lapsed", now+600)      // runs out after the incident began
	before := mk("before", now-5*3600)   // ran out before it began
	renewed := mk("renewed", now-5*3600) // ran out before it began, renewed after
	forever := mk("forever", 0)          // no end: nothing to give back
	deOnly := mk("de-only", now+86400)   // may use DE only, the incident is on NL
	g, err := m.CreateGroup("de", []string{model.BuiltinToken(de, model.LaneVLESS)}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetUserGroups(deOnly, []int64{g.ID}); err != nil {
		t.Fatal(err)
	}

	in, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindIncident, Title: "NL down",
		NodeIDs: []int64{nl}, StartsAt: now - 4*3600})
	if err != nil {
		t.Fatal(err)
	}
	if in.SnapshotAt == 0 {
		t.Fatal("an incident posted under way recorded nobody (or the answer does not say so)")
	}
	// What happens after it began does not change who it hit.
	setExpire(lapsed, now-3600)
	setExpire(renewed, now+30*86400)

	ctx := context.Background()
	if _, err := m.CompensateIncident(ctx, in.ID, 3, false); validationCode(err) != "err.compensationNotOver" {
		t.Fatalf("open incident compensated: %v", err)
	}
	if _, _, err := m.ResolveIncident(in.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CompensateIncident(ctx, in.ID, 0, false); validationCode(err) != "err.compensationHours" {
		t.Fatalf("0 hours: %v", err)
	}
	got, err := m.CompensateIncident(ctx, in.ID, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.CompensatedAt == 0 || got.CompensatedSeconds != 4*3600 || got.CompensatedUsers != 2 {
		t.Fatalf("compensation recorded as %+v", got)
	}
	expire := func(id int64) int64 {
		u, err := m.store.GetUser(id)
		if err != nil {
			t.Fatal(err)
		}
		return u.ExpireAt
	}
	if e := expire(running); e != now+10*86400+4*3600 {
		t.Errorf("running term: expire %d, want %d", e, now+10*86400+4*3600)
	}
	if e := expire(lapsed); e < now+4*3600 || e > time.Now().Unix()+4*3600 {
		t.Errorf("lapsed term should get the hours from now, expire %d", e)
	}
	for name, c := range map[string]struct{ id, want int64 }{
		"before": {before, now - 5*3600}, "renewed": {renewed, now + 30*86400},
		"forever": {forever, 0}, "de-only": {deOnly, now + 86400},
	} {
		if e := expire(c.id); e != c.want {
			t.Errorf("%s: expire moved to %d", name, e)
		}
	}
	if _, err := m.CompensateIncident(ctx, in.ID, 4, false); validationCode(err) != "err.compensationDone" {
		t.Fatalf("second compensation: %v", err)
	}
	if e := expire(running); e != now+10*86400+4*3600 {
		t.Errorf("second compensation moved the term to %d", e)
	}
}

// A maintenance window records who it hits when it opens, not when it is posted.
func TestMaintenanceSnapshotAtOpening(t *testing.T) {
	t.Parallel()
	m := bcManager(t)
	now := time.Now().Unix()
	u, err := m.store.CreateUser("later", "uuid-later", "pw", "tok-later", 0, now-60, 0)
	if err != nil {
		t.Fatal(err)
	}
	in, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindMaintenance, Title: "works",
		StartsAt: now + 3600, EndsAt: now + 7200})
	if err != nil {
		t.Fatal(err)
	}
	if in, _ = m.store.GetIncident(in.ID); in.SnapshotAt != 0 {
		t.Fatal("a window that has not opened recorded its users")
	}
	if err := m.store.ExecForTest(`UPDATE users SET expire_at = ? WHERE id = ?`, now+86400, u.ID); err != nil {
		t.Fatal(err)
	}
	m.snapshotIncidents(now + 3600)
	in, _ = m.store.GetIncident(in.ID)
	cands, err := m.store.CompensationCandidates(in, now+7200)
	if err != nil {
		t.Fatal(err)
	}
	if in.SnapshotAt != now+3600 || len(cands) != 1 || cands[0].ID != u.ID {
		t.Fatalf("snapshot at %d, candidates %+v", in.SnapshotAt, cands)
	}
}

func TestCompensationHours(t *testing.T) {
	t.Parallel()
	in := &model.Incident{Kind: model.IncidentKindIncident, StartsAt: 1000, ResolvedAt: 1000 + 3601}
	if got := CompensationHours(in, 1_000_000); got != 2 {
		t.Fatalf("1h and a second = %d hours, want 2", got)
	}
	in.ResolvedAt = 1000
	if got := CompensationHours(in, 1_000_000); got != 1 {
		t.Fatalf("an instant = %d hours, want 1", got)
	}
}
