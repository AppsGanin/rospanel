package store

import (
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// StrictEgress is a single bool in a JSON blob, which is exactly how a setting
// gets silently dropped: a save path that rebuilds the struct field by field, or a
// column read into a type without it, and the operator's "never go direct" is
// quietly off again after the next save — with nothing on screen to say so. Pin
// that it survives the store, and that a config saved before it existed reads as
// off rather than as garbage.
func TestStrictEgressSurvivesTheStore(t *testing.T) {
	st := openTestStore(t)
	if err := st.SetRoutingConfig(model.RoutingConfig{StrictEgress: true, RoutingOrder: []string{"warp", "opera", "direct"}}); err != nil {
		t.Fatal(err)
	}
	set, err := st.GetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !set.Routing.StrictEgress {
		t.Error("StrictEgress was saved on and read back off")
	}

	if _, err := st.db.Exec(`UPDATE settings SET routing_config = ? WHERE id = 1`,
		`{"block_ads":true,"routing_order":["warp","opera","direct"]}`); err != nil {
		t.Fatal(err)
	}
	if set, err = st.GetSettings(); err != nil {
		t.Fatal(err)
	}
	if set.Routing.StrictEgress {
		t.Error("a config saved before StrictEgress existed reads as strict — it must default off")
	}
	if !set.Routing.BlockAds {
		t.Error("the rest of a pre-existing config did not load")
	}
}
