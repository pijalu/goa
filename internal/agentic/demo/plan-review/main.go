//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Demo: planner agent and reviewer agent talking to each other via AgentBus.
// Flow:
//   1. User asks planner to create a plan
//   2. Planner sends the draft plan to reviewer via send_message
//   3. Reviewer critiques the plan and sends feedback to planner
//   4. Planner revises based on feedback and sends final plan to reviewer
//   5. Reviewer approves the final plan
// Usage:
//   go run demo/plan-review/main.go                           # live LLM
//   go run demo/plan-review/main.go -endpoint=http://host:port # custom endpoint
package main
import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
	"github.com/pijalu/goa/internal/agentic/helper"
)

func main() {
	cfg := shared.MustParse(
		"http://localhost:1234/v1/chat/completions",
		"local-model",
	)

	logger := agentic.NewLogger(agentic.Warn)

	// --- Shared communication bus ---
	bus := agentic.NewAgentBus()
	plannerInbox, _ := bus.Register("planner")
	reviewerInbox, _ := bus.Register("reviewer")

	// --- Create send tools ---
	plannerSend := &agentic.SendMessageTool{Bus: bus, FromName: "planner"}
	reviewerSend := &agentic.SendMessageTool{Bus: bus, FromName: "reviewer"}

	model := cfg.ToModel()

	// --- Create agents ---
	plannerCfg := cfg.ToAgentConfig()
	plannerCfg.Model = model
	plannerCfg.SystemPrompt = plannerPrompt()
	plannerCfg.Logger = logger
	plannerCfg.Tools = []agentic.Tool{plannerSend}

	reviewerCfg := cfg.ToAgentConfig()
	reviewerCfg.Model = model
	reviewerCfg.SystemPrompt = reviewerPrompt()
	reviewerCfg.Logger = logger
	reviewerCfg.Tools = []agentic.Tool{reviewerSend}

	planner := agentic.NewAgent(plannerCfg)
	planner.Output = make(chan agentic.Message, 20)

	reviewer := agentic.NewAgent(reviewerCfg)
	reviewer.Output = make(chan agentic.Message, 20)

	// --- Auto-receive connectors ---
	plannerConn := agentic.NewCommConnector(planner, plannerInbox)
	reviewerConn := agentic.NewCommConnector(reviewer, reviewerInbox)

	// --- Channel-driven conversation coordinator ---
	coord := newCoordinator()
	planner.AddObserver(coord)
	reviewer.AddObserver(coord)

	// --- Prefixed console observers ---
	planner.AddObserver(newPrefixedObserver("PLANNER "))
	reviewer.AddObserver(newPrefixedObserver("REVIEWER"))

	logObserver := helper.NewMessageLogObserver()
	planner.AddObserver(logObserver)
	reviewer.AddObserver(logObserver)

	// --- Drain output channels ---
	go func() {
		for range planner.Output {
		}
	}()
	go func() {
		for range reviewer.Output {
		}
	}()

	// --- Kick off ---
	goal := "Create a user onboarding flow for a SaaS product. Break it into steps, send the draft to the reviewer for feedback, revise based on their input, and stop once they approve."
	fmt.Printf("\nUSER → PLANNER: %s\n\n", goal)

	if err := planner.Run(context.Background(), goal); err != nil {
		log.Fatalf("planner error: %v", err)
	}

	coord.Wait()

	plannerConn.Stop()
	reviewerConn.Stop()

	close(planner.Output)
	close(reviewer.Output)

	fmt.Println()
	fmt.Println("=== Done ===")

	jsonData, _ := logObserver.JSON()
	fmt.Println(string(jsonData))
}

// --- Coordinator ---

type coordinator struct {
	events chan coordEvent
	done   chan struct{}
}

type coordEvent struct{ kind string }

func newCoordinator() *coordinator {
	cc := &coordinator{
		events: make(chan coordEvent, 20),
		done:   make(chan struct{}),
	}
	go cc.loop()
	return cc
}

func (cc *coordinator) loop() {
	count := 1
	for e := range cc.events {
		switch e.kind {
		case "send":
			count++
		case "end":
			count--
		}
		if count <= 0 {
			close(cc.done)
			return
		}
	}
}

func (cc *coordinator) OnEvent(event agentic.OutputEvent) {
	select {
	case <-cc.done:
		return
	default:
	}
	switch event.Type {
	case agentic.EventToolCall:
		if event.ToolName == "send_message" {
			select {
			case cc.events <- coordEvent{kind: "send"}:
			case <-cc.done:
			}
		}
	case agentic.EventEnd:
		select {
		case cc.events <- coordEvent{kind: "end"}:
		case <-cc.done:
		}
	}
}

func (cc *coordinator) Wait() {
	<-cc.done
}

// --- Prefixed observer ---

type prefixedObserver struct {
	prefix string
}

func newPrefixedObserver(prefix string) *prefixedObserver {
	return &prefixedObserver{prefix: prefix}
}

func (o *prefixedObserver) OnEvent(event agentic.OutputEvent) {
	switch event.Type {
	case agentic.EventContent:
		if event.Text != "" && event.State != agentic.StateThinking {
			if event.Role == agentic.User {
				fmt.Printf("[%s] ← %s\n", o.prefix, event.Text)
			} else if event.Role == agentic.Assistant {
				fmt.Printf("[%s] %s\n", o.prefix, event.Text)
			}
		}
	case agentic.EventToolCall:
		if event.ToolName == "send_message" {
			text := extractJSONField(event.ToolInput, "content")
			fmt.Printf("[%s] → send_message: %s\n", o.prefix, text)
		} else {
			fmt.Printf("[%s] → tool: %s\n", o.prefix, event.ToolName)
		}
	case agentic.EventToolResult:
		fmt.Printf("[%s] ← ack: %s\n", o.prefix, event.Text)
	case agentic.EventEnd:
		fmt.Printf("[%s] (turn complete)\n", o.prefix)
		fmt.Println()
	}
}

func extractJSONField(jsonStr, field string) string {
	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return jsonStr
	}
	if v, ok := raw[field]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return jsonStr
}

// --- Prompts ---

func plannerPrompt() string {
	return strings.TrimSpace(`
You are the "planner" agent. Your job is to create structured plans and send them to the "reviewer" for feedback.

Rules:
- Use the send_message tool to send your plans to the reviewer.
- When you receive feedback, revise the plan and send it back.
- Once the reviewer approves, stop.
- Be concise. One plan per message.
- The user asked you to plan a user onboarding flow. Break it into clear steps, get reviewer feedback, revise, and finalize.
`)
}

func reviewerPrompt() string {
	return strings.TrimSpace(`
You are the "reviewer" agent. Your job is to review plans sent by the "planner" and provide constructive feedback.

Rules:
- Use the send_message tool to reply to the planner.
- Check for: completeness, feasibility, missing steps, and user experience issues.
- On first review: provide specific feedback (not just "looks good").
- On second review: either approve or request one more revision.
- Be concise. One review per message.
`)
}
