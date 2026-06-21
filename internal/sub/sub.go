// Package sub builds the per-user subscription: the machine payload consumed by
// VPN clients and the human-facing page (QR + one-tap import buttons).
package sub

import (
	"encoding/base64"
	"html/template"
	"net/url"
	"strings"

	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// ShareLinks returns the enabled protocol links for a user, in client-import
// order. Protocols switched off in the Connections panel are omitted.
func ShareLinks(u model.User, set *model.Settings) []string {
	links := make([]string, 0, 4)
	if set.VLESSEnabled {
		links = append(links, link.VLESS(u, set))
	}
	if set.RealityEnabled {
		links = append(links, link.Reality(u, set))
	}
	if set.TrojanEnabled {
		links = append(links, link.Trojan(u, set))
	}
	if set.HysteriaEnabled {
		links = append(links, link.Hysteria2(u, set))
	}
	return links
}

// Base64Payload is the universal v2ray-style subscription body: the links joined
// by newlines, base64-encoded. Consumed by v2rayNG, Hiddify, Streisand, NekoBox,
// Shadowrocket, etc.
func Base64Payload(links []string) string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))
}

// URL is the public subscription URL for a token (always https on the host).
func URL(set *model.Settings, token string) string {
	return "https://" + set.Host + "/" + set.SubPathOr() + "/" + token
}

// DeepLink is one "open in client" button. Href is template.URL so html/template
// keeps the custom client schemes (happ://, v2rayng://, …) instead of sanitizing
// them to "#ZgotmplZ". Platform notes which OS the client targets.
type DeepLink struct {
	Label    string
	Platform string
	Href     template.URL
}

// DeepLinks builds best-effort import deep-links for the popular clients, most
// popular first. Schemes drift across client releases — verify periodically.
func DeepLinks(subURL string) []DeepLink {
	enc := url.QueryEscape(subURL)
	return []DeepLink{
		{"Happ", "Все платформы · TV", template.URL("happ://add/" + enc)},
		{"INCY", "Все платформы · TV", template.URL("incy://import/" + subURL)},
		{"Hiddify", "Все платформы", template.URL("hiddify://import/" + subURL)},
		{"Karing", "Все платформы · TV", template.URL("karing://install-config?url=" + enc)},
		{"sing-box", "Все платформы", template.URL("sing-box://import-remote-profile?url=" + enc)},
		{"Clash Meta / Mihomo", "Windows · macOS · Linux · Android", template.URL("clash://install-config?url=" + enc)},
		{"V2Box", "iOS · macOS · Android", template.URL("v2box://install-sub?url=" + enc)},
		{"v2rayNG", "Android", template.URL("v2rayng://install-sub?url=" + enc)},
		{"NekoBox", "Android", template.URL("sn://subscription?url=" + enc)},
		{"Streisand", "iOS · macOS · tvOS", template.URL("streisand://import/" + subURL)},
		{"Shadowrocket", "iOS · macOS · tvOS", template.URL("shadowrocket://add/sub://" + enc)},
	}
}
