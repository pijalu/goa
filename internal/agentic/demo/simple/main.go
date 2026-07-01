//go:build ignore
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main
import (
	"context"
	"encoding/json"
	"fmt"

	agentic "github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/demo/shared"
	"github.com/pijalu/goa/internal/agentic/helper"
)

type Calculator struct{}

func (c Calculator) IsRetryable(err error) bool { return false }

func (c Calculator) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "calculator",
		Description: "math operations",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"a":  map[string]string{"type": "number"},
				"b":  map[string]string{"type": "number"},
				"op": map[string]string{"type": "string"},
			},
			"required": []string{"a", "b", "op"},
		},
	}
}

func (c Calculator) Execute(input string) (string, error) {
	var in struct {
		A  float64 `json:"a"`
		B  float64 `json:"b"`
		Op string  `json:"op"`
	}

	json.Unmarshal([]byte(input), &in)

	var result float64
	switch in.Op {
	case "+":
		result = in.A + in.B
	case "-":
		result = in.A - in.B
	case "*":
		result = in.A * in.B
	case "/":
		result = in.A / in.B
	}

	if result == float64(int64(result)) {
		return fmt.Sprintf("%d", int64(result)), nil
	}
	return fmt.Sprintf("%g", result), nil
}

func main() {
	cfg := shared.Parse(
		"http://localhost:1234/v1/chat/completions",
		"local-model",
	)

	log := agentic.NewLogger(agentic.Warn)
	agentCfg := cfg.ToAgentConfig()
	agentCfg.Logger = log

	systemPrompt := `You must use tools for all calculations.
Show your work step by step before using tools.
Explain what you're doing.`
	agentCfg.SystemPrompt = systemPrompt
	agentCfg.Tools = []agentic.Tool{Calculator{}}

	agent := agentic.NewAgent(agentCfg)

	// Add observers
	agent.AddObserver(helper.NewConsoleObserver())
	logObserver := helper.NewMessageLogObserver()
	agent.AddObserver(logObserver)

	// Run the conversation
	agent.Run(context.Background(), "Compute ((10+5)*2)-3")
	// Continue the conversation
	agent.Run(context.Background(), "Now multiply that by 4")

	// Get structured log
	jsonData, _ := logObserver.JSON()
	fmt.Println(string(jsonData))

	// Clean up
	agent.Stop()
}
