package model

import (
	"reflect"
	"testing"
	"time"
)

// Clone must copy every slice the settings can reach, at any depth: the store hands the
// same decoded settings out as clones, and one caller appending to a routing list would
// otherwise be editing the next caller's settings. Every field is filled — a slice gets
// an element, which is filled in turn — so a reference field added later and left out of
// Clone fails here by name.
func TestSettingsCloneSharesNothing(t *testing.T) {
	var s Settings
	fill(reflect.ValueOf(&s).Elem(), 0)
	c := s.Clone()
	if !reflect.DeepEqual(&s, c) {
		t.Fatal("the clone differs from the original")
	}
	if n := shared(reflect.ValueOf(s), reflect.ValueOf(*c), "Settings", t); n == 0 {
		// Proof the walk looked at something: an unfilled fixture would pass anything.
		t.Fatal("no slice was compared — the fixture filled nothing")
	}
	// And a nil list stays nil, which callers tell apart from an empty one.
	var empty Settings
	if e := empty.Clone(); e.SubRules != nil || e.Routing.Lanes != nil || e.ConnPolicy.Countries != nil {
		t.Error("a nil slice came back as an empty one")
	}
}

// The same for an inbound: its options carry raw JSON and lists.
func TestInboundCloneSharesNothing(t *testing.T) {
	var in Inbound
	fill(reflect.ValueOf(&in).Elem(), 0)
	c := in.Clone()
	if !reflect.DeepEqual(in, c) {
		t.Fatal("the clone differs from the original")
	}
	if n := shared(reflect.ValueOf(in), reflect.ValueOf(c), "Inbound", t); n == 0 {
		t.Fatal("no slice was compared — the fixture filled nothing")
	}
}

// fill sets every settable field under v to a non-zero value.
func fill(v reflect.Value, depth int) {
	if depth > 8 {
		return
	}
	if v.Type() == reflect.TypeOf(time.Time{}) {
		v.Set(reflect.ValueOf(time.Unix(1700000000, 0)))
		return
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), depth+1)
			}
		}
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, depth+1)
		fill(e, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fill(p.Elem(), depth+1)
		v.Set(p)
	}
}

// shared reports every slice, map or pointer in b that is the very one in a, and
// returns how many it compared.
func shared(a, b reflect.Value, path string, t *testing.T) int {
	n := 0
	switch a.Kind() {
	case reflect.Struct:
		if a.Type() == reflect.TypeOf(time.Time{}) {
			return 0 // a value: its location pointer is shared by design and immutable
		}
		for i := range a.NumField() {
			if f := a.Type().Field(i); f.IsExported() {
				n += shared(a.Field(i), b.Field(i), path+"."+f.Name, t)
			}
		}
	case reflect.Slice:
		n++
		if a.Len() > 0 && a.Pointer() == b.Pointer() {
			t.Errorf("%s is shared between the clone and the original", path)
		}
		for i := range min(a.Len(), b.Len()) {
			n += shared(a.Index(i), b.Index(i), path+"[]", t)
		}
	case reflect.Map:
		n++
		if !a.IsNil() && a.Pointer() == b.Pointer() {
			t.Errorf("%s is shared between the clone and the original", path)
		}
	case reflect.Pointer:
		n++
		if !a.IsNil() && a.Pointer() == b.Pointer() {
			t.Errorf("%s is shared between the clone and the original", path)
		}
	}
	return n
}
