// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Streaming render benchmarks. They pin the property the streaming path must
// hold: per-chunk render cost is independent of how much has already been
// streamed. Run with:
//
//	go test ./tui -run xxx -bench 'BenchmarkStream' -benchtime 50x
//	go test ./tui -run xxx -bench 'BenchmarkStreamGrowth' -cpuprofile cpu.prof
//
// A growth trend across answerChunks/lines sizes means per-chunk work became
// O(answer) again (see bugs.md "Excessive CPU usage during streaming").

package tui

import (
	"fmt"
	"strings"
	"testing"
)

func newProfScenario(b *testing.B, history int) (*TUI, *fakeTerminal, *ChatViewport) {
	b.Helper()
	const w, h = 100, 30
	term := &fakeTerminal{w: w, h: h}
	engine := NewTUI(term)
	chat := NewChatViewport()
	status := NewStatusMsg()
	inp := NewEditor()
	for _, c := range []Component{NewHeader("goa", "test"), chat, status, inp, NewFooter()} {
		engine.AddChild(c)
	}
	engine.SetFocus(inp)
	status.SetTUI(engine)
	inp.SetTUI(engine)
	if err := engine.Start(); err != nil {
		b.Fatalf("Start: %v", err)
	}
	for i := 0; i < history; i++ {
		chat.AddSystemMessage(fmt.Sprintf("history row %03d with some padding text to be realistic", i))
	}
	engine.RenderNow()
	return engine, term, chat
}

func markdownChunk(i int) string {
	switch i % 5 {
	case 0:
		return fmt.Sprintf("Paragraph %d with **bold** and `code` words that wrap across lines nicely.", i)
	case 1:
		return fmt.Sprintf("- list item %d with some detail text", i)
	case 2:
		return fmt.Sprintf("```go\nfunc demo%d() { return %d }\n```", i, i)
	case 3:
		return fmt.Sprintf("## Heading %d", i)
	default:
		return fmt.Sprintf("Another paragraph %d talking about implementation details.", i)
	}
}

// BenchmarkStreamMarkdown measures per-chunk cost of a growing markdown answer.
func BenchmarkStreamMarkdown(b *testing.B) {
	for _, history := range []int{10, 200} {
		b.Run(fmt.Sprintf("history%d", history), func(b *testing.B) {
			engine, _, chat := newProfScenario(b, history)
			defer engine.Stop()
			stream := []string{}
			chat.AddAssistantMessage("")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stream = append(stream, markdownChunk(i))
				chat.UpdateLastMessage(strings.Join(stream, "\n\n"), ConsoleAssistantMessage)
				engine.RenderNow()
				if len(stream) > 120 {
					stream = stream[:0]
					chat.AddAssistantMessage("")
				}
			}
		})
	}
}

// BenchmarkStreamGrowth measures the per-chunk cost as the streamed answer grows.
func BenchmarkStreamGrowth(b *testing.B) {
	for _, n := range []int{5, 50, 200, 600} {
		b.Run(fmt.Sprintf("answerChunks%d", n), func(b *testing.B) {
			engine, _, chat := newProfScenario(b, 20)
			defer engine.Stop()
			stream := make([]string, 0, n+1)
			chat.AddAssistantMessage("")
			for i := 0; i < n; i++ {
				stream = append(stream, markdownChunk(i))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stream = append(stream, markdownChunk(n+i))
				chat.UpdateLastMessage(strings.Join(stream, "\n\n"), ConsoleAssistantMessage)
				engine.RenderNow()
			}
		})
	}
}

// BenchmarkStreamRender measures per-chunk render cost with a long transcript.
func BenchmarkStreamRender(b *testing.B) {
	for _, history := range []int{10, 200, 1000} {
		b.Run(fmt.Sprintf("history%d", history), func(b *testing.B) {
			engine, _, chat := newProfScenario(b, history)
			defer engine.Stop()
			stream := []string{}
			chat.AddAssistantMessage("")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stream = append(stream, fmt.Sprintf("%d. streamed chunk line of text that is here", i))
				chat.UpdateLastMessage(strings.Join(stream, "\n"), ConsoleAssistantMessage)
				engine.RenderNow()
				if len(stream) > 200 {
					stream = stream[:0]
					chat.AddAssistantMessage("")
				}
			}
		})
	}
}

// BenchmarkStreamUnbrokenParagraph measures the pathological shape: one
// paragraph, no blank lines, so no blank-line block boundary ever appears.
func BenchmarkStreamUnbrokenParagraph(b *testing.B) {
	for _, n := range []int{5, 100, 500} {
		b.Run(fmt.Sprintf("lines%d", n), func(b *testing.B) {
			engine, _, chat := newProfScenario(b, 20)
			defer engine.Stop()
			stream := make([]string, 0, n+1)
			chat.AddAssistantMessage("")
			for i := 0; i < n; i++ {
				stream = append(stream, fmt.Sprintf("line %d of one unbroken paragraph with **bold** and `code`", i))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				stream = append(stream, fmt.Sprintf("line %d of one unbroken paragraph with **bold** and `code`", n+i))
				chat.UpdateLastMessage(strings.Join(stream, "\n"), ConsoleAssistantMessage)
				engine.RenderNow()
			}
		})
	}
}
