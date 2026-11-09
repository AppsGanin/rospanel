package sub

import (
	"slices"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

func keys(ls []DeepLink) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Key
	}
	return out
}

func TestDeepLinksOrderAndOff(t *testing.T) {
	stock := keys(DeepLinks("https://h/s/t", i18n.EN, &model.Settings{}))
	if len(stock) != 12 || stock[0] != "happ" {
		t.Fatalf("stock order: %v", stock)
	}
	got := keys(DeepLinks("https://h/s/t", i18n.EN, &model.Settings{SubApps: "v2rayng, -happ,bogus,v2rayng,-incy"}))
	if got[0] != "v2rayng" || len(got) != 10 {
		t.Fatalf("ordered: %v", got)
	}
	for _, k := range got {
		if k == "happ" || k == "incy" {
			t.Fatalf("%s is off but listed: %v", k, got)
		}
	}
	// The apps the spec does not name follow in the stock order.
	if got[1] != "v2raytun" {
		t.Fatalf("unnamed apps out of order: %v", got)
	}
}

// A client of the operator's own: saved with a fresh key, placed where the list
// put it, its link filled with the subscription in each of the three forms.
func TestCustomApps(t *testing.T) {
	set := &model.Settings{}
	err := SetAppOrder(set, []AppPref{
		{Custom: true, Label: " My VPN ", Platforms: "iOS", Link: "myvpn://add?u={url_enc}&b={url_b64}&r={url}", On: true},
		{Key: "-bogus"}, {Key: "shadowrocket", On: false}, {Key: "karing", On: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.SubCustomApps) != 1 || set.SubCustomApps[0].Name != "My VPN" || set.SubCustomApps[0].ID == "" {
		t.Fatalf("custom: %+v", set.SubCustomApps)
	}
	key := CustomAppKey(set.SubCustomApps[0].ID)
	if !strings.HasPrefix(set.SubApps, key+",-shadowrocket,karing,happ,") || strings.Count(set.SubApps, ",") != 12 {
		t.Fatalf("order: %q", set.SubApps)
	}
	ls := DeepLinks("https://h/s/t", i18n.EN, set)
	if ls[0].Key != key || ls[0].Label != "My VPN" ||
		string(ls[0].Href) != "myvpn://add?u=https%3A%2F%2Fh%2Fs%2Ft&b=aHR0cHM6Ly9oL3MvdA==&r=https://h/s/t" {
		t.Fatalf("custom link: %+v", ls[0])
	}

	// Saving the list back keeps the key; a second client with the same key does not.
	prefs := ParseAppOrder(set)
	if !prefs[0].Custom || prefs[0].Link == "" || prefs[0].Platforms != "iOS" {
		t.Fatalf("parse: %+v", prefs[0])
	}
	dup := prefs[0]
	dup.Label = "Other"
	if err := SetAppOrder(set, append(prefs, dup)); err != nil {
		t.Fatal(err)
	}
	if len(set.SubCustomApps) != 2 || CustomAppKey(set.SubCustomApps[0].ID) != key || set.SubCustomApps[1].ID == set.SubCustomApps[0].ID {
		t.Fatalf("keys after resave: %+v", set.SubCustomApps)
	}

	// Removing it from the list removes it.
	if err := SetAppOrder(set, prefs[1:]); err != nil {
		t.Fatal(err)
	}
	if len(set.SubCustomApps) != 0 || strings.Contains(set.SubApps, key) {
		t.Fatalf("after removal: %+v %q", set.SubCustomApps, set.SubApps)
	}
}

func TestCustomAppLinkRefused(t *testing.T) {
	for _, l := range []string{
		"javascript:alert('{url}')", "JavaScript://x/{url}", "data:text/html,{url}", "myapp://add/", "noscheme{url}",
		`myapp://"onclick={url}`, "myapp://a b/{url}", "1app://{url}",
	} {
		if err := SetAppOrder(&model.Settings{}, []AppPref{{Custom: true, Label: "x", Link: l}}); err == nil {
			t.Errorf("%q accepted", l)
		}
	}
	if err := SetAppOrder(&model.Settings{}, []AppPref{{Custom: true, Label: "", Link: "a://{url}"}}); err == nil {
		t.Error("a nameless client accepted")
	}
	// A bad row that reached the store some other way never becomes a button.
	set := &model.Settings{SubCustomApps: []model.SubCustomApp{{ID: "1", Name: "x", Link: "javascript:{url}"}}}
	for _, d := range DeepLinks("https://h/s/t", i18n.EN, set) {
		if d.Key == "x1" {
			t.Fatal("an unsafe stored link reached the page")
		}
	}
}

// The page follows the order; Happ off takes the wizard (which installs Happ) with
// it, and every app off takes the "open in app" button too.
func TestPageAppOrder(t *testing.T) {
	u := model.User{Name: "u", SubToken: "tok"}
	render := func(set *model.Settings) string {
		set.Host, set.SubPath = "vpn.example.com", "sub"
		html, err := Page(u, set, One(set), Billing{}, Devices{}, Access{}, true, i18n.RU)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return string(html)
	}
	h := render(&model.Settings{SubApps: "shadowrocket,karing"})
	if strings.Index(h, "<span>Shadowrocket</span>") > strings.Index(h, "<span>Happ</span>") {
		t.Error("Shadowrocket is not listed first")
	}
	if !strings.Contains(h, `id="wizard"`) {
		t.Error("the wizard is gone while Happ is on")
	}
	h = render(&model.Settings{SubApps: "-happ"})
	if strings.Contains(h, `id="wizard"`) || strings.Contains(h, "<span>Happ</span>") {
		t.Error("Happ is off but the page still offers it")
	}
	off := &model.Settings{}
	prefs := ParseAppOrder(off)
	for i := range prefs {
		prefs[i].On = false
	}
	if err := SetAppOrder(off, prefs); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(render(off), `id="openapps-btn"`) {
		t.Error("every app is off but the page still has the open-in-app button")
	}
}

// The wizard follows the operator's pick: its texts name that client, the add
// button opens its link, and the download step offers only the platforms given.
func TestPageWizardApp(t *testing.T) {
	u := model.User{Name: "u", SubToken: "tok"}
	set := &model.Settings{Host: "vpn.example.com", SubPath: "sub", SubWizardApps: allPlatforms("v2raytun"),
		SubAppLinks: map[string]map[string][]model.SubAppLink{"v2raytun": {
			"android": {{Label: "", URL: "https://play.example/v2raytun"}},
			"ios":     {{Label: "x", URL: "javascript:alert(1)"}},
		}}}
	html, err := Page(u, set, One(set), Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatal(err)
	}
	h := string(html)
	if !strings.Contains(h, "Добавить в v2RayTun") || strings.Contains(h, "Добавить в Happ") {
		t.Error("the wizard does not name the chosen client")
	}
	if !strings.Contains(h, `data-plat="android"`) || strings.Contains(h, `data-plat="ios"`) || strings.Contains(h, "happ-android") {
		t.Error("the wizard's platforms are not the operator's links")
	}
	if !strings.Contains(h, `play.example\/v2raytun`) || !strings.Contains(h, ">Скачать<") {
		t.Error("the download link or its fallback label is missing")
	}
	// The default is still Happ with its own links.
	html, _ = Page(u, &model.Settings{Host: "vpn.example.com", SubPath: "sub"}, One(set), Billing{}, Devices{}, Access{}, true, i18n.RU)
	if !strings.Contains(string(html), "Добавить в Happ") || !strings.Contains(string(html), "happ-android") {
		t.Error("the default wizard is not Happ")
	}
	// A Happ wizard whose links the operator edited uses theirs, not the stock ones.
	html, _ = Page(u, &model.Settings{Host: "vpn.example.com", SubPath: "sub", SubAppLinks: map[string]map[string][]model.SubAppLink{"happ": {
		"windows": {{Label: "Mirror", URL: "https://mirror.example/happ.exe"}},
	}}}, One(set), Billing{}, Devices{}, Access{}, true, i18n.RU)
	if h := string(html); !strings.Contains(h, "mirror.example") || strings.Contains(h, "happ-android") {
		t.Error("Happ's edited links are not the ones shown")
	}
	// No links at all (the operator emptied them): the first step says to install it.
	set.SubAppLinks = map[string]map[string][]model.SubAppLink{"v2raytun": {}}
	html, _ = Page(u, set, One(set), Billing{}, Devices{}, Access{}, true, i18n.RU)
	if !strings.Contains(string(html), "Установите v2RayTun на своём устройстве") {
		t.Error("no hint when the wizard has no download links")
	}
}

// Each client's download links ride the list: Happ's stock ones come prefilled and
// are not stored while untouched, edited ones are, and a removed client takes its
// links with it.
func TestAppLinksInTheList(t *testing.T) {
	set := &model.Settings{}
	prefs := ParseAppOrder(set)
	if len(prefs[0].Links) != 6 || len(prefs[1].Links) != 6 {
		t.Fatalf("prefill: happ %d platforms, incy %d", len(prefs[0].Links), len(prefs[1].Links))
	}
	if err := SetAppOrder(set, prefs); err != nil || set.SubAppLinks != nil {
		t.Fatalf("untouched stock links stored: %v %v", err, set.SubAppLinks)
	}
	prefs[1].Links = map[string][]model.SubAppLink{"android": {{Label: "Play", URL: "https://play.example/incy"}, {URL: " "}}}
	prefs = append(prefs, AppPref{Custom: true, Label: "Mine", Link: "mine://{url}", On: true,
		Links: map[string][]model.SubAppLink{"ios": {{URL: "https://apps.example/mine"}}}})
	if err := SetAppOrder(set, prefs); err != nil {
		t.Fatal(err)
	}
	mine := CustomAppKey(set.SubCustomApps[0].ID)
	if len(set.SubAppLinks) != 2 || len(set.SubAppLinks["incy"]["android"]) != 1 || set.SubAppLinks[mine]["ios"][0].URL != "https://apps.example/mine" {
		t.Fatalf("stored: %v", set.SubAppLinks)
	}
	// A stock client emptied means no links, not its stock ones.
	prefs = ParseAppOrder(set)
	prefs[0].Links = map[string][]model.SubAppLink{}
	if err := SetAppOrder(set, prefs[:len(prefs)-1]); err != nil {
		t.Fatal(err)
	}
	if l, ok := set.SubAppLinks["happ"]; !ok || len(l) != 0 {
		t.Fatalf("emptied Happ: %v", set.SubAppLinks)
	}
	for _, p := range Wizard(i18n.RU, set, DeepLinks("https://h/s/t", i18n.RU, set)) {
		if len(p.Links) != 0 {
			t.Fatalf("emptied Happ still offers %s links: %v", p.Key, p.Links)
		}
	}
	if _, ok := set.SubAppLinks[mine]; ok {
		t.Fatal("a removed client's links stayed")
	}
	bad := ParseAppOrder(set)
	bad[2].Links = map[string][]model.SubAppLink{"tv": {{URL: "javascript:alert(1)"}}}
	if err := SetAppOrder(set, bad); err == nil {
		t.Fatal("an unsafe download link was accepted")
	}
}

// Every stock client comes with download links, each a valid address, and the
// table names no client the page does not have.
func TestStockAppLinks(t *testing.T) {
	keys := map[string]bool{}
	for _, d := range DeepLinks("https://h/s/t", i18n.EN, &model.Settings{}) {
		keys[d.Key] = true
		links := StockAppLinks(i18n.EN, d.Key)
		if len(links) == 0 {
			t.Errorf("%s has no stock links", d.Key)
		}
		for plat, ls := range links {
			if !slices.Contains(model.WizardPlatformKeys, plat) {
				t.Errorf("%s: unknown platform %q", d.Key, plat)
			}
			for _, l := range ls {
				if l.Label == "" || !model.ValidWizardLink(l.URL) {
					t.Errorf("%s/%s: bad link %+v", d.Key, plat, l)
				}
			}
		}
	}
	for k := range stockAppLinks {
		if !keys[k] {
			t.Errorf("stock links for %q, which is not a client", k)
		}
	}
	// A copy each time: editing one must not change the table.
	StockAppLinks(i18n.EN, "incy")["ios"][0].URL = "x"
	if StockAppLinks(i18n.EN, "incy")["ios"][0].URL == "x" {
		t.Error("StockAppLinks hands out the shared table")
	}
}

// A platform may have a client of its own: the wizard there names it, adds to it,
// and offers its download links, while the other platforms keep the common one; a
// platform whose client is switched off drops out of the wizard.
func TestWizardPerPlatform(t *testing.T) {
	set := &model.Settings{SubWizardApps: allPlatforms("hiddify"), SubApps: "-streisand"}
	set.SubWizardApps["android"], set.SubWizardApps["ios"], set.SubWizardApps["tv"] = "v2rayng", "streisand", "v2rayng"
	deep := DeepLinks("https://h/s/t", i18n.RU, set)
	by := map[string]WizardPlatform{}
	for _, p := range Wizard(i18n.RU, set, deep) {
		by[p.Key] = p
	}
	if _, ok := by["ios"]; ok {
		t.Error("iOS's client is off but the wizard still offers iOS")
	}
	if _, ok := by["tv"]; ok {
		t.Error("v2rayNG has no TV link but the wizard still offers TV")
	}
	a := by["android"]
	if a.AddLabel != "Добавить в v2rayNG" || deep[a.AppIndex].Key != "v2rayng" || len(a.Links) != 1 || !strings.Contains(a.Links[0].URL, "2dust/v2rayNG") {
		t.Errorf("android: %+v", a)
	}
	if w := by["windows"]; w.AddLabel != "Добавить в Hiddify" || !strings.HasPrefix(string(w.Href), "hiddify://") {
		t.Errorf("windows: %+v", w)
	}
	u := model.User{Name: "u", SubToken: "tok"}
	set.Host, set.SubPath = "vpn.example.com", "sub"
	html, err := Page(u, set, One(set), Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatal(err)
	}
	h := string(html)
	if !strings.Contains(h, "Добавить в v2rayNG") || !strings.Contains(h, "Добавить в Hiddify") || strings.Contains(h, `data-plat="ios"`) {
		t.Error("the page's wizard is not per platform")
	}
}

// allPlatforms picks one wizard client for every platform.
func allPlatforms(key string) map[string]string {
	m := map[string]string{}
	for _, k := range model.WizardPlatformKeys {
		m[k] = key
	}
	return m
}
