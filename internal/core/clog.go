package core

import (
	"log/slog"

	"github.com/AppsGanin/rospanel/internal/logbuf"
)

// logInfo/logWarn/logErr are package-level structured logging helpers for the
// core package. They delegate to slog so calls are routed through the process-wide
// slogHandler (installed in main) and end up in all configured sinks.
func logInfo(msg string, args ...any) { slog.Info(msg, args...) }
func logWarn(msg string, args ...any) { slog.Warn(msg, args...) }
func logErr(msg string, args ...any)  { slog.Error(msg, args...) }

// AppLogTail returns the buffered recent panel log lines (for the dashboard).
func (m *Manager) AppLogTail() []string { return logbuf.Default.Tail() }

// SubscribeAppLogs returns a channel of new panel log lines and an unsubscribe.
func (m *Manager) SubscribeAppLogs() (<-chan string, func()) { return logbuf.Default.Subscribe() }
