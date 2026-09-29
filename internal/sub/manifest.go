package sub

import (
	"encoding/json"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// Manifest is the web app manifest that lets a user put their subscription page on
// the home screen: it opens straight on their page, in a window of its own. The
// start URL carries the user's token — it is their page, and only theirs to install.
func Manifest(set *model.Settings, token string, lang i18n.Lang, logoType string) []byte {
	name := branding.Name(set.PanelName)
	if name == branding.DefaultName {
		name = i18n.T(lang, "sub.defaultBrand")
	}
	theme := branding.ParseTheme(set.PanelTheme)
	subURL := URL(set, token)
	m := map[string]any{
		"name":             name,
		"short_name":       name,
		"start_url":        subURL,
		"scope":            subURL,
		"display":          "standalone",
		"background_color": theme.Bg,
		"theme_color":      theme.Accent,
		"icons": []map[string]string{
			{"src": subURL + "/logo.svg", "sizes": "any", "type": logoType, "purpose": "any"},
		},
	}
	b, _ := json.Marshal(m)
	return b
}
