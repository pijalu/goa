// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bedrock

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func init() { provider.RegisterApiProvider(&BedrockProvider{}) }

type BedrockProvider struct{}

func (p *BedrockProvider) API() provider.Api { return provider.ApiBedrockConverse }

func (p *BedrockProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	return streamBedrock(model, ctx, opts)
}

func (p *BedrockProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

const (
	bedrockService = "bedrock"
	defaultRegion  = "us-east-1"
)

// awsCreds holds AWS credentials for SigV4 signing.
type awsCreds struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
}

// resolveCredentials loads AWS credentials from env vars and AWS profile chain.
func resolveCredentials() awsCreds {
	creds := awsCreds{
		AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		Region:          resolveAWSRegion(),
	}
	return creds
}

func resolveAWSRegion() string {
	if r := os.Getenv("AWS_REGION"); r != "" {
		return r
	}
	if r := os.Getenv("AWS_DEFAULT_REGION"); r != "" {
		return r
	}
	return defaultRegion
}

func streamBedrock(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	stream := provider.NewAssistantMessageEventStream(256)

	creds := resolveCredentials()
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return nil, fmt.Errorf("AWS credentials required: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY")
	}

	modelID := model.ID
	if modelID == "" {
		modelID = "anthropic.claude-sonnet-4-20250514"
	}

	region := creds.Region
	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com", region)

	// Bedrock Converse Stream API
	action := fmt.Sprintf("/model/%s/converse-stream", modelID)

	body := buildBedrockBody(model, ctx, opts)
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx.GoContext(), "POST", endpoint+action, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Sign the request with AWS Signature V4
	if err := signRequest(req, bodyBytes, creds, bedrockService); err != nil {
		return nil, fmt.Errorf("sign request: %w", err)
	}

	client := provider.NewStreamingHTTPClient()

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bedrock request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		bodyErr, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("bedrock returned %d: %s", resp.StatusCode, string(bodyErr))
	}

	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseBedrockStream(resp.Body, stream)
	return stream, nil
}

func buildBedrockBody(model provider.Model, ctx provider.Context, opts provider.StreamOptions) map[string]interface{} {
	body := map[string]interface{}{
		"messages":        convertBedrockMessages(ctx.Messages),
		"inferenceConfig": map[string]interface{}{},
	}

	if ctx.SystemPrompt != "" {
		body["system"] = []map[string]interface{}{
			{"text": ctx.SystemPrompt},
		}
	}

	infConfig := body["inferenceConfig"].(map[string]interface{})
	if opts.MaxTokens > 0 {
		infConfig["maxTokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		infConfig["temperature"] = *opts.Temperature
	}

	if len(ctx.Tools) > 0 {
		body["toolConfig"] = map[string]interface{}{
			"tools": convertBedrockTools(ctx.Tools),
		}
	}

	return body
}

func convertBedrockTools(tools []provider.ToolSchema) []map[string]interface{} {
	out := make([]map[string]interface{}, len(tools))
	for i, t := range tools {
		out[i] = map[string]interface{}{
			"toolSpec": map[string]interface{}{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": map[string]interface{}{
					"json": t.InputSchema,
				},
			},
		}
	}
	return out
}

func convertBedrockMessages(messages []provider.Message) []map[string]interface{} {
	var out []map[string]interface{}
	for _, msg := range messages {
		switch msg.Role {
		case provider.RoleUser:
			out = append(out, map[string]interface{}{
				"role":    "user",
				"content": convertBedrockContent(msg.Content),
			})
		case provider.RoleAssistant:
			out = append(out, map[string]interface{}{
				"role":    "assistant",
				"content": convertBedrockAssistantContent(msg.Content),
			})
		case provider.RoleToolResult:
			for _, block := range msg.Content {
				if block.Type == provider.ContentBlockToolResult {
					out = append(out, map[string]interface{}{
						"role": "user",
						"content": []map[string]interface{}{
							{
								"toolResult": map[string]interface{}{
									"toolUseId": block.ToolCallID,
									"content":   []map[string]interface{}{{"text": block.Text}},
									"status":    map[bool]string{true: "error", false: "success"}[block.IsError],
								},
							},
						},
					})
				}
			}
		}
	}
	return out
}

func convertBedrockContent(blocks []provider.ContentBlock) []map[string]interface{} {
	var result []map[string]interface{}
	for _, b := range blocks {
		switch b.Type {
		case provider.ContentBlockText:
			result = append(result, map[string]interface{}{"text": b.Text})
		case provider.ContentBlockImage:
			result = append(result, map[string]interface{}{
				"image": map[string]interface{}{
					"format": b.ImageMimeType,
					"source": map[string]interface{}{
						"bytes": b.ImageData,
					},
				},
			})
		}
	}
	if len(result) == 0 {
		result = append(result, map[string]interface{}{"text": ""})
	}
	return result
}

func convertBedrockAssistantContent(blocks []provider.ContentBlock) []map[string]interface{} {
	var result []map[string]interface{}
	for _, b := range blocks {
		switch b.Type {
		case provider.ContentBlockText:
			result = append(result, map[string]interface{}{"text": b.Text})
		case provider.ContentBlockToolCall:
			result = append(result, map[string]interface{}{
				"toolUse": map[string]interface{}{
					"toolUseId": b.ToolCallID,
					"name":      b.ToolName,
					"input":     json.RawMessage(b.ToolArguments),
				},
			})
		}
	}
	return result
}

func parseBedrockStream(body io.ReadCloser, stream *provider.AssistantMessageEventStream) {
	defer body.Close()

	bacc := &bedrockStreamAcc{stream: stream}

	err := provider.ParseSSE(body, func(chunk string) {
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(chunk), &event); err != nil {
			return
		}
		switch event.Type {
		case "contentBlockStart":
			bacc.handleContentBlockStart(chunk)
		case "contentBlockDelta":
			bacc.handleContentBlockDelta(chunk)
		case "contentBlockStop":
			bacc.flushToolCalls()
		case "messageStop":
			bacc.handleMessageStop(chunk)
		}
	})

	if err != nil {
		stream.CloseWithError(fmt.Errorf("bedrock SSE error: %w", err))
		return
	}
	if !bacc.started {
		stream.End(&provider.AssistantMessage{})
	}
}

type bedrockStreamAcc struct {
	stream     *provider.AssistantMessageEventStream
	toolAccums []bedrockToolAccum
	contentBuf strings.Builder
	started    bool
}

func (b *bedrockStreamAcc) ensureStart() {
	if b.started {
		return
	}
	b.started = true
	b.stream.Push(provider.AssistantMessageEvent{Type: provider.EventStart, Partial: &provider.AssistantMessage{}})
}

func (b *bedrockStreamAcc) handleContentBlockStart(chunk string) {
	var evt struct {
		Index int `json:"contentBlockIndex"`
		Block struct {
			ToolUse *struct {
				ID   string `json:"toolUseId"`
				Name string `json:"name"`
			} `json:"toolUse"`
		} `json:"contentBlock"`
	}
	json.Unmarshal([]byte(chunk), &evt)
	if evt.Block.ToolUse != nil {
		b.toolAccums = append(b.toolAccums, bedrockToolAccum{
			id: evt.Block.ToolUse.ID, name: evt.Block.ToolUse.Name,
		})
	}
}

func (b *bedrockStreamAcc) handleContentBlockDelta(chunk string) {
	var evt struct {
		Index int `json:"contentBlockIndex"`
		Delta struct {
			Text    string `json:"text"`
			ToolUse *struct {
				Input string `json:"input"`
			} `json:"toolUse"`
		} `json:"delta"`
	}
	json.Unmarshal([]byte(chunk), &evt)

	if evt.Delta.Text != "" {
		b.ensureStart()
		b.contentBuf.WriteString(evt.Delta.Text)
		b.stream.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: evt.Delta.Text})
	}
	if evt.Delta.ToolUse != nil {
		for i := range b.toolAccums {
			if i == evt.Index {
				b.toolAccums[i].args += evt.Delta.ToolUse.Input
			}
		}
	}
}

func (b *bedrockStreamAcc) flushToolCalls() {
	for _, ta := range b.toolAccums {
		if ta.name == "" {
			continue
		}
		b.ensureStart()
		id := ta.id
		if id == "" {
			id = "tool_" + ta.name
		}
		b.stream.Push(provider.AssistantMessageEvent{
			Type: provider.EventToolCallEnd,
			ToolCall: &provider.ContentBlock{
				Type: provider.ContentBlockToolCall, ToolCallID: id,
				ToolName: ta.name, ToolArguments: ta.args,
			},
		})
	}
	b.toolAccums = nil
}

func (b *bedrockStreamAcc) handleMessageStop(chunk string) {
	var evt struct {
		StopReason string `json:"stopReason"`
	}
	json.Unmarshal([]byte(chunk), &evt)

	s := b.contentBuf.String()
	var blocks []provider.ContentBlock
	if s != "" {
		blocks = append(blocks, provider.ContentBlock{Type: provider.ContentBlockText, Text: s})
	}
	b.stream.End(&provider.AssistantMessage{Content: blocks, StopReason: mapBedrockStopReason(evt.StopReason)})
}

type bedrockToolAccum struct {
	id   string
	name string
	args string
}

func mapBedrockStopReason(reason string) provider.StopReason {
	switch reason {
	case "end_turn":
		return provider.StopReasonEndTurn
	case "max_tokens":
		return provider.StopReasonMaxTokens
	case "stop_sequence":
		return provider.StopReasonStopSequence
	case "tool_use":
		return provider.StopReasonToolCall
	case "content_filtered":
		return provider.StopReasonContentFiltered
	default:
		return provider.StopReasonEndTurn
	}
}

// ---------------------------------------------------------------------------
// AWS Signature V4 implementation
// ---------------------------------------------------------------------------

func signRequest(req *http.Request, body []byte, creds awsCreds, service string) error {
	now := time.Now().UTC()
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	bodyHash := sha256Hex(body)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)

	// Canonical request
	canonicalURI := req.URL.Path
	canonicalQueryString := req.URL.RawQuery

	// Sort headers
	var headerNames []string
	for name := range req.Header {
		headerNames = append(headerNames, strings.ToLower(name))
	}
	sort.Strings(headerNames)

	var signedHeaders []string
	var canonicalHeaders strings.Builder
	for _, name := range headerNames {
		value := strings.TrimSpace(req.Header.Get(name))
		canonicalHeaders.WriteString(fmt.Sprintf("%s:%s\n", name, value))
		signedHeaders = append(signedHeaders, name)
	}

	signedHeadersStr := strings.Join(signedHeaders, ";")

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders.String(),
		signedHeadersStr,
		bodyHash,
	)

	// String to sign
	algorithm := "AWS4-HMAC-SHA256"
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", now.Format("20060102"), creds.Region, service)
	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s",
		algorithm,
		now.Format("20060102T150405Z"),
		credentialScope,
		sha256Hex([]byte(canonicalRequest)),
	)

	// Signing key
	signingKey := deriveSigningKey(creds.SecretAccessKey, now.Format("20060102"), creds.Region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Authorization header
	authHeader := fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm, creds.AccessKeyID, credentialScope, signedHeadersStr, signature)
	req.Header.Set("Authorization", authHeader)

	return nil
}

func sha256Hex(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func hmacSHA256(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}
