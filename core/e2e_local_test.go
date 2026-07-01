//go:build e2e
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// End-to-end test against a local LLM server (llama.cpp / OpenAI-compatible).
// Requires a server running at http://localhost:1234.
// Run: go test -count=1 -tags e2e -run TestE2E ./...
package core
import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	_ "github.com/pijalu/goa/core/commands" // auto-register commands
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	_ "github.com/pijalu/goa/internal/agentic/provider/openai" // register openai-completions backend
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tools"
)

const (
	testEndpoint = "http://localhost:1234/v1/chat/completions"
	testModel    = "google/gemma-4-e4b"
	testProvider = "local"
)

// skipIfNoLLM checks if the LLM server is available and skips the test if not.
func skipIfNoLLM(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	modelsURL := strings.TrimSuffix(testEndpoint, "/chat/completions") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", modelsURL, nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("LLM server not available at %s: %v", testEndpoint, err)
	}
	resp.Body.Close()
	t.Logf("LLM server is available at %s", testEndpoint)
}

// makeTestConfig creates a config pointing at the local LLM server.
func makeTestConfig() *config.Config {
	return &config.Config{
		ActiveProvider: testProvider,
		ActiveModel:    testModel,
		Providers: []config.ProviderConfig{
			{
				ID:           testProvider,
				Name:         "Local llama.cpp",
				Endpoint:     testEndpoint,
				DefaultModel: testModel,
				APIKey:       "",
			},
		},
		Execution: config.ExecutionConfig{
			Mode: internal.ExecutionYolo,
		},
	}
}

// TestE2E_ProviderManager_ListModels verifies we can list models from the local provider.
func TestE2E_ProviderManager_ListModels(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	pm := provider.NewProviderManager(cfg)

	models, err := pm.ListModels(testProvider)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("Expected at least one model, got none")
	}

	t.Logf("Available models (%d):", len(models))
	for _, m := range models {
		t.Logf("  - %s", m.ID)
	}

	// Verify our target model is in the list
	found := false
	for _, m := range models {
		if m.ID == testModel {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected model %q in model list, but it was not found", testModel)
	}
}

// TestE2E_ProviderManager_CreateAndActive verifies provider creation and active selection.
func TestE2E_ProviderManager_CreateAndActive(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	pm := provider.NewProviderManager(cfg)

	// Verify active provider
	p, model := pm.Active()
	if p == nil {
		t.Fatal("Active() returned nil provider")
	}
	if p.ID != testProvider {
		t.Errorf("Active provider ID = %q, want %q", p.ID, testProvider)
	}
	if model != testModel {
		t.Errorf("Active model = %q, want %q", model, testModel)
	}

	// Resolve model via new API
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if mdl.ID == "" {
		t.Fatal("ResolveActiveModel returned empty model ID")
	}
	_ = pm.BuildStreamOptions() // verify it doesn't panic
	t.Logf("Resolved model: %s (api: %s)", mdl.ID, mdl.Api)
}

// TestE2E_ProviderManager_TestConnection verifies connection to the local provider.
func TestE2E_ProviderManager_TestConnection(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	pm := provider.NewProviderManager(cfg)

	latency, modelCount, err := pm.TestConnection(testProvider)
	if err != nil {
		t.Fatalf("TestConnection failed: %v", err)
	}
	if modelCount == 0 {
		t.Error("Expected at least one model, got 0")
	}
	t.Logf("Connection latency: %v, models available: %d", latency, modelCount)
}

// TestE2E_CommandRouter_ModelsCommand tests the /models command end-to-end.
func TestE2E_CommandRouter_ModelsCommand(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	reg := GlobalRegistry()
	docEng := NewDocEngine(reg)
	router := NewCommandRouter(reg, docEng)
	pm := provider.NewProviderManager(cfg)

	ctx := Context{
		Config:          cfg,
		ProviderManager: pm,
	}

	// Parse and execute /models
	result := router.Parse("/models")
	if result == nil {
		t.Fatal("Parse('/models') returned nil")
	}
	if result.Command == nil {
		t.Fatal("ModelsCommand not found in registry")
	}

	output, err := router.Execute(ctx, result)
	if err != nil {
		t.Fatalf("Execute('/models') error: %v", err)
	}
	t.Logf("/models output: %s", output)
}

// TestE2E_AgentSession_SimpleChat starts a full agent session against the local LLM.
func TestE2E_AgentSession_SimpleChat(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	agentMgr, tuiEvents := startTestAgentSession(t, cfg, "You are a helpful assistant.")
	go logEvents(t, tuiEvents)

	if err := agentMgr.SendUserInput("Reply with exactly: 'Hello from Goa e2e test'"); err != nil {
		t.Fatalf("SendUserInput failed: %v", err)
	}
	t.Log("Message sent to agent, waiting for response...")

	time.Sleep(10 * time.Second)
	t.Log("Response capture complete")
}

// startTestAgentSession resolves the active model, builds stream options, and
// starts an AgentManager session for e2e tests.
func startTestAgentSession(t *testing.T, cfg *config.Config, systemPrompt string) (*AgentManager, chan interface{}) {
	t.Helper()
	pm := provider.NewProviderManager(cfg)

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	streamOpts := pm.BuildStreamOptions()

	sessionStore := NewSessionStore(os.TempDir())
	loopDetector := NewLoopDetector(DefaultLoopDetectorConfig())
	tuiEvents := make(chan interface{}, 100)
	sessionState := NewSessionState(cfg.DefaultModeState())
	agentMgr := NewAgentManager(cfg, sessionStore, loopDetector, sessionState, tuiEvents, "")

	if _, err := agentMgr.StartSession(mdl, streamOpts, systemPrompt, nil, cfg); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	t.Log("Agent session started successfully")
	return agentMgr, tuiEvents
}

// logEvents streams assistant events to the test log.
func logEvents(t *testing.T, events chan interface{}) {
	t.Helper()
	for msg := range events {
		event, ok := msg.(agentic.OutputEvent)
		if !ok {
			continue
		}
		if event.Type == agentic.EventContent && event.Text != "" {
			t.Logf("AGENT: %s", event.Text)
		}
		if event.Type == agentic.EventEnd {
			t.Logf("AGENT FINISHED — tokens: %+v", event.Timings)
			return
		}
	}
}

// TestE2E_ModelsServiceCall tests the Goa /models command output is meaningful
// when using a real provider manager with a live connection.
func TestE2E_ModelsServiceCall(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	pm := provider.NewProviderManager(cfg)
	reg := GlobalRegistry()
	docEng := NewDocEngine(reg)
	router := NewCommandRouter(reg, docEng)
	ctx := Context{
		Config:          cfg,
		ProviderManager: pm,
	}

	// First, verify ListModels works via ProviderManager
	models, err := pm.ListModels(testProvider)
	if err != nil {
		t.Fatalf("ProviderManager.ListModels failed: %v", err)
	}

	// Now execute the /models command through the router
	result := router.Parse("/models")
	output, err := router.Execute(ctx, result)
	if err != nil {
		t.Fatalf("Router.Execute('/models') error: %v", err)
	}

	// The output should mention available models
	t.Logf("/models output:\n%s", output)

	if len(models) > 0 {
		// Check that at least one model ID appears in the output
		found := false
		for _, m := range models {
			if strings.Contains(output, m.ID) {
				found = true
				break
			}
		}
		if !found {
			t.Logf("Warning: none of the %d model IDs appear in the command output", len(models))
		}
	}
}

// TestE2E_ModelSelectionAndChat validates the full flow:
// list models → select model → start chat → send message
func TestE2E_ModelSelectionAndChat(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	pm := provider.NewProviderManager(cfg)
	logAvailableModels(t, pm)
	cfg.ActiveModel = testModel
	assertActiveModel(t, pm, testModel)

	systemPrompt := "You are a helpful assistant. Keep responses very brief."
	userInput := "Reply with exactly: 'Selected model: " + testModel + " works'"

	agentMgr, tuiEvents := startTestAgentSession(t, cfg, systemPrompt)
	if err := agentMgr.SendUserInput(userInput); err != nil {
		t.Fatalf("SendUserInput failed: %v", err)
	}

	responseText := collectAssistantResponse(tuiEvents, 30*time.Second)
	logChatResponse(t, responseText)
	assertRealResponse(t, responseText, systemPrompt, userInput, "Selected model: "+testModel+" works")
}

// logAvailableModels lists models from the provider for debugging.
func logAvailableModels(t *testing.T, pm *provider.ProviderManager) {
	t.Helper()
	models, err := pm.ListModels(testProvider)
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("No models available")
	}
	t.Logf("Available models: %d", len(models))
	for _, m := range models {
		t.Logf("  - %s", m.ID)
	}
}

// assertActiveModel verifies the provider manager resolves the expected model.
func assertActiveModel(t *testing.T, pm *provider.ProviderManager, want string) {
	t.Helper()
	p, model := pm.Active()
	if p == nil {
		t.Fatal("No active provider")
	}
	if model != want {
		t.Errorf("Active model = %q, want %q", model, want)
	}
	t.Logf("Active provider: %s, model: %s", p.ID, model)
}

// collectAssistantResponse drains assistant events until EventEnd or timeout.
func collectAssistantResponse(events chan interface{}, timeout time.Duration) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var responseText string
	for {
		select {
		case msg := <-events:
			event, ok := msg.(agentic.OutputEvent)
			if !ok {
				continue
			}
			// Only count assistant-generated content. System and user
			// messages are also emitted as EventContent but are not the
			// LLM response.
			if event.Type == agentic.EventContent && event.Text != "" && event.Role == agentic.Assistant {
				responseText += event.Text
			}
			if event.Type == agentic.EventEnd {
				return responseText
			}
		case <-ctx.Done():
			return responseText
		}
	}
}

// logChatResponse prints the response for debugging.
func logChatResponse(t *testing.T, responseText string) {
	t.Helper()
	t.Logf("=== CHAT RESPONSE (%d chars) ===\n%s\n=== END ===", len(responseText), responseText)
	if responseText == "" {
		return
	}
	t.Logf("=== AGENT RESPONSE ===")
	t.Logf("%s", responseText[:min(500, len(responseText))])
	t.Logf("=== END ===")
}

// assertRealResponse fails if the text is just the prompt echo, and warns if the
// exact expected string is missing.
func assertRealResponse(t *testing.T, responseText, systemPrompt, userInput, expected string) {
	t.Helper()
	if responseText == "" {
		t.Errorf("Response was empty")
		return
	}
	if isPromptEcho(responseText, systemPrompt, userInput) {
		t.Fatalf("Response appears to be a prompt echo rather than an LLM response: %q", responseText)
	}
	if !strings.Contains(responseText, expected) {
		t.Logf("WARNING: Response does not contain exact expected string %q; model may have paraphrased. Response: %q", expected, responseText)
	}
}

// isPromptEcho reports whether text is exactly the system prompt or user input,
// indicating the agent never received a real assistant response.
func isPromptEcho(text, systemPrompt, userInput string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	if strings.TrimSpace(systemPrompt) != "" && text == strings.TrimSpace(systemPrompt) {
		return true
	}
	if strings.TrimSpace(userInput) != "" && text == strings.TrimSpace(userInput) {
		return true
	}
	return false
}

// TestE2E_SummarizeProject_WithToolCalls exercises the full agent loop against
// a local tool-capable model. It verifies that reasoning is streamed, that the
// agent invokes read/bash, that tool results are seen, and that a final
// summary is produced rather than looping forever.
func TestE2E_SummarizeProject_WithToolCalls(t *testing.T) {
	skipIfNoLLM(t)

	cfg := makeTestConfig()
	// Use a tool-capable model if the server exposes qwen; otherwise the
	// active model from the config is used and the test still validates the
	// wire protocol.
	if cfg.ActiveModel == "" || cfg.ActiveModel == "google/gemma-4-e4b" {
		cfg.ActiveModel = "qwen/qwen3.5-9b"
	}

	systemPrompt := "You are a terminal AI coding assistant. Use tools when needed, then answer concisely."
	agentMgr, tuiEvents := startTestAgentSessionWithTools(t, cfg, systemPrompt)

	if err := agentMgr.SendUserInput("read README.md and summarize it in one sentence"); err != nil {
		t.Fatalf("SendUserInput failed: %v", err)
	}

	result := collectSummarizeEvents(tuiEvents, 180*time.Second)

	if !result.sawThinking {
		t.Error("expected at least one thinking delta to be streamed")
	}
	if !result.sawToolCall {
		t.Error("expected at least one tool call")
	}
	if !result.sawToolResult {
		t.Error("expected at least one tool result")
	}
	if result.loopDetected {
		t.Fatalf("detected repeated identical tool calls; agent is not seeing tool results correctly: %+v", result.toolCallKeys)
	}
	if result.finalResponse == "" {
		t.Fatal("expected non-empty final assistant response after tool results")
	}

	t.Logf("Final response (%d chars):\n%s", len(result.finalResponse), result.finalResponse[:min(500, len(result.finalResponse))])
}

// summarizeResult aggregates events from a summarize-project run.
type summarizeResult struct {
	sawThinking   bool
	sawToolCall   bool
	sawToolResult bool
	loopDetected  bool
	finalResponse string
	toolCallKeys  []string
}

// collectSummarizeEvents drains the event channel for the summarize test and
// returns a summary of what happened.
func collectSummarizeEvents(events chan interface{}, timeout time.Duration) summarizeResult {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	result := &summarizeResult{}
	seenToolCalls := make(map[string]int)
	for {
		select {
		case msg := <-events:
			event, ok := msg.(agentic.OutputEvent)
			if !ok {
				continue
			}
			if result.handleEvent(event, seenToolCalls) {
				return *result
			}
		case <-ctx.Done():
			return *result
		}
	}
}

// handleEvent updates the summarizeResult from a single agent event. Returns
// true when the turn has ended.
func (r *summarizeResult) handleEvent(event agentic.OutputEvent, seenToolCalls map[string]int) bool {
	switch event.Type {
	case agentic.EventContent:
		if event.Role == agentic.Assistant {
			if event.State == agentic.StateThinking {
				r.sawThinking = true
			} else if event.State == agentic.StateContent {
				r.finalResponse += event.Text
			}
		}
	case agentic.EventToolCall:
		r.sawToolCall = true
		key := event.ToolName + "|" + event.ToolInput
		seenToolCalls[key]++
		if seenToolCalls[key] > 1 {
			r.loopDetected = true
		}
		r.toolCallKeys = append(r.toolCallKeys, key)
	case agentic.EventToolResult:
		r.sawToolResult = true
		// After a tool result the assistant buffer for this turn is
		// complete; reset the response accumulator for the next turn.
		r.finalResponse = ""
	case agentic.EventEnd:
		return true
	}
	return false
}

// startTestAgentSessionWithTools is like startTestAgentSession but registers
// the real read, bash, and search tools so the LLM can use them.
func startTestAgentSessionWithTools(t *testing.T, cfg *config.Config, systemPrompt string) (*AgentManager, chan interface{}) {
	t.Helper()
	pm := provider.NewProviderManager(cfg)

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	streamOpts := pm.BuildStreamOptions()

	sessionStore := NewSessionStore(os.TempDir())
	loopDetector := NewLoopDetector(DefaultLoopDetectorConfig())
	tuiEvents := make(chan interface{}, 100)
	sessionState := NewSessionState(cfg.DefaultModeState())
	agentMgr := NewAgentManager(cfg, sessionStore, loopDetector, sessionState, tuiEvents, "")
	agentMgr.SetLogger(agentic.NewLogger(agentic.Debug))

	wtMgr := internal.NewWorktreeManager(os.TempDir(), internal.WorktreeMultiAgent)
	reg := tools.NewToolRegistry()
	reg.Register(&tools.ReadFileTool{WorktreeMgr: wtMgr})
	reg.Register(&tools.BashTool{WorktreeMgr: wtMgr})
	reg.Register(&tools.SearchTool{WorktreeMgr: wtMgr})

	streamOpts.OnResponse = func(status int, headers map[string]string) {
		t.Logf("LLM response status: %d", status)
	}

	if _, err := agentMgr.StartSession(mdl, streamOpts, systemPrompt, reg.All(), cfg); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	t.Log("Agent session with tools started successfully")
	return agentMgr, tuiEvents
}
