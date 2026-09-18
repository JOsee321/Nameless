package core

import (
	"context"
	"sync"
)

// Job is a unit of work submitted to the Pool.
// It receives the context that was active when the pool was started.
type Job func(ctx context.Context)

// Pool is a fixed-size goroutine worker pool backed by a buffered channel.
// All modules submit jobs to the same pool so the total number of concurrent
// outbound requests is bounded globally, not per-module.
type Pool struct {
	jobs    chan Job
	wg      sync.WaitGroup
	once    sync.Once
	closeCh chan struct{}
}

// NewPool creates a Pool with the given number of worker goroutines.
// Workers are started immediately; call Pool.Wait to drain them.
func NewPool(ctx context.Context, workers int) *Pool {
	if workers < 1 {
		workers = 1
	}
	p := &Pool{
		// Buffer the channel at workers*2 so producers rarely block waiting
		// for a free slot when workers are briefly busy.
		jobs:    make(chan Job, workers*2),
		closeCh: make(chan struct{}),
	}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.run(ctx)
	}
	return p
}

// run is the hot loop executed by each worker goroutine.
func (p *Pool) run(ctx context.Context) {
	defer p.wg.Done()
	for {
		select {
		case job, ok := <-p.jobs:
			if !ok {
				return
			}
			job(ctx)
		case <-ctx.Done():
			return
		}
	}
}

// Submit enqueues a job for execution.
// It blocks if all workers are busy and the internal channel buffer is full.
// Returns immediately with ctx.Err() if the context is already cancelled.
func (p *Pool) Submit(ctx context.Context, job Job) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case p.jobs <- job:
		return nil
	}
}

// Close signals workers to stop accepting new jobs once the queue is drained,
// then waits for all in-flight jobs to finish.
// Close is idempotent — calling it multiple times is safe.
func (p *Pool) Close() {
	p.once.Do(func() {
		close(p.jobs)
	})
	p.wg.Wait()
}
