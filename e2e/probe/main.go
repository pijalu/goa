// probe runs the production wiring (cascade load + InitSubsystems) against a
// project dir and reports agent-driven tool state: config flags, registry
// membership. Diagnostic tool for e2e companion/orchestration validation.
//
// Usage: go run ./e2e/probe <project-dir>
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/app"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe <project-dir>")
		os.Exit(2)
	}
	dir := os.Args[1]
	loader := config.NewCascadeLoader(dir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		fmt.Println("cascade load error:", err)
		os.Exit(1)
	}
	fmt.Printf("Tools.Enabled.RequestReview = %v\n", cfg.Tools.Enabled.RequestReview)
	fmt.Printf("Tools.Enabled.DelegateTo    = %v\n", cfg.Tools.Enabled.DelegateTo)
	fmt.Printf("ActiveModel = %s provider = %s\n", cfg.ActiveModel, cfg.ActiveProvider)

	subs := app.ProbeSubsystems(cfg, loader, dir)
	tools := subs.ProbeToolNames()
	sort.Strings(tools)
	fmt.Printf("registered tools (%d): %v\n", len(tools), tools)
	fmt.Printf("request_review registered: %v\n", contains(tools, "request_review"))
	fmt.Printf("delegate_to registered:    %v\n", contains(tools, "delegate_to"))

	rrReg, rrOn, dtReg, dtOn := subs.ProbeAgentDrivenToolState()
	fmt.Printf("request_review: registered=%v enabled=%v\n", rrReg, rrOn)
	fmt.Printf("delegate_to:    registered=%v enabled=%v\n", dtReg, dtOn)
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
