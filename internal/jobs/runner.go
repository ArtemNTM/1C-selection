package jobs

import (
	"context"
	"log/slog"
	"time"
)

type Job interface {
	Run(context.Context) error
}

type Runner struct {
	Job      Job
	Interval time.Duration
	Timeout  time.Duration
	Log      *slog.Logger
}

func (r Runner) Run(ctx context.Context) error {
	if r.Job == nil {
		return nil
	}
	interval := r.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return r.run(ctx, realTicker{time.NewTicker(interval)}, timeout)
}

type ticker interface {
	Chan() <-chan time.Time
	Stop()
}

type realTicker struct {
	*time.Ticker
}

func (t realTicker) Chan() <-chan time.Time {
	return t.C
}

func (r Runner) run(ctx context.Context, ticks ticker, timeout time.Duration) error {
	defer ticks.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks.Chan():
			runCtx, cancel := context.WithTimeout(ctx, timeout)
			err := r.Job.Run(runCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				logger := r.Log
				if logger == nil {
					logger = slog.Default()
				}
				logger.Error("background job failed", "error", err)
			}
		}
	}
}
