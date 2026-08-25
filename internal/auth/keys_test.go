package auth

import (
	"strings"
	"testing"
)

func TestRandomSecretGenerators(t *testing.T) {
	path, err := RandomSecretPath()
	if err != nil || path == "" {
		t.Fatalf("RandomSecretPath failed: %v", err)
	}

	pass, err := RandomPassword()
	if err != nil || pass == "" {
		t.Fatalf("RandomPassword failed: %v", err)
	}

	tok, err := RandomToken()
	if err != nil || tok == "" {
		t.Fatalf("RandomToken failed: %v", err)
	}
}

func TestRealityKeyGenerators(t *testing.T) {
	priv, pub, err := GenerateRealityKeys()
	if err != nil {
		t.Fatalf("GenerateRealityKeys failed: %v", err)
	}
	if len(priv) == 0 || len(pub) == 0 {
		t.Errorf("empty keys generated: priv=%q, pub=%q", priv, pub)
	}

	shortIDs, err := RandomShortIDs()
	if err != nil {
		t.Fatalf("RandomShortIDs failed: %v", err)
	}
	parts := strings.Split(shortIDs, ",")
	if len(parts) != 8 {
		t.Errorf("RandomShortIDs count = %d; want 8", len(parts))
	}

	realityPath, err := RandomRealityPath()
	if err != nil || !strings.HasPrefix(realityPath, "/") {
		t.Errorf("RandomRealityPath = %q; want prefix /", realityPath)
	}
}

func TestRandomShadowKey(t *testing.T) {
	key16, err := RandomShadowKey(16)
	if err != nil || len(key16) == 0 {
		t.Fatalf("RandomShadowKey(16) failed: %v", err)
	}

	key32, err := RandomShadowKey(32)
	if err != nil || len(key32) == 0 {
		t.Fatalf("RandomShadowKey(32) failed: %v", err)
	}
}
