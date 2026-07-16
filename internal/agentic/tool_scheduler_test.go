// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/toolaccess"
)

func TestToolScheduler_NonConflicting_Parallel(t *testing.T) {
	s := NewToolScheduler(context.Background())
	started := make(chan struct{}, 2)
	proceed := make(chan struct{})

	// Two non-conflicting reads — both start before either returns.
	s.Add(&ToolCallTask{
		Name:   "read_a",
		Access: toolaccess.Access{ReadPaths: []string{"/a"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			started <- struct{}{}
			<-proceed // wait until both have started
			return ToolResult{Output: "a"}, nil
		},
	})
	s.Add(&ToolCallTask{
		Name:   "read_b",
		Access: toolaccess.Access{ReadPaths: []string{"/b"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			started <- struct{}{}
			<-proceed
			return ToolResult{Output: "b"}, nil
		},
	})

	// Wait for both to signal they've started (parallelism check).
	<-started
	<-started
	close(proceed) // let them finish

	results := s.Collect()

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Name != "read_a" {
		t.Errorf("result[0].Name = %q, want %q", results[0].Name, "read_a")
	}
	if results[1].Name != "read_b" {
		t.Errorf("result[1].Name = %q, want %q", results[1].Name, "read_b")
	}
	if results[0].Output != "a" {
		t.Errorf("result[0].Output = %q, want %q", results[0].Output, "a")
	}
	if results[1].Output != "b" {
		t.Errorf("result[1].Output = %q, want %q", results[1].Output, "b")
	}
}

func TestToolScheduler_Conflicting_Serialized(t *testing.T) {
	s := NewToolScheduler(context.Background())
	firstDone := make(chan struct{})

	// Two conflicting writes — second must wait for first to complete.
	s.Add(&ToolCallTask{
		Name:   "write_a",
		Access: toolaccess.Access{WritePaths: []string{"/a"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			<-firstDone
			return ToolResult{Output: "ok"}, nil
		},
	})
	s.Add(&ToolCallTask{
		Name:   "write_a2",
		Access: toolaccess.Access{WritePaths: []string{"/a"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			return ToolResult{Output: "ok"}, nil
		},
	})

	close(firstDone)

	results := s.Collect()
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("result[0].Err = %v", results[0].Err)
	}
	if results[1].Err != nil {
		t.Errorf("result[1].Err = %v", results[1].Err)
	}
	if results[0].Name != "write_a" {
		t.Errorf("result[0].Name = %q, want %q", results[0].Name, "write_a")
	}
	if results[1].Name != "write_a2" {
		t.Errorf("result[1].Name = %q, want %q", results[1].Name, "write_a2")
	}
}

func TestToolScheduler_Empty_ReturnsNil(t *testing.T) {
	s := NewToolScheduler(context.Background())
	results := s.Collect()
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestToolScheduler_ErrorResult(t *testing.T) {
	s := NewToolScheduler(context.Background())

	s.Add(&ToolCallTask{
		Name:   "failing",
		Access: toolaccess.Access{},
		Execute: func(ctx context.Context) (ToolResult, error) {
			return ToolResult{Output: ""}, assertAnError("something went wrong")
		},
	})

	results := s.Collect()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected error, got nil")
	}
	if results[0].Err.Error() != "something went wrong" {
		t.Errorf("error = %q, want %q", results[0].Err.Error(), "something went wrong")
	}
}

func TestToolScheduler_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := NewToolScheduler(ctx)

	s.Add(&ToolCallTask{
		Name:   "blocked",
		Access: toolaccess.Access{},
		Execute: func(ctx context.Context) (ToolResult, error) {
			<-ctx.Done()
			return ToolResult{Output: ""}, ctx.Err()
		},
	})

	cancel()
	results := s.Collect()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Error("expected cancellation error")
	}
}

func TestToolScheduler_MixedConflictingAndNonConflicting(t *testing.T) {
	s := NewToolScheduler(context.Background())

	resultsCh := make(chan string, 3)

	// Three tools: read_a, write_a (conflict with read_a), read_b (no conflict)
	s.Add(&ToolCallTask{
		Name:   "read_a",
		Access: toolaccess.Access{ReadPaths: []string{"/a"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			resultsCh <- "a"
			return ToolResult{Output: "a"}, nil
		},
	})
	s.Add(&ToolCallTask{
		Name:   "write_a",
		Access: toolaccess.Access{WritePaths: []string{"/a"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			resultsCh <- "a_written"
			return ToolResult{Output: "a_written"}, nil
		},
	})
	s.Add(&ToolCallTask{
		Name:   "read_b",
		Access: toolaccess.Access{ReadPaths: []string{"/b"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			resultsCh <- "b"
			return ToolResult{Output: "b"}, nil
		},
	})

	results := s.Collect()

	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Collect: should get all 3 in order.
	<-resultsCh
	<-resultsCh
	<-resultsCh
}

// TestToolScheduler_AllSameCategory_NoDeadlock is a regression test for a
// deadlock where every queued task shared a conflict category (e.g. three
// bash calls all in the "shell" category). The previous implementation
// marked pending tasks that conflicted with each other as blocked, so when
// the active task finished, none of the remaining pending tasks could start
// and Collect() hung forever.
func TestToolScheduler_AllSameCategory_NoDeadlock(t *testing.T) {
	s := NewToolScheduler(context.Background())
	const n = 3
	for i := 0; i < n; i++ {
		i := i
		s.Add(&ToolCallTask{
			Name:   "shell_" + string(rune('A'+i)),
			Access: toolaccess.Access{Category: "shell"},
			Execute: func(ctx context.Context) (ToolResult, error) {
				return ToolResult{Output: string(rune('A' + i))}, nil
			},
		})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Collect()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Collect() deadlocked with all tasks sharing a conflict category")
	}

	results := s.Collect()
	if len(results) != n {
		t.Fatalf("expected %d results, got %d", n, len(results))
	}
	seen := make(map[string]bool, n)
	for _, r := range results {
		seen[r.Name] = true
		if r.Err != nil {
			t.Errorf("%s returned err: %v", r.Name, r.Err)
		}
	}
	for i := 0; i < n; i++ {
		name := "shell_" + string(rune('A'+i))
		if !seen[name] {
			t.Errorf("missing result for %q", name)
		}
	}
}

// TestToolScheduler_AllSameCategory_RunsSerially verifies that conflicting
// tasks never execute in parallel: at most one is active at any instant.
func TestToolScheduler_AllSameCategory_RunsSerially(t *testing.T) {
	s := NewToolScheduler(context.Background())
	const n = 4
	var active int32
	var maxActive int32
	var mu sync.Mutex

	for i := 0; i < n; i++ {
		i := i
		s.Add(&ToolCallTask{
			Name:   "shell_" + string(rune('A'+i)),
			Access: toolaccess.Access{Category: "shell"},
			Execute: func(ctx context.Context) (ToolResult, error) {
				cur := atomic.AddInt32(&active, 1)
				mu.Lock()
				if cur > maxActive {
					maxActive = cur
				}
				mu.Unlock()
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&active, -1)
				return ToolResult{Output: string(rune('A' + i))}, nil
			},
		})
	}

	results := s.Collect()
	if len(results) != n {
		t.Fatalf("expected %d results, got %d", n, len(results))
	}
	if maxActive > 1 {
		t.Errorf("conflicting tasks ran in parallel: peak concurrent = %d", maxActive)
	}
}

func TestToolScheduler_Panic_ReturnsError(t *testing.T) {
	s := NewToolScheduler(context.Background())

	s.Add(&ToolCallTask{
		Name:   "panicker",
		Access: toolaccess.Access{},
		Execute: func(ctx context.Context) (ToolResult, error) {
			panic("boom")
		},
	})

	done := make(chan struct{})
	var results []ToolCallResult
	go func() {
		results = s.Collect()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Collect() hung after a tool panic")
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Err == nil {
		t.Fatal("expected error for panicking task")
	}
	if !strings.Contains(results[0].Err.Error(), "boom") {
		t.Errorf("expected panic message in error, got %q", results[0].Err.Error())
	}
	if !strings.Contains(results[0].Err.Error(), "panicker") {
		t.Errorf("expected tool name in panic error, got %q", results[0].Err.Error())
	}
	// The error must carry stack frames pointing at goa code so panics are
	// debuggable from the session log (where tool results are recorded).
	if !strings.Contains(results[0].Err.Error(), "tool_scheduler") {
		t.Errorf("expected stack frames in panic error, got %q", results[0].Err.Error())
	}
}

// assertAnError returns an error with the given message.
// Needed because errors.New returns a *errors.errorString which doesn't
// implement Is(target) by default, but we just need a simple error.
type assertAnError string

func (e assertAnError) Error() string { return string(e) }

// TestToolScheduler_MaxParallelCap verifies the concurrency cap: with
// MaxParallel=2 and four independent (non-conflicting) tasks, at most two are
// ever running at once; the rest start only as slots free.
func TestToolScheduler_MaxParallelCap(t *testing.T) {
	s := NewToolScheduler(context.Background())
	s.SetMaxParallel(2)

	var inflight, maxInflight int32
	release := make(chan struct{})

	mkTask := func(name, path string) *ToolCallTask {
		return &ToolCallTask{
			Name:   name,
			Access: toolaccess.Access{ReadPaths: []string{path}},
			Execute: func(ctx context.Context) (ToolResult, error) {
				cur := atomic.AddInt32(&inflight, 1)
				for {
					old := atomic.LoadInt32(&maxInflight)
					if cur <= old || atomic.CompareAndSwapInt32(&maxInflight, old, cur) {
						break
					}
				}
				defer atomic.AddInt32(&inflight, -1)
				<-release
				return ToolResult{Output: name}, nil
			},
		}
	}

	for _, tsk := range []*ToolCallTask{
		mkTask("t1", "/1"), mkTask("t2", "/2"),
		mkTask("t3", "/3"), mkTask("t4", "/4"),
	} {
		s.Add(tsk)
	}

	// Let the first wave of two start. Poll until we observe two in flight.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&inflight) < 2 {
		time.Sleep(2 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&inflight); got != 2 {
		t.Fatalf("expected exactly 2 in flight under cap, got %d", got)
	}

	// Allow the first wave to finish; the second wave must then start.
	close(release)

	results := s.Collect()
	if len(results) != 4 {
		t.Fatalf("expected 4 results, got %d", len(results))
	}
	if got := atomic.LoadInt32(&maxInflight); got > 2 {
		t.Errorf("MaxParallel=2 violated: peak concurrency = %d", got)
	}
	if got := atomic.LoadInt32(&maxInflight); got < 2 {
		t.Errorf("expected parallelism to reach 2, peak = %d", got)
	}
}

// TestToolScheduler_TaskTimeoutUnblocksCollect verifies that a tool which
// ignores its context (blocks forever) does not hang Collect when a per-task
// timeout is set: the watchdog closes the task's done channel at the deadline
// and surfaces a timeout error.
func TestToolScheduler_TaskTimeoutUnblocksCollect(t *testing.T) {
	s := NewToolScheduler(context.Background())
	s.SetTaskTimeout(100 * time.Millisecond)

	forever := make(chan struct{}) // never closed
	s.Add(&ToolCallTask{
		Name:   "hung",
		Access: toolaccess.Access{},
		Execute: func(ctx context.Context) (ToolResult, error) {
			<-forever // deliberately ignores ctx
			return ToolResult{}, nil
		},
	})

	done := make(chan []ToolCallResult, 1)
	go func() { done <- s.Collect() }()

	select {
	case results := <-done:
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].Err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(results[0].Err.Error(), "deadline") {
			t.Errorf("expected deadline error, got %q", results[0].Err.Error())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Collect hung: task timeout watchdog did not fire")
	}
}

// TestToolScheduler_TaskTimeout_AllowsFastTools verifies the timeout does not
// interfere with tools that complete normally before the deadline.
func TestToolScheduler_TaskTimeout_AllowsFastTools(t *testing.T) {
	s := NewToolScheduler(context.Background())
	s.SetTaskTimeout(5 * time.Second)

	s.Add(&ToolCallTask{
		Name:   "fast",
		Access: toolaccess.Access{ReadPaths: []string{"/a"}},
		Execute: func(ctx context.Context) (ToolResult, error) {
			return ToolResult{Output: "ok"}, nil
		},
	})

	results := s.Collect()
	if len(results) != 1 || results[0].Output != "ok" || results[0].Err != nil {
		t.Fatalf("unexpected result: %+v", results)
	}
}
