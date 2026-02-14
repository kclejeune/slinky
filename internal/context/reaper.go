package context

import (
	"context"
	"log/slog"
	"time"

	"golang.org/x/sys/unix"
)

type Reaper struct {
	mgr      *Manager
	interval time.Duration
	isAlive  func(int) bool
}

func NewReaper(mgr *Manager) *Reaper {
	return &Reaper{
		mgr:      mgr,
		interval: 30 * time.Second,
		isAlive:  processAlive,
	}
}

// Run blocks until ctx is cancelled, sweeping at the configured interval.
func (r *Reaper) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.sweep()
		}
	}
}

func (r *Reaper) sweep() {
	pids := r.mgr.TrackedPIDs()
	for _, pid := range pids {
		if !r.isAlive(pid) {
			dirs := r.mgr.RemoveSession(pid)
			if len(dirs) > 0 {
				slog.Info("reaped dead session", "pid", pid, "deactivated", dirs)
			}
		}
	}
}

func processAlive(pid int) bool {
	return unix.Kill(pid, 0) == nil
}
