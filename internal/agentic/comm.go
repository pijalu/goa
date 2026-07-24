// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// CommMessage is the unit of exchange between agents on an AgentBus.
// It carries only content — thinking/reasoning stays internal to each agent.
type CommMessage struct {
	From    string `json:"from"`    // Sender agent name
	To      string `json:"to"`      // Recipient agent name
	Content string `json:"content"` // Message payload (what the recipient sees)
}

// AgentBus routes CommMessage between registered agents using Go channels.
// It is safe for concurrent use by multiple agents.
type AgentBus struct {
	mu      sync.RWMutex
	agents  map[string]chan CommMessage
	bufSize int // inbox buffer size per agent
}

// NewAgentBus creates a new message bus with buffered channels.
func NewAgentBus() *AgentBus {
	return &AgentBus{
		agents:  make(map[string]chan CommMessage),
		bufSize: 10,
	}
}

// Register adds an agent to the bus under the given name.
// It returns a receive-only channel for the agent to consume incoming messages.
// Returns an error if the name is already registered.
func (b *AgentBus) Register(name string) (<-chan CommMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, exists := b.agents[name]; exists {
		return nil, fmt.Errorf("agent %q already registered on bus", name)
	}

	ch := make(chan CommMessage, b.bufSize)
	b.agents[name] = ch
	return ch, nil
}

// Unregister removes an agent from the bus and closes its inbox channel.
// Safe to call multiple times; no-op if name not found.
func (b *AgentBus) Unregister(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if ch, ok := b.agents[name]; ok {
		close(ch)
		delete(b.agents, name)
	}
}

// Send delivers a CommMessage to the target agent's inbox channel, bounded by
// ctx. It returns an error if the target is not registered, ctx is cancelled,
// or the inbox stays full for longer than the send timeout (5s).
//
// Send holds the bus read lock for the whole send (including the blocking
// select). Unregister takes the write lock, so it cannot close the inbox
// channel while a Send is mid-flight on it. This closes the previous
// send-on-closed-channel race (Send read the channel under RLock, released
// the lock, then sent — allowing Unregister to close the channel in between
// and panic the sender). Multiple Sends run concurrently (RLock); only
// Unregister is serialized behind in-flight Sends, bounded by the send
// timeout.
func (b *AgentBus) Send(ctx context.Context, msg CommMessage) error {
	b.mu.RLock()
	ch, ok := b.agents[msg.To]
	if !ok {
		b.mu.RUnlock()
		return fmt.Errorf("agent %q not found on bus", msg.To)
	}

	select {
	case ch <- msg:
		b.mu.RUnlock()
		return nil
	case <-ctx.Done():
		b.mu.RUnlock()
		return ctx.Err()
	case <-time.After(5 * time.Second):
		b.mu.RUnlock()
		return fmt.Errorf("inbox for %q is full (timeout)", msg.To)
	}
}

// AgentNames returns the names of all currently registered agents.
func (b *AgentBus) AgentNames() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	names := make([]string, 0, len(b.agents))
	for name := range b.agents {
		names = append(names, name)
	}
	return names
}

// SendMessageTool implements Tool, allowing an agent to send messages to
// other agents on the bus. Create one per agent with its FromName set.
type SendMessageTool struct {
	BaseTool
	Bus      *AgentBus
	FromName string // Name of the agent that owns this tool instance
}

// Schema returns the tool schema. The "to" field dynamically reflects
// currently-registered agents (excluding the sender).
func (t *SendMessageTool) Schema() ToolSchema {
	names := t.Bus.AgentNames()

	// Build enum of available recipients (exclude self)
	var enum []interface{}
	for _, n := range names {
		if n != t.FromName {
			enum = append(enum, n)
		}
	}

	toSchema := map[string]interface{}{
		"type":        "string",
		"description": "Name of the agent to send the message to",
	}
	if len(enum) > 0 {
		toSchema["enum"] = enum
	}

	return ToolSchema{
		Name:        "send_message",
		Description: "Send a message to another agent on the communication bus.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"to":      toSchema,
				"content": map[string]interface{}{"type": "string", "description": "The message content to send"},
			},
			"required": []string{"to", "content"},
		},
	}
}

// Execute parses the tool input and routes the CommMessage via the bus.
// The agent's turn context is forwarded so a blocked send can be cancelled.
func (t *SendMessageTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

// ExecuteContext is the context-aware variant used by the agent when it
// detects SendMessageTool implements ContextTool.
func (t *SendMessageTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var req struct {
		To      string `json:"to"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		return "", fmt.Errorf("parse send_message input: %w", err)
	}

	msg := CommMessage{
		From:    t.FromName,
		To:      req.To,
		Content: req.Content,
	}

	if err := t.Bus.Send(ctx, msg); err != nil {
		return "", t.sendError(err)
	}

	return fmt.Sprintf("Message sent to %s", req.To), nil
}

// sendError wraps a bus Send failure with actionable guidance. Models
// frequently hallucinate recipient names (e.g. "coordinator") or reach for
// send_message when no other agent exists, so the error lists the agents that
// are actually addressable and points at the right tool for talking to the
// user. Without this the model retries the same bad recipient or gives up.
func (t *SendMessageTool) sendError(err error) error {
	others := t.otherAgents()
	if len(others) == 0 {
		return fmt.Errorf("%w\nThere are no other agents on the bus — you are the only agent (%q) running. "+
			"Do not use send_message. To ask the user something use the ask_user_question tool; "+
			"to finish or report a goal's status call the goal tool with action \"update\" (plain text does not stop a goal).", err, t.FromName)
	}
	return fmt.Errorf("%w\nAvailable agents you can message: %s (you are %q). "+
		"If you meant to ask the user something, use the ask_user_question tool instead.",
		err, strings.Join(others, ", "), t.FromName)
}

// otherAgents returns the names of registered agents excluding the sender.
func (t *SendMessageTool) otherAgents() []string {
	names := t.Bus.AgentNames()
	others := make([]string, 0, len(names))
	for _, n := range names {
		if n != t.FromName {
			others = append(others, n)
		}
	}
	return others
}

// ReceiveMessageTool implements Tool, allowing an agent to explicitly poll
// its inbox for pending messages. Use this instead of CommConnector when
// you want the LLM to control when messages are checked.
type ReceiveMessageTool struct {
	BaseTool
	Inbox <-chan CommMessage
}

// Schema returns the tool schema.
func (t *ReceiveMessageTool) Schema() ToolSchema {
	return ToolSchema{
		Name:        "receive_message",
		Description: "Check for pending messages from other agents. Returns the next message if available, or indicates the inbox is empty.",
		Schema: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	}
}

// Execute non-blockingly reads one message from the inbox channel.
func (t *ReceiveMessageTool) Execute(input string) (string, error) {
	select {
	case msg, ok := <-t.Inbox:
		if !ok {
			return "", fmt.Errorf("inbox closed")
		}
		return fmt.Sprintf("[From %s]: %s", msg.From, msg.Content), nil
	default:
		return "No new messages.", nil
	}
}

// CommConnector wires an agent's inbox directly to its Run() method,
// automatically feeding incoming messages as new conversation turns.
// This removes the need for the LLM to poll via receive_message.
type CommConnector struct {
	agent  *Agent
	inbox  <-chan CommMessage
	done   chan struct{}
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// NewCommConnector starts a background goroutine that reads from inbox
// and calls agent.Run() for each incoming message. The message is formatted
// with sender attribution so the receiving agent knows who it's from.
func NewCommConnector(agent *Agent, inbox <-chan CommMessage) *CommConnector {
	ctx, cancel := context.WithCancel(context.Background())
	c := &CommConnector{
		agent:  agent,
		inbox:  inbox,
		done:   make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	c.wg.Add(1)
	go c.loop()
	return c
}

func (c *CommConnector) loop() {
	defer c.wg.Done()
	for {
		select {
		case msg, ok := <-c.inbox:
			if !ok {
				return
			}
			// Format with sender info so the agent knows the source
			content := fmt.Sprintf("[Message from %s]: %s", msg.From, msg.Content)
			_ = c.agent.Run(c.ctx, content)
		case <-c.done:
			return
		case <-c.ctx.Done():
			return
		}
	}
}

// Stop signals the connector to shut down and waits for the goroutine to exit.
// It cancels the connector's own context so an in-flight agent.Run (which was
// previously invoked with context.Background and could block forever) unblocks
// promptly. Stop is safe to call exactly once.
func (c *CommConnector) Stop() {
	c.cancel()
	close(c.done)
	c.wg.Wait()
}

// SetupCommAgent is a convenience helper that wires an existing agent into
// the bus: it registers the agent, creates a SendMessageTool for it,
// and optionally starts a CommConnector for auto-receive.
// Returns the inbox channel, the send tool, and the connector (if autoReceive).
// On error (e.g. name already registered) the returned inbox is nil and the
// caller must not use the other return values.
func SetupCommAgent(bus *AgentBus, name string, agent *Agent, autoReceive bool) (<-chan CommMessage, *SendMessageTool, *CommConnector, error) {
	inbox, err := bus.Register(name)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("register %q on bus: %w", name, err)
	}

	sendTool := &SendMessageTool{
		Bus:      bus,
		FromName: name,
	}

	var connector *CommConnector
	if autoReceive {
		connector = NewCommConnector(agent, inbox)
	}

	return inbox, sendTool, connector, nil
}
