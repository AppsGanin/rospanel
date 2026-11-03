package rospanel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The READMEs carry numbers and protocol lists that are easy to forget when the
// code moves: a new decoy template, a protocol added to the custom-inbound editor.
// This test makes the docs fail loudly instead of drifting, which is what the
// "11 templates against 12 in the code" and the missing Shadowsocks-2022 were.
func TestREADMEsMatchDecoyTemplateCount(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("internal", "decoy", "templates"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			count++
		}
	}
	if count == 0 {
		t.Fatal("no decoy templates found")
	}
	// Each README states the count in its own language; keep the numerals and the
	// noun together so a translation cannot silently keep the old number.
	want := map[string]string{
		"README.md":    fmt.Sprintf("(%d templates)", count),
		"README-RU.md": fmt.Sprintf("(%d шаблонов)", count),
		"README-ZH.md": fmt.Sprintf("（%d 种模板）", count),
		"README-AR.md": fmt.Sprintf("(%d قالبًا)", count),
		"README-FA.md": fmt.Sprintf("(%d قالب)", count),
	}
	for file, claim := range want {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), claim) {
			t.Errorf("%s does not state %q — the decoy count changed without the docs", file, claim)
		}
	}
}

// Every protocol the custom-inbound editor offers has to be named in the docs. A
// protocol that exists only in the code is a feature nobody can find, and the
// README's protocol list is the page an operator reads before buying a server.
func TestREADMEsNameEveryCustomInboundProtocol(t *testing.T) {
	// The names the READMEs use, which are not the ids the model stores.
	display := map[string]string{
		model.InbVLESS:       "VLESS",
		model.InbTrojan:      "Trojan",
		model.InbHysteria:    "Hysteria2",
		model.InbShadowsocks: "Shadowsocks-2022",
		model.InbTurnVK:      "TURN-VK",
		model.InbWireGuard:   "WireGuard",
	}
	files := []string{"README.md", "README-RU.md", "README-ZH.md", "README-AR.md", "README-FA.md"}
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, protocol := range model.InboundProtocols {
			name, ok := display[protocol]
			if !ok {
				t.Fatalf("new inbound protocol %q has no display name in this test — add it and a README mention", protocol)
			}
			if !strings.Contains(string(body), name) {
				t.Errorf("%s never names %q (protocol %q)", file, name, protocol)
			}
		}
	}
}
