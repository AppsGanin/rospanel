package sub

import "github.com/AppsGanin/rospanel/internal/i18n"

// The connect wizard: install the app, add the subscription, connect. It recommends
// one client — Happ, first in DeepLinks, on every platform — so a newcomer has one
// thing to do instead of a list to choose from; "another app" still opens the list.
//
// These are Happ's own download links (happ.su/main). They drift across releases;
// check them there when a store or a release file name changes.

// WizardPlatform is where the app comes from on one platform.
type WizardPlatform struct {
	Key   string // ios | android | windows | macos | linux | tv — what the page detects
	Name  string
	Links []WizardLink
}

// WizardLink is one place to get the app.
type WizardLink struct {
	Label string
	URL   string
}

// Wizard lists the platforms, in the order the switcher shows them.
func Wizard(lang i18n.Lang) []WizardPlatform {
	t := func(k string) string { return i18n.T(lang, k) }
	const gh = "https://github.com/Happ-proxy/happ-desktop/releases/latest/download/"
	return []WizardPlatform{
		{"ios", "iPhone · iPad", []WizardLink{
			{t("sub.wzStoreRu"), "https://apps.apple.com/ru/app/happ-lite/id6799917773"},
			{t("sub.wzStoreGlobal"), "https://apps.apple.com/us/app/happ-proxy-utility/id6504287215"},
		}},
		{"android", "Android", []WizardLink{
			{"Google Play", "https://play.google.com/store/apps/details?id=com.happproxy"},
			{t("sub.wzApk"), "https://github.com/Happ-proxy/happ-android/releases/latest/download/Happ.apk"},
		}},
		{"windows", "Windows", []WizardLink{
			{"Windows x64", gh + "setup-Happ.x64.exe"},
			{"Windows ARM", gh + "setup-Happ.arm64.exe"},
		}},
		{"macos", "macOS", []WizardLink{
			{t("sub.wzStoreRu"), "https://apps.apple.com/ru/app/happ-proxy-utility/id6783623643"},
			{t("sub.wzStoreGlobal"), "https://apps.apple.com/us/app/happ-proxy-utility/id6504287215"},
			{"DMG", gh + "Happ.macOS.universal.dmg"},
		}},
		{"linux", "Linux", []WizardLink{
			{"Linux .deb", gh + "Happ.linux.x64.deb"},
			{"Linux .rpm", gh + "Happ.linux.x64.rpm"},
		}},
		{"tv", "TV", []WizardLink{
			{"Android TV", "https://play.google.com/store/apps/details?id=com.happproxy"},
			{"Apple TV", "https://apps.apple.com/us/app/happ-proxy-utility-for-tv/id6748297274"},
		}},
	}
}
