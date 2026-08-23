// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "strings"

// escByte is the ESC control character that begins every escape sequence.
const escByte byte = 0x1b

// SGRCoalescer is an emit-time filter that removes redundant SGR escape runs
// from a terminal byte stream without changing what the terminal renders.
//
// It sits on the Compositor's row-emission path: every assembled frame buffer
// passes through Filter immediately before the terminal write. Styled rows
// bake "prefix + text + reset" per piece, so composed output repeats
// identical SGR runs and carries reset+re-open pairs wherever two
// same-styled pieces touch. The coalescer tracks the attributes the terminal
// is presumed to hold and elides:
//
//   - consecutive duplicate escape runs (a maximal SGR run whose application
//     leaves the tracked state unchanged), and
//   - a reset immediately re-opening what was already active ("\x1b[0m" +
//     identical prefix collapses to nothing),
//
// and rewrites a run to a shorter canonical form when — and only when — that
// form provably reaches the run's target state from the state the terminal is
// known to hold. A canonical run only ADDS attributes; it clears nothing, so
// it may not replace a run whose target drops an attribute the terminal
// currently has (e.g. Reset + fgWhite while faint is active: re-emitting
// fgWhite alone would leave faint on, dimming the "white" text). For those
// runs the original bytes pass through, or Reset + canonical is used when
// shorter.
//
// Every non-SGR sequence (cursor positioning, erases, scroll region, sync
// markers, OSC hyperlinks) passes through verbatim. Tracker state persists
// across Filter calls, mirroring the contiguous wire stream, so an
// unterminated style at one frame's tail can suppress the matching prefix at
// the next frame's head.
//
// Safety model: elisions are only made while the tracker is "trusted". An
// SGR parameter the tracker does not model (basic palette 30-37/90-97,
// colon sub-parameters, …) poisons the trust — from that point every run is
// passed through verbatim until the next hard reset re-synchronizes the
// tracker to a fully known state. A poisoned stream is therefore never
// corrupted; it merely loses compression.
type SGRCoalescer struct {
	st       AnsiState // tracked terminal attribute state
	trusted  bool      // false after an unmodeled parameter: no elisions
	poisoned bool      // persistent divergence: set by an unmodeled parameter
	out      strings.Builder
}

// NewSGRCoalescer returns a coalescer assuming the terminal is at default
// attributes (the state TUI startup establishes).
func NewSGRCoalescer() *SGRCoalescer {
	return &SGRCoalescer{trusted: true}
}

// Reset re-synchronizes the tracker after an out-of-band write the coalescer
// did not see (e.g. Compositor.Restore's shutdown "\x1b[0m"): the terminal is
// back at default attributes.
func (sc *SGRCoalescer) Reset() {
	sc.st = AnsiState{}
	sc.trusted = true
	sc.poisoned = false
}

// Filter returns frame with redundant SGR runs removed. The input must be a
// contiguous slice of the actual wire stream (a whole frame buffer); escapes
// sequences are expected to be complete within it, which the compositor's
// row emitters guarantee.
func (sc *SGRCoalescer) Filter(frame string) string {
	sc.out.Reset()
	buf := []byte(frame)
	copied := 0            // bytes of buf already reflected in out
	runLo, runHi := -1, -1 // pending maximal SGR run span
	flushRun := func() {
		if runLo < 0 {
			return
		}
		sc.out.Write(buf[copied:runLo]) // literal bytes before the run
		sc.out.WriteString(sc.decide(string(buf[runLo:runHi])))
		copied = runHi
		runLo, runHi = -1, -1
	}
	for i := 0; i < len(buf); {
		if buf[i] != escByte {
			// Any literal byte bounds a pending SGR run: the run's decision
			// must land before the text it precedes.
			flushRun()
			i++
			continue
		}
		j := skipANSISequence(buf, i)
		if j <= i { // pathological input: pass the byte through singly
			i++
			continue
		}
		if isSGRSequence(buf[i:j]) {
			if runLo < 0 {
				runLo = i
			}
			runHi = j
		} else {
			// Non-SGR sequence (CUP, EL, DECSTBM, OSC, sync markers…): it
			// never changes SGR state but bounds a run — decide the pending
			// run before copying it through.
			flushRun()
		}
		i = j
	}
	flushRun()
	sc.out.Write(buf[copied:])
	return sc.out.String()
}

// decide applies one maximal SGR run (concatenated "\x1b[...m" sequences,
// never containing literal text) to the tracked state and returns the bytes
// to emit for it.
//
// Decision table (elisions only while trusted, unpoisoned and fully modeled):
//
//	target == current → ""                        (rules a + b)
//	otherwise        → shortest of the candidates that provably reach
//	                  target from current:
//	                  - canonical run   (only when valid, see below)
//	                  - Reset + canonical run (valid from any state)
//	                  - the run verbatim
//
// The bare canonical run re-states target's attributes from scratch but
// clears nothing: it is valid only when current holds no attribute that
// target lacks (apply(canonical, current) == target). Otherwise it would
// silently preserve stale attributes and corrupt rendering (streaming color
// bleed: a dropped faint/dim making following "white" text render gray).
func (sc *SGRCoalescer) decide(group string) string {
	st := sc.st
	sawReset, grpPoison := walkSGRRun(group, &st)

	out := group
	if sc.trusted && !sc.poisoned && !grpPoison {
		switch {
		case st.EqualSGR(&sc.st):
			out = ""
		default:
			out = shortestEquivalentRun(group, &sc.st, &st)
		}
	}
	sc.st = st
	switch {
	case sawReset && !grpPoison:
		// A clean hard reset re-establishes full knowledge regardless of
		// prior poisoning.
		sc.trusted, sc.poisoned = true, false
	case grpPoison:
		sc.trusted, sc.poisoned = false, true
	}
	return out
}

// shortestEquivalentRun returns the shortest emission for group's state
// transition current → target among the provably-equivalent candidates:
//
//   - canonical: re-states every target attribute. Valid only when current
//     holds no attribute target lacks (reachesTarget), since a canonical run
//     adds attributes but never clears them.
//   - resetCanonical: hard reset then re-open. Valid from ANY state, so it is
//     always a legal fallback when it is shorter than the original bytes.
//   - group verbatim: always correct, never shorter than itself.
//
// Ties prefer canonical (it avoids a hard reset) then the original run.
func shortestEquivalentRun(group string, current, target *AnsiState) string {
	best := group

	canon := target.sgrSequence()
	if reachesTarget(canon, current, target) && len(canon) < len(best) {
		best = canon
	}
	resetCanon := Reset + canon
	if canon != Reset && len(resetCanon) < len(best) {
		best = resetCanon
	}
	return best
}

// reachesTarget reports whether emitting seq starting from current leaves the
// terminal in target's state. seq is a canonical run built by sgrSequence, so
// it only contains fully-modeled tokens and the walk is exact.
func reachesTarget(seq string, current, target *AnsiState) bool {
	sim := *current
	walkSGRRun(seq, &sim)
	return sim.EqualSGR(target)
}

// walkSGRRun applies every SGR parameter of one maximal escape run to st,
// reporting whether a hard reset occurred and whether any unmodeled
// parameter was seen after the last hard reset.
func walkSGRRun(group string, st *AnsiState) (sawReset, poison bool) {
	gbuf := []byte(group)
	for i := 0; i < len(gbuf); {
		j := skipANSISequence(gbuf, i)
		if j <= i || j > len(gbuf) {
			break
		}
		toks := strings.Split(string(gbuf[i+2:j-1]), ";")
		for t := 0; t < len(toks); t++ {
			hard, unknown := applySGRToken(st, toks, &t)
			if hard {
				sawReset, poison = true, false
			}
			if unknown {
				poison = true
			}
		}
		i = j
	}
	return sawReset, poison
}

// applySGRToken applies token toks[*t] to st, consuming color sub-parameter
// tokens by advancing *t. It reports whether the token was a hard reset and
// whether it was unrecognized by the state model.
func applySGRToken(st *AnsiState, toks []string, t *int) (hardReset, unknown bool) {
	switch tok := toks[*t]; tok {
	case "":
		// ECMA-48: an empty parameter defaults to 0 (hard reset).
		*st = AnsiState{}
		return true, false
	case "38":
		st.fgColor = consumeColor(toks, t)
		return false, st.fgColor == ""
	case "48":
		st.bgColor = consumeColor(toks, t)
		return false, st.bgColor == ""
	case "58":
		// ISO 8613-6 underline color. Modeled like fg/bg so runs carrying it
		// stay fully tracked (no poison) and can still be elided/normalized.
		st.ulColor = consumeColor(toks, t)
		return false, st.ulColor == ""
	}
	if fn, known := sgrHandlers[toks[*t]]; known {
		fn(st)
		return toks[*t] == "0", false
	}
	// Unmodeled attribute (basic palette, colon forms, …): the real state
	// now diverges from the tracked one — persistently until a hard reset.
	return false, true
}

// isSGRSequence reports whether seq is an SGR escape ("\x1b[...m") with no
// private-parameter marker. Sequences like "\x1b[>4m" or "\x1b[?25l" are
// other CSI families and must never be treated as attribute changes.
func isSGRSequence(seq []byte) bool {
	if len(seq) < 3 || seq[0] != escByte || seq[1] != '[' || seq[len(seq)-1] != 'm' {
		return false
	}
	for _, c := range seq[2 : len(seq)-1] {
		if c == '<' || c == '=' || c == '>' || c == '?' {
			return false
		}
	}
	return true
}
