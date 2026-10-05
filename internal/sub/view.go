package sub

import (
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// View is the subscription page as data, for whoever draws the page themselves: the
// same figures, links and offers, in the same words (the lang it was asked in), plus
// the raw numbers behind them. Actions — paying, topping up, a promo code — go
// through the API, not the page's own endpoints.
type View struct {
	Name        string `json:"name"`
	Status      string `json:"status"` // active | disabled | expired | limited | device_limited
	StatusLabel string `json:"status_label"`
	Online      bool   `json:"online"`
	LastSeenAt  int64  `json:"last_seen_at"` // unix, 0 = never

	UsedBytes   int64 `json:"used_bytes"`
	LimitBytes  int64 `json:"limit_bytes"` // 0 = unlimited
	UsedPct     int   `json:"used_pct"`
	ExpireAt    int64 `json:"expire_at"`              // 0 = no end (or held — hold_seconds)
	HoldSeconds int64 `json:"hold_seconds,omitempty"` // a term that starts at the first connection
	// Texts are the figures above as the page words them.
	Texts ViewTexts `json:"texts"`

	SubURL string    `json:"sub_url"`
	Apps   []ViewApp `json:"apps"` // one-tap import into each client
	// Links are every config the user may use, on every server. ShowConfigs is the
	// operator's choice whether the page lists them (the apps import them anyway).
	Links       []ViewLink `json:"links"`
	ShowConfigs bool       `json:"show_configs"`
	// RefCode is the user's invite code while the referral programme runs, for a bot
	// that builds its own ?start=r_<code> link.
	RefCode string     `json:"ref_code,omitempty"`
	AWG     []ViewAWG  `json:"awg"`
	Turn    []ViewTurn `json:"turn"`
	Brand   ViewBrand  `json:"brand"`
	Devices Devices    `json:"devices"`
	Billing *Billing   `json:"billing,omitempty"` // absent while billing shows nothing
	// TGLink is the user bot's link that binds this account to the Telegram it is
	// opened in — or, when TGLinked, moves it there. Absent while the bot or the
	// switches in Settings → Telegram leave nothing to offer. Its code lasts 15
	// minutes: fetch the view again rather than keep the link.
	TGLink   string `json:"tg_link,omitempty"`
	TGLinked bool   `json:"tg_linked"`
}

// ViewTexts are a View's figures in words.
type ViewTexts struct {
	Used     string `json:"used"`      // "1.2 GB"
	Limit    string `json:"limit"`     // "50 GB", "∞"
	Expire   string `json:"expire"`    // "until 01.10.2026", "no limit"
	Reset    string `json:"reset"`     // when the quota next refills, "" = never
	LastSeen string `json:"last_seen"` // "3 h ago", "" = online or never
}

// ViewApp is a client and the link that imports the subscription into it.
type ViewApp struct {
	Name      string `json:"name"`
	Platforms string `json:"platforms,omitempty"`
	URL       string `json:"url"`
	Home      string `json:"home,omitempty"` // where the app itself comes from
}

// ViewLink is one config with the name a client shows for it.
type ViewLink struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// ViewAWG is one server's AmneziaWG config: the file and its QR.
type ViewAWG struct {
	Name    string `json:"name"`
	ConfURL string `json:"conf_url"`
	QRURL   string `json:"qr_url"`
}

// ViewTurn is one WireGuard-over-TURN lane and the apps that import it.
type ViewTurn struct {
	Name    string    `json:"name"`
	Peer    string    `json:"peer"`
	Link    string    `json:"link,omitempty"`
	ConfURL string    `json:"conf_url"`
	Apps    []ViewApp `json:"apps"`
}

// ViewBrand is the operator's name and colours, for a page that should look like theirs.
type ViewBrand struct {
	Name    string `json:"name"`
	Accent  string `json:"accent"`
	Text    string `json:"text"`
	Muted   string `json:"muted"`
	Bg      string `json:"bg"`
	Surface string `json:"surface"`
}

// PageView builds the View of the user's page. Unlike the browser page it always
// carries the configs: its caller holds an API key, not just the subscription link.
func PageView(u model.User, local *model.Settings, servers []Server, billing Billing, devices Devices, lang i18n.Lang) (View, error) {
	d, err := buildPageData(u, local, servers, billing, devices, true, lang)
	if err != nil {
		return View{}, err
	}
	v := View{
		Name: u.Name, Status: u.Status, StatusLabel: d.StatusLabel, Online: d.Online, LastSeenAt: u.LastSeen,
		UsedBytes: u.UsedUp + u.UsedDown, LimitBytes: u.DataLimit, UsedPct: d.UsedPct,
		ExpireAt: u.ExpireAt, HoldSeconds: u.HoldSeconds,
		Texts:  ViewTexts{Used: d.Used, Limit: d.Limit, Expire: d.Expire, Reset: d.ResetText, LastSeen: d.LastSeen},
		SubURL: d.SubURL, ShowConfigs: local.SubShowConfigs,
		Apps: []ViewApp{}, Links: []ViewLink{}, AWG: []ViewAWG{}, Turn: []ViewTurn{},
		Brand:   ViewBrand{Name: d.BrandName, Accent: d.Brand, Text: d.Ink, Muted: d.Muted, Bg: d.Bg, Surface: d.Surface},
		Devices: devices,
	}
	if v.Devices.List == nil {
		v.Devices.List = []DeviceRow{}
	}
	for _, a := range d.DeepLinks {
		v.Apps = append(v.Apps, ViewApp{Name: a.Label, Platforms: a.Platform, URL: string(a.Href)})
	}
	for _, l := range d.Links {
		v.Links = append(v.Links, ViewLink{Name: l.Proto, URL: l.URL})
	}
	for _, a := range d.AWG {
		v.AWG = append(v.AWG, ViewAWG{Name: a.Label, ConfURL: a.ConfURL, QRURL: a.QRURL})
	}
	for _, t := range d.Turn {
		vt := ViewTurn{Name: t.Label, Peer: t.Peer, Link: t.Link, ConfURL: t.ConfURL, Apps: []ViewApp{}}
		for _, a := range t.Apps {
			vt.Apps = append(vt.Apps, ViewApp{Name: a.Name, URL: a.Link, Home: a.AppLink})
		}
		v.Turn = append(v.Turn, vt)
	}
	if billing.Show {
		b := billing
		// Lists as [], never null: a page drawn from this should not have to guard.
		b.Plans = nonNil(b.Plans)
		for i := range b.Plans {
			b.Plans[i].Options = nonNil(b.Plans[i].Options)
		}
		b.Providers = nonNil(b.Providers)
		b.History = nonNil(b.History)
		b.Invitees = nonNil(b.Invitees)
		v.Billing = &b
	}
	return v, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
