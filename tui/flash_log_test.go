// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestAnalyzeFlashLog(t *testing.T) {
	path := "/tmp/goa-term-flash.log"
	f, err := os.Open(path)
	if err != nil {
		t.Skipf("flash log not available: %v", err)
	}
	defer f.Close()

	writes, err := parseFlashLog(f)
	if err != nil {
		t.Fatalf("parse flash log: %v", err)
	}
	t.Logf("parsed %d writes", len(writes))

	const mascot = "goa coding agent"
	// The reported terminal size is 150 columns x 29 rows.
	for _, h := range []int{29} {
		for _, w := range []int{150} {
			emu := newScreenEmulator(h, w)
			frameIdx := 0
			var logoFrames []int
			for _, write := range writes {
				emu.Process(write)
				if strings.Contains(write, "\x1b[?2026l") {
					frameIdx++
					if visibleContains(emu, h, mascot) {
						logoFrames = append(logoFrames, frameIdx)
					}
				}
			}
			if len(logoFrames) > 1 {
				// Mascot visible in more than one frame means it reappeared after
				// the first frame; report the flash.
				t.Logf("height=%d width=%d: logo visible in frames %v", h, w, logoFrames)
			}
		}
	}
}

var headerRe = regexp.MustCompile(`# \S+ write (\d+) bytes`)

func parseFlashLog(f *os.File) ([]string, error) {
	br := bufio.NewReader(f)
	var writes []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		m := headerRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("parse size: %v", err)
		}
		// Read exactly n bytes from the reader, which may span newlines.
		buf := make([]byte, n)
		_, err = br.Read(buf)
		if err != nil {
			return nil, fmt.Errorf("read %d bytes: %v", n, err)
		}
		writes = append(writes, string(buf))
	}
	return writes, nil
}
