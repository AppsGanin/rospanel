package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeTagsCanonicalForm(t *testing.T) {
	got, ok := NormalizeTags([]string{" VIP ", "reseller-a", "vip", "", "Акция", "reseller-a"})
	if !ok {
		t.Fatal("valid tags were refused")
	}
	want := []string{"reseller-a", "vip", "акция"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// The result must be stable under its own encoding: what was stored reads back
	// as the same list, so a filter never misses a tag on a spelling technicality.
	if back := DecodeTags(EncodeTags(got)); !reflect.DeepEqual(back, got) {
		t.Errorf("round trip: %v → %q → %v", got, EncodeTags(got), back)
	}
}

func TestNormalizeTagsRefusesWhatCannotBeStored(t *testing.T) {
	cases := map[string][]string{
		"comma":    {"a,b"},
		"newline":  {"a\nb"},
		"too long": {strings.Repeat("x", MaxUserTagLen+1)},
		"too many": func() []string {
			out := make([]string, MaxUserTags+1)
			for i := range out {
				out[i] = "t" + string(rune('a'+i))
			}
			return out
		}(),
	}
	for name, in := range cases {
		if got, ok := NormalizeTags(in); ok {
			t.Errorf("%s: accepted %v as %v", name, in, got)
		}
	}
	// Exactly the limit is fine; duplicates do not count toward it.
	in := make([]string, 0, MaxUserTags*2)
	for i := 0; i < MaxUserTags; i++ {
		in = append(in, "t"+string(rune('a'+i)), "T"+string(rune('a'+i)))
	}
	if got, ok := NormalizeTags(in); !ok || len(got) != MaxUserTags {
		t.Errorf("at the limit: ok=%v n=%d", ok, len(got))
	}
	if got, ok := NormalizeTags([]string{strings.Repeat("я", MaxUserTagLen)}); !ok || len(got) != 1 {
		t.Errorf("limit is in characters, not bytes: ok=%v got=%v", ok, got)
	}
}

func TestDecodeTagsNeverFails(t *testing.T) {
	for _, in := range []string{"", "  ", ",", ",,", " a , B ,a", "x,\ny"} {
		got := DecodeTags(in)
		if got == nil {
			t.Errorf("%q: nil (a user row must always carry a list, even an empty one)", in)
		}
	}
	if got := DecodeTags(" a , B ,a"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("hand-edited value: got %v", got)
	}
}
