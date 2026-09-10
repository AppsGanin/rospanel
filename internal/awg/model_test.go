package awg

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/AppsGanin/rospanel/internal/model"
)

// The engine's Params and the store's model.AWGParams are two spellings of one
// thing, and the pair of hand-written copies between them is exactly the shape
// that rots: add a field to one side, forget the other, and that parameter is
// silently dropped from every tunnel and every client config while everything
// still compiles and every other test still passes.
//
// So: fill every field with a distinct non-zero value, go there and back, and
// require it all to survive. The reflection pass is the half that matters — it
// fails when a NEW field is added to Params and not to this test, which is the
// moment the copies drift.
func TestParamsSurviveTheModelRoundTrip(t *testing.T) {
	full := Params{
		Jc: 7, Jmin: 51, Jmax: 999, S1: 31, S2: 41, S3: 51, S4: 13,
		H1:           Range{101, 201},
		H2:           Range{1001, 1101},
		H3:           Range{2001, 2101},
		H4:           Range{3001, 3101},
		I1:           "<b 0x000100002112a442><r 12>",
		I2:           "<r 2><b 0x0100><t>",
		HeaderKey:    "3q2+796tvu/erb7v3q2+796tvu/erb7v3q2+796tvu8=",
		Padding:      Range{1, 32},
		Trailers:     true,
		RekeyAfter:   Range{110, 130},
		RekeyTimeout: Range{4, 6},
		RejectAfter:  Range{175, 195},
		Keepalive:    Range{12, 18},
		Handshakes:   Range{10, 15},
	}
	v := reflect.ValueOf(full)
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Fatalf("%s is not exercised — add it here, and check FromModel/ToModel carry it",
				v.Type().Field(i).Name)
		}
	}

	back, err := FromModel(ToModel(full))
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if back != full {
		t.Errorf("round trip lost fields:\n got %+v\nwant %+v", back, full)
	}

	// And through the store's JSON, which is where the row actually lives.
	raw, err := json.Marshal(ToModel(full))
	if err != nil {
		t.Fatal(err)
	}
	var stored model.AWGParams
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	if back, err = FromModel(stored); err != nil || back != full {
		t.Errorf("json round trip: %+v err=%v\nraw: %s", back, err, raw)
	}
}

// A row written before 3.1 has h1–h4 as bare numbers and none of the rest. It has
// to keep loading: a block that fails to parse reads as "never generated", and the
// panel would mint fresh keys over a working tunnel and silently invalidate every
// config already handed out.
func TestPre31RowsStillLoad(t *testing.T) {
	const legacy = `{"jc":4,"jmin":50,"jmax":1000,"s1":30,"s2":40,` +
		`"h1":1815237610,"h2":631861364,"h3":1224664619,"h4":1465269913}`
	var stored model.AWGParams
	if err := json.Unmarshal([]byte(legacy), &stored); err != nil {
		t.Fatalf("a pre-3.1 row would not load: %v", err)
	}
	if stored.IsZero() {
		t.Fatal("a pre-3.1 row read as never generated — this would regenerate its keys")
	}
	p, err := FromModel(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("a pre-3.1 row no longer validates: %v", err)
	}
	want := Params{Jc: 4, Jmin: 50, Jmax: 1000, S1: 30, S2: 40,
		H1: Range{1815237610, 1815237610}, H2: Range{631861364, 631861364},
		H3: Range{1224664619, 1224664619}, H4: Range{1465269913, 1465269913}}
	if p != want {
		t.Errorf("pre-3.1 row read as %+v, want %+v", p, want)
	}
}

// A block the store cannot parse must not read as an empty one. Params{} is what
// "no tunnel here yet" looks like, and answering a corrupt row with it is how a
// panel talks itself into regenerating keys nobody asked it to touch.
func TestAnUnreadableRangeIsAnError(t *testing.T) {
	if _, err := FromModel(model.AWGParams{Jc: 4, H1: "nonsense"}); err == nil {
		t.Error("a malformed range was accepted")
	}
	if _, err := FromModel(model.AWGParams{Jc: 4, H1: "200-100"}); err == nil {
		t.Error("a backwards range was accepted")
	}
}
