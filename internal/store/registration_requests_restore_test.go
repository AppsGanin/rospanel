package store

import (
	"path/filepath"
	"testing"
)

// A request put back after a failed approval keeps its id and everything it came
// with, so whoever was told the id still finds it.
func TestRestoreRegistrationRequestKeepsItsID(t *testing.T) {
	t.Parallel()
	st, err := Open(filepath.Join(t.TempDir(), "rr.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, mk := range []func() (*int64, error){
		func() (*int64, error) {
			r, err := st.CreateWebRegistrationRequest("ann@example.com", "Ann", "ads", 7, 1000)
			if err != nil {
				return nil, err
			}
			return &r.ID, nil
		},
		func() (*int64, error) {
			r, err := st.CreateRegistrationRequest(4242, "Bob", 1000)
			if err != nil {
				return nil, err
			}
			return &r.ID, nil
		},
	} {
		id, err := mk()
		if err != nil {
			t.Fatal(err)
		}
		before, _ := st.GetRegistrationRequest(*id)
		if ok, err := st.ClaimRegistrationRequest(*id); err != nil || !ok {
			t.Fatalf("claim: %v %v", ok, err)
		}
		if err := st.RestoreRegistrationRequest(before); err != nil {
			t.Fatal(err)
		}
		after, err := st.GetRegistrationRequest(*id)
		if err != nil || *after != *before {
			t.Fatalf("restored %+v, want %+v (%v)", after, before, err)
		}
	}
}
