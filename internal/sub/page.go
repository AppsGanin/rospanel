package sub

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
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
	ManualNote     string
	ScanQR         string
	CopyLink       string
	OpenInApp      string
	DownloadClash  string
	SingleConfigs  string
	AWGTitle       string
	AWGHint        string
	AWGDownload    string
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
		ManualNote:     t("sub.manualNote"),
		ScanQR:         t("sub.scanQR"),
		CopyLink:       t("sub.copyLink"),
		OpenInApp:      t("sub.openInApp"),
		DownloadClash:  t("sub.downloadClash"),
		SingleConfigs:  t("sub.singleConfigs"),
		AWGTitle:       t("sub.awgTitle"),
		AWGHint:        t("sub.awgHint"),
		AWGDownload:    t("sub.awgDownload"),
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

		Devices:        t("sub.devices"),
		DevicesHint:    t("sub.devicesHint"),
		DevicesEmpty:   t("sub.devicesEmpty"),
		DeviceRemove:   t("sub.deviceRemove"),
		DeviceConfirm:  t("sub.deviceRemoveConfirm"),
		DeviceFailed:   t("sub.deviceRemoveFailed"),
		DeviceNeverUse: t("sub.deviceNeverSeen"),
	}
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
	// AWG lists one card per server whose AmneziaWG lane the user may use: the
	// config file to import and its QR.
	AWG []awgCard

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
	Show       bool
	List       []DeviceRow
	Count      int
	Limit      int    // 0 = unlimited
	CountText  string // "2 / 3", or just the count when unlimited
	UnbindPath string // POST target that releases one device (<SubURL>/devices/unbind)
}

// DeviceRow is one bound install as the page shows it.
type DeviceRow struct {
	HWID     string
	Title    string // model, OS, or the raw id — whatever the client told us
	Sub      string // OS + version, when known
	LastSeen string // humanised "3 h ago"
}

// Billing is the optional "renew / pay" block on the subscription page. It's built
// by the server (which has plan + payment-provider access) and left zero (Show
// false) when billing is off or no paid plans exist.
type Billing struct {
	Show        bool
	CurrentPlan string        // active plan name ("" = none / manual)
	ExpireText  string        // "until DD.MM.YYYY" for a paid expiry, else ""
	Plans       []BillingPlan // paid plans offered for purchase/renewal
	Providers   []BillingPay  // enabled payment methods (empty ⇒ manual only)
	Manual      bool          // no automatic provider ⇒ pay button creates a manual order
	Note        string        // manual-payment instructions when no provider is set
	PayPath     string        // POST target that starts a payment (<SubURL>/pay)
	OrderPath   string        // GET target that reports a pending provider payment (<SubURL>/order)
	// Locked is true while a paid plan is active: only that plan (renewal) is shown,
	// switching to another is blocked, and Cancelable offers cancellation instead.
	Locked     bool
	Cancelable bool
	CancelPath string // POST target that cancels the active plan (<SubURL>/cancel)
}

// BillingPlan is one purchasable paid tariff shown on the page.
type BillingPlan struct {
	ID      int64
	Name    string
	Label   string // price + period, e.g. "199 ₽ / 30 d"
	Current bool   // the user's currently active plan
}

// BillingPay is one payment method the user can choose.
type BillingPay struct {
	Key   string
	Label string
}

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
// Page renders the human-facing subscription page. sets spans every server the
// user is on — the local one plus each enabled node — so the "individual configs"
// list shows one labelled entry per protocol × server (with a single server it's
// unchanged). sets[0] is the local server, used for the sub URL, branding and
// billing.
func Page(u model.User, servers []Server, billing Billing, devices Devices, showDownload bool, lang i18n.Lang) ([]byte, error) {
	if len(servers) == 0 {
		return nil, fmt.Errorf("no settings for subscription page")
	}
	set := servers[0].Set
	subURL := URL(set, u.SubToken)
	used := u.UsedUp + u.UsedDown

	// Only lanes enabled in the Connections panel appear on the page, across every
	// server: the built-in ones first, then that server's custom inbounds. The label
	// carries the node name (Settings.ProtoLabel / link.CustomLabel), so a multi-node
	// user can tell the entries apart.
	var protoLinks []protoLink
	var awgCards []awgCard
	for _, srv := range servers {
		s := srv.Set
		if s.AWGEnabled && s.AWGPort != 0 && srv.allowsBuiltin(model.LaneAWG) {
			awgCards = append(awgCards, awgCard{
				Label:   s.ProtoLabel(model.ProtoAWG),
				ConfURL: AWGConfURL(s, u.SubToken, s.ServerID),
				QRURL:   fmt.Sprintf("%s/awg/%d.png", subURL, s.ServerID),
			})
		}
		if s.VLESSEnabled && srv.allowsBuiltin(model.LaneVLESS) {
			protoLinks = append(protoLinks, protoLink{s.ProtoLabel(model.ProtoVLESS), link.VLESS(u, s)})
		}
		if s.RealityEnabled && srv.allowsBuiltin(model.LaneReality) {
			protoLinks = append(protoLinks, protoLink{s.ProtoLabel(model.ProtoReality), link.Reality(u, s)})
		}
		if s.HysteriaEnabled && srv.allowsBuiltin(model.LaneHysteria) {
			protoLinks = append(protoLinks, protoLink{s.ProtoLabel(model.ProtoHysteria), link.Hysteria2(u, s)})
		}
		for _, in := range srv.Custom {
			if !srv.allowsInbound(in.ID) {
				continue
			}
			if l := link.Custom(u, in, s); l != "" {
				protoLinks = append(protoLinks, protoLink{link.CustomLabel(in, s), l})
			}
		}
	}

	statusLabel, statusClass := subStatus(u.Status, lang)
	// A custom panel name is the operator's own text and passes through verbatim;
	// only the stock name is localised, so an English page does not announce itself
	// in Russian in the <title> and the header.
	brandName := branding.Name(set.PanelName)
	isDefault := brandName == branding.DefaultName
	if isDefault {
		brandName = i18n.T(lang, "sub.defaultBrand")
	}
	theme := branding.ParseTheme(set.PanelTheme)
	data := pageData{
		L:           text(lang),
		Name:        u.Name,
		BrandName:   brandName,
		Brand:       theme.Accent,
		BrandDark:   branding.Darken(theme.Accent, 0.16),
		AccentFg:    branding.Fg(theme.Accent, theme.Surface),
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
		DeepLinks:   DeepLinks(subURL, lang),
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
		ShowConfigs:  set.SubShowConfigs && showDownload,
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
	}
	if !data.Online && u.LastSeen > 0 {
		data.LastSeen = relTime(time.Now().Unix()-u.LastSeen, lang)
	}

	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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
