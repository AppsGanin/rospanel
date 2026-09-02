package server

import (
	"net/http"

	rospanel "github.com/AppsGanin/rospanel"
	"github.com/AppsGanin/rospanel/internal/version"
)

// changelog hands the panel the release history it was built with, newest first,
// and the version this binary is — so the page can mark where the reader stands.
func (rt *Router) changelog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"version":  version.Version,
		"releases": rospanel.Changelog(),
	})
}
