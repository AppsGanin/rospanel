package sub

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"time"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

//go:embed logo.svg
var logoSVG []byte

// Logo returns the embedded РосПанель logo (SVG).
func Logo() []byte { return logoSVG }

//go:embed page.html
var pageHTML string

var pageTmpl = template.Must(template.New("sub").Parse(pageHTML))

// appRedirectTmpl is a tiny page that immediately hands off to a client's deep
// link. It's opened in the EXTERNAL browser (via Telegram's openLink) because a
// custom app scheme (happ://, v2rayng://, …) can't be launched from inside the
// Telegram webview — but the browser it lands in resolves the scheme and opens
// the app. Href is template.URL so the scheme survives html/template's URL filter.
var appRedirectTmpl = template.Must(template.New("appredir").Parse(
	`<!doctype html><html lang="ru"><head><meta charset="utf-8">` +
		`<meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>Открываем приложение…</title>` +
		`<script>location.replace("{{.Href}}")</script>` +
		`<meta http-equiv="refresh" content="0;url={{.Href}}"></head>` +
		`<body style="font-family:sans-serif;padding:24px;color:#333">` +
		`<p>Открываем приложение…<br>Если оно не открылось — ` +
		`<a href="{{.Href}}">нажмите здесь</a>.</p></body></html>`))

// AppRedirect renders the deep-link hand-off page for one client's share link.
func AppRedirect(href template.URL) ([]byte, error) {
	var buf bytes.Buffer
	if err := appRedirectTmpl.Execute(&buf, struct{ Href template.URL }{href}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type pageData struct {
	Name      string
	BrandName string // panel display name (defaults to «РосПанель»)
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
	IsDefault bool   // true when the stock РосПанель name is in effect
	SubURL    string
	Links     []protoLink
	DeepLinks []DeepLink

	StatusLabel string
	StatusClass string
	Used        string
	Limit       string
	HasLimit    bool
	UsedPct     int
	Expire      string
	HasExpire   bool
	Online      bool
	LastSeen    string

	Billing Billing
}

// Billing is the optional "renew / pay" block on the subscription page. It's built
// by the server (which has plan + payment-provider access) and left zero (Show
// false) when billing is off or no paid plans exist.
type Billing struct {
	Show        bool
	CurrentPlan string        // active plan name ("" = none / manual)
	ExpireText  string        // "до DD.MM.YYYY" for a paid expiry, else ""
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
	Label   string // price + period, e.g. "199 ₽ / 30 дн."
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
func subStatus(s string) (label, class string) {
	switch s {
	case "active":
		return "Активно", "green"
	case "disabled":
		return "Отключено", "gray"
	case "expired":
		return "Срок истёк", "red"
	case "limited":
		return "Лимит исчерпан", "orange"
	default:
		return s, "gray"
	}
}

// Page renders the human-facing subscription page (usage stats, QR of the sub
// URL, copy button, per-client import buttons, and the raw links).
func Page(u model.User, set *model.Settings, billing Billing) ([]byte, error) {
	subURL := URL(set, u.SubToken)
	used := u.UsedUp + u.UsedDown

	// Only protocols enabled in the Connections panel appear on the page.
	var protoLinks []protoLink
	if set.VLESSEnabled {
		protoLinks = append(protoLinks, protoLink{set.ProtoLabel(model.ProtoVLESS), link.VLESS(u, set)})
	}
	if set.RealityEnabled {
		protoLinks = append(protoLinks, protoLink{set.ProtoLabel(model.ProtoReality), link.Reality(u, set)})
	}
	if set.TrojanEnabled {
		protoLinks = append(protoLinks, protoLink{set.ProtoLabel(model.ProtoTrojan), link.Trojan(u, set)})
	}
	if set.HysteriaEnabled {
		protoLinks = append(protoLinks, protoLink{set.ProtoLabel(model.ProtoHysteria), link.Hysteria2(u, set)})
	}

	statusLabel, statusClass := subStatus(u.Status)
	theme := branding.ParseTheme(set.PanelTheme)
	data := pageData{
		Name:        u.Name,
		BrandName:   branding.Name(set.PanelName),
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
		IsDefault:   branding.Name(set.PanelName) == branding.DefaultName,
		SubURL:      subURL,
		Links:       protoLinks,
		DeepLinks:   DeepLinks(subURL),
		StatusLabel: statusLabel,
		StatusClass: statusClass,
		Used:        fmtBytes(used),
		Limit:       "∞",
		Expire:      "бессрочно",
		Online:      u.LastSeen > 0 && time.Now().Unix()-u.LastSeen < 120,
		Billing:     billing,
	}
	if u.DataLimit > 0 {
		data.HasLimit = true
		data.Limit = fmtBytes(u.DataLimit)
		data.UsedPct = min(100, int(used*100/u.DataLimit))
	}
	if u.ExpireAt > 0 {
		data.HasExpire = true
		data.Expire = "до " + time.Unix(u.ExpireAt, 0).Format("02.01.2006")
	}
	if !data.Online && u.LastSeen > 0 {
		data.LastSeen = relTime(time.Now().Unix() - u.LastSeen)
	}

	var buf bytes.Buffer
	if err := pageTmpl.Execute(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
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

func relTime(sec int64) string {
	switch {
	case sec < 3600:
		return fmt.Sprintf("%d мин назад", sec/60)
	case sec < 86400:
		return fmt.Sprintf("%d ч назад", sec/3600)
	default:
		return fmt.Sprintf("%d дн назад", sec/86400)
	}
}
