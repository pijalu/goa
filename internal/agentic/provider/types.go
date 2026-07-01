// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package provider defines the LLM provider domain — types, interfaces, and
// infrastructure for connecting to LLM APIs, handling streaming responses,
// managing model metadata, and routing requests to the correct provider.
//
// The canonical type definitions now live in the provider/schema sub-package.
// This file re-exports them under the provider package for backward
// compatibility during the migration to the config-driven provider architecture.
package provider

import (
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// Re-exported API / Provider identifiers.
type Api = schema.Api
type Provider = schema.Provider
type Transport = schema.Transport
type Role = schema.Role
type StopReason = schema.StopReason
type ContentBlockType = schema.ContentBlockType
type EventType = schema.EventType

const (
	ApiOpenAICompletions    = schema.ApiOpenAICompletions
	ApiOpenAIResponses      = schema.ApiOpenAIResponses
	ApiOpenAICodexResponses = schema.ApiOpenAICodexResponses
	ApiAzureOpenAIResponses = schema.ApiAzureOpenAIResponses
	ApiAnthropicMessages    = schema.ApiAnthropicMessages
	ApiGoogleGenerativeAI   = schema.ApiGoogleGenerativeAI
	ApiGoogleVertex         = schema.ApiGoogleVertex
	ApiMistralConversations = schema.ApiMistralConversations
	ApiBedrockConverse      = schema.ApiBedrockConverse

	ProviderOpenAI     = schema.ProviderOpenAI
	ProviderAnthropic  = schema.ProviderAnthropic
	ProviderGoogle     = schema.ProviderGoogle
	ProviderMistral    = schema.ProviderMistral
	ProviderAWS        = schema.ProviderAWS
	ProviderAzure      = schema.ProviderAzure
	ProviderGitHub     = schema.ProviderGitHub
	ProviderTogether   = schema.ProviderTogether
	ProviderFireworks  = schema.ProviderFireworks
	ProviderGroq       = schema.ProviderGroq
	ProviderPerplexity = schema.ProviderPerplexity
	ProviderDeepSeek   = schema.ProviderDeepSeek
	ProviderOpenRouter = schema.ProviderOpenRouter
	ProviderLMStudio   = schema.ProviderLMStudio
	ProviderOllama     = schema.ProviderOllama
	ProviderKimi       = schema.ProviderKimi
	ProviderKimiCode   = schema.ProviderKimiCode
	ProviderOpenCode   = schema.ProviderOpenCode
	ProviderOpenCodeGo = schema.ProviderOpenCodeGo
	ProviderCustom     = schema.ProviderCustom

	TransportSSE       = schema.TransportSSE
	TransportWebSocket = schema.TransportWebSocket

	RoleSystem     = schema.RoleSystem
	RoleUser       = schema.RoleUser
	RoleAssistant  = schema.RoleAssistant
	RoleToolResult = schema.RoleToolResult

	StopReasonEndTurn         = schema.StopReasonEndTurn
	StopReasonMaxTokens       = schema.StopReasonMaxTokens
	StopReasonStopSequence    = schema.StopReasonStopSequence
	StopReasonToolCall        = schema.StopReasonToolCall
	StopReasonError           = schema.StopReasonError
	StopReasonContentFiltered = schema.StopReasonContentFiltered

	ContentBlockText       = schema.ContentBlockText
	ContentBlockThinking   = schema.ContentBlockThinking
	ContentBlockToolCall   = schema.ContentBlockToolCall
	ContentBlockToolResult = schema.ContentBlockToolResult
	ContentBlockImage      = schema.ContentBlockImage

	EventStart         = schema.EventStart
	EventDone          = schema.EventDone
	EventError         = schema.EventError
	EventTextStart     = schema.EventTextStart
	EventTextDelta     = schema.EventTextDelta
	EventTextEnd       = schema.EventTextEnd
	EventThinkingStart = schema.EventThinkingStart
	EventThinkingDelta = schema.EventThinkingDelta
	EventThinkingEnd   = schema.EventThinkingEnd
	EventToolCallStart = schema.EventToolCallStart
	EventToolCallDelta = schema.EventToolCallDelta
	EventToolCallEnd   = schema.EventToolCallEnd
)

// Re-exported thinking / reasoning types.
type ThinkingLevel = schema.ThinkingLevel
type ThinkingLevelMap = schema.ThinkingLevelMap
type ThinkingBudgets = schema.ThinkingBudgets
type ThinkingFormat = schema.ThinkingFormat

const (
	ThinkingOff     = schema.ThinkingOff
	ThinkingMinimal = schema.ThinkingMinimal
	ThinkingLow     = schema.ThinkingLow
	ThinkingMedium  = schema.ThinkingMedium
	ThinkingHigh    = schema.ThinkingHigh
	ThinkingXHigh   = schema.ThinkingXHigh
	ThinkingMax     = schema.ThinkingMax

	ThinkingFormatNone               = schema.ThinkingFormatNone
	ThinkingFormatThinkingContent    = schema.ThinkingFormatThinkingContent
	ThinkingFormatReasoningContent   = schema.ThinkingFormatReasoningContent
	ThinkingFormatChunkedReasoning   = schema.ThinkingFormatChunkedReasoning
	ThinkingFormatSignatureReasoning = schema.ThinkingFormatSignatureReasoning
	ThinkingFormatSeparateField      = schema.ThinkingFormatSeparateField
	ThinkingFormatNoOutput           = schema.ThinkingFormatNoOutput
	ThinkingFormatTextPrefixed       = schema.ThinkingFormatTextPrefixed
)

// Re-exported caching type.
type CacheRetention = schema.CacheRetention

const (
	CacheRetentionNone  = schema.CacheRetentionNone
	CacheRetentionShort = schema.CacheRetentionShort
	CacheRetentionLong  = schema.CacheRetentionLong
)

// Re-exported core structs.
type ContentBlock = schema.ContentBlock
type Message = schema.Message
type Usage = schema.Usage
type ToolSchema = schema.ToolSchema
type Context = schema.Context
type AssistantMessage = schema.AssistantMessage
type AssistantMessageEvent = schema.AssistantMessageEvent
type AssistantMessageEventStream = schema.AssistantMessageEventStream
type ModelPricing = schema.ModelPricing
type Model = schema.Model
type StreamOptions = schema.StreamOptions
type SimpleStreamOptions = schema.SimpleStreamOptions

// Constructor helpers remain in the provider package for ergonomics.
var (
	NewTextMessage                 = schema.NewTextMessage
	NewUserMessage                 = schema.NewUserMessage
	NewUserMessageWithImage        = schema.NewUserMessageWithImage
	NewSystemMessage               = schema.NewSystemMessage
	NewAssistantMessage            = schema.NewAssistantMessage
	NewToolResultMessage           = schema.NewToolResultMessage
	NewAssistantMessageEventStream = schema.NewAssistantMessageEventStream
)

// Schema aliases for new architecture code.
// VariantProfile and friends are accessed via the schema package directly.
type VariantProfile = schema.VariantProfile

// Re-export schema helpers used by provider internals.
var ResolveProfile = schema.ResolveProfile
var ResolveURLTemplate = schema.ResolveURLTemplate
var MergeProfiles = schema.MergeProfiles

// Time helpers (preserved from original types.go).
type Time = time.Time
