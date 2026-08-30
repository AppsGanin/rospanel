package core

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/Shu1t3/rospanel-shu1t3/internal/happ"
	"github.com/Shu1t3/rospanel-shu1t3/internal/xray"
)

// ── Subscription management ────────────────────────────────────────────────

// CreateHappSubscription validates the URL, creates the subscription, and runs
// the first sync. Returns the subscription ID and initial sync result.
func (m *Manager) CreateHappSubscription(ctx context.Context, name, rawURL string) (int64, *happ.SyncResult, error) {
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		return 0, nil, fmt.Errorf("invalid subscription URL: %w", err)
	}

	id, err := m.store.CreateHappSubscription(name, rawURL)
	if err != nil {
		return 0, nil, fmt.Errorf("create subscription: %w", err)
	}

	res, err := m.syncHappSubscriptionInner(ctx, id)
	if err != nil {
		slog.Warn("happ: initial sync failed", "sub_id", id, "err", err)
		// Don't abort — subscription is created; user can retry.
		return id, nil, nil
	}
	return id, res, nil
}

// SyncHappSubscription fetches a subscription and upserts its nodes.
// On fetch error, existing nodes are NOT removed (safe refresh semantics).
func (m *Manager) SyncHappSubscription(ctx context.Context, subID int64) (*happ.SyncResult, error) {
	return m.syncHappSubscriptionInner(ctx, subID)
}

// SyncAllHappSubscriptions syncs all enabled subscriptions. Called by the scheduler.
func (m *Manager) SyncAllHappSubscriptions(ctx context.Context) {
	ids, err := m.store.ListEnabledHappSubscriptionIDs()
	if err != nil {
		slog.Error("happ: list enabled subscriptions", "err", err)
		return
	}
	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if _, err := m.syncHappSubscriptionInner(ctx, id); err != nil {
			slog.Warn("happ: sync failed", "sub_id", id, "err", err)
		}
	}
}

func (m *Manager) syncHappSubscriptionInner(ctx context.Context, subID int64) (*happ.SyncResult, error) {
	sub, err := m.store.GetHappSubscription(subID)
	if err != nil || sub == nil {
		return nil, fmt.Errorf("subscription %d not found: %w", subID, err)
	}

	res := &happ.SyncResult{SubscriptionID: subID, At: time.Now()}

	fetchRes, fetchErr := happ.Fetch(ctx, sub.URL, subID)
	if fetchErr != nil {
		res.Error = fetchErr
		_ = m.store.UpdateHappSubscriptionFetch(subID, 0, fetchErr.Error())
		return res, fetchErr
	}

	added, updated, err := m.store.UpsertHappNodesFull(subID, fetchRes.Nodes)
	if err != nil {
		res.Error = err
		_ = m.store.UpdateHappSubscriptionFetch(subID, 0, err.Error())
		return res, err
	}

	res.Added = added
	res.Updated = updated
	res.Total = len(fetchRes.Nodes)
	_ = m.store.UpdateHappSubscriptionFetch(subID, len(fetchRes.Nodes), "")

	// Trigger Xray reconcile so the new outbounds take effect.
	m.TriggerReconcile()
	return res, nil
}

// DeleteHappSubscription removes a subscription and all its nodes.
func (m *Manager) DeleteHappSubscription(subID int64) error {
	if err := m.store.DeleteHappSubscription(subID); err != nil {
		return err
	}
	m.TriggerReconcile()
	return nil
}

// ── Node management ────────────────────────────────────────────────────────

// ListHappSubscriptions returns all subscriptions for UI display.
func (m *Manager) ListHappSubscriptions() ([]*happ.Subscription, error) {
	return m.store.ListHappSubscriptions()
}

// ListAllHappNodes returns all nodes across subscriptions for UI display.
func (m *Manager) ListAllHappNodes() ([]*happ.Node, error) {
	return m.store.ListAllHappNodes()
}

// SetHappNodeEnabled enables or disables a node, then triggers a config reconcile
// so the Xray outbound is added/removed from the live config.
func (m *Manager) SetHappNodeEnabled(nodeID int64, enabled bool) error {
	if err := m.store.SetHappNodeEnabled(nodeID, enabled); err != nil {
		return err
	}
	m.TriggerReconcile()
	return nil
}

// DeleteHappNode removes a single node and reconciles Xray.
func (m *Manager) DeleteHappNode(nodeID int64) error {
	if err := m.store.DeleteHappNode(nodeID); err != nil {
		return err
	}
	m.TriggerReconcile()
	return nil
}

// HappOutbounds returns Xray outbound configs for all enabled Happ nodes.
// Called during Xray config generation.
func (m *Manager) HappOutbounds() ([]xray.Outbound, error) {
	nodes, err := m.store.ListEnabledHappNodes()
	if err != nil {
		return nil, err
	}
	out := make([]xray.Outbound, 0, len(nodes))
	for _, n := range nodes {
		ob, err := happ.ToXrayOutbound(n)
		if err != nil {
			slog.Warn("happ: skip outbound generation", "node_id", n.ID, "protocol", n.Protocol, "err", err)
			continue
		}
		if ob != nil {
			out = append(out, *ob)
		}
	}
	return out, nil
}

// ── Scheduler ─────────────────────────────────────────────────────────────

// StartHappScheduler launches the 59-minute auto-sync goroutine.
// It stops when ctx is cancelled (graceful shutdown).
func (m *Manager) StartHappScheduler(ctx context.Context) {
	syncFn := func(ctx context.Context, subID int64) (happ.SyncResult, error) {
		res, err := m.syncHappSubscriptionInner(ctx, subID)
		if res == nil {
			return happ.SyncResult{SubscriptionID: subID}, err
		}
		return *res, err
	}
	listFn := func() ([]int64, error) {
		return m.store.ListEnabledHappSubscriptionIDs()
	}
	sched := happ.NewScheduler(59*time.Minute, listFn, syncFn)
	go sched.Run(ctx)
}
