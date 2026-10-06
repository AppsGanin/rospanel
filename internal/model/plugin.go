package model

// Plugin is an installed plugin as stored. The manifest and the package are kept
// as the operator approved them; everything the panel derives from them (exports,
// points, translations) is re-read from the package when the plugin loads.
type Plugin struct {
	ID           string
	Version      string
	Manifest     string // plugin.json as packaged
	Package      []byte // the zip
	SHA256       string // of Package
	Enabled      bool
	Status       string // PluginActive, …
	StatusError  string
	GrantedPerms []string
	GrantedNet   []string
	Config       map[string]string
	PrevPackage  []byte // the version before the last update, for rollback
	PrevVersion  string
	Sort         int // order among hooks and channels
	InstalledAt  int64
	UpdatedAt    int64
}

// Plugin states.
const (
	PluginActive   = "active"   // enabled and loaded
	PluginPaused   = "paused"   // enabled, stopped by the breaker or the quota until resumed
	PluginError    = "error"    // enabled, failed to load
	PluginDisabled = "disabled" // turned off by the operator
)
