// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// Api identifies the wire protocol / API shape used to communicate with an LLM.
// Each constant corresponds to a distinct message format, streaming protocol,
// and auth mechanism. A single provider may expose multiple APIs.
type Api string

const (
	ApiOpenAICompletions    Api = "openai-completions"
	ApiOpenAIResponses      Api = "openai-responses"
	ApiOpenAICodexResponses Api = "openai-codex-responses"
	ApiAzureOpenAIResponses Api = "azure-openai-responses"
	ApiAnthropicMessages    Api = "anthropic-messages"
	ApiGoogleGenerativeAI   Api = "google-generative-ai"
	ApiGoogleVertex         Api = "google-vertex"
	ApiMistralConversations Api = "mistral-conversations"
	ApiBedrockConverse      Api = "bedrock-converse-stream"
)

// Provider identifies an LLM service provider. Multiple providers may share
// the same API protocol.
type Provider string

const (
	ProviderOpenAI     Provider = "openai"
	ProviderAnthropic  Provider = "anthropic"
	ProviderGoogle     Provider = "google"
	ProviderMistral    Provider = "mistral"
	ProviderAWS        Provider = "aws"
	ProviderAzure      Provider = "azure"
	ProviderGitHub     Provider = "github"
	ProviderTogether   Provider = "together"
	ProviderFireworks  Provider = "fireworks"
	ProviderGroq       Provider = "groq"
	ProviderPerplexity Provider = "perplexity"
	ProviderDeepSeek   Provider = "deepseek"
	ProviderOpenRouter Provider = "openrouter"
	ProviderLMStudio   Provider = "lm-studio"
	ProviderOllama     Provider = "ollama"
	ProviderKimi        Provider = "kimi"
	ProviderKimiCode    Provider = "kimi-code"
	ProviderOpenCode    Provider = "opencode"
	ProviderOpenCodeGo  Provider = "opencode-go"
	ProviderCustom      Provider = "custom"
)

// Transport indicates the wire protocol for API communication.
type Transport string

const (
	TransportSSE       Transport = "sse"
	TransportWebSocket Transport = "websocket"
)

// Role identifies the message sender.
type Role string

const (
	RoleSystem     Role = "system"
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "tool_result"
)

// StopReason describes why the LLM stopped generating content.
type StopReason string

const (
	StopReasonEndTurn         StopReason = "end_turn"
	StopReasonMaxTokens       StopReason = "max_tokens"
	StopReasonStopSequence    StopReason = "stop_sequence"
	StopReasonToolCall        StopReason = "tool_call"
	StopReasonError           StopReason = "error"
	StopReasonContentFiltered StopReason = "content_filtered"
)

// ContentBlockType discriminates the types of content blocks in messages.
type ContentBlockType string

const (
	ContentBlockText       ContentBlockType = "text"
	ContentBlockThinking   ContentBlockType = "thinking"
	ContentBlockToolCall   ContentBlockType = "tool_call"
	ContentBlockToolResult ContentBlockType = "tool_result"
	ContentBlockImage      ContentBlockType = "image"
)

// EventType discriminates the types of events in an AssistantMessageEventStream.
type EventType string

const (
	// Lifecycle events
	EventStart EventType = "start"
	EventDone  EventType = "done"
	EventError EventType = "error"

	// Text content events
	EventTextStart EventType = "text_start"
	EventTextDelta EventType = "text_delta"
	EventTextEnd   EventType = "text_end"

	// Thinking/reasoning content events
	EventThinkingStart EventType = "thinking_start"
	EventThinkingDelta EventType = "thinking_delta"
	EventThinkingEnd   EventType = "thinking_end"

	// Tool call events
	EventToolCallStart EventType = "toolcall_start"
	EventToolCallDelta EventType = "toolcall_delta"
	EventToolCallEnd   EventType = "toolcall_end"
)
