package jobs

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type manualTicker struct {
	ticks chan time.Time
}

func (t *manualTicker) Chan() <-chan time.Time {
	return t.ticks
}

func (t *manualTicker) Stop() {}

type jobFunc func(context.Context) error

func (f jobFunc) Run(ctx context.Context) error {
	return f(ctx)
}

func TestRunnerRunsOnEachTickAndRetriesFailures(t *testing.T) {
	ticker := &manualTicker{ticks: make(chan time.Time, 2)}
	var calls atomic.Int32
	first := make(chan struct{})
	second := make(chan struct{})
	runner := Runner{Job: jobFunc(func(context.Context) error {
		n := calls.Add(1)
		if n == 1 {
			close(first)
			return errors.New("temporary")
		}
		close(second)
		return nil
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runner.run(ctx, ticker, time.Second)
	}()
	ticker.ticks <- time.Now()
	<-first
	ticker.ticks <- time.Now()
	<-second
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("calls = %d, want 2", got)
	}
}

func TestRunnerCancellationWaitsForSlowJobWithoutOverlap(t *testing.T) {
	ticker := &manualTicker{ticks: make(chan time.Time, 2)}
	started := make(chan struct{})
	var running atomic.Int32
	var maxRunning atomic.Int32
	var once sync.Once
	runner := Runner{Job: jobFunc(func(ctx context.Context) error {
		n := running.Add(1)
		for {
			old := maxRunning.Load()
			if n <= old || maxRunning.CompareAndSwap(old, n) {
				break
			}
		}
		once.Do(func() {
			close(started)
		})
		<-ctx.Done()
		running.Add(-1)
		return ctx.Err()
	})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runner.run(ctx, ticker, time.Hour)
	}()
	ticker.ticks <- time.Now()
	<-started
	ticker.ticks <- time.Now()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := maxRunning.Load(); got != 1 {
		t.Fatalf("max concurrent runs = %d, want 1", got)
	}
}
