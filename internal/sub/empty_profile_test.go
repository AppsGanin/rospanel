package sub

import (
	"strings"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A user whose access groups grant nothing has no proxies. The profile handed to their
// client must still be VALID: mihomo treats a select group with no members as a load
// error, and sing-box does the same for a urltest with an empty outbound list — so the
// client rejected the whole document and the account read as broken rather than empty.
//
// This state needs no misconfiguration to reach: any membership row restricts the user,
// so a group that grants nothing yet, or one whose only grants were a deleted node's
// lanes, lands here.
func TestEmptyAccessRendersAValidProfile(t *testing.T) {
	u := model.User{Name: "nobody", UUID: "u", Password: "p", SubToken: "tok"}
	// One server, but the user is allowed none of its lanes.
	servers := []Server{{
		Set:    &model.Settings{Host: "example.org", SNI: "example.org", ServerID: 0},
		Access: model.Access{Tokens: map[string]bool{}},
	}}

	clash := ClashYAMLMulti(u, servers)
	if strings.Contains(clash, "proxies: []") {
		t.Error("clash: emitted a select group with no members — mihomo refuses the whole profile")
	}
	if !strings.Contains(clash, "MATCH,DIRECT") {
		t.Errorf("clash: no usable rule for an account with no servers:\n%s", clash)
	}

	sb := SingBoxJSONMulti(u, servers)
	for _, bad := range []string{`"type": "urltest"`, `"outbounds": null`} {
		if strings.Contains(sb, bad) {
			t.Errorf("sing-box: %s in a profile with no servers — the client refuses it:\n%s", bad, sb)
		}
	}
	if !strings.Contains(sb, `"final": "direct"`) {
		t.Errorf("sing-box: no usable route for an account with no servers:\n%s", sb)
	}
}
