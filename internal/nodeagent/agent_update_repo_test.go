package nodeagent

import (
	"os"
	"testing"

	"github.com/AppsGanin/rospanel/internal/updater"
)

func TestResolveUpdateRepo(t *testing.T) {
	orig := os.Getenv("ROSPANEL_REPO")
	defer os.Setenv("ROSPANEL_REPO", orig)

	// Case 1: No env, no updateRepo passed → default updater.Repo (fork)
	_ = os.Unsetenv("ROSPANEL_REPO")
	if got := resolveUpdateRepo(""); got != updater.Repo {
		t.Errorf("resolveUpdateRepo(\"\") = %q, want default %q", got, updater.Repo)
	}

	// Case 2: updateRepo passed from panel SyncResponse → uses updateRepo
	const customRepo = "myfork/rospanel-custom"
	if got := resolveUpdateRepo(customRepo); got != customRepo {
		t.Errorf("resolveUpdateRepo(%q) = %q, want %q", customRepo, got, customRepo)
	}

	// Case 3: ROSPANEL_REPO env set on host → takes highest priority
	const envRepo = "envfork/rospanel-env"
	_ = os.Setenv("ROSPANEL_REPO", envRepo)
	if got := resolveUpdateRepo(customRepo); got != envRepo {
		t.Errorf("resolveUpdateRepo(%q) with ROSPANEL_REPO=%q = %q, want %q", customRepo, envRepo, got, envRepo)
	}
}
