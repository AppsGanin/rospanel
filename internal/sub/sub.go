// Package sub builds the per-user subscription: the machine payload consumed by
// VPN clients and the human-facing page (QR + one-tap import buttons).
package sub

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"net/url"
	"strings"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/link"
	"github.com/AppsGanin/rospanel/internal/model"
)

// ShareLinks returns one server's links for a user, in client-import order: the
// enabled built-in lanes first, then each custom inbound in its display order.
// Protocols switched off in the Connections panel are omitted.
func ShareLinks(u model.User, srv Server) []string {
	set := srv.Set
	links := make([]string, 0, 3+len(srv.Custom))
	if set.VLESSEnabled && srv.allowsBuiltin(model.LaneVLESS) {
		links = append(links, link.VLESS(u, set))
	}
	if set.RealityEnabled && srv.allowsBuiltin(model.LaneReality) {
		links = append(links, link.Reality(u, set))
	}
	if set.HysteriaEnabled && srv.allowsBuiltin(model.LaneHysteria) {
		links = append(links, link.Hysteria2(u, set))
	}
	for _, in := range srv.Custom {
		if !srv.allowsInbound(in.ID) {
			continue
		}
		if l := link.Custom(u, in, set); l != "" {
			links = append(links, l)
		}
	}
	return links
}

// ShareLinksAll concatenates the links for a user across every server — the local
// one plus each enabled node — so a subscription carries one entry per lane × server.
// Each settings clone carries its own host/ports/keys and a NodeLabel that
// disambiguates the links.
func ShareLinksAll(u model.User, servers []Server) []string {
	var links []string
	for _, srv := range servers {
		links = append(links, ShareLinks(u, srv)...)
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
func DeepLinks(subURL string, lang i18n.Lang) []DeepLink {
	enc := url.QueryEscape(subURL)
	// Only the generic platform blurbs are translated; the OS names below are
	// proper nouns and read the same in every language.
	all := i18n.T(lang, "sub.allPlatforms")
	allTV := i18n.T(lang, "sub.allPlusTV")
	// Shadowrocket's sub:// URI carries the subscription URL base64-encoded (NOT
	// percent-encoded) — feeding it a %-escaped URL makes it fail with "invalid URL".
	subB64 := base64.StdEncoding.EncodeToString([]byte(subURL))
	return []DeepLink{
		{"Happ", allTV, template.URL("happ://add/" + subURL)},
		{"INCY", allTV, template.URL("incy://import/" + subURL)},
		{"v2RayTun", allTV, template.URL("v2raytun://import/" + subURL)},
		{"Hiddify", all, template.URL("hiddify://import/" + subURL)},
		{"Karing", allTV, template.URL("karing://install-config?url=" + enc)},
		{"sing-box", all, template.URL("sing-box://import-remote-profile?url=" + enc)},
		{"Clash Meta / Mihomo", "Windows · macOS · Linux · Android", template.URL("clash://install-config?url=" + enc)},
		{"V2Box", "iOS · macOS · Android", template.URL("v2box://install-sub?url=" + enc)},
		{"v2rayNG", "Android", template.URL("v2rayng://install-sub?url=" + enc)},
		{"NekoBox", "Android", template.URL("sn://subscription?url=" + enc)},
		{"Streisand", "iOS · macOS · tvOS", template.URL("streisand://import/" + subURL)},
		{"Shadowrocket", "iOS · macOS · tvOS", template.URL("shadowrocket://add/sub://" + subB64)},
	}
}

// AWGConfURL is where a user downloads their AmneziaWG config for one server
// (0 = the master): <sub>/awg/<id>.conf; the QR of the same text is <id>.png.
func AWGConfURL(set *model.Settings, token string, serverID int64) string {
	return fmt.Sprintf("%s/awg/%d.conf", URL(set, token), serverID)
}

// AWGFileName is the config's file name — the Amnezia apps show it as the
// tunnel's name, so it is the server's label with everything a file system or a
// header would object to replaced.
func AWGFileName(set *model.Settings) string {
	label := set.ProtoLabel(model.ProtoAWG)
	var b strings.Builder
	for _, r := range label {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ', r == '·', r == '.':
			b.WriteRune('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = "amneziawg"
	}
	if len(name) > 15 { // wg interface names are 15 bytes; the apps derive one from the file
		name = name[:15]
	}
	return name + ".conf"
}
