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
}

// Theme returns the active theme, if any.
func (h *Host) Theme() (ThemeInfo, bool) {
	ids := h.Active(func(m *manifest.Manifest) bool { return m.Provides.Theme })
	if len(ids) == 0 {
		return ThemeInfo{}, false
	}
	inst, err := h.get(ids[0])
	if err != nil {
		return ThemeInfo{}, false
	}
	p := inst.pub.Load()
	if p == nil || p.pkg == nil {
		return ThemeInfo{}, false
	}
	return ThemeInfo{Plugin: ids[0], Version: p.pkg.SHA256[:12]}, true
}

// ThemeFile returns a file of the active theme and its content type.
func (h *Host) ThemeFile(name string) ([]byte, string, bool) {
	t, ok := h.Theme()
	if !ok {
		return nil, "", false
	}
	inst, err := h.get(t.Plugin)
	if err != nil {
		return nil, "", false
	}
	p := inst.pub.Load()
	if p == nil || p.pkg == nil {
		return nil, "", false
	}
	b, ok := p.pkg.Theme[name]
	ct := manifest.ThemeTypes[path.Ext(name)]
	if !ok || ct == "" {
		return nil, "", false
	}
	return b, ct, true
}
