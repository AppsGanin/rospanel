// Package status renders the public status page: which servers are up, and how
// they have behaved over the retention window.
//
// It is the only surface the panel exposes to someone holding no token at all, so
// it is deliberately thin. What it shows is a name, a state and a bar chart; what
// it never shows is hosts, addresses, versions, user counts or traffic — anything
// that would help someone map the installation rather than answer "is it me or
// them". The page is off unless an operator turns it on.
package status

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

//go:embed page.html
var pageHTML string

var pageTmpl = template.Must(template.New("status").Parse(pageHTML))

// Server is one row on the page.
type Server struct {
	ID     int64
	Name   string
	Up     bool
	Uptime string // formatted percentage, "" when nothing was sampled yet
	Days   []Day
	// Notice is the kind of the operator's notice covering this server right now
	// ("incident" or "maintenance"), "" when there is none.
	Notice string
}

// Notice is one incident or maintenance window as the page prints it.
type Notice struct {
	Kind    string // incident | maintenance
	Phase   string // scheduled | active | done
	Label   string // the kind, worded
	Title   string
	Body    string
	When    string // when it started, or the maintenance window
	Ended   string // when it was over, "" while it is not
	Servers string // the servers it names, "" for the whole service
	Updated string // when it was last edited, "" if never after it was posted
}

// Day is one bar: a class picking its colour and a title a cursor reveals.
type Day struct {
	Class string // up | partial | down | none
	Title string
}

// Data is everything the template renders.
type Data struct {
	Lang      string
	Dir       string
	Title     string
	LogoURL   string
	Brand     string
	BrandDark string
	Ink       string
	Muted     string
	Bg        string
	Surface   string
	SuccessFg string
	DangerFg  string
	WarningFg string
	InfoFg    string

	Headline   string
	HeadlineOK bool
	// State is the page's overall state, worst first: "down" (a server is
	// unreachable), "incident", "maintenance" or "ok".
	State       string
	Servers     []Server
	Active      []Notice // incidents and maintenance going on now
	Upcoming    []Notice // maintenance not started yet, soonest first
	History     []Notice // over within the history window, latest first
	WindowLabel string
	UpdatedAt   string
	L           text
}

// text is the page's localised strings, a struct rather than a map so a typo is a
// template error rather than an empty span.
type text struct {
	Title         string
	AllUp         string
	SomeDown      string
	StateIncident string
	StateMaint    string
	Up            string
	Down          string
	Uptime        string
	NoHistory     string
	Updated       string
	Days          string
	Today         string
	Servers       string
	Active        string
	Upcoming      string
	History       string
	NoEvents      string
	Incident      string
	Maintenance   string
	Scheduled     string
	Resolved      string
	Done          string
	Affects       string
	EditedAt      string
	LegendUp      string
	LegendPartial string
	LegendDown    string
	LegendNone    string
}

func texts(lang i18n.Lang, days int) text {
	t := func(k string) string { return i18n.T(lang, k) }
	return text{
		Title:         t("status.title"),
		AllUp:         t("status.allUp"),
		SomeDown:      t("status.someDown"),
		StateIncident: t("status.stateIncident"),
		StateMaint:    t("status.stateMaintenance"),
		Up:            t("status.up"),
		Down:          t("status.down"),
		Uptime:        t("status.uptime"),
		NoHistory:     t("status.noHistory"),
		Updated:       t("status.updated"),
		Days:          i18n.T(lang, "status.window", days),
		Today:         t("status.today"),
		Servers:       t("status.servers"),
		Active:        t("status.active"),
		Upcoming:      t("status.upcoming"),
		History:       t("status.history"),
		NoEvents:      t("status.noEvents"),
		Incident:      t("status.kindIncident"),
		Maintenance:   t("status.kindMaintenance"),
		Scheduled:     t("status.phaseScheduled"),
		Resolved:      t("status.phaseResolved"),
		Done:          t("status.phaseDone"),
		Affects:       t("status.affects"),
		EditedAt:      t("status.editedAt"),
		LegendUp:      t("status.legendUp"),
		LegendPartial: t("status.legendPartial"),
		LegendDown:    t("status.legendDown"),
		LegendNone:    t("status.legendNone"),
	}
}

// DayClass maps a day's sampled ratio to the bar's colour class. The thresholds
// are the ones a reader expects: a day is green only if it was green all day,
// amber for a dip, red for a real outage, and neutral when nothing was recorded
// (before the panel was installed, or while it was itself down).
func DayClass(samples int, ratio float64) string {
	switch {
	case samples == 0:
		return "none"
	case ratio >= 0.999:
		return "up"
	case ratio >= 0.9:
		return "partial"
	default:
		return "down"
	}
}

// FormatUptime renders an uptime percentage with two decimals, the convention
// every status page uses (99.95% says something 100% rounding would hide).
func FormatUptime(pct float64) string { return fmt.Sprintf("%.2f%%", pct) }

// Render produces the page HTML.
func Render(d Data) ([]byte, error) {
	if d.State == "" { // SetIncidents was not called: the servers alone decide
		d.State = "ok"
		if !d.HeadlineOK {
			d.State = "down"
		}
	}
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, d); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Theme fills the palette fields from the panel's branding theme, so the status
// page looks like the operator's panel and subscription page rather than a
// third-party service.
func (d *Data) Theme(panelName, panelTheme string, lang i18n.Lang, days int) {
	name := branding.Name(panelName)
	if name == branding.DefaultName {
		name = i18n.T(lang, "sub.defaultBrand")
	}
	th := branding.ParseTheme(panelTheme)
	d.Brand = th.Accent
	d.BrandDark = branding.Darken(th.Accent, 0.16)
	d.Ink = th.Text
	d.Muted = th.Muted
	d.Bg = th.Bg
	d.Surface = th.Surface
	d.SuccessFg = branding.Fg("#059669", th.Surface)
	d.DangerFg = branding.Fg("#dc2626", th.Surface)
	d.WarningFg = branding.Fg("#ea580c", th.Surface)
	d.InfoFg = branding.Fg("#2563eb", th.Surface)
	d.Lang = string(lang)
	d.Dir = lang.Dir()
	d.L = texts(lang, days)
	d.Title = strings.TrimSpace(name + " · " + d.L.Title)
}

// SetIncidents files the operator's notices into the page's sections, marks the
// servers they cover and settles the page's State; call it after Servers and
// HeadlineOK are set. now is in the zone the page prints its times in.
func (d *Data) SetIncidents(incidents []model.Incident, now time.Time, lang i18n.Lang) {
	names := make(map[int64]string, len(d.Servers))
	for _, s := range d.Servers {
		names[s.ID] = s.Name
	}
	loc, unix := now.Location(), now.Unix()
	var ends []int64 // History's end times, to order it by
	for i := range incidents {
		in := &incidents[i]
		n := Notice{
			Kind:  in.Kind,
			Phase: in.Phase(unix),
			Title: in.Title,
			Body:  in.Body,
		}
		n.Label = d.L.Incident
		if in.Kind == model.IncidentKindMaintenance {
			n.Label = d.L.Maintenance
			n.When = Window(lang, loc, in.StartsAt, in.EndsAt)
		} else {
			n.When = i18n.T(lang, "status.since", Stamp(lang, time.Unix(in.StartsAt, 0).In(loc)))
		}
		if end := in.EndedAt(unix); end > 0 {
			n.Ended = Stamp(lang, time.Unix(end, 0).In(loc))
		}
		// "Edited" only for a change after it was posted: the post itself is not news.
		if in.UpdatedAt > in.CreatedAt+60 && n.Phase != model.IncidentDone {
			n.Updated = Stamp(lang, time.Unix(in.UpdatedAt, 0).In(loc))
		}
		var named []string
		for _, id := range in.NodeIDs {
			if name, ok := names[id]; ok {
				named = append(named, name)
			}
		}
		n.Servers = strings.Join(named, ", ")
		// Named servers that have all left the page (switched off, deleted) leave a
		// notice about nothing a customer can be on: it would read as concerning the
		// whole service. Its history stays.
		orphan := len(in.NodeIDs) > 0 && len(named) == 0
		if orphan && n.Phase != model.IncidentDone {
			continue
		}
		switch n.Phase {
		case model.IncidentActive:
			d.Active = append(d.Active, n)
			for j := range d.Servers {
				if !in.Concerns(d.Servers[j].ID) {
					continue
				}
				// An incident outranks maintenance on the same server.
				if d.Servers[j].Notice == "" || in.Kind == model.IncidentKindIncident {
					d.Servers[j].Notice = in.Kind
				}
			}
		case model.IncidentScheduled:
			d.Upcoming = append([]Notice{n}, d.Upcoming...) // latest-first in, soonest-first out
		default:
			d.History = append(d.History, n)
			ends = append(ends, in.EndedAt(unix))
		}
	}
	// What ended last comes first: a long incident resolved yesterday is more recent
	// news than a short one from last week, whatever their starts say.
	order := make([]int, len(d.History))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return ends[order[a]] > ends[order[b]] })
	history := make([]Notice, len(order))
	for i, j := range order {
		history[i] = d.History[j]
	}
	if len(history) > 0 {
		d.History = history
	}
	d.State = "ok"
	for _, n := range d.Active {
		if n.Kind == model.IncidentKindIncident {
			d.State = "incident"
			break
		}
		d.State = "maintenance"
	}
	if !d.HeadlineOK {
		d.State = "down"
	}
}

// Stamp prints a moment as the page's readers expect it in their language: day,
// month and time, with the zone it is in ("16 окт, 12:00 MSK").
func Stamp(lang i18n.Lang, t time.Time) string {
	return dayMonth(lang, t) + " " + t.Format("15:04 MST")
}

// Window prints a maintenance window: "16 окт, 12:00–15:00 MSK", across days with
// both dates, and "с 16 окт, 12:00 MSK" when it has no end.
func Window(lang i18n.Lang, loc *time.Location, start, end int64) string {
	from := time.Unix(start, 0).In(loc)
	if end <= 0 {
		return i18n.T(lang, "status.since", Stamp(lang, from))
	}
	to := time.Unix(end, 0).In(loc)
	if from.Format("2006-01-02") == to.Format("2006-01-02") {
		return dayMonth(lang, from) + " " + from.Format("15:04") + "–" + to.Format("15:04 MST")
	}
	return dayMonth(lang, from) + " " + from.Format("15:04") + " – " + Stamp(lang, to)
}

// dayMonth is "16 окт" / "Oct 16" / "10月16日": the order and the month names are
// the language's own (status.date, status.months).
func dayMonth(lang i18n.Lang, t time.Time) string {
	months := strings.Split(i18n.T(lang, "status.months"), ",")
	month := strconv.Itoa(int(t.Month()))
	if len(months) == 12 {
		month = strings.TrimSpace(months[t.Month()-1])
	}
	return strings.NewReplacer("{d}", strconv.Itoa(t.Day()), "{m}", month).
		Replace(i18n.T(lang, "status.date"))
}
