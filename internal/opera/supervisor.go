package opera

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"sync"
	"time"
)

// restartDelay is how long the supervisor waits before relaunching a crashed
// opera-proxy (Opera's free VPN endpoints occasionally drop).
const restartDelay = 5 * time.Second

// Supervisor runs the opera-proxy helper as a child process, restarting it on
// unexpected exit. It can be reconfigured (region/port) at runtime: Start
// replaces any running instance, Stop terminates it and disables restarts.
type Supervisor struct {
	binPath string

	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan struct{} // closed by the current child's monitor once cmd.Wait reaps it
	running bool          // child is currently alive (guarded; never read cmd.ProcessState, which cmd.Wait writes off-lock)
	epoch   int           // bumped on every Start/Stop; stale monitor goroutines exit
	country string
	port    int
	active  bool // whether opera-proxy should be running (gates auto-restart)
}

// New builds a supervisor for the opera-proxy binary at binPath.
func New(binPath string) *Supervisor { return &Supervisor{binPath: binPath} }

// Start launches opera-proxy bound to 127.0.0.1:port for the given Opera VPN
// region, replacing any instance already running.
func (s *Supervisor) Start(country string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	s.killLocked()
	s.country, s.port, s.active = country, port, true
	return s.spawnLocked()
}

// Stop terminates opera-proxy and disables auto-restart.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.epoch++
	s.active = false
	s.killLocked()
}

// Running reports whether an opera-proxy process is currently alive.
func (s *Supervisor) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

// killLocked terminates the current child (if any) and waits for it to be reaped
// before returning, so the listen port is actually free for the next spawnLocked.
// Without the wait, a new opera-proxy could rebind 127.0.0.1:port while the old
// process still holds it and fail with "address already in use". Caller holds s.mu.
func (s *Supervisor) killLocked() {
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		if s.done != nil {
			// The monitor closes done right after cmd.Wait (before it re-takes s.mu),
			// so waiting here can't deadlock. Bounded, in case Wait hangs.
			select {
			case <-s.done:
			case <-time.After(2 * time.Second):
			}
		}
	}
	s.cmd = nil
	s.done = nil
	s.running = false
}

// spawnLocked starts opera-proxy and a monitor goroutine. Caller holds s.mu.
func (s *Supervisor) spawnLocked() error {
	epoch := s.epoch
	cmd := exec.Command(s.binPath,
		"-country", s.country,
		"-bind-address", fmt.Sprintf("127.0.0.1:%d", s.port),
	)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start opera-proxy: %w", err)
	}
	done := make(chan struct{})
	s.cmd = cmd
	s.done = done
	s.running = true
	go logLines(stdout)
	go logLines(stderr)
	go s.monitor(cmd, epoch, done)
	log.Printf("opera: started (pid %d, country %s, 127.0.0.1:%d)", cmd.Process.Pid, s.country, s.port)
	return nil
}

// monitor waits for the child to exit and relaunches it after a delay, unless
// it was superseded (newer epoch) or intentionally stopped.
func (s *Supervisor) monitor(cmd *exec.Cmd, epoch int, done chan struct{}) {
	err := cmd.Wait()
	close(done) // signal killLocked (before re-taking s.mu) that the port is freed
	s.mu.Lock()
	if epoch != s.epoch {
		s.mu.Unlock()
		return // a newer Start/Stop already took over (it owns s.running)
	}
	s.running = false // exited; stays down through the restart delay
	s.mu.Unlock()
	log.Printf("opera: exited (%v) — restarting in %s", err, restartDelay)
	time.Sleep(restartDelay)
	s.mu.Lock()
	defer s.mu.Unlock()
	if epoch != s.epoch || !s.active {
		return
	}
	if err := s.spawnLocked(); err != nil {
		log.Printf("opera: restart failed: %v", err)
	}
}

// WaitReady blocks until the local proxy port accepts connections or timeout
// elapses, so callers can confirm opera-proxy came up before relying on it.
func (s *Supervisor) WaitReady(port int, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			c.Close()
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("opera-proxy did not become ready on %s within %s", addr, timeout)
}

// logLines forwards a child output stream to the standard logger, prefixed.
func logLines(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 16*1024), 64*1024)
	for sc.Scan() {
		log.Printf("opera: %s", sc.Text())
	}
}
