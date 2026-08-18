package agentic

import "strings"

func countRetryProgress(events []OutputEvent) (attempt1, attempt2, restored int) {
	for _, event := range events {
		if event.Type == EventProgress {
			if strings.Contains(event.Text, "attempt 1/2") {
				attempt1++
			}
			if strings.Contains(event.Text, "attempt 2/2") {
				attempt2++
			}
		}
		if event.Type == EventContent && event.Role == System && strings.Contains(event.Text, "Connection restored") {
			restored++
		}
	}
	return
}
