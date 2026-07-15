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
	writes, err := openAndParseFlashLog(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skipf("flash log not available: %v", err)
		}
		t.Fatalf("parse flash log: %v", err)
	}
	t.Logf("parsed %d writes", len(writes))

	logoFrames := findLogoFrames(writes, 29, 150, "goa coding agent")
	if len(logoFrames) > 1 {
		// Mascot visible in more than one frame means it reappeared after
		// the first frame; report the flash.
		t.Logf("height=%d width=%d: logo visible in frames %v", 29, 150, logoFrames)
	}
}

// openAndParseFlashLog opens the flash log and parses its writes.
func openAndParseFlashLog(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseFlashLog(f)
}

// findLogoFrames replays writes through a screen emulator and records the frame
// numbers where the mascot is visible after each synced frame.
func findLogoFrames(writes []string, h, w int, mascot string) []int {
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
	return logoFrames
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
