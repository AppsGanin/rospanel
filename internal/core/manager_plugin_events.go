package core

import (
	"context"
	"time"
)

// Plugins subscribe to the webhook events (internal/plugin) and are delivered to
// through the same outbox, with the same retries — but by their own dispatcher and
// workers. A plugin call may take its full ten seconds; sharing the endpoints'
// workers, a slow plugin would hold every external webhook back behind it.

// PluginEvents is what the manager needs from the plugin host.
type PluginEvents interface {
	// EventSubscribers lists the active plugins subscribed to event.
	EventSubscribers(event string) []string
	// DeliverEvent hands one stored delivery to a plugin. gone means the plugin is
	// not there to take it any more (removed, off, paused): the delivery is dropped
	// rather than retried.
	DeliverEvent(ctx context.Context, plugin string, body []byte) (gone bool, err error)
}

const (
	pluginWorkers   = 2
	pluginTimeout   = 15 * time.Second // the plugin's own deadline plus its grace
	pluginBusyRetry = 2 * time.Second  // a delivery to a plugin busy with another
)

// SetPluginEvents connects the plugin host. Until it is set no event goes to a
// plugin.
func (m *Manager) SetPluginEvents(p PluginEvents) {
	m.pluginsMu.Lock()
	m.plugins = p
	m.pluginsMu.Unlock()
	select {
	case m.pluginKick <- struct{}{}: // deliver what a restart left behind
	default:
	}
}

func (m *Manager) pluginHost() PluginEvents {
	m.pluginsMu.RLock()
	defer m.pluginsMu.RUnlock()
	return m.plugins
}

func (m *Manager) pluginSubscribers(event string) []string {
	if p := m.pluginHost(); p != nil {
		return p.EventSubscribers(event)
	}
	return nil
}

// DropPluginDeliveries clears what is waiting for a plugin that stopped.
func (m *Manager) DropPluginDeliveries(plugin string) {
	if err := m.store.DropPluginDeliveries(plugin); err != nil {
		logErr("plugin events: dropping deliveries failed", "plugin", plugin, "err", err)
	}
}

type pluginJob struct {
	outboxID int64
	plugin   string
	event    string
	body     []byte
	attempt  int
}

func (m *Manager) startPluginEventWorkers() {
	ch := make(chan pluginJob, pluginWorkers)
	for i := 0; i < pluginWorkers; i++ {
		m.runAsync(func() {
			for {
				select {
				case <-m.done:
					return // what is leased comes back with the lease
				case job := <-ch:
					m.deliverPluginEvent(job)
				}
			}
		})
	}
	m.runAsync(func() {
		tick := time.NewTicker(webhookPoll)
		defer tick.Stop()
		for {
			for m.dispatchPluginEvents(ch) {
			}
			select {
			case <-m.done:
				return
			case <-m.pluginKick:
			case <-tick.C:
			}
		}
	})
}

// dispatchPluginEvents leases one batch of plugin deliveries, reporting whether it
// was full. Nothing is leased before the host is connected.
func (m *Manager) dispatchPluginEvents(ch chan<- pluginJob) bool {
	if m.pluginHost() == nil {
		return false
	}
	ds, err := m.store.LeasePluginDeliveries(time.Now().Unix(), int64(webhookLease.Seconds()), webhookBatch)
	if err != nil {
		logErr("plugin events: leasing deliveries failed", "err", err)
		return false
	}
	for _, d := range ds {
		select {
		case ch <- pluginJob{outboxID: d.ID, plugin: d.PluginID, event: d.Event, body: d.Body, attempt: d.Attempt + 1}:
		case <-m.done:
			return false
		}
	}
	return len(ds) == webhookBatch
}

func (m *Manager) deliverPluginEvent(job pluginJob) {
	host := m.pluginHost()
	if host == nil {
		return // leased; back after the lease
	}
	// Calls into one plugin are serialized, so a second worker taking that plugin's
	// next event would only sit waiting — for up to a call's full deadline — while
	// other plugins' events queue behind both. It goes back instead, unspent.
	if !m.claimPlugin(job.plugin) {
		if e := m.store.RetryWebhookDelivery(job.outboxID, job.attempt-1, time.Now().Add(pluginBusyRetry).Unix()); e != nil {
			logErr("plugin events: putting back a delivery failed", "plugin", job.plugin, "err", e)
		}
		return
	}
	defer m.releasePlugin(job.plugin)
	ctx, cancel := context.WithTimeout(context.Background(), pluginTimeout)
	gone, err := host.DeliverEvent(ctx, job.plugin, job.body)
	cancel()
	if err == nil || gone || job.attempt >= webhookMaxAttempts {
		if err != nil && !gone {
			logWarn("plugin events: giving up", "plugin", job.plugin, "event", job.event, "attempts", job.attempt, "err", err)
		}
		if e := m.store.FinishWebhookDelivery(job.outboxID); e != nil {
			logErr("plugin events: clearing a delivery failed", "plugin", job.plugin, "err", e)
		}
		return
	}
	delay := webhookBackoff[len(webhookBackoff)-1]
	if job.attempt-1 < len(webhookBackoff) {
		delay = webhookBackoff[job.attempt-1]
	}
	if e := m.store.RetryWebhookDelivery(job.outboxID, job.attempt, time.Now().Add(delay).Unix()); e != nil {
		logErr("plugin events: scheduling a retry failed", "plugin", job.plugin, "err", e)
	}
}

func (m *Manager) claimPlugin(plugin string) bool {
	m.pluginBusyMu.Lock()
	defer m.pluginBusyMu.Unlock()
	if m.pluginBusy[plugin] {
		return false
	}
	if m.pluginBusy == nil {
		m.pluginBusy = map[string]bool{}
	}
	m.pluginBusy[plugin] = true
	return true
}

func (m *Manager) releasePlugin(plugin string) {
	m.pluginBusyMu.Lock()
	delete(m.pluginBusy, plugin)
	m.pluginBusyMu.Unlock()
}
