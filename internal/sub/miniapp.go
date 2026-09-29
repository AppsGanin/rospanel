package sub

import (
	"html/template"
	"strings"

	"github.com/AppsGanin/rospanel/internal/branding"
	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// MiniAppToken is the path segment of the Mini App entrance, where a subscription
// token would be: /<sub path>/app. Tokens are long and random, never this.
const MiniAppToken = "app"

// MiniAppURL is the Mini App's fixed address — for the bot's menu button and for
// @BotFather's "Configure Mini App" — or "" while the panel has no host.
func MiniAppURL(set *model.Settings) string {
	if strings.TrimSpace(set.Host) == "" {
		return ""
	}
	return "https://" + set.Host + "/" + set.SubPathOr() + "/" + MiniAppToken
}

var miniAppTpl = template.Must(template.New("miniapp").Parse(`<!doctype html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<meta name="robots" content="noindex, nofollow" />
<title>{{.Brand}}</title>
<script src="{{.Base}}/tg.js"></script>
<style>
  body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center;
    background: {{.Bg}}; color: {{.Ink}}; font: 16px/1.45 system-ui, -apple-system, sans-serif; }
  .box { max-width: 360px; padding: 24px; text-align: center; }
  .btn { display: none; margin-top: 16px; padding: 12px 18px; border: 0; border-radius: 12px;
    background: {{.Accent}}; color: {{.OnAccent}}; font: inherit; font-weight: 700; cursor: pointer; }
</style>
</head>
<body>
<div class="box">
  <p id="msg">{{.Opening}}</p>
  <button class="btn" id="bot" type="button">{{.OpenBot}}</button>
</div>
<script>
  var TG = (window.Telegram && window.Telegram.WebApp) || null;
  try { if (TG) { TG.ready(); TG.expand(); } } catch (e) {}
  // initData from the SDK, or straight from the launch URL when the SDK could not be
  // fetched (Telegram puts it in the fragment either way).
  var data = (TG && TG.initData) || "";
  if (!data) {
    try { data = new URLSearchParams(location.hash.slice(1)).get("tgWebAppData") || ""; } catch (e) {}
  }
  function say(text, bot) {
    document.getElementById("msg").textContent = text;
    if (!bot) return;
    var b = document.getElementById("bot");
    b.style.display = "inline-block";
    b.onclick = function () {
      try { if (TG) { TG.openTelegramLink(bot); return; } } catch (e) {}
      location.href = bot;
    };
  }
  if (!data) {
    say({{.NoTelegram}});
  } else {
    fetch({{.Base}} + "/auth", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-RosPanel-Sub": "1" },
      body: JSON.stringify({ init_data: data }),
    })
      .then(function (r) { return r.json(); })
      .then(function (j) {
        // The fragment goes along: it is how the page knows it runs inside Telegram.
        if (j.url) location.replace(j.url + location.hash);
        else say(j.message || {{.Failed}}, j.bot);
      })
      .catch(function () { say({{.Failed}}); });
  }
</script>
</body>
</html>
`))

// MiniAppPage is the Mini App's first screen: it hands Telegram's initData to the
// panel and moves on to the user's own page.
func MiniAppPage(set *model.Settings, lang i18n.Lang) ([]byte, error) {
	name := branding.Name(set.PanelName)
	if name == branding.DefaultName {
		name = i18n.T(lang, "sub.defaultBrand")
	}
	theme := branding.ParseTheme(set.PanelTheme)
	var b strings.Builder
	err := miniAppTpl.Execute(&b, map[string]string{
		"Lang": string(lang), "Brand": name, "Base": "/" + set.SubPathOr() + "/" + MiniAppToken,
		"Bg": theme.Bg, "Ink": theme.Text, "Accent": theme.Accent, "OnAccent": branding.OnFill(theme.Accent),
		"Opening": i18n.T(lang, "sub.miniOpening"), "OpenBot": i18n.T(lang, "sub.miniOpenBot"),
		"NoTelegram": i18n.T(lang, "sub.miniNoTelegram"), "Failed": i18n.T(lang, "sub.miniFailed"),
	})
	return []byte(b.String()), err
}
