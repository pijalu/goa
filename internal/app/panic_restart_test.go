// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRunWithPanicRestart_SurvivesPanic verifies a panic is recovered and
// process runs again from the top, so a transient render panic does not kill
// the event loop.
func TestRunWithPanicRestart_SurvivesPanic(t *testing.T) {
	var calls int32
	var panics int32
	runWithPanicRestart(5,
		func(r any, stack []byte) { atomic.AddInt32(&panics, 1) },
		func() { t.Fatal("should not exhaust") },
		func() {
			n := atomic.AddInt32(&calls, 1)
			if n == 1 {
				panic("boom")
			}
			// second invocation returns cleanly
		})
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls), "process ran twice")
	assert.Equal(t, int32(1), atomic.LoadInt32(&panics), "one panic recovered")
}

// TestRunWithPanicRestart_StopsAfterCap verifies the circuit breaker: after
// maxRestarts consecutive panics the loop stops and onExhausted fires, instead
// of spinning forever.
func TestRunWithPanicRestart_StopsAfterCap(t *testing.T) {
	const max = 3
	var panics, exhausted int32
	runWithPanicRestart(max,
		func(r any, stack []byte) { atomic.AddInt32(&panics, 1) },
		func() { atomic.AddInt32(&exhausted, 1) },
		func() { panic("always") })
	// The loop allows up to max restarts after panics, so exactly max panic
	// recoveries occur before onExhausted stops it.
	assert.Equal(t, int32(max), atomic.LoadInt32(&panics),
		"expected %d panic recoveries", max)
	assert.Equal(t, int32(1), atomic.LoadInt32(&exhausted), "exhausted callback fired once")
}

// TestRunWithPanicRestart_CleanExit verifies that when process returns without
// panicking, the loop stops immediately (no restart, no exhaustion).
func TestRunWithPanicRestart_CleanExit(t *testing.T) {
	var panics, exhausted int32
	runWithPanicRestart(5,
		func(r any, stack []byte) { atomic.AddInt32(&panics, 1) },
		func() { atomic.AddInt32(&exhausted, 1) },
		func() { /* clean return */ })
	assert.Equal(t, int32(0), atomic.LoadInt32(&panics))
	assert.Equal(t, int32(0), atomic.LoadInt32(&exhausted))
}
