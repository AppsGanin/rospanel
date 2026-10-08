package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"slices"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
)

// callOpts shape what a call into a plugin may do.
type callOpts struct {
	readOnly bool          // a decision hook: panel.api is GET only (the panel may be holding a lock)
	lang     string        // the language panel.t defaults to
	wait     time.Duration // >0: give up (ErrBusy) rather than wait longer for a busy plugin
	// public: the caller is anyone on the internet (onHttp, a payment callback). A
	// call that fails there is logged but not counted against the plugin: rejecting a
	// forged request is its job, and ten of them must not pause it.
	public bool
}

// ErrBusy is a plugin still in another call when a decision could not wait for it.
var ErrBusy = errors.New("plugin: busy")

// Call invokes an export of an active plugin with arg (marshalled to JSON) and
// returns its JSON result. It is the one way into a plugin: the breaker, the VM's
// replacement and the database's cleanup all happen here.
func (h *Host) Call(ctx context.Context, id, export string, arg any, timeout time.Duration) (json.RawMessage, error) {
	return h.call(ctx, id, export, arg, timeout, callOpts{})
}

func (h *Host) call(ctx context.Context, id, export string, arg any, timeout time.Duration, o callOpts) (json.RawMessage, error) {
	inst, err := h.get(id)
	if err != nil {
		return nil, err
	}
	// A plugin calling back into itself — panel.api opening an order paid with its
	// own payment method — would wait on the lock its outer call holds.
	if calling(ctx, id) {
		return nil, ErrReentry
	}
	var raw []byte
	if arg != nil {
		if raw, err = json.Marshal(arg); err != nil {
			return nil, err
		}
	}
	// Never wait for the plugin longer than the call itself may take: a call stuck
	// in another (or in a loop through the panel the context did not carry) ends as
	// "busy" instead of holding its caller — an event worker, the bot — for good.
	wait := o.wait
	if wait <= 0 {
		wait = timeout
	}
	if !inst.mu.lockWithin(ctx, wait) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, ErrBusy
	}
	defer inst.mu.Unlock()
	if inst.status != model.PluginActive || h.isClosed() {
		return nil, ErrNotActive
	}
	if !inst.running { // active by its record, still starting: come back later
		return nil, ErrBusy
	}
	if err := ctx.Err(); err != nil { // the caller left while this call queued
		return nil, err
	}
	// The VM runs on its own clock. A caller that goes away (a client disconnecting
	// from onHttp or the subscription page) must not tear the VM down mid-call — that
	// counted as the plugin failing, and ten disconnects paused it. The caller's
	// deadline still holds: it shortens the call's own, so the JS is interrupted, not
	// the module killed.
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < timeout {
			timeout = max(rem, 10*time.Millisecond)
		}
	}
	return inst.invoke(context.WithoutCancel(withCalling(ctx, id)), export, raw, timeout, o)
}

// ErrPaused is a call that ended with the plugin paused (its database over quota).
var ErrPaused = errors.New("plugin: paused")

// ErrReentry is a plugin's call reaching the same plugin again.
var ErrReentry = errors.New("plugin: a call into itself (through the panel) is refused")

type callingKey struct{}

// withCalling marks ctx as inside a call of plugin id; calling reports the mark.
func withCalling(ctx context.Context, id string) context.Context {
	prev, _ := ctx.Value(callingKey{}).([]string)
	return context.WithValue(ctx, callingKey{}, append(slices.Clone(prev), id))
}

func calling(ctx context.Context, id string) bool {
	ids, _ := ctx.Value(callingKey{}).([]string)
	return slices.Contains(ids, id)
}

// gate is the lock on one plugin. Its waiters are served in the order they came (a
// channel's senders queue up): with a polling TryLock a sign-up waiting its 300 ms
// lost to every event and cron call that simply blocked.
type gate struct{ ch chan struct{} }

func newGate() gate { return gate{ch: make(chan struct{}, 1)} }

func (g gate) Lock()   { g.ch <- struct{}{} }
func (g gate) Unlock() { <-g.ch }

// lockWithin takes the lock if it comes free within d, and while ctx lasts: a
// sign-up must not stand behind a plugin's minute-long cron job.
func (g gate) lockWithin(ctx context.Context, d time.Duration) bool {
	select {
	case g.ch <- struct{}{}:
		return true
	default:
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case g.ch <- struct{}{}:
		return true
	case <-t.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// invoke runs one call with inst.mu held. It replaces a dead or bloated VM,
// rolls back what the call left open in the database, and counts failures.
func (inst *instance) invoke(ctx context.Context, export string, arg []byte, timeout time.Duration, o callOpts) (json.RawMessage, error) {
	if err := inst.ensureVM(ctx, o); err != nil {
		inst.failed(fmt.Errorf("reload: %w", err))
		return nil, err
	}
	out, err := inst.vm.Call(ctx, export, arg, timeout, inst.hostFunc(o))
	if inst.db != nil {
		inst.db.EndCall()
	}
	inst.dropBlobs()
	if inst.vm != nil {
		grown := float64(inst.vm.Memory()) - float64(inst.vmBase)
		if limit := float64(inst.pkg.Manifest.Memory()); grown > recycleAt*limit {
			inst.logf("warn", "memory grew by %d MB (limit %d MB): the VM is replaced before the next call",
				int(grown)>>20, int(limit)>>20)
			inst.vm.Close()
			inst.vm = nil
			inst.host.releaseMemory()
		}
	}
	if inst.db != nil && inst.db.OverQuota() {
		// The call itself is answered (a payment it confirmed stays confirmed); the
		// plugin is paused for the next one.
		inst.pause(fmt.Sprintf("its database grew past the %d MB quota", inst.pkg.Manifest.Quota()>>20))
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	if err != nil {
		if o.public {
			inst.logf("warn", "%s: %v", export, err)
			return nil, err
		}
		inst.failed(fmt.Errorf("%s: %w", export, err))
		return nil, err
	}
	inst.fails = 0
	if export != "onEnable" { // a call that did real work: the plugin is back
		inst.trips = 0
	}
	return out, nil
}

// failed logs a failed call and pauses the plugin after breakerLimit in a row.
// The cause is as often outside the plugin as in it — the service it calls is
// down — so the panel brings it back on its own (retryLater) and keeps what waits
// for it.
func (inst *instance) failed(err error) {
	inst.fails++
	msg := err.Error()
	var je *jsvm.JSError
	if errors.As(err, &je) && je.Stack != "" {
		msg += "\n" + je.Stack
	}
	inst.logf("error", "%s", msg)
	if inst.fails < breakerLimit || inst.status != model.PluginActive || inst.host.deps.NoBreaker {
		return
	}
	reason := fmt.Sprintf("%d %s; the last: %v", inst.fails, tripMark, err)
	inst.stop()
	inst.setState(true, model.PluginPaused, reason)
	d := inst.retryLater()
	inst.logf("error", "paused: %s — the panel retries it in %s", reason, d)
	inst.host.log.Warn("plugin paused", "plugin", inst.id, "reason", reason, "retry_in", d)
	inst.notify(reason, true)
}

// tripMark is in the status of a plugin the breaker paused: what tells a restart
// to keep retrying it.
const tripMark = "failed calls in a row"

// Failed reports whether err came from the plugin's own code or limits (as
// opposed to the plugin not being there).
func Failed(err error) bool {
	var je *jsvm.JSError
	var te *jsvm.TrapError
	return errors.As(err, &je) || errors.As(err, &te) || errors.Is(err, jsvm.ErrNoExport)
}

// releaseMemory hands the memory of a closed VM back to the OS. A VM that bloated
// did so by up to its memory cap, and Go returns freed memory only slowly — on the
// test master two bombs in a row left the panel 140 MB heavier for minutes. Requests
// are coalesced: one release at most every releaseEvery, and always one within
// releaseEvery of the last request, so nothing is kept for long.
func (h *Host) releaseMemory() {
	if !h.releasePending.CompareAndSwap(false, true) {
		return // the scheduled release covers this one
	}
	wait := time.Until(time.Unix(0, h.lastRelease.Load()).Add(releaseEvery))
	time.AfterFunc(max(wait, 0), func() {
		h.releasePending.Store(false)
		h.lastRelease.Store(time.Now().UnixNano())
		debug.FreeOSMemory()
	})
}

const releaseEvery = 30 * time.Second
