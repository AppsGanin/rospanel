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

// What the panel asks plugins at its decision points (internal/plugin, the
// beforeSignup / beforeDeviceBind / quotePrice exports). Plain data: the panel's
// core asks through an interface and never imports the plugin host.

// SignupCheck is a sign-up about to happen.
type SignupCheck struct {
	Channel    string `json:"channel"` // telegram (API by telegram_id) | web | miniapp | bot
	TelegramID int64  `json:"telegram_id,omitempty"`
	Username   string `json:"username,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	IP         string `json:"ip,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Source     string `json:"source,omitempty"`
	Lang       string `json:"lang,omitempty"`
}

// DeviceCheck is a device about to take one of a user's slots.
type DeviceCheck struct {
	UserID      int64  `json:"user_id"`
	HWID        string `json:"hwid"`
	DeviceOS    string `json:"device_os,omitempty"`
	DeviceModel string `json:"device_model,omitempty"`
	UserAgent   string `json:"user_agent,omitempty"`
	IP          string `json:"ip,omitempty"`
	Count       int    `json:"count"` // devices bound before this one
	Cap         int    `json:"cap"`   // the user's limit, 0 = none
	Lang        string `json:"lang,omitempty"`
}

// PriceRequest is a plan being priced for a user.
type PriceRequest struct {
	UserID  int64  `json:"user_id"`
	PlanID  int64  `json:"plan_id"`
	Plan    string `json:"plan"`
	Periods int    `json:"periods"`
	Devices int    `json:"devices"`
	BaseRub int    `json:"base_rub"` // after the period discount, before a promo code
	Lang    string `json:"lang,omitempty"`
}

// BotUser is who pressed a plugin's button or sent its command in the user bot.
// ID is 0 for a Telegram with no account yet.
type BotUser struct {
	ID         int64  `json:"id"`
	Name       string `json:"name,omitempty"`
	TelegramID int64  `json:"telegram_id"`
	Username   string `json:"username,omitempty"`
}

// BotButton is a button a plugin adds to the user bot: Data comes back to the
// plugin when pressed (the panel prefixes it with px:<plugin>:), URL opens a page.
type BotButton struct {
	Text string `json:"text"`
	Data string `json:"data,omitempty"`
	URL  string `json:"url,omitempty"`
}

// BotReply is what the bot shows after a plugin's button or command: plain text
// (the panel escapes it) and rows of buttons.
type BotReply struct {
	Text    string        `json:"text"`
	Buttons [][]BotButton `json:"buttons,omitempty"`
}

// BotCommandInfo is a plugin's command, as the bot's command menu lists it.
type BotCommandInfo struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// PluginDraft is a plugin being written in the panel.
type PluginDraft struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Mode      string            `json:"mode"` // builder | code
	PluginID  string            `json:"plugin_id"`
	Version   string            `json:"version"`
	Files     map[string][]byte `json:"-"`
	CreatedBy string            `json:"created_by"`
	CreatedAt int64             `json:"created_at"`
	UpdatedAt int64             `json:"updated_at"`
}

// Draft modes: the rule builder writes the files, or they are edited by hand.
const (
	DraftBuilder = "builder"
	DraftCode    = "code"
)
