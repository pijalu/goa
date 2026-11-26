package agentic

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type idleTestClock struct {
	mu       sync.Mutex
	timer    *time.Timer
	deadline time.Time
	fired    chan time.Time
}

func newIdleTestClock() *idleTestClock {
	return &idleTestClock{fired: make(chan time.Time, 16)}
}

func (c *idleTestClock) AfterFunc(delay time.Duration, fn func()) *time.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deadline = time.Now().Add(delay)
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(delay, func() {
		c.fired <- time.Now()
		fn()
	})
	return c.timer
}

func (c *idleTestClock) activeDelay(t *testing.T) time.Duration {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer == nil || c.deadline.IsZero() {
		t.Fatal("idle summarize timer was not armed")
	}
	return time.Until(c.deadline)
}

func (c *idleTestClock) fire() {
	c.mu.Lock()
	timer := c.timer
	c.mu.Unlock()
	if timer == nil {
		return
	}
	timer.Reset(0)
}

func TestIdleSummarizeAfterFuncUsesConfiguredDelay(t *testing.T) {
	clock := newIdleTestClock()
	scheduler := newIdleSummarizeSchedulerWithAfterFunc(func(context.Context) error { return nil }, clock.AfterFunc)
	scheduler.Configure(IdleSummarizeConfig{TriggerPercent: 10, Delay: 240 * time.Second})
	scheduler.Started()
	scheduler.Stopped()

	delay := clock.activeDelay(t)
	if delay > 240*time.Second || delay < 239*time.Second {
		t.Fatalf("timer delay = %v, want configured 240 seconds", delay)
	}
	scheduler.Close()
}

func TestIdleSummarizeStartInvalidatesAndStopsPendingTimer(t *testing.T) {
	clock := newIdleTestClock()
	scheduler := newIdleSummarizeSchedulerWithAfterFunc(func(context.Context) error { return nil }, clock.AfterFunc)
	scheduler.Configure(IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	scheduler.Started()
	scheduler.Stopped()
	_ = clock.activeDelay(t)
	scheduler.Started()

	clock.mu.Lock()
	active := clock.timer.Stop()
	clock.mu.Unlock()
	if active {
		t.Fatal("new exchange left the previous idle timer active")
	}
	scheduler.Stopped()
	_ = clock.activeDelay(t)
	scheduler.Close()
}

func TestIdleSummarizeZeroDelayDisablesTimer(t *testing.T) {
	clock := newIdleTestClock()
	scheduler := newIdleSummarizeSchedulerWithAfterFunc(func(context.Context) error { return nil }, clock.AfterFunc)
	scheduler.Configure(IdleSummarizeConfig{TriggerPercent: 10, Delay: 0})
	scheduler.Started()
	scheduler.Stopped()

	clock.mu.Lock()
	timer := clock.timer
	clock.mu.Unlock()
	if timer != nil {
		t.Fatal("zero delay armed an idle summarize timer")
	}
	scheduler.Close()
}

func TestIdleSummarizeRunReportsNonCancellationFailure(t *testing.T) {
	clock := newIdleTestClock()
	failure := errors.New("summarize provider failed")
	runs := make(chan struct{}, 1)
	scheduler := newIdleSummarizeSchedulerWithAfterFunc(func(context.Context) error { return nil }, clock.AfterFunc)
	scheduler.Configure(IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	scheduler.Started()
	scheduler.Stopped()

	result := scheduler.run(context.Background(), func(context.Context) error {
		close(runs)
		return failure
	})
	if !errors.Is(result.err, failure) {
		t.Fatalf("run error = %v, want provider failure", result.err)
	}
	if result.cancelled || result.duplicate || !result.attempted {
		t.Fatalf("run result = %+v, want one completed failed attempt", result)
	}
	select {
	case <-runs:
	default:
		t.Fatal("scheduled compaction was not attempted")
	}
	scheduler.Close()
}

func TestIdleSummarizeStaleAndDuplicateRunsAreNoops(t *testing.T) {
	clock := newIdleTestClock()
	calls := 0
	scheduler := newIdleSummarizeSchedulerWithAfterFunc(func(context.Context) error {
		calls++
		return nil
	}, clock.AfterFunc)
	scheduler.Configure(IdleSummarizeConfig{TriggerPercent: 10, Delay: time.Hour})
	scheduler.Started()
	scheduler.Stopped()
	generation := scheduler.generation
	scheduler.generation++
	if result := scheduler.runAtGeneration(context.Background(), generation); result.attempted || calls != 0 {
		t.Fatalf("stale run = %+v, calls = %d; want no-op", result, calls)
	}
	result := scheduler.runAtGeneration(context.Background(), scheduler.generation)
	if !result.attempted || result.duplicate || calls != 1 {
		t.Fatalf("first current run = %+v, calls = %d", result, calls)
	}
	result = scheduler.runAtGeneration(context.Background(), scheduler.generation)
	if result.attempted || !result.duplicate || calls != 1 {
		t.Fatalf("duplicate run = %+v, calls = %d; want one attempt per stop", result, calls)
	}
	scheduler.Close()
}
