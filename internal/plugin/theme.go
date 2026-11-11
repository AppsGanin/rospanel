package plugin

import (
	"path"

	"github.com/AppsGanin/rospanel/internal/plugin/manifest"
)

// ThemeInfo is the subscription page's active theme: the first active plugin (in
// order) that has one. Version changes with the package, for the stylesheet's
// cache-busting query.
type ThemeInfo struct {
	Plugin  string
	Version string
	// PanelStyles: the panel's stylesheet stays under the theme's; false when the
	// theme styles the whole page itself (provides.theme.panel_styles).
	PanelStyles bool
}

// Theme returns the active theme, if any.
func (h *Host) Theme() (ThemeInfo, bool) {
	t, _, ok := h.ThemeFiles()
	return t, ok
}

// ThemeFile returns a file of the active theme and its content type.
func (h *Host) ThemeFile(name string) ([]byte, string, bool) {
	_, files, ok := h.ThemeFiles()
	if !ok {
		return nil, "", false
	}
	b, ok := files[name]
	ct := manifest.ThemeTypes[path.Ext(name)]
	if !ok || ct == "" {
		return nil, "", false
	}
	return b, ct, true
}

// ThemeFiles returns the active theme and every file of it — the templates that
// replace sections of the page (theme/<section>.html) and the assets they name —
// all read from one package, so the version and the files never disagree.
func (h *Host) ThemeFiles() (ThemeInfo, map[string][]byte, bool) {
	ids := h.Active(func(m *manifest.Manifest) bool { return m.Provides.ThemeOn() })
	if len(ids) == 0 {
		return ThemeInfo{}, nil, false
	}
	inst, err := h.get(ids[0])
	if err != nil {
		return ThemeInfo{}, nil, false
	}
	p := inst.pub.Load()
	// The package may have been replaced since Active looked: by one that is no theme.
	if p == nil || p.pkg == nil || p.pkg.Manifest == nil || !p.pkg.Manifest.Provides.ThemeOn() {
		return ThemeInfo{}, nil, false
	}
	t := ThemeInfo{Plugin: ids[0], Version: p.pkg.SHA256[:12], PanelStyles: p.pkg.Manifest.Provides.Theme.PanelStyles}
	return t, p.pkg.Theme, true
}
