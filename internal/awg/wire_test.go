package awg

import (
	"encoding/json"
	"testing"
)

// A parameter block written before 3.1 must serialise byte for byte as it did
// then. This is not tidiness: the block travels to a node inside the sync
// response, and an agent that reads h1–h4 as numbers fails to decode the WHOLE
// response if they arrive as strings — so the node stops syncing at all, not
// merely loses its tunnel. The zero 3.1 fields must stay out of the JSON for the
// same reason they stay out of the config: they were never set.
func TestAPre31BlockSerialisesAsItAlwaysDid(t *testing.T) {
	legacy := Params{Jc: 4, Jmin: 50, Jmax: 1000, S1: 30, S2: 40,
		H1: Range{11, 11}, H2: Range{12, 12}, H3: Range{13, 13}, H4: Range{14, 14}}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"jc":4,"jmin":50,"jmax":1000,"s1":30,"s2":40,"h1":11,"h2":12,"h3":13,"h4":14}`
	if string(b) != want {
		t.Errorf("wire shape changed:\n got %s\nwant %s", b, want)
	}
}

// A 3.1 block carries the new fields, and its headers are ranges — which is
// precisely why it must not be handed to an agent that predates them.
func TestA31BlockCarriesRangesAndTheNewFields(t *testing.T) {
	b, err := json.Marshal(RandomParams())
	if err != nil {
		t.Fatal(err)
	}
	var back Params
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("a 3.1 block does not survive its own round trip: %v\n%s", err, b)
	}
	if back.HeaderKey == "" || back.H1.Min == back.H1.Max || !back.Trailers {
		t.Errorf("3.1 fields lost in the round trip: %s", b)
	}
}
