// SPDX-License-Identifier: GPL-3.0-or-later

package app

import "runtime/debug"

// readerMaxRestarts bounds how many times an event reader restarts after a
// render/chat panic before giving up, so a tight panic loop cannot spin the CPU
// forever.
const readerMaxRestarts = 10

// runWithPanicRestart runs process in a loop, restarting it in a fresh stack
// frame whenever it panics, up to maxRestarts consecutive times.
//
// It replaces the recursive self-restart anti-pattern (a deferred recover that
// re-invokes the reader), which grew the stack by one frame per panic and could
// never be unrolled. Here each restart is a plain loop iteration: no frame
// accumulation, and a bounded number of restarts prevents an infinite panic
// spin.
//
//   - process runs until it returns cleanly (e.g. done channel closed), at
//     which point the loop stops.
//   - A panic in process is recovered; onPanic is called with the value and a
//     stack trace, then process runs again from the top.
//   - After maxRestarts consecutive panics, onExhausted is called once and the
//     loop stops.
func runWithPanicRestart(maxRestarts int, onPanic func(r any, stack []byte), onExhausted func(), process func()) {
	restarts := 0
	for {
		panicked := true
		func() {
			defer func() {
				if r := recover(); r != nil {
					onPanic(r, debug.Stack())
				} else {
					panicked = false
				}
			}()
			process()
		}()
		if !panicked {
			return // process returned cleanly; stop the loop
		}
		restarts++
		if restarts >= maxRestarts {
			onExhausted()
			return
		}
	}
}
