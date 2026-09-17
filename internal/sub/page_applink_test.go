package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The AmneziaWG card carries a link to the apps themselves — a user handed a config
// still has to install something — and it is the page's own language: the site keeps
// a Russian page of its own, everything else lands on the default one.
func TestAWGCardLinksToTheAmneziaSite(t *testing.T) {
	u := model.User{ID: 1, SubToken: "tok"}
	set := &model.Settings{
		Host: "vpn.example.com", SubPath: "sub", ServerID: model.LocalNodeID,
		AWGEnabled: true, AWGPort: 51820,
	}
	for _, c := range []struct {
		lang         i18n.Lang
		want, absent string
	}{
		{i18n.RU, "https://amnezia.org/ru/downloads", "https://amnezia.org/downloads"},
		{i18n.EN, "https://amnezia.org/downloads", "https://amnezia.org/ru/downloads"},
	} {
		html, err := Page(u, set, []Server{{Set: set, Access: model.UnrestrictedAccess()}}, Billing{}, Devices{}, true, c.lang)
		if err != nil {
			t.Fatalf("%s render: %v", c.lang, err)
		}
		s := string(html)
		if !strings.Contains(s, `href="`+c.want+`"`) {
			t.Errorf("%s page lacks the Amnezia site link %s", c.lang, c.want)
		}
		if strings.Contains(s, `href="`+c.absent+`"`) {
			t.Errorf("%s page links to %s, the other language's page", c.lang, c.absent)
		}
	}
}
