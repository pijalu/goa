// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

// SGRStateAt replays the escape sequences inside row[:cut] through the SGR
// state tracker and returns the canonical sequence that re-establishes, from
// a default pen, the attribute state active at the cut offset.
//
// It exists for column-range row emission: when a compositor rewrites only
// the changed cells of a row, the terminal pen at the start of that write is
// whatever the previous frame left (default, because canvas rows are
// style-closed). A cut landing mid-styled-run must therefore carry the row's
// own active state as a prefix, or the repainted cells render with default
// colors while their untouched neighbours keep theirs (the streaming color
// bleed regression).
//
// ok is false when the prefix holds anything the tracker cannot model
// exactly — a non-SGR escape (cursor/erase/OSC/SS3), an SGR with colon
// sub-parameters (unmodelled syntax), a lone or malformed ESC, or a sequence
// the cut would split. Callers must then fall back to a full-row rewrite:
// a guessed state is worse than none.
//
// A default-state prefix yields ("", true): nothing needs restoring.
func SGRStateAt(row string, cut int) (string, bool) {
	if cut < 0 || cut > len(row) {
		return "", false
	}
	if cut == 0 {
		return "", true
	}
	var st AnsiState
	for i := 0; i < cut; {
		if row[i] != escByte {
			i++
			continue
		}
		seq, next, ok := sgrSequenceAt(row, i, cut)
		if !ok {
			return "", false
		}
		st.Process(seq)
		i = next
	}
	if st.isDefaultSGR() {
		return "", true
	}
	return st.sgrSequence(), true
}

// sgrSequenceAt reports the complete SGR sequence starting at i and the index
// just past it, provided the sequence lies entirely within cut. ok is false
// for every other escape form: the caller cannot know what a non-SGR
// sequence did to the terminal, so the replay must be abandoned rather than
// approximated. The scan mirrors skipANSISequence/skipCSI's extent rules.
func sgrSequenceAt(row string, i, cut int) (seq string, next int, ok bool) {
	if i+1 >= len(row) || row[i+1] != '[' {
		return "", 0, false // lone ESC, OSC, SS3, or two-byte escape: unmodellable
	}
	next = i + 2
	for next < len(row) && row[next] < 0x40 {
		if row[next] == ':' {
			return "", 0, false // colon sub-parameters: AnsiState splits on ';' only
		}
		next++
	}
	if next >= len(row) || row[next] != 'm' || next >= cut {
		return "", 0, false // not an SGR final byte, or the cut splits it
	}
	next++
	return row[i:next], next, true
}
