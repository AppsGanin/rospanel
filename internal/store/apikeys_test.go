package store

import (
	"path/filepath"
	"testing"
)

func TestAPIKeyLifecycle(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "keys.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	k, err := st.CreateAPIKey("integration")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if k.RawKey == "" || k.Prefix == "" {
		t.Fatalf("expected raw key and prefix, got %+v", k)
	}
	if k.RawKey[:len(k.Prefix)] != k.Prefix {
		t.Fatalf("prefix %q is not a prefix of raw key %q", k.Prefix, k.RawKey)
	}

	// A valid raw key resolves and stamps last_used_at.
	got, err := st.LookupAPIKey(k.RawKey)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got == nil || got.ID != k.ID {
		t.Fatalf("lookup returned %+v, want id %d", got, k.ID)
	}
	if got.LastUsedAt == 0 {
		t.Fatalf("last_used_at not stamped")
	}

	// A wrong key resolves to nothing (no error).
	if bad, err := st.LookupAPIKey("rp_notarealkey"); err != nil || bad != nil {
		t.Fatalf("bad key lookup = (%v, %v), want (nil, nil)", bad, err)
	}

	// Listing shows the key without its raw value.
	keys, err := st.ListAPIKeys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("list = (%d keys, %v), want 1", len(keys), err)
	}
	if keys[0].RawKey != "" {
		t.Fatalf("list must not expose raw key")
	}

	// Revoked keys stop authenticating but stay listed.
	if err := st.RevokeAPIKey(k.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got, err := st.LookupAPIKey(k.RawKey); err != nil || got != nil {
		t.Fatalf("revoked key lookup = (%v, %v), want (nil, nil)", got, err)
	}
	keys, _ = st.ListAPIKeys()
	if len(keys) != 1 || keys[0].Active() {
		t.Fatalf("revoked key should remain listed as inactive: %+v", keys)
	}
}

func TestAPIPathSetting(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "path.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	set, err := st.GetSettings()
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if set.APIPath != "" {
		t.Fatalf("api_path should default empty, got %q", set.APIPath)
	}
	if err := st.SetAPIPath("abc123"); err != nil {
		t.Fatalf("set api path: %v", err)
	}
	set, _ = st.GetSettings()
	if set.APIPath != "abc123" {
		t.Fatalf("api_path = %q, want abc123", set.APIPath)
	}
}
