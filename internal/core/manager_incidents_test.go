package core

import (
	"context"
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
	if _, _, err := m.ResolveIncident(in.ID); err != nil {
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
	first, changed, err := m.ResolveIncident(in.ID)
	if err != nil || !changed || first.ResolvedAt == 0 {
		t.Fatalf("resolve: %+v %v %v", first, changed, err)
	}
	again, changed, err := m.ResolveIncident(in.ID)
	if err != nil || changed || again.ResolvedAt != first.ResolvedAt {
		t.Errorf("resolving twice: changed=%v, %d → %d (%v)", changed, first.ResolvedAt, again.ResolvedAt, err)
	}
	if _, _, err := m.ResolveIncident(in.ID + 100); validationCode(err) != "err.incidentNotFound" {
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
	if _, _, err := m.ResolveIncident(ahead.ID); validationCode(err) != "err.incidentNotStarted" {
		t.Errorf("finishing maintenance before it began: %v", err)
	}
	past, err := m.SaveIncident(model.Incident{Kind: model.IncidentKindMaintenance, Title: "past", StartsAt: now - 7200, EndsAt: now - 3600})
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := m.ResolveIncident(past.ID)
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

	msg := m.incidentMessage(in, set, i18n.EN, false)
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

	done := m.incidentMessage(in, set, i18n.RU, true)
	if !strings.HasPrefix(done, "✅ <b>Работы завершены: ") || strings.Contains(done, "a &amp; b") || strings.Contains(done, "🕐") {
		t.Errorf("finished message:\n%s", done)
	}
	in.Kind = model.IncidentKindIncident
	if got := m.incidentMessage(in, set, i18n.RU, true); !strings.HasPrefix(got, "✅ <b>Решено: ") {
		t.Errorf("resolved message:\n%s", got)
	}

	// No public page: no link to one. The whole service: no server line.
	set.StatusEnabled = false
	in.NodeIDs = nil
	if got := m.incidentMessage(in, set, i18n.RU, false); strings.Contains(got, "https://") || strings.Contains(got, "📍") {
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
	msg := m.incidentMessage(in, set, i18n.RU, false)
	if n := utf8.RuneCountInString(msg); n > broadcastTextMax || !strings.Contains(msg, "&amp;…") {
		t.Errorf("message of %d runes (limit %d), ends %q", n, broadcastTextMax, msg[len(msg)-20:])
	}
	in.Body = "short"
	if msg := m.incidentMessage(in, set, i18n.RU, false); !strings.Contains(msg, "\n\nshort") || strings.Contains(msg, "…") {
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
