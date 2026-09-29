package sub

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

//go:embed logo.svg
var logoSVG []byte

// Logo returns the embedded RosPanel logo (SVG).
func Logo() []byte { return logoSVG }

// Mulish, self-hosted. The subscription page used to pull this from Google Fonts,
// which is throttled in Russia — a render-blocking <link> to a throttled host delays
// paint for exactly the users this panel serves. Same reasoning as the Telegram SDK
// proxy; here self-hosting is simpler than proxying, since the SPA already ships the
// same font. Subsets carry unicode-range in the page CSS, so a browser downloads only
// what the text needs (cyrillic for the Russian UI, latin-ext only for glyphs like ₽).
//
//go:embed fonts/*.woff2
var fontFS embed.FS

// Font returns an embedded webfont by bare file name. It refuses any name with a path
// separator, so a request can only ever name a file directly inside fonts/.
func Font(name string) ([]byte, bool) {
	if name == "" || strings.ContainsAny(name, `/\`) || !strings.HasSuffix(name, ".woff2") {
		return nil, false
	}
	b, err := fontFS.ReadFile("fonts/" + name)
	if err != nil {
		return nil, false
	}
	return b, true
}

//go:embed page.html
var pageHTML string

var pageTmpl = template.Must(template.New("sub").Parse(pageHTML))

// appRedirectTmpl is a tiny page that immediately hands off to a client's deep
// link. It's opened in the EXTERNAL browser (via Telegram's openLink) because a
// custom app scheme (happ://, v2rayng://, …) can't be launched from inside the
// Telegram webview — but the browser it lands in resolves the scheme and opens
// the app. Href is template.URL so the scheme survives html/template's URL filter.
var appRedirectTmpl = template.Must(template.New("appredir").Parse(
	`<!doctype html><html lang="{{.Lang}}"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>{{.Opening}}</title>` +
		`<script>location.replace("{{.Href}}")</script>` +
		`<meta http-equiv="refresh" content="0;url={{.Href}}"></head>` +
		`<body style="font-family:sans-serif;padding:24px;color:#333">` +
		`<p>{{.Opening}}<br>{{.IfNotOpened}} ` +
		`<a href="{{.Href}}">{{.ClickHere}}</a>.</p></body></html>`))

// AppRedirect renders the deep-link hand-off page for one client's share link.
func AppRedirect(href template.URL, lang i18n.Lang) ([]byte, error) {
	var buf bytes.Buffer
	data := struct {
		Href        template.URL
		Lang        string
		Opening     string
		IfNotOpened string
		ClickHere   string
	}{
		Href:        href,
		Lang:        string(lang),
		Opening:     i18n.T(lang, "sub.openingApp"),
		IfNotOpened: i18n.T(lang, "sub.ifNotOpened"),
		ClickHere:   i18n.T(lang, "sub.clickHere"),
	}
	if err := appRedirectTmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pageText is every localised string the template renders. A struct rather than a
// map so the template refers to fields by name and a typo is a template error at
// render time instead of a silently empty span.
type pageText struct {
	Title          string
	DefaultBrand   string
	Greeting       string
	Online         string
	Offline        string
	Traffic        string
	TrafficUntil   string
	PayProcessing  string
	CurrentPlan    string
	Pay            string
	Renew          string
	CancelSub      string
	ChangePlanNote string
	ChangeTitle    string
	ChangeHint     string
	AddHint        string
	RenewTitle     string
	WzOpen         string
	WzStep1        string
	WzStep1Hint    string
	WzStep2        string
	WzStep2Hint    string
	WzStep3        string
	WzStep3Hint    string
	WzAdd          string
	WzOther        string
	WzBack         string
	WzNext         string
	WzDone         string
	ChangeBtn      string
	ChangeConfirm  string
	AddTitle       string
	AddBtn         string
	ManualNote     string
	ScanQR         string
	CopyLink       string
	OpenInApp      string
	DownloadClash  string
	SingleConfigs  string
	AWGTitle       string
	AWGHint        string
	AWGDownload    string
	AWGSite        string // where the Amnezia apps are downloaded, in the page's language
	TurnTitle      string
	TurnHint       string
	TurnPeer       string
	TurnLink       string
	TurnNoLink     string
	TurnManual     string
	AppLink        string
	Copy           string
	Copied         string
	PickApp        string
	PayMethod      string
	PayTitle       string
	CancelConfirm  string
	CancelFailed   string
	NetworkError   string
	OrderCreated   string
	OrderTitle     string
	PayFailed      string
	Error          string

	Balance          string
	Topup            string
	TopupAmount      string
	AutoRenew        string
	PayBalance       string
	PromoTitle       string
	PromoPlaceholder string
	PromoApply       string
	RefTitle         string
	RefInvitees      string
	PaidTitle        string
	GetFree          string
	History          string
	Share            string
	TabSub           string
	TabPay           string
	TabRef           string

	Devices        string
	DevicesHint    string
	DevicesEmpty   string
	DeviceRemove   string
	DeviceConfirm  string
	DeviceFailed   string
	DeviceNeverUse string
}

func text(lang i18n.Lang) pageText {
	t := func(key string) string { return i18n.T(lang, key) }
	return pageText{
		Title:          t("sub.title"),
		DefaultBrand:   t("sub.defaultBrand"),
		Greeting:       t("sub.greeting"),
		Online:         t("sub.online"),
		Offline:        t("sub.offline"),
		Traffic:        t("sub.traffic"),
		TrafficUntil:   t("sub.trafficUntilPrefix"),
		PayProcessing:  t("sub.payProcessing"),
		CurrentPlan:    t("sub.currentPlan"),
		Pay:            t("sub.pay"),
		Renew:          t("sub.renew"),
		CancelSub:      t("sub.cancelSub"),
		ChangePlanNote: t("sub.changePlanNote"),
		ChangeTitle:    t("sub.changeTitle"),
		ChangeHint:     t("sub.changeHint"),
		AddHint:        t("sub.addHint"),
		RenewTitle:     t("sub.renewTitle"),
		WzOpen:         t("sub.wzOpen"),
		WzStep1:        t("sub.wzStep1"),
		WzStep1Hint:    t("sub.wzStep1Hint"),
		WzStep2:        t("sub.wzStep2"),
		WzStep2Hint:    t("sub.wzStep2Hint"),
		WzStep3:        t("sub.wzStep3"),
		WzStep3Hint:    t("sub.wzStep3Hint"),
		WzAdd:          t("sub.wzAdd"),
		WzOther:        t("sub.wzOther"),
		WzBack:         t("sub.wzBack"),
		WzNext:         t("sub.wzNext"),
		WzDone:         t("sub.wzDone"),
		ChangeBtn:      t("sub.changeBtn"),
		ChangeConfirm:  t("sub.changeConfirm"),
		AddTitle:       t("sub.addTitle"),
		AddBtn:         t("sub.addBtn"),
		ManualNote:     t("sub.manualNote"),
		ScanQR:         t("sub.scanQR"),
		CopyLink:       t("sub.copyLink"),
		OpenInApp:      t("sub.openInApp"),
		DownloadClash:  t("sub.downloadClash"),
		SingleConfigs:  t("sub.singleConfigs"),
		AWGTitle:       t("sub.awgTitle"),
		AWGHint:        t("sub.awgHint"),
		AWGDownload:    t("sub.awgDownload"),
		AWGSite:        amneziaSite(lang),
		TurnTitle:      t("sub.turnTitle"),
		TurnHint:       t("sub.turnHint"),
		TurnPeer:       t("sub.turnPeer"),
		TurnLink:       t("sub.turnLink"),
		TurnNoLink:     t("sub.turnNoLink"),
		TurnManual:     t("sub.turnManual"),
		AppLink:        t("sub.appLink"),
		Copy:           t("sub.copy"),
		Copied:         t("sub.copied"),
		PickApp:        t("sub.pickApp"),
		PayMethod:      t("sub.payMethod"),
		PayTitle:       t("sub.payTitle"),
		CancelConfirm:  t("sub.cancelConfirm"),
		CancelFailed:   t("sub.cancelFailed"),
		NetworkError:   t("sub.networkError"),
		OrderCreated:   t("sub.orderCreated"),
		OrderTitle:     t("sub.orderTitle"),
		PayFailed:      t("sub.payFailed"),
		Error:          t("sub.error"),

		Balance:          t("sub.balance"),
		Topup:            t("sub.topup"),
		TopupAmount:      t("sub.topupAmount"),
		AutoRenew:        t("sub.autoRenew"),
		PayBalance:       t("sub.payBalance"),
		PromoTitle:       t("sub.promoTitle"),
		PromoPlaceholder: t("sub.promoPlaceholder"),
		PromoApply:       t("sub.promoApply"),
		RefTitle:         t("sub.refTitle"),
		RefInvitees:      t("sub.refInvitees"),
		PaidTitle:        t("sub.paidTitle"),
		GetFree:          t("sub.getFree"),
		History:          t("sub.history"),
		Share:            t("sub.share"),
		TabSub:           t("sub.tabSub"),
		TabPay:           t("sub.tabPay"),
		TabRef:           t("sub.tabRef"),

		Devices:        t("sub.devices"),
		DevicesHint:    t("sub.devicesHint"),
		DevicesEmpty:   t("sub.devicesEmpty"),
		DeviceRemove:   t("sub.deviceRemove"),
		DeviceConfirm:  t("sub.deviceRemoveConfirm"),
		DeviceFailed:   t("sub.deviceRemoveFailed"),
		DeviceNeverUse: t("sub.deviceNeverSeen"),
	}
}

// amneziaSite is where the Amnezia apps themselves are downloaded. The site keeps a
// Russian page of its own; every other language lands on the default one.
func amneziaSite(lang i18n.Lang) string {
	if lang == i18n.RU {
		return "https://amnezia.org/ru/downloads"
	}
	return "https://amnezia.org/downloads"
}

// turnCard is one WireGuard inbound behind a TURN relay: what the user's TURN client
// needs (the relay's address and the call link), and the WireGuard config it carries.
type turnCard struct {
	Label   string
	Peer    string // host:port of the relay
	Link    string // call invite link, "" when the operator set none
	ConfURL string
	// Apps are the client apps that import this lane from one link.
	Apps []turnApp
}

// turnApp is one app's import link. Href is the same link as a template.URL, so its
// scheme survives html/template's URL filter; AppLink is where the app itself comes
// from, so a user handed a link can see what they are installing.
type turnApp struct {
	Name string // the app's own name and platform; a brand, not translated
	Link string
	Href template.URL
	// AppLink is the app's own page — its repository, where every one of these lives.
	AppLink string
}

// awgCard is one server's AmneziaWG config on the page.
type awgCard struct {
	Label   string
	ConfURL string
	QRURL   string
}

type pageData struct {
	L         pageText
	Name      string
	BrandName string // panel display name (defaults to the stock RosPanel name)
	Brand     string // accent colour #rrggbb
	BrandDark string // darker accent for hover/active states
	AccentFg  string // accent text colour adjusted for the surface
	OnBrand   string // label colour on an accent fill: white, or dark ink on a light accent
	SuccessFg string // status text colours adjusted for the surface
	WarningFg string
	DangerFg  string
	Ink       string // main text colour
	Muted     string // secondary text colour
	Bg        string // page background base
	Surface   string // card background
	IsDefault bool   // true when the stock RosPanel name is in effect
	SubURL    string
	Links     []protoLink
	DeepLinks []DeepLink
	Wizard    []WizardPlatform
	// AWG lists one card per server whose AmneziaWG lane the user may use: the
	// config file to import and its QR.
	AWG []awgCard
	// Turn lists one card per WireGuard inbound behind a TURN relay the user may use.
	Turn []turnCard

	StatusLabel string
	StatusClass string
	Used        string
	Limit       string
	HasLimit    bool
	UsedPct     int
	ResetText   string // date the traffic quota next refills, e.g. "07.08.2026"
	HasReset    bool
	Expire      string
	HasExpire   bool
	Online      bool
	LastSeen    string

	Billing     Billing
	Devices     Devices
	ShowConfigs bool // render the raw per-lane share links
	// ShowDownload renders the "download the Clash config" button. Off when the
	// operator requires an HWID: the button fetches this same URL from the browser,
	// which sends no id and would be refused — an offer the page cannot keep.
	ShowDownload bool
}

// Devices is the "your devices" block, shown only when the operator turned device
// binding on. Letting the person unbind their own old phone is what keeps the cap
// from turning into a support queue: the alternative is every replaced device
// becoming a message to the operator.
type Devices struct {
	Show       bool        `json:"show"`
	List       []DeviceRow `json:"list"`
	Count      int         `json:"count"`
	Limit      int         `json:"limit"`      // 0 = unlimited
	CountText  string      `json:"count_text"` // "2 / 3", or just the count when unlimited
	UnbindPath string      `json:"-"`          // POST target that releases one device (<SubURL>/devices/unbind)
}

// DeviceRow is one bound install as the page shows it.
type DeviceRow struct {
	HWID     string `json:"hwid"`
	Title    string `json:"title"`     // model, OS, or the raw id — whatever the client told us
	Sub      string `json:"sub"`       // OS + version, when known
	LastSeen string `json:"last_seen"` // humanised "3 h ago"
}

// Billing is the optional "renew / pay" block on the subscription page. It's built
// by the server (which has plan + payment-provider access) and left zero (Show
// false) when billing is off or no paid plans exist.
type Billing struct {
	Show        bool          `json:"show"`
	CurrentPlan string        `json:"current_plan"` // active plan name ("" = none / manual)
	ExpireText  string        `json:"expire_text"`  // "until DD.MM.YYYY" for a paid expiry, else ""
	Plans       []BillingPlan `json:"plans"`        // paid plans offered for purchase/renewal
	// Providers are the payment methods offered, in the order they are shown. Manual
	// payment, when the operator has it on, is one of them under ManualPayKey.
	Providers []BillingPay `json:"providers"`
	Manual    bool         `json:"manual"` // manual payment is offered
	// ManualOnly is manual payment with nothing else beside it: only then does the
	// page carry the operator's details and say that an admin confirms the transfer.
	// With a provider on the list too, both arrive with the order the user opens.
	ManualOnly bool   `json:"manual_only"`
	Note       string `json:"note"` // the operator's own manual-payment instructions
	PayPath    string `json:"-"`    // POST target that starts a payment (<SubURL>/pay)
	OrderPath  string `json:"-"`    // GET target that reports a pending provider payment (<SubURL>/order)
	// Locked is true while a paid plan is active: only that plan (renewal) is shown,
	// switching to another is blocked, and Cancelable offers cancellation instead.
	Locked     bool   `json:"locked"`
	Cancelable bool   `json:"cancelable"`
	CancelPath string `json:"-"` // POST target that cancels the active plan (<SubURL>/cancel)

	// ExpireAt is the user's expiry as rendered; a purchase from the balance sends it
	// back so a repeated click cannot buy a second period.
	ExpireAt int64 `json:"-"`

	// The wallet. WalletPath is the base the page posts its actions to
	// (<WalletPath>/topup, /promo, /autorenew).
	WalletPath string `json:"-"`
	Wallet     bool   `json:"wallet"`  // the balance block is shown
	Topup      bool   `json:"topup"`   // the balance can be topped up (some payment method exists)
	Balance    string `json:"balance"` // "150" or "19.90", in roubles
	AutoRenew  bool   `json:"auto_renew"`
	// RenewSwitch: the user's plan is one renewal can extend (paid, with a term).
	RenewSwitch bool `json:"renew_switch"`
	// RenewNote says what the renewal will do (hidden while renewal is off, and the
	// switch shows it); RenewShort marks a balance that falls short of it.
	RenewNote  string `json:"renew_note"`
	RenewShort bool   `json:"renew_short"`
	TopupMin   int    `json:"topup_min"`
	TopupHint  string `json:"topup_hint"` // "from 100 ₽"
	BonusDays  string `json:"bonus_days"` // banked referral days, "" when none
	Promo      bool   `json:"promo"`      // a promo field is offered
	RefLink    string `json:"ref_link"`   // the user's invite link, "" when the programme is off
	RefHint    string `json:"ref_hint"`   // what one paying invitee earns
	RefStats   string `json:"ref_stats"`  // "invited 3 · paid 1 · earned 40 ₽"
	// Invitees are the users who came by the link, newest first: when they joined,
	// and what they earned the inviter (or whether they paid).
	Invitees     []HistoryLine `json:"invitees"`
	InviteesMore string        `json:"invitees_more"` // "and 12 more", "" when the list is whole
	RefShare     string        `json:"ref_share"`     // a t.me/share link that hands RefLink to a chat

	// History is what came into and went out of the user's money, newest first.
	History []HistoryLine `json:"history"`

	// Changes are the plans the user may move to now; Addons what they can add to the
	// plan they hold.
	Changes []Extra `json:"changes"`
	Addons  []Extra `json:"addons"`
	// Stamp is what an add-on bought from the balance is held to (core.PurchaseStamp).
	Stamp int64 `json:"-"`
}

// HistoryLine is one line of the payment tab's history.
type HistoryLine struct {
	Title  string `json:"title"` // "Balance top-up", "“Standard” plan"
	When   string `json:"when"`
	Amount string `json:"amount"` // signed: "+50 ₽", "−199 ₽"
	In     bool   `json:"in"`     // money in
	Muted  bool   `json:"muted"`  // not an amount but a quiet status ("no payment yet")
}

// BillingPlan is one purchasable paid tariff shown on the page.
type BillingPlan struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Label   string `json:"label"`   // price + period, e.g. "199 ₽ / 30 d"
	Current bool   `json:"current"` // the user's currently active plan
	// OldPrice is the price before a discount code, shown struck through ("" = none).
	OldPrice string `json:"old_price"`
	// FromBalance: the balance covers the price, so the button pays from it.
	FromBalance bool `json:"from_balance"`
	// Promo names the discount code in the price ("Promo code X: −40 ₽"), "" = none.
	Promo string `json:"promo"`
	// Free: the discount takes the whole price — nothing is paid, from the balance or
	// otherwise.
	Free bool `json:"free"`
	// Button is the pay button's label for the first option.
	Button string `json:"button"`
	// Options are the terms on offer — one period, and each multi-period discount —
	// empty when there is only one.
	Options []PlanOption `json:"options"`
	// Devices are the device counts a new plan can be bought with (the first is the
	// plan's own), empty when it sells no extra devices.
	Devices []DeviceOption `json:"devices"`
}

// DeviceOption is one device count a plan can be bought with.
type DeviceOption struct {
	Extra int    `json:"extra"` // devices beyond the plan's own
	Label string `json:"label"` // "3 devices (+100 ₽ per period)"
}

// Extra is a purchase on top of the plan held, or a move to another plan: its kind
// (change, devices, traffic), the plan (a change) or the count or pack index, what it
// is and costs, and whether the balance covers it (or it is free).
type Extra struct {
	Kind        string `json:"kind"`
	PlanID      int64  `json:"plan_id"`
	N           int    `json:"n"`
	Label       string `json:"label"` // the whole offer in one line (the confirmation)
	FromBalance bool   `json:"from_balance"`
	// Name, Sub and Badge are the offer as a card: the plan or the add-on, its price
	// per period or what it lasts, and what taking it costs or gives now. Up marks a
	// change that is paid for.
	Name  string `json:"name"`
	Sub   string `json:"sub"`
	Badge string `json:"badge"`
	Up    bool   `json:"up"`
}

// PlanOption is one term a plan can be bought for.
type PlanOption struct {
	Periods     int    `json:"periods"`
	Label       string `json:"label"` // "3 × 30 d — 537 ₽ (−10%)"
	FromBalance bool   `json:"from_balance"`
	Button      string `json:"button"` // the pay button's label for this term
}

// BillingPay is one payment method the user can choose.
type BillingPay struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// ManualPayKey is the method key that stands for manual payment, so the page offers
// it beside the providers and the pay route knows which tap meant "I will transfer
// it myself". Provider keys come from the payments registry, which has no such key.
const ManualPayKey = "manual"

type protoLink struct {
	Proto string
	URL   string
}

// subStatus maps the derived user status to a label + badge color class.
func subStatus(s string, lang i18n.Lang) (label, class string) {
	switch s {
	case "active":
		return i18n.T(lang, "sub.statusActive"), "green"
	case "disabled":
		return i18n.T(lang, "sub.statusOff"), "gray"
	case "expired":
		return i18n.T(lang, "sub.statusExpired"), "red"
	case "limited":
		return i18n.T(lang, "sub.statusLimited"), "orange"
	default:
		return s, "gray"
	}
}

// Page renders the human-facing subscription page (usage stats, QR of the sub
// URL, copy button, per-client import buttons, and the raw links).
//
// servers spans every server the user is on — the local one plus each enabled node
// — so the "individual configs" list shows one labelled entry per protocol × server
// (with a single server it's unchanged). local is the panel's own settings, and
// everything about the panel rather than about a server reads from it: the sub URL
// behind the QR and the copy button, the AmneziaWG download links, the branding.
// It is passed rather than taken from servers[0] because the ordering decides what
// lands there — a node comes first by weight, by distance or by load, and the master
// leaves the list entirely once it is full with hide-when-full set. Every one of
// those would have addressed the panel's own links at a node, which serves none of
// them.
func Page(u model.User, local *model.Settings, servers []Server, billing Billing, devices Devices, showDownload bool, lang i18n.Lang) ([]byte, error) {
	data, err := buildPageData(u, local, servers, billing, devices, showDownload, lang)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildPageData gathers what the page shows; Page renders it and View hands it out.
func buildPageData(u model.User, local *model.Settings, servers []Server, billing Billing, devices Devices, showDownload bool, lang i18n.Lang) (pageData, error) {
	if len(servers) == 0 {
		return pageData{}, fmt.Errorf("no settings for subscription page")
	}
	subURL := URL(local, u.SubToken)
	used := u.UsedUp + u.UsedDown

	// Only lanes enabled in the Connections panel appear on the page, across every
	// server: the built-in ones first, then that server's custom inbounds. The label
	// carries the node name (Settings.ProtoLabel / link.CustomLabel), so a multi-node
	// user can tell the entries apart.
	var protoLinks, extLinks []protoLink
	var awgCards []awgCard
	var turnCards []turnCard
	for _, srv := range servers {
		s := srv.Set
		if s.AWGEnabled && s.AWGPort != 0 && srv.allowsBuiltin(model.LaneAWG) {
			awgCards = append(awgCards, awgCard{
				Label: s.ProtoLabelFor(model.ProtoAWG, &u),
				// The label names the server whose tunnel this is, but the config
				// itself is downloaded from the panel — the node hosts no /sub path.
				ConfURL: AWGConfURL(local, u.SubToken, s.ServerID),
				QRURL:   fmt.Sprintf("%s/awg/%d.png", subURL, s.ServerID),
			})
		}
		if s.VLESSEnabled && srv.allowsBuiltin(model.LaneVLESS) {
			protoLinks = append(protoLinks, protoLink{s.ProtoLabelFor(model.ProtoVLESS, &u), link.VLESS(u, s)})
		}
		if s.RealityEnabled && s.RealityPublicKey != "" && srv.allowsBuiltin(model.LaneReality) {
			protoLinks = append(protoLinks, protoLink{s.ProtoLabelFor(model.ProtoReality, &u), link.Reality(u, s)})
		}
		if s.HysteriaEnabled && srv.allowsBuiltin(model.LaneHysteria) {
			protoLinks = append(protoLinks, protoLink{s.ProtoLabelFor(model.ProtoHysteria, &u), link.Hysteria2(u, s)})
		}
		for _, in := range srv.Custom {
			if !srv.allowsInbound(in.ID) {
				continue
			}
			if in.Protocol == model.InbWireGuard {
				peer := net.JoinHostPort(s.Host, strconv.Itoa(in.Port))
				turnCards = append(turnCards, turnCard{
					Label:   link.CustomLabelFor(in, u, s),
					Peer:    peer,
					Link:    in.Opts.TurnLink,
					ConfURL: TurnConfURL(local, u.SubToken, in.ID),
				})
				c := &turnCards[len(turnCards)-1]
				// Free Turn Proxy's masked wire first: the apps that speak it are the ones
				// the call service does not shape. WINGS V reaches the relay unmasked.
				for _, app := range []struct {
					name, link, appLink string
				}{
					{"VK Turn Proxy (iOS)", TurnImportLink(u, s, in),
						"https://github.com/anton48/vk-turn-proxy-ios"},
					{"Free Turn Proxy (Android)", FreeTurnImportLink(u, s, in, TurnClientConf(u, s, in)),
						"https://github.com/samosvalishe/turn-proxy-android"},
					{"WINGS V (Android, Windows, Linux)", WingsVImportLink(u, s, in),
						"https://github.com/WINGS-N/WINGSV"},
				} {
					if app.link != "" {
						c.Apps = append(c.Apps, turnApp{
							Name: app.name, Link: app.link, Href: template.URL(app.link), AppLink: app.appLink,
						})
					}
				}
				continue
			}
			if l := link.Custom(u, in, s); l != "" {
				protoLinks = append(protoLinks, protoLink{link.CustomLabelFor(in, u, s), l})
			}
		}
		for _, r := range srv.relayEntries(u) {
			protoLinks = append(protoLinks, protoLink{r.name, r.link(s)})
		}
		// External servers are not ours: the link is theirs and so is the label. They
		// hang off whichever entry carries them for the whole subscription, so they are
		// gathered here and appended once the servers are done — last, and in the order
		// the link list has them.
		for _, e := range srv.externalEndpoints() {
			extLinks = append(extLinks, protoLink{e.Name, e.Link})
		}
	}
	protoLinks = append(protoLinks, extLinks...)

	statusLabel, statusClass := subStatus(u.Status, lang)
	// A custom panel name is the operator's own text and passes through verbatim;
	// only the stock name is localised, so an English page does not announce itself
	// in Russian in the <title> and the header.
	brandName := branding.Name(local.PanelName)
	isDefault := brandName == branding.DefaultName
	if isDefault {
		brandName = i18n.T(lang, "sub.defaultBrand")
	}
	theme := branding.ParseTheme(local.PanelTheme)
	data := pageData{
		L:           text(lang),
		Name:        u.Name,
		BrandName:   brandName,
		Brand:       theme.Accent,
		BrandDark:   branding.Darken(theme.Accent, 0.16),
		AccentFg:    branding.Fg(theme.Accent, theme.Surface),
		OnBrand:     branding.OnFill(theme.Accent),
		SuccessFg:   branding.Fg("#059669", theme.Surface),
		WarningFg:   branding.Fg("#ea580c", theme.Surface),
		DangerFg:    branding.Fg("#dc2626", theme.Surface),
		Ink:         theme.Text,
		Muted:       theme.Muted,
		Bg:          theme.Bg,
		Surface:     theme.Surface,
		IsDefault:   isDefault,
		SubURL:      subURL,
		Links:       protoLinks,
		AWG:         awgCards,
		Turn:        turnCards,
		DeepLinks:   DeepLinks(subURL, lang, local.SubHappCrypt),
		Wizard:      Wizard(lang),
		StatusLabel: statusLabel,
		StatusClass: statusClass,
		Used:        fmtBytes(used),
		Limit:       "∞",
		Expire:      i18n.T(lang, "sub.never"),
		Online:      u.LastSeen > 0 && time.Now().Unix()-u.LastSeen < 120,
		Billing:     billing,
		Devices:     devices,
		// Gated by showDownload for the same reason the download button is, and it is the
		// sharper of the two: the browser path renders this page WITHOUT running the
		// device cap (see subscription.go — it returns before admitDevice), so printing
		// the raw share links here handed every credential to a client that never
		// identified itself. Anyone holding the subscription URL could fetch the page
		// with a browser Accept header, copy the links and use them from any number of
		// devices, with no slot consumed and the HWID roster none the wiser.
		ShowConfigs:  local.SubShowConfigs && showDownload,
		ShowDownload: showDownload,
	}
	if u.DataLimit > 0 {
		data.HasLimit = true
		data.Limit = fmtBytes(u.DataLimit)
		data.UsedPct = min(100, int(used*100/u.DataLimit))
		if next, ok := nextResetTime(u.ResetPeriod, u.LastResetAt); ok {
			data.HasReset = true
			data.ResetText = next.Format("02.01.2006")
		}
	}
	if u.ExpireAt > 0 {
		data.HasExpire = true
		data.Expire = i18n.T(lang, "sub.until", time.Unix(u.ExpireAt, 0).Format("02.01.2006"))
	} else if u.HoldSeconds > 0 {
		// No date to show yet: the term is waiting for the first connection, and the
		// person looking at this page is the one who starts it.
		data.HasExpire = true
		data.Expire = i18n.T(lang, "sub.holdTerm", i18n.TN(lang, "notify.days", int(u.HoldSeconds/86400)))
	}
	if !data.Online && u.LastSeen > 0 {
		data.LastSeen = relTime(time.Now().Unix()-u.LastSeen, lang)
	}
	return data, nil
}

// nextResetTime returns when the automatic traffic-quota reset next fires, given
// the user's reset period and last-reset anchor. Mirrors core.resetDue: "days:N"
// is a rolling cycle (anchor + N days); the calendar periods return the next
// boundary. Returns ok=false when no reset is scheduled.
func nextResetTime(period string, lastReset int64) (time.Time, bool) {
	if period == "" || period == "none" || lastReset == 0 {
		return time.Time{}, false
	}
	last := time.Unix(lastReset, 0)
	if spec, ok := strings.CutPrefix(period, "days:"); ok {
		n, err := strconv.Atoi(spec)
		if err != nil || n <= 0 {
			return time.Time{}, false
		}
		return last.AddDate(0, 0, n), true
	}
	y, m, d := last.Date()
	loc := last.Location()
	switch period {
	case "daily":
		return time.Date(y, m, d+1, 0, 0, 0, 0, loc), true
	case "weekly":
		// Start of the ISO week (Monday) following the anchor's week.
		offset := (int(last.Weekday()) + 6) % 7 // days since Monday
		return time.Date(y, m, d-offset+7, 0, 0, 0, 0, loc), true
	case "monthly":
		return time.Date(y, m+1, 1, 0, 0, 0, 0, loc), true
	case "yearly":
		return time.Date(y+1, 1, 1, 0, 0, 0, 0, loc), true
	}
	return time.Time{}, false
}

func fmtBytes(n int64) string {
	if n <= 0 {
		return "0"
	}
	u := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(u)-1 {
		v /= 1024
		i++
	}
	if v < 10 && i > 0 {
		return fmt.Sprintf("%.1f %s", v, u[i])
	}
	return fmt.Sprintf("%.0f %s", v, u[i])
}

// RelTime renders an age in seconds as "3 h ago" in the reader's language. Exported
// because the server builds the device rows (it holds the store) while the wording
// belongs to the page.
func RelTime(sec int64, lang i18n.Lang) string { return relTime(sec, lang) }

func relTime(sec int64, lang i18n.Lang) string {
	// A sighting stamped in the future — a device whose clock ran ahead, or a host
	// whose clock was corrected backwards — would otherwise render as "-3 min ago".
	if sec < 0 {
		sec = 0
	}
	switch {
	case sec < 3600:
		return i18n.T(lang, "sub.minutesAgo", sec/60)
	case sec < 86400:
		return i18n.T(lang, "sub.hoursAgo", sec/3600)
	default:
		return i18n.T(lang, "sub.daysAgo", sec/86400)
	}
}
