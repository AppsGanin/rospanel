package sub

import (
	"fmt"
	"html/template"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The connect wizard: install the app, add the subscription, connect. It recommends
// one client per platform — Happ unless the operator picked another for it
// (model.Settings.SubWizardApps) — so a newcomer has one thing to do instead of a
// list to choose from; "another app" still opens the list.
//
// These are Happ's own download links (happ.su/main). They drift across releases;
// check them there when a store or a release file name changes.

// WizardPlatform is the wizard on one platform: its client, where that comes from,
// and the steps' texts naming it.
type WizardPlatform struct {
	Key   string // ios | android | windows | macos | linux | tv — what the page detects
	Name  string
	Links []WizardLink
	// AppIndex is the client's index in DeepLinks (the /app/<n> hand-off), Href its
	// import link.
	AppIndex                      int
	Href                          template.URL
	Hint1, Hint2, Hint3, AddLabel string
}

// WizardLink is one place to get the app.
type WizardLink struct {
	Label string
	URL   string
}

// WizardAppFor is the key of the client the wizard installs on one platform.
func WizardAppFor(set *model.Settings, plat string) string {
	if k := set.SubWizardApps[plat]; k != "" {
		return k
	}
	return "happ"
}

// WizardApps is the wizard's client on every platform, Happ where none was picked.
func WizardApps(set *model.Settings) map[string]string {
	out := make(map[string]string, len(model.WizardPlatformKeys))
	for _, k := range model.WizardPlatformKeys {
		out[k] = WizardAppFor(set, k)
	}
	return out
}

// Wizard lists the platforms the wizard offers, in the order the switcher shows
// them: each with its client (WizardAppFor) and that client's download buttons
// there (AppLinks). deep is the page's buttons (DeepLinks); a platform whose client
// is switched off, or has no download links there, is left out.
func Wizard(lang i18n.Lang, set *model.Settings, deep []DeepLink) []WizardPlatform {
	var out []WizardPlatform
	for _, p := range happWizard(lang) { // the platform names and their order
		key := WizardAppFor(set, p.Key)
		idx := slices.IndexFunc(deep, func(d DeepLink) bool { return d.Key == key })
		if idx < 0 {
			continue
		}
		app := deep[idx].Label
		var ls []WizardLink
		for _, l := range AppLinks(lang, set, key)[p.Key] {
			if !model.ValidWizardLink(l.URL) {
				continue
			}
			label := l.Label
			if label == "" {
				label = i18n.T(lang, "sub.wzDownload")
			}
			ls = append(ls, WizardLink{label, l.URL})
		}
		hint1 := i18n.T(lang, "sub.wzStep1Hint", app)
		if len(ls) == 0 {
			hint1 = i18n.T(lang, "sub.wzStep1NoLinks", app)
		}
		out = append(out, WizardPlatform{
			Key: p.Key, Name: p.Name, Links: ls, AppIndex: idx, Href: deep[idx].Href,
			Hint1: hint1, Hint2: i18n.T(lang, "sub.wzStep2Hint", app), Hint3: i18n.T(lang, "sub.wzStep3Hint", app),
			AddLabel: i18n.T(lang, "sub.wzAdd", app),
		})
	}
	// A platform without download links is one its client may not even exist on, so
	// it is left out — unless no platform has any, when the wizard still walks
	// through adding and connecting, telling the visitor to install the client.
	withLinks := slices.DeleteFunc(slices.Clone(out), func(p WizardPlatform) bool { return len(p.Links) == 0 })
	if len(withLinks) > 0 {
		return withLinks
	}
	return out
}

// AppLinks is one client's download buttons per platform: the operator's, or
// while the operator has saved none of their own, the stock ones (StockAppLinks).
func AppLinks(lang i18n.Lang, set *model.Settings, key string) map[string][]model.SubAppLink {
	if links, ok := set.SubAppLinks[key]; ok {
		return links
	}
	return StockAppLinks(lang, key)
}

// StockAppLinks is a stock client's own download buttons per platform — its store
// listings and release pages — or an empty map for a client of the operator's.
//
// They drift: a store pulls an app, a repository moves. Checked 2026-11-09 against
// the App Store lookup API, Google Play and GitHub. Pulled then, so linked to the
// developer's own page instead: v2RayTun from the App Store (its site), the
// official sing-box from the App Store (its TestFlight page), v2rayNG from Google
// Play (GitHub). NekoBox's Google Play build is not the developers' (their README
// says so), so GitHub only.
func StockAppLinks(lang i18n.Lang, key string) map[string][]model.SubAppLink {
	out := map[string][]model.SubAppLink{}
	if key == "happ" {
		for _, p := range happWizard(lang) {
			for _, l := range p.Links {
				out[p.Key] = append(out[p.Key], model.SubAppLink{Label: l.Label, URL: l.URL})
			}
		}
		return out
	}
	for plat, ls := range stockAppLinks[key] {
		out[plat] = append([]model.SubAppLink(nil), ls...)
	}
	return out
}

// App Store and Google Play listing addresses.
func appStore(id string) string    { return "https://apps.apple.com/app/id" + id }
func googlePlay(pkg string) string { return "https://play.google.com/store/apps/details?id=" + pkg }
func ghReleases(repo string) string {
	return "https://github.com/" + repo + "/releases/latest"
}

// stockAppLinks is every stock client's download buttons but Happ's, whose own
// carry translated labels (happWizard). The labels here are store and site names.
var stockAppLinks = func() map[string]map[string][]model.SubAppLink {
	l := func(label, url string) model.SubAppLink { return model.SubAppLink{Label: label, URL: url} }
	incyGH, hiddifyGH := ghReleases("INCY-DEV/incy-platforms"), ghReleases("hiddify/hiddify-app")
	karingGH, vergeGH := ghReleases("KaringX/karing"), ghReleases("clash-verge-rev/clash-verge-rev")
	return map[string]map[string][]model.SubAppLink{
		"incy": {
			"ios":     {l("App Store", appStore("6756943388"))},
			"android": {l("Google Play", googlePlay("llc.itdev.incy")), l("APK", incyGH)},
			"windows": {l("GitHub", incyGH)},
			"macos":   {l("App Store", appStore("6756943388")), l("GitHub", incyGH)},
			"linux":   {l("GitHub", incyGH)},
			"tv":      {l("Google Play", googlePlay("llc.itdev.incy"))},
		},
		"v2raytun": {
			"ios":     {l("v2raytun.com", "https://v2raytun.com/")},
			"android": {l("Google Play", googlePlay("com.v2raytun.android"))},
			"windows": {l("v2raytun.com", "https://v2raytun.com/")},
			"macos":   {l("v2raytun.com", "https://v2raytun.com/")},
			"tv":      {l("Google Play", googlePlay("com.v2raytun.android"))},
		},
		"hiddify": {
			"ios":     {l("App Store", appStore("6596777532"))},
			"android": {l("Google Play", googlePlay("app.hiddify.com")), l("APK", hiddifyGH)},
			"windows": {l("GitHub", hiddifyGH)},
			"macos":   {l("App Store", appStore("6596777532")), l("GitHub", hiddifyGH)},
			"linux":   {l("GitHub", hiddifyGH)},
			"tv":      {l("Google Play", googlePlay("app.hiddify.com"))},
		},
		"karing": {
			"ios":     {l("App Store", appStore("6472431552"))},
			"android": {l("APK", karingGH)},
			"windows": {l("GitHub", karingGH)},
			"macos":   {l("App Store", appStore("6472431552"))},
			"tv":      {l("Apple TV", appStore("6472431552")), l("Android TV", karingGH)},
		},
		"singbox": {
			"ios":     {l("TestFlight", "https://sing-box.sagernet.org/clients/apple/")},
			"android": {l("Google Play", googlePlay("io.nekohasekai.sfa")), l("APK", ghReleases("SagerNet/sing-box"))},
			"macos":   {l("TestFlight", "https://sing-box.sagernet.org/clients/apple/")},
			"tv":      {l("Android TV", googlePlay("io.nekohasekai.sfa"))},
		},
		"clash": {
			"android": {l("Clash Meta for Android", ghReleases("MetaCubeX/ClashMetaForAndroid"))},
			"windows": {l("Clash Verge Rev", vergeGH)},
			"macos":   {l("Clash Verge Rev", vergeGH)},
			"linux":   {l("Clash Verge Rev", vergeGH)},
		},
		"v2box": {
			"ios":     {l("App Store", appStore("6446814690"))},
			"android": {l("Google Play", googlePlay("dev.hexasoftware.v2box"))},
			"macos":   {l("App Store", appStore("6446814690"))},
		},
		"v2rayng": {
			"android": {l("APK", ghReleases("2dust/v2rayNG"))},
		},
		"nekobox": {
			"android": {l("APK", ghReleases("MatsuriDayo/NekoBoxForAndroid"))},
		},
		"streisand": {
			"ios":   {l("App Store", appStore("6450534064"))},
			"macos": {l("App Store", appStore("6450534064"))},
			"tv":    {l("Apple TV", appStore("6450534064"))},
		},
		"shadowrocket": {
			"ios":   {l("App Store", appStore("932747118"))},
			"macos": {l("App Store", appStore("932747118"))},
			"tv":    {l("Apple TV", appStore("932747118"))},
		},
	}
}()

// cleanAppLinks keeps the known platforms and the rows with an address, and
// refuses an address that is not http(s) or a label that would not fit a button.
func cleanAppLinks(name string, in map[string][]model.SubAppLink) (map[string][]model.SubAppLink, error) {
	out := map[string][]model.SubAppLink{}
	for _, k := range model.WizardPlatformKeys {
		for _, l := range in[k] {
			l.Label, l.URL = strings.TrimSpace(l.Label), strings.TrimSpace(l.URL)
			if l.URL == "" {
				continue
			}
			if !model.ValidWizardLink(l.URL) {
				return nil, fmt.Errorf("sub_apps: %s, %s: %q is not an absolute http(s) URL", name, k, l.URL)
			}
			if utf8.RuneCountInString(l.Label) > 40 || strings.ContainsAny(l.Label, "\r\n") {
				return nil, fmt.Errorf("sub_apps: %s, %s: a label of up to 40 characters", name, k)
			}
			if len(out[k]) == 5 {
				return nil, fmt.Errorf("sub_apps: %s, %s: at most 5 links", name, k)
			}
			out[k] = append(out[k], l)
		}
	}
	return out, nil
}

// happWizard is Happ's own download links per platform.
func happWizard(lang i18n.Lang) []WizardPlatform {
	t := func(k string) string { return i18n.T(lang, k) }
	const gh = "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/"
	return []WizardPlatform{
		{Key: "ios", Name: "iPhone · iPad", Links: []WizardLink{
			{t("sub.wzStoreRu"), "https://apps.apple.com/ru/app/happ-lite/id6799917773"},
			{t("sub.wzStoreGlobal"), "https://apps.apple.com/us/app/happ-proxy-utility/id6504287215"},
		}},
		{Key: "android", Name: "Android", Links: []WizardLink{
			{"Google Play", "https://play.google.com/store/apps/details?id=com.happproxy"},
			{t("sub.wzApk"), "https://github.com/Happ-proxy/happ-android/releases/latest/download/Happ.apk"},
		}},
		{Key: "windows", Name: "Windows", Links: []WizardLink{
			{"Windows x64", gh + "setup-Happ.x64.exe"},
			{"Windows ARM", gh + "setup-Happ.arm64.exe"},
		}},
		{Key: "macos", Name: "macOS", Links: []WizardLink{
			{t("sub.wzStoreRu"), "https://apps.apple.com/ru/app/happ-proxy-utility/id6783623643"},
			{t("sub.wzStoreGlobal"), "https://apps.apple.com/us/app/happ-proxy-utility/id6504287215"},
			{"DMG", gh + "Happ.macOS.universal.dmg"},
		}},
		{Key: "linux", Name: "Linux", Links: []WizardLink{
			{"Linux .deb", gh + "Happ.linux.x64.deb"},
			{"Linux .rpm", gh + "Happ.linux.x64.rpm"},
		}},
		{Key: "tv", Name: "TV", Links: []WizardLink{
			{"Android TV", "https://play.google.com/store/apps/details?id=com.happproxy"},
			{"Apple TV", "https://apps.apple.com/us/app/happ-proxy-utility-for-tv/id6748297274"},
		}},
	}
}
