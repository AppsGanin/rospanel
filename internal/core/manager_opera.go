package core

import (
	"fmt"
	"time"

	"github.com/AppsGanin/rospanel/internal/opera"
)

// operaReadyTimeout bounds how long we wait for the opera-proxy helper to start
// accepting connections after launch before reporting failure.
const operaReadyTimeout = 20 * time.Second

// syncOpera reconciles the opera-proxy helper to the desired state. Enabling
// downloads the binary if missing and (re)starts it; it does NOT block the
// caller on readiness or tear the helper down on timeout — opera-proxy retries
// on its own and the "opera" lane falls back to direct until it's up. Only a
// hard failure (binary download / process spawn) returns an error.
func (m *Manager) syncOpera(enabled bool, country string, port int) error {
	if !enabled {
		logInfo("opera: disabling")
		m.operaSup.Stop()
		return nil
	}
	if _, err := opera.EnsureBinary(m.operaDir); err != nil {
		logErr("opera: binary download failed", "err", err)
		return fmt.Errorf("opera-proxy: загрузка не удалась: %w", err)
	}
	logInfo("opera: starting helper", "country", country, "port", port)
	if err := m.operaSup.Start(country, port); err != nil {
		logErr("opera: start failed", "err", err)
		return fmt.Errorf("opera-proxy: запуск не удался: %w", err)
	}
	// Observe readiness off the request path; don't stop the helper on timeout.
	go func() {
		if err := m.operaSup.WaitReady(port, operaReadyTimeout); err != nil {
			logWarn("opera: not ready yet, helper keeps retrying", "err", err)
		} else {
			logInfo("opera: helper ready", "port", port, "country", country)
		}
		m.probeLanes()
	}()
	return nil
}

// OperaRunning reports whether the opera-proxy helper is currently up.
func (m *Manager) OperaRunning() bool { return m.operaSup.Running() }
