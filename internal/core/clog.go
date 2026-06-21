package core

import (
	"log"

	"github.com/AppsGanin/rospanel/internal/logbuf"
)

// Leveled log helpers. The "[INFO]"/"[WARN]"/"[ERROR]" tag rides in front of the
// message (the std logger already prefixes "rospanel: " and a timestamp), which
// lets the dashboard log viewer filter lines by severity. Use these for the
// panel's own operational logging so coverage and format stay consistent.
func logInfo(format string, a ...any) { log.Printf("[INFO] "+format, a...) }
func logWarn(format string, a ...any) { log.Printf("[WARN] "+format, a...) }
func logErr(format string, a ...any)  { log.Printf("[ERROR] "+format, a...) }

// AppLogTail returns the buffered recent panel log lines (for the dashboard).
func (m *Manager) AppLogTail() []string { return logbuf.Default.Tail() }

// SubscribeAppLogs returns a channel of new panel log lines and an unsubscribe.
func (m *Manager) SubscribeAppLogs() (<-chan string, func()) { return logbuf.Default.Subscribe() }
