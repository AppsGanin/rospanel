package server

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The per-field permission lists of the partial updates are written by hand, and a
// field missing from one would be changed on the route's permission alone — by a key
// whose role holds another section's. So every field of each request must be listed:
// a field added later fails here until someone decides whose it is.
func TestPatchFieldPermsCoverEveryField(t *testing.T) {
	t.Parallel()
	check := func(name string, req any, list func() []fieldPerm) {
		v := reflect.ValueOf(req).Elem()
		// Set every pointer field, so each list entry reports set.
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.Kind() == reflect.Pointer {
				f.Set(reflect.New(f.Type().Elem()))
			}
		}
		var listed []string
		for _, f := range list() {
			if !f.set {
				t.Errorf("%s: %s is listed but did not report set", name, f.field)
			}
			listed = append(listed, f.field)
		}
		ty := v.Type()
		for i := 0; i < ty.NumField(); i++ {
			tag := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]
			if tag == "" || tag == "-" {
				continue
			}
			if !slices.Contains(listed, tag) {
				t.Errorf("%s: field %s has no permission in the list", name, tag)
			}
		}
	}
	var s apiSettingsReq
	check("PATCH /v1/settings", &s, func() []fieldPerm { return settingsFieldPerms(s) })
	var n apiPatchNodeReq
	check("PATCH /v1/nodes/{id}", &n, func() []fieldPerm { return nodeFieldPerms(n) })
}
