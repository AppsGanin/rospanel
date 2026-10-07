package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/model"
	"github.com/AppsGanin/rospanel/internal/plugin/jsvm"
)

// callOpts shape what a call into a plugin may do.
type callOpts struct {
	readOnly bool          // a decision hook: panel.api is GET only (the panel may be holding a lock)
	lang     string        // the language panel.t defaults to
	wait     time.Duration // >0: give up (ErrBusy) rather than wait longer for a busy plugin
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
	var raw []byte
	if arg != nil {
		if raw, err = json.Marshal(arg); err != nil {
			return nil, err
		}
	}
	if o.wait > 0 {
		if !lockWithin(&inst.mu, o.wait) {
			return nil, ErrBusy
		}
	} else {
		inst.mu.Lock()
	}
	defer inst.mu.Unlock()
	if inst.status != model.PluginActive {
		return nil, ErrNotActive
	}
	return inst.invoke(ctx, export, raw, timeout, o)
}

// lockWithin takes mu if it comes free within d: a sign-up must not stand behind a
// plugin's minute-long cron job.
func lockWithin(mu *sync.Mutex, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for !mu.TryLock() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
	return true
}

// invoke runs one call with inst.mu held. It replaces a dead or bloated VM,
// rolls back what the call left open in the database, and counts failures.
func (inst *instance) invoke(ctx context.Context, export string, arg []byte, timeout time.Duration, o callOpts) (json.RawMessage, error) {
	if err := inst.ensureVM(ctx); err != nil {
		inst.failed(fmt.Errorf("reload: %w", err))
		return nil, err
	}
	out, err := inst.vm.Call(ctx, export, arg, timeout, inst.hostFunc(o))
	if inst.db != nil {
		inst.db.EndCall()
	}
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
		inst.pause(fmt.Sprintf("its database grew past the %d MB quota", inst.pkg.Manifest.Quota()>>20))
		return out, err
	}
	if err != nil {
		inst.failed(fmt.Errorf("%s: %w", export, err))
		return nil, err
	}
	inst.fails = 0
	return out, nil
}

// failed logs a failed call and pauses the plugin after breakerLimit in a row.
func (inst *instance) failed(err error) {
	inst.fails++
	msg := err.Error()
	var je *jsvm.JSError
	if errors.As(err, &je) && je.Stack != "" {
		msg += "\n" + je.Stack
	}
	inst.logf("error", "%s", msg)
	if inst.fails >= breakerLimit && inst.status == model.PluginActive && !inst.host.deps.NoBreaker {
		inst.pause(fmt.Sprintf("%d failed calls in a row; the last: %v", inst.fails, err))
	}
}

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
