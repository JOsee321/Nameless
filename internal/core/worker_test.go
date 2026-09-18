package core_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"nameless/internal/core"
)

func TestPoolRunsAllJobs(t *testing.T) {
	ctx := context.Background()
	p := core.NewPool(ctx, 4)

	var count atomic.Int64
	for i := 0; i < 100; i++ {
		if err := p.Submit(ctx, func(ctx context.Context) {
			count.Add(1)
		}); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	p.Close()

	if got := count.Load(); got != 100 {
		t.Errorf("jobs executed: got %d, want 100", got)
	}
}

func TestPoolContextCancellationStopsWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	var started atomic.Int64
	p := core.NewPool(ctx, 2)

	// Submit a long-running job then cancel.
	_ = p.Submit(ctx, func(ctx context.Context) {
		started.Add(1)
		<-ctx.Done() // blocks until cancelled
	})

	// Give the job time to start.
	time.Sleep(20 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		p.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not shut down after context cancellation")
	}
}

func TestPoolCloseIsIdempotent(t *testing.T) {
	ctx := context.Background()
	p := core.NewPool(ctx, 2)
	p.Close()
	p.Close() // must not panic
}
