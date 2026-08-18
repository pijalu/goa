package openairesponses

import (
	"fmt"
	"io"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// streamResponsesWebSocket uses the shared session-affine transport and feeds
// its newline-delimited event stream through the normal Responses parser.
func streamResponsesWebSocket(model provider.Model, ctx provider.Context, opts provider.StreamOptions, body []byte, baseURL string, flavor string) (*provider.AssistantMessageEventStream, error) {
	url := websocketURL(baseURL)
	headers := cloneHeaders(opts.Headers)
	headers["Content-Type"] = "application/json"
	if opts.APIKey != "" {
		headers["Authorization"] = "Bearer " + opts.APIKey
	}
	if flavor == "codex" {
		if headers["originator"] == "" {
			headers["originator"] = "goa"
		}
		if opts.CodexAccountID != "" {
			headers["chatgpt-account-id"] = opts.CodexAccountID
			if headers["OpenAI-Beta"] == "" {
				headers["OpenAI-Beta"] = "responses=experimental"
			}
			if headers["accept"] == "" {
				headers["accept"] = "text/event-stream"
			}
		}
	}
	if opts.PromptCacheKey != "" {
		headers["X-Session-ID"] = opts.PromptCacheKey
	} else {
		headers["X-Session-ID"] = opts.SessionID
	}
	response, err := (&transport.WebSocketTransport{
		HeaderTimeout: opts.WebsocketConnectTimeout,
	}).Do(ctx.GoContext(), &transport.TransportRequest{Method: "POST", URL: url, Headers: headers, Body: body})
	if err != nil {
		return nil, fmt.Errorf("OpenAI Responses WebSocket: %w", err)
	}
	stream := provider.NewAssistantMessageEventStream(256)
	go provider.CloseStreamOnCancel(ctx.GoContext(), stream)
	go parseResponsesWebSocket(response.Body, stream)
	return stream, nil
}

func websocketURL(base string) string {
	if base == "" {
		return "wss://api.openai.com/v1/responses"
	}
	if strings.HasPrefix(base, "http://") {
		return strings.Replace(base, "http://", "ws://", 1)
	}
	return strings.Replace(base, "https://", "wss://", 1)
}

func cloneHeaders(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src)+2)
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func parseResponsesWebSocket(body io.ReadCloser, stream *provider.AssistantMessageEventStream) {
	defer body.Close()
	parseResponsesSSE(body, stream)
}
