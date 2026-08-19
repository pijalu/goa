package multiagent

func markStructuralEvent(want map[string]bool, message OrchestratorMessage) {
	if message.To == "stream_start" && message.Kind == "content" {
		want["stream_start"] = true
	}
	if message.Kind == "thinking_start" {
		want["thinking_start"] = true
	}
	if message.To == "stream_end" {
		want["stream_end"] = true
	}
	if message.Kind == "thinking_end" {
		want["thinking_end"] = true
	}
	if message.From == "gate" {
		want["gate"] = true
	}
}

func structuralEventsComplete(want map[string]bool) bool {
	for _, seen := range want {
		if !seen {
			return false
		}
	}
	return true
}
