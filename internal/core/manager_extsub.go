package core

import (
	"context"
	"strings"
	"time"

	"github.com/AppsGanin/rospanel/internal/extsub"
	"github.com/AppsGanin/rospanel/internal/model"
)

// External subscriptions (model.ExtSubscription): servers that are not ours, handed
// to users beside our own. The panel reads a source, keeps the servers it lists,
// and re-reads it on a schedule; nothing here touches Xray — these servers appear
// only in what a user downloads, so a change is live the moment it is stored.

// ExtSyncReport is what one reading of a source changed.
type ExtSyncReport struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Removed int `json:"removed"`
	Total   int `json:"total"`
}

// ExtSubscriptions lists the sources for the UI.
func (m *Manager) ExtSubscriptions() ([]model.ExtSubscription, error) {
	return m.store.ExtSubscriptions()
}

// ExtServers lists every server of every source for the UI and the group editor.
func (m *Manager) ExtServers() ([]model.ExtServer, error) {
	return m.store.ExtServers()
}

// EnabledExtServers is what a subscription may hand a user (before their access
// is applied).
func (m *Manager) EnabledExtServers() []model.ExtServer {
	list, err := m.store.EnabledExtServers()
	if err != nil {
		logErr("extsub: reading the enabled servers failed", "err", err)
		return nil
	}
	return list
}

// CreateExtSubscription validates a source, stores it and reads it once, so the
// operator sees the servers — or the reason there are none — in the same click.
// A first read that fails does not undo the creation: the source is kept with its
// error, and the next sync or a retry from the UI picks it up.
func (m *Manager) CreateExtSubscription(ctx context.Context, name, source string) (*model.ExtSubscription, ExtSyncReport, error) {
	name, err := model.CleanExtSubscriptionName(name)
	if err != nil {
		return nil, ExtSyncReport{}, fromFieldErr(err)
	}
	source = strings.TrimSpace(source)
	if err := extsub.ValidateSource(source); err != nil {
		return nil, ExtSyncReport{}, invalidCode("err.extSourceInvalid",
			"источник не подходит: {{err}}", map[string]any{"err": err.Error()})
	}
	if name == "" {
		name = extSourceLabel(source)
	}
	id, err := m.store.CreateExtSubscription(name, source)
	if err != nil {
		return nil, ExtSyncReport{}, err
	}
	logInfo("extsub: subscription added", "id", id, "name", name, "url", extsub.IsURL(source))
	report, _ := m.SyncExtSubscription(ctx, id)
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return nil, report, err
	}
	return sub, report, nil
}

// extSourceLabel names a source the operator left unnamed: a URL by its host, a
// pasted payload by what it is.
func extSourceLabel(source string) string {
	if extsub.IsURL(source) {
		rest := source[strings.Index(source, "://")+3:]
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			rest = rest[:i]
		}
		if rest != "" {
			return rest
		}
	}
	if extsub.IsHappLink(source) {
		return "Happ"
	}
	return "import"
}

// SyncExtSubscription re-reads one source and reconciles its servers. A failed read
// keeps the servers already there and records the error on the source.
func (m *Manager) SyncExtSubscription(ctx context.Context, id int64) (ExtSyncReport, error) {
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return ExtSyncReport{}, err
	}
	if sub == nil {
		return ExtSyncReport{}, invalidCode("err.extNotFound", "подписка не найдена")
	}
	now := time.Now().Unix()
	eps, err := extsub.Load(ctx, sub.Source)
	if err != nil {
		_ = m.store.MarkExtSubscriptionSync(id, 0, err.Error(), now)
		logWarn("extsub: read failed", "id", id, "name", sub.Name, "err", err)
		return ExtSyncReport{}, invalidCode("err.extSyncFailed",
			"не удалось прочитать подписку: {{err}}", map[string]any{"err": err.Error()})
	}
	found := make([]model.ExtServer, 0, len(eps))
	for _, ep := range eps {
		found = append(found, model.ExtServer{
			SubID: id, Key: ep.Key(), Name: ep.Name, Protocol: ep.Protocol,
			Host: ep.Host, Port: ep.Port, Link: ep.Link,
		})
	}
	added, updated, removed, err := m.store.ReplaceExtServers(id, found, now)
	if err != nil {
		_ = m.store.MarkExtSubscriptionSync(id, 0, err.Error(), now)
		return ExtSyncReport{}, err
	}
	if err := m.store.MarkExtSubscriptionSync(id, len(found), "", now); err != nil {
		return ExtSyncReport{}, err
	}
	if added > 0 || removed > 0 {
		logInfo("extsub: servers reconciled", "id", id, "name", sub.Name, "added", added, "updated", updated, "removed", removed)
	}
	return ExtSyncReport{Added: added, Updated: updated, Removed: removed, Total: len(found)}, nil
}

// SyncAllExtSubscriptions re-reads every enabled source, one after another.
func (m *Manager) SyncAllExtSubscriptions(ctx context.Context) {
	subs, err := m.store.ExtSubscriptions()
	if err != nil {
		logErr("extsub: listing sources failed", "err", err)
		return
	}
	for _, sub := range subs {
		if !sub.Enabled || ctx.Err() != nil {
			continue
		}
		_, _ = m.SyncExtSubscription(ctx, sub.ID)
	}
}

// RunExtSubLoop re-reads the sources every ExtSyncInterval until ctx ends. The
// first pass waits a little: at boot the sources were read at most an hour ago,
// and the network is the last thing a starting panel should depend on.
func (m *Manager) RunExtSubLoop(ctx context.Context) {
	timer := time.NewTimer(2 * time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		m.SyncAllExtSubscriptions(ctx)
		timer.Reset(model.ExtSyncInterval)
	}
}

// DeleteExtSubscription drops a source, its servers and their grants.
func (m *Manager) DeleteExtSubscription(id int64) error {
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return err
	}
	if sub == nil {
		return nil // already gone; deleting twice is not an error
	}
	if err := m.store.DeleteExtSubscription(id); err != nil {
		return err
	}
	logInfo("extsub: subscription removed", "id", id, "name", sub.Name)
	return nil
}

// SetExtSubscriptionEnabled switches a source on or off.
func (m *Manager) SetExtSubscriptionEnabled(id int64, enabled bool) error {
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return err
	}
	if sub == nil {
		return invalidCode("err.extNotFound", "подписка не найдена")
	}
	return m.store.SetExtSubscriptionEnabled(id, enabled)
}

// SetExtServerEnabled switches one server on or off.
func (m *Manager) SetExtServerEnabled(id int64, enabled bool) error {
	ok, err := m.store.SetExtServerEnabled(id, enabled)
	if err != nil {
		return err
	}
	if !ok {
		return invalidCode("err.extServerNotFound", "сервер не найден")
	}
	return nil
}

// SetExtSubscriptionServersEnabled switches every server of a source at once.
func (m *Manager) SetExtSubscriptionServersEnabled(id int64, enabled bool) error {
	sub, err := m.store.ExtSubscription(id)
	if err != nil {
		return err
	}
	if sub == nil {
		return invalidCode("err.extNotFound", "подписка не найдена")
	}
	return m.store.SetExtSubscriptionServersEnabled(id, enabled)
}
