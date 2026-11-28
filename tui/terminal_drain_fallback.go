// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"sync"
	"time"
)

// drainInputFallback polls stdin through the shared stdin poller until no
// data arrives for idleMs, maxMs elapses, or stdin reports EOF/an error.
// It is used on platforms where non-blocking terminal reads are unavailable
// (Windows) or when setting non-blocking mode fails.
func drainInputFallback(maxMs, idleMs int) {
	deadline := time.NewTimer(time.Duration(maxMs) * time.Millisecond)
	defer deadline.Stop()
	idle := time.NewTimer(time.Duration(idleMs) * time.Millisecond)
	defer idle.Stop()

	for {
		r, ok := sharedStdinPoller.read(idle.C, deadline.C)
		if !ok || r.err != nil || r.n == 0 {
			return
		}
		idle.Reset(time.Duration(idleMs) * time.Millisecond)
	}
}

// stdinReadResult carries one read from stdin.
type stdinReadResult struct {
	n   int
	err error
}

// sharedStdinPoller is the single process-wide stdin reader used by the
// fallback drain. A blocking Read on stdin cannot be cancelled once it
// starts, so spawning a reader goroutine per drain leaks it: the orphaned
// goroutine keeps consuming stdin after the drain ends and races a restarted
// session's readLoop for keys (bug W5). The shared poller is started once
// and performs a Read only while servicing a drain's request, so:
//
//   - at most one poller goroutine ever exists, however many drains run;
//   - once a drain ends, no further Read is issued on its behalf (the
//     poller parks on the requests channel);
//   - at most one Read can remain blocked when a drain expires mid-read,
//     bounding — rather than eliminating — the input it can consume.
var sharedStdinPoller = &stdinPoller{requests: make(chan chan stdinReadResult)}

// stdinPoller serializes blocking stdin reads for drain sessions.
type stdinPoller struct {
	once     sync.Once
	requests chan chan stdinReadResult
}

// read issues one read request and waits for the result. It returns
// (result, true) when the read completed, or (zero, false) when idle or
// deadline fired first (drain window ended). In the latter case the request
// may stay queued and its Read complete later; the buffered reply channel
// keeps the poller from blocking on the abandoned result.
func (p *stdinPoller) read(idle, deadline <-chan time.Time) (stdinReadResult, bool) {
	p.once.Do(func() { go p.run() })

	reply := make(chan stdinReadResult, 1)
	select {
	case p.requests <- reply:
	case <-idle:
		return stdinReadResult{}, false
	case <-deadline:
		return stdinReadResult{}, false
	}

	select {
	case r := <-reply:
		return r, true
	case <-idle:
		return stdinReadResult{}, false
	case <-deadline:
		return stdinReadResult{}, false
	}
}

// run services read requests one at a time. Between requests it parks on
// the requests channel, so it never reads stdin on its own initiative.
func (p *stdinPoller) run() {
	buf := make([]byte, 256)
	for reply := range p.requests {
		n, err := stdinRead(buf)
		reply <- stdinReadResult{n, err}
	}
}

var stdinRead = defaultStdinRead

func defaultStdinRead(buf []byte) (int, error) {
	return stdinFile.Read(buf)
}

// stdinFile is the standard input file handle.
var stdinFile = os.Stdin
