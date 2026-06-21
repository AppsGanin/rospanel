package core

import (
	"time"

	"github.com/AppsGanin/rospanel/internal/backup"
)

// XrayLogTail returns the buffered recent Xray log lines.
func (m *Manager) XrayLogTail() []string { return m.sup.LogTail() }

// XrayConfig returns the live on-disk Xray config.json.
func (m *Manager) XrayConfig() ([]byte, error) { return m.sup.ConfigBytes() }

// XrayStatus reports whether Xray is running and the unix start time of the
// current process (which changes on every reload — clients poll it to detect when
// a config change has finished restarting Xray).
func (m *Manager) XrayStatus() (running bool, startedAt int64) {
	return m.sup.Running(), m.sup.StartedAt()
}

// BackupManifest returns a Manifest describing the current server state for
// inclusion in backup archives.
func (m *Manager) BackupManifest() backup.Manifest {
	set, _ := m.store.GetSettings()
	users, _ := m.store.ListUsers()
	return backup.Manifest{
		Domain:     set.Host,
		SecretPath: set.PanelSecretPath,
		UserCount:  len(users),
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
}

// SubscribeXrayLogs returns a channel of new Xray log lines and an unsubscribe.
func (m *Manager) SubscribeXrayLogs() (<-chan string, func()) { return m.sup.SubscribeLogs() }
