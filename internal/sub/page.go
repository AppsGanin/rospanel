package sub

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"time"

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

type pageData struct {
	Name      string
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
func Page(u model.User, set *model.Settings) ([]byte, error) {
	subURL := URL(set, u.SubToken)
	used := u.UsedUp + u.UsedDown

	// Only protocols enabled in the Connections panel appear on the page.
	var protoLinks []protoLink
	if set.VLESSEnabled {
		protoLinks = append(protoLinks, protoLink{model.ProtoVLESS, link.VLESS(u, set)})
	}
	if set.RealityEnabled {
		protoLinks = append(protoLinks, protoLink{model.ProtoReality, link.Reality(u, set)})
	}
	if set.TrojanEnabled {
		protoLinks = append(protoLinks, protoLink{model.ProtoTrojan, link.Trojan(u, set)})
	}
	if set.HysteriaEnabled {
		protoLinks = append(protoLinks, protoLink{model.ProtoHysteria, link.Hysteria2(u, set)})
	}

	statusLabel, statusClass := subStatus(u.Status)
	data := pageData{
		Name:        u.Name,
		SubURL:      subURL,
		Links:       protoLinks,
		DeepLinks:   DeepLinks(subURL),
		StatusLabel: statusLabel,
		StatusClass: statusClass,
		Used:        fmtBytes(used),
		Limit:       "∞",
		Expire:      "бессрочно",
		Online:      u.LastSeen > 0 && time.Now().Unix()-u.LastSeen < 120,
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
