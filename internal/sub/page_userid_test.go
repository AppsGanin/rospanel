package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/i18n"
	"github.com/AppsGanin/rospanel/internal/model"
)

// The page shows the user's id as its first badge, one tap to copy, to quote to support.
func TestPageShowsUserID(t *testing.T) {
	u := model.User{ID: 4242, Name: "Alice", SubToken: "tok"}
	servers := langTestServers()
	html, err := Page(u, servers[0].Set, servers, Billing{}, Devices{}, Access{}, true, i18n.RU)
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	badge := strings.Index(page, `onclick="copyQuiet('4242')">ID <bdi>4242</bdi></button>`)
	status := strings.Index(page, `<span class="badge `)
	if badge < 0 || status < 0 || badge > status {
		t.Fatalf("id badge at %d, first status badge at %d: want the id first", badge, status)
	}
}
