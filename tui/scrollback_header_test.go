package tui

import (
	"strings"
	"testing"
)

func assertScrollbackHeader(t *testing.T, sb []string) {
	t.Helper()
	assertUniqueFillers(t, sb)
	transcriptStarted := false
	for index, line := range sb {
		if isTranscriptLine(line) {
			transcriptStarted = true
		}
		if isLogoLine(line) && transcriptStarted {
			t.Errorf("scrollback[%d]: logo reappears after transcript: %q", index, strings.TrimRight(line, " "))
		}
	}
}

func assertUniqueFillers(t *testing.T, sb []string) {
	seen := map[string]int{}
	for _, line := range sb {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "│ filler") {
			seen[trimmed]++
		}
	}
	for line, count := range seen {
		if count > 1 {
			t.Errorf("scrollback row %q duplicated %d times", line, count)
		}
	}
}

func isTranscriptLine(line string) bool {
	return strings.Contains(line, "filler") || strings.Contains(line, "/model") || strings.Contains(line, "Context loaded") || strings.Contains(line, "skills loaded") || strings.Contains(line, "Connected to model") || strings.Contains(line, "model done")
}

func isLogoLine(line string) bool {
	return strings.Contains(line, "███") || strings.Contains(line, "▀▄")
}
