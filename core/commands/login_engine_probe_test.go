package commands

import (
	"fmt"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

func TestProbeEngineBlock(t *testing.T) {
	term := &fakeTerm{w: 100, h: 30}
	engine := tui.NewTUI(term)
	engine.AddChild(tui.NewChatViewport())
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()
	engine.RunLoops()

	flow := &blockingCodexFlow{release: make(chan struct{}), started: make(chan struct{}), tokens: &oauth.Tokens{AccessToken: "x"}}
	cmd := &LoginCommand{Store: mustStore(t), flowFactory: func(string) oauthFlow { return flow }}
	ctx := core.Context{EventBus: event.MakeBus(16, 16, 16, 16)}
	ctx.SelectOptionFunc = func(title string, items []tui.SelectorItem, current string, onSel func(string, bool)) {
		fmt.Printf("PROBE selector items=%v current=%q\n", values(items), current)
		go func() { onSel("oauth", true) }() // no engine.ApplySync — direct
	}
	go func() { _ = cmd.Run(ctx, []string{"openai-codex"}) }()

	select {
	case <-flow.started:
		fmt.Println("PROBE flow started")
	case <-time.After(2 * time.Second):
		fmt.Println("PROBE flow NOT started")
	}

	resp := make(chan struct{})
	go func() { engine.ApplySync(func() { close(resp) }) }()
	select {
	case <-resp:
		fmt.Println("PROBE engine responsive")
	case <-time.After(2 * time.Second):
		fmt.Println("PROBE engine BLOCKED")
	}
	close(flow.release)
	time.Sleep(50 * time.Millisecond)
}

func values(items []tui.SelectorItem) []string {
	var v []string
	for _, it := range items {
		v = append(v, it.Value)
	}
	return v
}
