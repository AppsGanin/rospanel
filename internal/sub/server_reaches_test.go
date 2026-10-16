package sub

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// A server is the reader's when any lane or inbound of it is theirs.
func TestServerReaches(t *testing.T) {
	t.Parallel()
	set := &model.Settings{ServerID: 3}
	cases := []struct {
		name   string
		access model.Access
		custom []model.Inbound
		want   bool
	}{
		{"unrestricted", model.Access{All: true}, nil, true},
		{"a lane of it", model.Access{Tokens: map[string]bool{model.BuiltinToken(3, "vless"): true}}, nil, true},
		{"a lane of another", model.Access{Tokens: map[string]bool{model.BuiltinToken(30, "vless"): true}}, nil, false},
		{"one of its inbounds", model.Access{Tokens: map[string]bool{model.InboundToken(9): true}}, []model.Inbound{{ID: 9}}, true},
		{"nothing", model.Access{Tokens: map[string]bool{}}, []model.Inbound{{ID: 9}}, false},
	}
	for _, c := range cases {
		if got := (Server{Set: set, Access: c.access, Custom: c.custom}).Reaches(); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
