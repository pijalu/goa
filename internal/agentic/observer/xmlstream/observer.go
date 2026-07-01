// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package xmlstream

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// StreamingXMLWriter defines the interface for writing XML chunks.
type StreamingXMLWriter interface {
	WriteChunk(chunk string) error
	Close() error
}

// ConsoleWriter writes XML chunks to an io.Writer.
type ConsoleWriter struct {
	Writer io.Writer
}

func NewConsoleWriter(w io.Writer) *ConsoleWriter {
	if w == nil {
		w = os.Stdout
	}
	return &ConsoleWriter{Writer: w}
}

func (cw *ConsoleWriter) WriteChunk(chunk string) error {
	_, err := cw.Writer.Write([]byte(chunk))
	return err
}

func (cw *ConsoleWriter) Close() error {
	return nil
}

// CallbackWriter writes XML chunks via callback functions.
type CallbackWriter struct {
	WriteFunc func(chunk string) error
	CloseFunc func() error
}

func NewCallbackWriter(writeFunc func(chunk string) error, closeFunc func() error) *CallbackWriter {
	return &CallbackWriter{WriteFunc: writeFunc, CloseFunc: closeFunc}
}

func (cw *CallbackWriter) WriteChunk(chunk string) error {
	if cw.WriteFunc != nil {
		return cw.WriteFunc(chunk)
	}
	return nil
}

func (cw *CallbackWriter) Close() error {
	if cw.CloseFunc != nil {
		return cw.CloseFunc()
	}
	return nil
}

// HTTPChunkedWriter writes XML chunks as HTTP chunked transfer encoding.
type HTTPChunkedWriter struct {
	output chan string
	errors chan error
	closed bool
}

func NewHTTPChunkedWriter(bufferSize int) *HTTPChunkedWriter {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &HTTPChunkedWriter{output: make(chan string, bufferSize), errors: make(chan error, 1)}
}

func (hw *HTTPChunkedWriter) Output() <-chan string { return hw.output }
func (hw *HTTPChunkedWriter) Errors() <-chan error  { return hw.errors }

func (hw *HTTPChunkedWriter) WriteChunk(chunk string) error {
	if hw.closed {
		return fmt.Errorf("writer is closed")
	}
	select {
	case hw.output <- chunk:
		return nil
	default:
		return fmt.Errorf("output buffer full")
	}
}

func (hw *HTTPChunkedWriter) Close() error {
	if hw.closed {
		return nil
	}
	hw.closed = true
	close(hw.output)
	return nil
}

func FlushHTTP(w http.ResponseWriter, r *http.Request, hw *HTTPChunkedWriter) error {
	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("ResponseWriter does not support chunked transfer")
	}

	for chunk := range hw.Output() {
		_, err := fmt.Fprintf(w, "%x\r\n%s\r\n", len(chunk), chunk)
		if err != nil {
			return err
		}
		flusher.Flush()
	}

	_, err := fmt.Fprint(w, "0\r\n\r\n")
	flusher.Flush()
	return err
}

// Config holds configuration for XMLStreamingObserver.
type Config struct {
	Writer         StreamingXMLWriter
	Model          string
	ConversationID string
	IncludeTimings bool
}

// Mode represents the current nesting level
type Mode int

const (
	ModeMain Mode = iota
	ModeSkill
)

// XMLStreamingObserver implements agentic.OutputObserver and writes
// conversation events as streaming XML chunks to a StreamingXMLWriter.
type XMLStreamingObserver struct {
	cfg       Config
	startTime time.Time
	mu        sync.Mutex
	closed    bool

	mode Mode

	// Main context
	mainRole      agentic.Role
	mainBlock     string
	mainBlockOpen bool
	mainInBlocks  bool
	mainInMessage bool

	// Skill context
	skillRole      agentic.Role
	skillBlock     string
	skillBlockOpen bool
	skillInBlocks  bool
	skillInMessage bool

	skillDepth int
	toolDepth  int // number of pending tool calls (waiting for result)
}

// NewXMLStreamingObserver creates a new XML streaming observer
func NewXMLStreamingObserver(cfg Config) (*XMLStreamingObserver, error) {
	if cfg.Writer == nil {
		return nil, fmt.Errorf("writer is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	convID := cfg.ConversationID
	if convID == "" {
		convID = generateUUID()
	}

	obs := &XMLStreamingObserver{cfg: cfg, startTime: time.Now(), mode: ModeMain}
	obs.write(`<conversation><metadata><id>` + EscapeXMLAttr(convID) + `</id><model>` + EscapeXMLAttr(cfg.Model) + `</model><start>` + EscapeXMLAttr(obs.startTime.Format(time.RFC3339)) + `</start></metadata><messages>`)

	return obs, nil
}

func (o *XMLStreamingObserver) OnEvent(event agentic.OutputEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return
	}

	switch event.Type {
	case agentic.EventStateChange:
	case agentic.EventContent:
		o.handleContent(event)
	case agentic.EventToolCall:
		o.handleToolCall(event)
	case agentic.EventToolResult:
		o.handleToolResult(event)
	case agentic.EventTokenStats:
		o.handleTokenStats(event)
	case agentic.EventEnd:
		o.handleEnd()
	case agentic.EventClear:
		o.handleClear()
	}
}

func (o *XMLStreamingObserver) write(s string) {
	if s == "" {
		return
	}
	_ = o.cfg.Writer.WriteChunk(s)
}

func (o *XMLStreamingObserver) closeMainBlock() {
	if o.mainBlockOpen {
		o.write(`</` + o.mainBlock + `>`)
		o.mainBlock = ""
		o.mainBlockOpen = false
	}
}

func (o *XMLStreamingObserver) closeSkillBlock() {
	if o.skillBlockOpen {
		o.write(`</` + o.skillBlock + `>`)
		o.skillBlock = ""
		o.skillBlockOpen = false
	}
}

func (o *XMLStreamingObserver) closeMainMessage() {
	// Close any pending tool call first
	if o.toolDepth > 0 && o.mode == ModeMain {
		o.write(`<output><![CDATA[]]></output></toolcall>`)
		o.toolDepth--
	}
	o.closeMainBlock()
	if o.mainInBlocks {
		o.write(`</blocks>`)
		o.mainInBlocks = false
	}
	if o.mainInMessage {
		o.write(`</message>`)
		o.mainInMessage = false
	}
	o.mainRole = ""
}

func (o *XMLStreamingObserver) closeSkillMessage() {
	// Close any pending tool call first
	if o.toolDepth > 0 && o.mode == ModeSkill {
		o.write(`<output><![CDATA[]]></output></toolcall>`)
		o.toolDepth--
	}
	o.closeSkillBlock()
	if o.skillInBlocks {
		o.write(`</blocks>`)
		o.skillInBlocks = false
	}
	if o.skillInMessage {
		o.write(`</message>`)
		o.skillInMessage = false
	}
	o.skillRole = ""
}

func (o *XMLStreamingObserver) startMainMessage(role agentic.Role, metadata map[string]string) {
	if role == "" {
		role = agentic.Assistant
	}

	if o.mainInMessage && o.mainRole != role {
		o.closeMainMessage()
	}

	if !o.mainInMessage {
		o.write(`<message><role>` + EscapeXML(string(role)) + `</role>`)
		o.writeMetadata(metadata)
		o.write(`<blocks>`)
		o.mainRole = role
		o.mainInMessage = true
		o.mainInBlocks = true
		o.mainBlock = ""
		o.mainBlockOpen = false
	}
}

func (o *XMLStreamingObserver) startSkillMessage(role agentic.Role, metadata map[string]string) {
	if role == "" {
		role = agentic.Assistant
	}

	if o.skillInMessage && o.skillRole != role {
		o.closeSkillMessage()
	}

	if !o.skillInMessage {
		o.write(`<message><role>` + EscapeXML(string(role)) + `</role>`)
		o.writeMetadata(metadata)
		o.write(`<blocks>`)
		o.skillRole = role
		o.skillInMessage = true
		o.skillInBlocks = true
		o.skillBlock = ""
		o.skillBlockOpen = false
	}
}

func (o *XMLStreamingObserver) writeMetadata(metadata map[string]string) {
	if len(metadata) == 0 {
		return
	}
	o.write(`<metadata>`)
	for k, v := range metadata {
		o.write(`<item key="` + EscapeXMLAttr(k) + `">` + EscapeXML(v) + `</item>`)
	}
	o.write(`</metadata>`)
}

// closePendingToolCalls closes any open toolcalls with empty output.
// Used when content arrives before the tool result (e.g. LLM mixed reasoning).
func (o *XMLStreamingObserver) closePendingToolCalls() {
	if o.toolDepth > 0 {
		o.write(`<output><![CDATA[]]></output></toolcall>`)
		o.toolDepth--
	}
}

func (o *XMLStreamingObserver) handleContent(event agentic.OutputEvent) {
	if event.Text == "" {
		return
	}

	blockType := ""
	switch event.State {
	case agentic.StateThinking:
		blockType = "thinking"
	case agentic.StateContent, agentic.StateToolResult:
		blockType = "content"
	}

	role := event.Role
	if role == "" {
		role = agentic.Assistant
	}

	if o.mode == ModeSkill {
		o.startSkillMessage(role, event.Metadata)
		if o.skillBlock != blockType {
			o.closeSkillBlock()
			o.write(`<` + blockType + `>`)
			o.skillBlock = blockType
			o.skillBlockOpen = true
		}
		o.write(EscapeXML(event.Text))
	} else {
		// Defensive: close any pending tool call before writing content
		// to prevent content from being nested inside <toolcall>
		if o.toolDepth > 0 {
			o.closePendingToolCalls()
		}
		o.startMainMessage(role, event.Metadata)
		if o.mainBlock != blockType {
			o.closeMainBlock()
			o.write(`<` + blockType + `>`)
			o.mainBlock = blockType
			o.mainBlockOpen = true
		}
		o.write(EscapeXML(event.Text))
	}
}

func (o *XMLStreamingObserver) handleToolCall(event agentic.OutputEvent) {
	if event.ToolName == "run_skill" {
		o.handleSkillCall(event)
		return
	}

	// Regular tool call
	if o.mode == ModeSkill {
		o.closeSkillBlock()
		o.write(`<toolcall><name>` + EscapeXML(event.ToolName) + `</name><input><![CDATA[`)
		if event.ToolInput != "" {
			o.write(event.ToolInput)
		}
		o.write(`]]></input>`)
	} else {
		// Ensure we're inside an assistant message, not a user message
		// Tool calls must only appear inside <message role="assistant">
		if o.mainInMessage && o.mainRole != "" && o.mainRole != agentic.Assistant {
			o.closeMainMessage()
		}
		if !o.mainInMessage {
			o.startMainMessage(agentic.Assistant, event.Metadata)
		}
		o.closeMainBlock()
		o.write(`<toolcall><name>` + EscapeXML(event.ToolName) + `</name><input><![CDATA[`)
		if event.ToolInput != "" {
			o.write(event.ToolInput)
		}
		o.write(`]]></input>`)
	}
	o.toolDepth++
}

func (o *XMLStreamingObserver) handleSkillCall(event agentic.OutputEvent) {
	skillName := "unknown"
	if event.ToolInput != "" {
		if name := extractSkillName(event.ToolInput); name != "" {
			skillName = name
		}
	}

	// Close current block but keep message open - skillcall is a block inside message
	o.closeMainBlock()

	o.write(`<skillcall><name>` + EscapeXML(skillName) + `</name><input><![CDATA[`)
	if event.ToolInput != "" {
		o.write(event.ToolInput)
	}
	o.write(`]]></input><conversation>`)

	o.mode = ModeSkill
	o.skillDepth++
	o.skillRole = ""
	o.skillBlock = ""
	o.skillBlockOpen = false
	o.skillInBlocks = false
	o.skillInMessage = false
}

func (o *XMLStreamingObserver) handleToolResult(event agentic.OutputEvent) {
	// If this is a skill result (no pending tool calls = skill result)
	if o.skillDepth > 0 && o.toolDepth == 0 {
		o.handleSkillResult(event)
		return
	}

	// Close any open block first
	if o.mode == ModeSkill {
		o.closeSkillBlock()
	} else {
		o.closeMainBlock()
	}

	o.write(`<output><![CDATA[`)
	if event.Text != "" {
		o.write(event.Text)
	}
	o.write(`]]></output></toolcall>`)
	o.toolDepth--
}

func (o *XMLStreamingObserver) handleSkillResult(event agentic.OutputEvent) {
	// Close skill message if open
	if o.skillInMessage {
		o.closeSkillMessage()
	}

	o.write(`</conversation><output><![CDATA[`)
	if event.Text != "" {
		o.write(event.Text)
	}
	o.write(`]]></output></skillcall>`)

	o.skillDepth--
	if o.skillDepth > 0 {
		o.mode = ModeSkill
	} else {
		o.mode = ModeMain
	}
}

func (o *XMLStreamingObserver) handleTokenStats(event agentic.OutputEvent) {
	if event.Timings == nil || !o.cfg.IncludeTimings {
		return
	}

	if o.mode == ModeSkill {
		o.closeSkillBlock()
		o.write(`<stats><tokens><prompt>` + fmt.Sprintf("%d", event.Timings.PromptN) + `</prompt><predicted>` + fmt.Sprintf("%d", event.Timings.PredictedN) + `</predicted></tokens><timing_ms><prompt>` + fmt.Sprintf("%.2f", event.Timings.PromptMs) + `</prompt><predicted>` + fmt.Sprintf("%.2f", event.Timings.PredictedMs) + `</predicted></timing_ms></stats>`)
	} else {
		// Ensure stats appear inside an assistant message, not a user message
		if o.mainInMessage && o.mainRole != "" && o.mainRole != agentic.Assistant {
			o.closeMainMessage()
			// Re-open as assistant message so stats are properly attributed
			o.startMainMessage(agentic.Assistant, event.Metadata)
		}
		o.closeMainBlock()
		o.write(`<stats><tokens><prompt>` + fmt.Sprintf("%d", event.Timings.PromptN) + `</prompt><predicted>` + fmt.Sprintf("%d", event.Timings.PredictedN) + `</predicted></tokens><timing_ms><prompt>` + fmt.Sprintf("%.2f", event.Timings.PromptMs) + `</prompt><predicted>` + fmt.Sprintf("%.2f", event.Timings.PredictedMs) + `</predicted></timing_ms></stats>`)
	}
}

func (o *XMLStreamingObserver) handleEnd() {
	if o.mode == ModeSkill {
		o.closeSkillMessage()
	} else {
		o.closeMainMessage()
	}
}

func (o *XMLStreamingObserver) handleClear() {
	o.closeMainMessage()

	if o.mode == ModeSkill {
		o.write(`</conversation><output><![CDATA[]]></output></skillcall>`)
		o.mode = ModeMain
	}

	o.skillDepth = 0
	o.write(`</messages></conversation><conversation><metadata><id>` + EscapeXMLAttr(o.cfg.ConversationID) + `</id><model>` + EscapeXMLAttr(o.cfg.Model) + `</model><start>` + EscapeXMLAttr(time.Now().Format(time.RFC3339)) + `</start></metadata><messages>`)
}

func (o *XMLStreamingObserver) Flush() {
	if o.closed {
		return
	}

	o.closeMainMessage()

	if o.mode == ModeSkill {
		o.write(`</conversation><output><![CDATA[]]></output></skillcall>`)
	}

	for o.skillDepth > 0 {
		o.write(`</conversation><output><![CDATA[]]></output></skillcall>`)
		o.skillDepth--
	}

	o.write(`</messages></conversation>`)

	o.closed = true
	o.cfg.Writer.Close()
}

func generateUUID() string {
	t := time.Now()
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s-%d-%d", hostname, t.UnixNano(), t.UnixMicro())
}

func extractSkillName(input string) string {
	var params struct {
		SkillName string `json:"skill_name"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}
	return params.SkillName
}

var _ agentic.OutputObserver = (*XMLStreamingObserver)(nil)

// ForwardObserver wraps another observer and forwards all events.
type ForwardObserver struct {
	Observer agentic.OutputObserver
}

func NewForwardObserver(obs agentic.OutputObserver) *ForwardObserver {
	return &ForwardObserver{Observer: obs}
}

func (fo *ForwardObserver) OnEvent(event agentic.OutputEvent) {
	fo.Observer.OnEvent(event)
}

var _ agentic.OutputObserver = &ForwardObserver{}
