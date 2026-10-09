package plugin

import (
	"context"
	"sync"
	"time"

	"github.com/AppsGanin/rospanel/internal/cron"
	"github.com/AppsGanin/rospanel/internal/model"
)

// cronState remembers which jobs are running and the last minute that fired.
type cronState struct {
	mu      sync.Mutex
	running map[string]bool // "plugin/job"
	last    time.Time
}

// RunCron starts every job of every active plugin whose schedule matches now (in
// the operator's time zone). Call it once a minute; a minute already fired is
// ignored. A job still running from before is skipped rather than stacked: calls
// into one plugin are serialized anyway, and a pile-up would only delay its events.
func (h *Host) RunCron(ctx context.Context, now time.Time) {
	minute := now.Truncate(time.Minute)
	h.cron.mu.Lock()
	if !minute.After(h.cron.last) {
		h.cron.mu.Unlock()
		return
	}
	h.cron.last = minute
	h.cron.mu.Unlock()

	h.mu.RLock()
	insts := make([]*instance, 0, len(h.plugins))
	for _, inst := range h.plugins {
		insts = append(insts, inst)
	}
	h.mu.RUnlock()
	for _, inst := range insts {
		p := inst.pub.Load()
		if p == nil || p.manifest == nil || p.status != model.PluginActive {
			continue
		}
		for _, job := range p.manifest.Provides.Cron {
			sched, err := cron.Parse(job.Schedule)
			if err != nil || !sched.Match(now) {
				continue
			}
			key := inst.id + "/" + job.Name
			h.cron.mu.Lock()
			if h.cron.running == nil {
				h.cron.running = map[string]bool{}
			}
			busy := h.cron.running[key]
			h.cron.running[key] = true
			h.cron.mu.Unlock()
			if busy {
				inst.logf("warn", "cron %s skipped: the previous run is still going", job.Name)
				continue
			}
			go func(id, name, key string) {
				defer func() {
					h.cron.mu.Lock()
					delete(h.cron.running, key)
					h.cron.mu.Unlock()
				}()
				_, _ = h.Call(ctx, id, name, nil, CronTimeout) // failures land in the plugin's log
			}(inst.id, job.Name, key)
		}
	}
}

// WaitCron waits for running cron jobs, up to d — at shutdown.
func (h *Host) WaitCron(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		h.cron.mu.Lock()
		n := len(h.cron.running)
		h.cron.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
