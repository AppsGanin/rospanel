package status

import (
	"strings"
	"testing"
	"time"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

var msk = time.FixedZone("MSK", 3*3600)

func at(day, hour, minute int) int64 {
	return time.Date(2026, 10, day, hour, minute, 0, 0, msk).Unix()
}

// The operator's entries land in the right section, mark the servers they cover,
// and set the page's overall state — an outage outranks an incident, an incident
// outranks maintenance.
func TestSetIncidentsFilesEntries(t *testing.T) {
	t.Parallel()
	now := time.Unix(at(16, 12, 0), 0).In(msk)
	base := func() Data {
		d := themed(t, "", i18n.EN)
		d.HeadlineOK = true
		d.Servers = []Server{{ID: 0, Name: "Main", Up: true}, {ID: 2, Name: "NL", Up: true}, {ID: 3, Name: "DE", Up: true}}
		return d
	}
	works := model.Incident{ID: 1, Kind: model.IncidentKindMaintenance, Title: "NL works", NodeIDs: []int64{2},
		StartsAt: at(16, 11, 0), EndsAt: at(16, 15, 0), CreatedAt: at(15, 9, 0), UpdatedAt: at(15, 9, 0)}
	planned := model.Incident{ID: 2, Kind: model.IncidentKindMaintenance, Title: "DE works", NodeIDs: []int64{3},
		StartsAt: at(18, 10, 0), EndsAt: at(18, 11, 0)}
	later := model.Incident{ID: 3, Kind: model.IncidentKindMaintenance, Title: "later", StartsAt: at(20, 10, 0)}
	past := model.Incident{ID: 4, Kind: model.IncidentKindIncident, Title: "old", NodeIDs: []int64{0, 99},
		StartsAt: at(10, 10, 0), ResolvedAt: at(10, 12, 30)}
	broken := model.Incident{ID: 5, Kind: model.IncidentKindIncident, Title: "NL broken", NodeIDs: []int64{2},
		StartsAt: at(16, 11, 30), CreatedAt: at(16, 11, 30), UpdatedAt: at(16, 11, 45)}

	d := base()
	// Latest start first, as the store hands them over.
	d.SetIncidents([]model.Incident{later, planned, works, past}, now, i18n.EN)
	if d.State != "maintenance" {
		t.Errorf("state with maintenance under way = %q", d.State)
	}
	if len(d.Active) != 1 || d.Active[0].Title != "NL works" || d.Active[0].When != "Oct 16, 11:00–15:00 MSK" || d.Active[0].Servers != "NL" {
		t.Errorf("active = %+v", d.Active)
	}
	if len(d.Upcoming) != 2 || d.Upcoming[0].Title != "DE works" || d.Upcoming[1].Title != "later" {
		t.Errorf("upcoming must be soonest first: %+v", d.Upcoming)
	}
	if d.Upcoming[1].When != "since Oct 20, 10:00 MSK" {
		t.Errorf("open-ended window = %q", d.Upcoming[1].When)
	}
	if len(d.History) != 1 || d.History[0].Ended != "Oct 10, 12:30 MSK" || d.History[0].Servers != "Main" {
		t.Errorf("history = %+v (a deleted server is dropped from the names)", d.History)
	}
	if d.Servers[1].Notice != model.IncidentKindMaintenance || d.Servers[0].Notice != "" || d.Servers[2].Notice != "" {
		t.Errorf("server notices = %+v", d.Servers)
	}
	if d.Active[0].Updated != "" {
		t.Error("an entry never edited after it was posted shows an edit time")
	}

	d = base()
	d.SetIncidents([]model.Incident{broken, works}, now, i18n.EN)
	if d.State != "incident" {
		t.Errorf("state with an open incident = %q", d.State)
	}
	if d.Servers[1].Notice != model.IncidentKindIncident {
		t.Errorf("an incident must outrank maintenance on the same server: %q", d.Servers[1].Notice)
	}
	if d.Active[0].Updated != "Oct 16, 11:45 MSK" || d.Active[0].When != "since Oct 16, 11:30 MSK" {
		t.Errorf("incident notice = %+v", d.Active[0])
	}

	d = base()
	d.HeadlineOK = false
	d.SetIncidents([]model.Incident{broken}, now, i18n.EN)
	if d.State != "down" {
		t.Errorf("a server down must outrank an incident: %q", d.State)
	}

	d = base()
	d.SetIncidents(nil, now, i18n.EN)
	if d.State != "ok" || d.Active != nil || d.History != nil {
		t.Errorf("no incidents: %+v", d)
	}
}

func TestStampAndWindowPerLanguage(t *testing.T) {
	t.Parallel()
	start, end := at(16, 12, 0), at(16, 15, 0)
	nextDay := at(17, 3, 0)
	cases := []struct {
		lang      i18n.Lang
		same, two string
	}{
		{i18n.RU, "16 окт, 12:00–15:00 MSK", "16 окт, 12:00 – 17 окт, 03:00 MSK"},
		{i18n.EN, "Oct 16, 12:00–15:00 MSK", "Oct 16, 12:00 – Oct 17, 03:00 MSK"},
		{i18n.ZH, "10月16日 12:00–15:00 MSK", "10月16日 12:00 – 10月17日 03:00 MSK"},
	}
	for _, c := range cases {
		if got := Window(c.lang, msk, start, end); got != c.same {
			t.Errorf("%s same day: %q, want %q", c.lang, got, c.same)
		}
		if got := Window(c.lang, msk, start, nextDay); got != c.two {
			t.Errorf("%s across days: %q, want %q", c.lang, got, c.two)
		}
	}
	// Every language names twelve months, or the page falls back to numbers.
	for _, lang := range []i18n.Lang{i18n.RU, i18n.EN, i18n.ZH, i18n.AR, i18n.FA} {
		if n := len(strings.Split(i18n.T(lang, "status.months"), ",")); n != 12 {
			t.Errorf("%s: %d month names", lang, n)
		}
		if got := Stamp(lang, time.Unix(start, 0).In(msk)); !strings.Contains(got, "16") || !strings.Contains(got, "12:00 MSK") {
			t.Errorf("%s stamp = %q", lang, got)
		}
	}
}

// What the operator typed reaches a public page: it must stay text.
func TestRenderIncidentsEscapedAndPlaced(t *testing.T) {
	t.Parallel()
	d := themed(t, "", i18n.EN)
	d.HeadlineOK = true
	d.Servers = []Server{{ID: 2, Name: "NL", Up: true, Uptime: "100.00%"}}
	now := time.Unix(at(16, 12, 0), 0).In(msk)
	d.SetIncidents([]model.Incident{
		{Kind: model.IncidentKindIncident, Title: "<img src=x onerror=alert(1)>", Body: "<script>x</script>\nline 2",
			NodeIDs: []int64{2}, StartsAt: at(16, 11, 0)},
		{Kind: model.IncidentKindIncident, Title: "fixed", StartsAt: at(15, 11, 0), ResolvedAt: at(15, 12, 0)},
	}, now, i18n.EN)
	html := render(t, d)
	for _, raw := range []string{"<img src=x", "<script>x</script>"} {
		if strings.Contains(html, raw) {
			t.Errorf("unescaped %q reached the page", raw)
		}
	}
	for _, want := range []string{
		`class="hero incident-tone"`, d.L.StateIncident, d.L.Active,
		`<span class="tag srv-tag incident-tone">` + d.L.Incident + `</span>`, // the server's badge
		`<details class="past">`, d.L.Resolved + " Oct 15, 12:00 MSK",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(html, d.L.Upcoming) {
		t.Error("an empty upcoming section was rendered")
	}

	empty := themed(t, "", i18n.EN)
	empty.HeadlineOK = true
	if html := render(t, empty); !strings.Contains(html, empty.L.NoEvents) {
		t.Error("an empty history does not say so")
	}
}

// A notice whose servers have all left the page is not shown as current (it would
// read as the whole service), and history is ordered by when things ended.
func TestSetIncidentsOrphansAndHistoryOrder(t *testing.T) {
	t.Parallel()
	now := time.Unix(at(16, 12, 0), 0).In(msk)
	d := themed(t, "", i18n.EN)
	d.HeadlineOK = true
	d.Servers = []Server{{ID: 0, Name: "Main", Up: true}}
	d.SetIncidents([]model.Incident{
		{Kind: model.IncidentKindIncident, Title: "gone server", NodeIDs: []int64{7}, StartsAt: at(16, 11, 0)},
		{Kind: model.IncidentKindIncident, Title: "long, ended yesterday", StartsAt: at(14, 9, 0), ResolvedAt: at(15, 9, 0)},
		{Kind: model.IncidentKindIncident, Title: "gone, ended", NodeIDs: []int64{7}, StartsAt: at(13, 9, 0), ResolvedAt: at(13, 10, 0)},
		{Kind: model.IncidentKindIncident, Title: "short, last week", StartsAt: at(10, 9, 0), ResolvedAt: at(10, 10, 0)},
		{Kind: model.IncidentKindMaintenance, Title: "ended in between", StartsAt: at(15, 9, 30), EndsAt: at(15, 10, 0)},
	}, now, i18n.EN)
	if len(d.Active) != 0 || d.State != "ok" {
		t.Errorf("a notice about a server off the page shows as current: %+v state %s", d.Active, d.State)
	}
	var titles []string
	for _, n := range d.History {
		titles = append(titles, n.Title)
	}
	want := "ended in between|long, ended yesterday|gone, ended|short, last week"
	if got := strings.Join(titles, "|"); got != want {
		t.Errorf("history order %q, want %q", got, want)
	}
}
