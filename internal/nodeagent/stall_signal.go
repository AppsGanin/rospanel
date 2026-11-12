package nodeagent

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// dumpOnSignal writes the goroutines to stacksFile on every SIGUSR1 and keeps
// running: `systemctl kill -s USR1 --kill-whom=main rospanel-node` shows where a
// node that stopped syncing is stuck without taking it down.
func (a *Agent) dumpOnSignal(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			path, loop := a.dumpStacks()
			slog.Info("node: goroutines dumped on request", "stacks", path, "sync_loop", loop)
		}
	}
}
