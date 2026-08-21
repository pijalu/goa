package openairesponses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// streamResponsesWebSocket is the Codex WS send path. Before building the
// body it runs the incremental decision (6b.2): when the stored session
// baseline proves the request is a strict append with identical properties,
// the body carries only the delta input chained by previous_response_id;
// otherwise it carries the full history. A WS-unsupported handshake failure
// (UPGRADE_REQUIRED) marks the session for the per-session SSE fallback and
// retries as full-history SSE with the same intent; other flavors and all
// fallback sends keep the prebuilt full-history body.
func streamResponsesWebSocket(model provider.Model, ctx provider.Context, opts provider.StreamOptions, bodyBytes []byte, baseURL string, flavor string) (*provider.AssistantMessageEventStream, error) {
	send := buildCodexWSSend(model, ctx, opts, bodyBytes, flavor)
	if send.useSSEFallback {
		return sendResponsesSSE(ctx, opts, send.bodyBytes, baseURL, flavor)
	}
	replayTurnStateWS(send, flavor)
	response, err := doResponsesWebSocket(ctx.GoContext(), opts, send.bodyBytes, baseURL, flavor)
	if err != nil && isWSUnsupportedError(err) && send.sessionKey != "" {
		return handleWSFallback(model, ctx, opts, baseURL, flavor, send)
	}
	if err != nil {
		return nil, err
	}
	captureTurnStateWS(send, flavor, response)
	return newWSResponseStream(ctx.GoContext(), response, send), nil
}

// replayTurnStateWS injects the captured turn-state token into the WS request
// body's client_metadata (Codex only). No-op when no token is stored.
func replayTurnStateWS(send *codexWSSend, flavor string) {
	if flavor != "codex" || send.sessionKey == "" {
		return
	}
	if ts := turnState(send.sessionKey); ts != "" {
		send.bodyBytes = injectTurnStateMetadata(send.bodyBytes, ts)
	}
}

// captureTurnStateWS stores the turn-state token from the WS response headers
// (Codex only). No-op when the header is absent or the session is unpinned.
func captureTurnStateWS(send *codexWSSend, flavor string, response *transport.TransportResponse) {
	if flavor != "codex" || send.sessionKey == "" || response == nil || response.Headers == nil {
		return
	}
	if ts := response.Headers[turnStateHeader]; ts != "" {
		captureTurnState(send.sessionKey, ts)
	}
}

// handleWSFallback marks the session for SSE fallback and retries with the
// full-history body.
func handleWSFallback(model provider.Model, ctx provider.Context, opts provider.StreamOptions, baseURL string, flavor string, send *codexWSSend) (*provider.AssistantMessageEventStream, error) {
	markWSFallback(send.sessionKey)
	fullBytes, fullErr := buildResponsesBodyBytes(model, ctx, opts, flavor)
	if fullErr != nil {
		return nil, fullErr
	}
	return sendResponsesSSE(ctx, opts, fullBytes, baseURL, flavor)
}

// codexWSSend holds one resolved WS send: the wire body, the baseline
// bookkeeping needed once the response completes, and the routing decision.
type codexWSSend struct {
	bodyBytes []byte
	// sessionKey pins the baseline/fallback to the session cache key; empty
	// means the request cannot be pinned (baseline capture is skipped).
	sessionKey string
	// fingerprint is the property fingerprint of the full-history request;
	// recorded with the baseline so the next turn can test property match.
	fingerprint requestFingerprint
	// lastInput is the full logical conversation (a deep copy of ctx.Messages).
	// On a full send it is exactly what went on the wire. On a delta send the
	// wire carried only the tail, but the prefix check already proved
	// ctx.Messages = baseline conversation + delta, so recording the full
	// conversation keeps the next turn's strict-append check correct even
	// though the server only echoes its own output items in response.completed.
	lastInput []provider.Message
	// recordBaseline enables baseline capture on a successful completed
	// response (Codex with a non-empty session key).
	recordBaseline bool
	// useSSEFallback routes the send to the SSE transport (session marked
	// WS-unsupported on a previous turn).
	useSSEFallback bool
}

// buildCodexWSSend resolves the send plan. Non-Codex flavors pass the prebuilt
// body through untouched. Codex consults the incremental decision against the
// current baseline; a retry therefore always rebuilds from the latest
// baseline, never reusing a stale previous_response_id.
func buildCodexWSSend(model provider.Model, ctx provider.Context, opts provider.StreamOptions, bodyBytes []byte, flavor string) *codexWSSend {
	send := &codexWSSend{bodyBytes: bodyBytes}
	if flavor != "codex" {
		return send
	}
	send.sessionKey = wsBaselineSessionKey(opts)
	if isWSFallback(send.sessionKey) {
		send.useSSEFallback = true
		return send
	}
	if send.sessionKey == "" {
		return send
	}
	send.recordBaseline = true
	send.lastInput = deepCopyMessages(ctx.Messages)
	fingerprint, err := fingerprintOfBodyBytes(bodyBytes)
	if err != nil {
		// An unparseable body cannot be fingerprinted; send full history as-is.
		return send
	}
	send.fingerprint = fingerprint
	baseline := wsBaseline(send.sessionKey)
	var prevProps requestFingerprint
	if baseline != nil {
		prevProps = baseline.Fingerprint
	}
	delta, responseID, ok := decideIncremental(baseline, prevProps, fingerprint, ctx.Messages, false)
	if !ok {
		return send
	}
	deltaBytes, err := buildCodexDeltaBodyBytes(bodyBytes, delta, responseID)
	if err != nil {
		// A delta body that cannot be built must never send; fall back to the
		// full-history bytes with the baseline left untouched.
		return send
	}
	send.bodyBytes = deltaBytes
	return send
}

// fingerprintOfBodyBytes decodes the marshaled request body back into a
// property fingerprint (kept in the file that owns the body build).
func fingerprintOfBodyBytes(bodyBytes []byte) (requestFingerprint, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		return requestFingerprint{}, fmt.Errorf("decode request body: %w", err)
	}
	return fingerprintFromBody(body), nil
}

// buildCodexDeltaBodyBytes rewrites the full-history body bytes into the
// incremental form: input = delta tail, chained by previous_response_id. Every
// other matched field stays byte-identical to the full-history request. The
// rewrite happens on the decoded body so no field is dropped or reordered.
func buildCodexDeltaBodyBytes(fullBodyBytes []byte, delta []provider.Message, responseID string) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(fullBodyBytes, &body); err != nil {
		return nil, fmt.Errorf("decode full body: %w", err)
	}
	body["input"] = convertResponsesInput(delta, "")
	body["previous_response_id"] = responseID
	out, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal delta request: %w", err)
	}
	return out, nil
}

// doResponsesWebSocket executes the one-shot WS POST over the shared
// session-affine transport. Auth and Codex headers are built here (provider
// side); the shared transport carries no auth logic.
func doResponsesWebSocket(ctx context.Context, opts provider.StreamOptions, body []byte, baseURL string, flavor string) (*transport.TransportResponse, error) {
	response, err := (&transport.WebSocketTransport{
		HeaderTimeout: opts.WebsocketConnectTimeout,
	}).Do(ctx, &transport.TransportRequest{Method: "POST", URL: websocketURL(baseURL), Headers: websocketHeaders(opts, flavor), Body: body})
	if err != nil {
		return nil, fmt.Errorf("OpenAI Responses WebSocket: %w", err)
	}
	return response, nil
}

// newWSResponseStream wraps the WS response body in the event stream parser.
// When the send carries baseline bookkeeping (Codex with a session key), a
// successful response.completed records the session baseline (last input +
// response id + added items + the matched fingerprint) so later turns can
// chain by previous_response_id. A failed/errored/cancelled stream leaves the
// previous baseline intact.
func newWSResponseStream(ctx context.Context, response *transport.TransportResponse, send *codexWSSend) *provider.AssistantMessageEventStream {
	stream := provider.NewAssistantMessageEventStream(256)
	go provider.CloseStreamOnCancel(ctx, stream)
	if send.recordBaseline {
		go parseResponsesWebSocketBaseline(response.Body, stream, send.sessionKey, send.lastInput, send.fingerprint)
		return stream
	}
	go parseResponsesWebSocket(response.Body, stream)
	return stream
}

// websocketHeaders builds the request headers for a WS send: the caller's
// headers plus auth, Codex session headers, and session affinity. Codex flavor
// adds originator/account and the experimental beta/accept headers. The session
// id header prefers the explicit prompt_cache_key (the Codex session id),
// falling back to SessionID.
func websocketHeaders(opts provider.StreamOptions, flavor string) map[string]string {
	headers := cloneHeaders(opts.Headers)
	headers["Content-Type"] = "application/json"
	if opts.APIKey != "" {
		headers["Authorization"] = "Bearer " + opts.APIKey
	}
	if flavor == "codex" {
		applyCodexWSHeaders(headers, opts)
	}
	if opts.PromptCacheKey != "" {
		headers["X-Session-ID"] = opts.PromptCacheKey
	} else {
		headers["X-Session-ID"] = opts.SessionID
	}
	return headers
}

// applyCodexWSHeaders sets the Codex-specific WS headers (originator, account
// id, experimental beta, SSE accept) without overriding caller-supplied values.
func applyCodexWSHeaders(headers map[string]string, opts provider.StreamOptions) {
	if headers["originator"] == "" {
		headers["originator"] = "goa"
	}
	if opts.CodexAccountID == "" {
		return
	}
	headers["chatgpt-account-id"] = opts.CodexAccountID
	if headers["OpenAI-Beta"] == "" {
		headers["OpenAI-Beta"] = "responses=experimental"
	}
	if headers["accept"] == "" {
		headers["accept"] = "text/event-stream"
	}
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

// parseResponsesWebSocketBaseline parses the WS event stream like
// parseResponsesWebSocket but, on a successful response.completed, records the
// session baseline (response id + added output items + the last input passed
// in). The baseline is recorded the moment the response.completed chunk is
// captured — which happens before handleResponsesCompleted calls stream.End()
// — so a consumer that drains the stream and immediately issues the next
// (chained) turn always observes the new baseline. A failed/errored/cancelled
// stream never emits response.completed, so it leaves the previous baseline
// intact. It also captures the turn-state token from response.metadata events
// for sticky routing within the turn.
func parseResponsesWebSocketBaseline(body io.ReadCloser, stream *provider.AssistantMessageEventStream, sessionKey string, lastInput []provider.Message, fingerprint requestFingerprint) {
	defer body.Close()
	cap := &baselineCapture{}
	parseResponsesSSEWithHook(body, stream, func(rawChunk string, endedOK bool) {
		// Record the WS session baseline at the moment response.completed is
		// captured, before the stream's End() unblocks the consumer.
		if cap.capture(rawChunk) {
			recordWSBaseline(sessionKey, lastInput, cap.responseID, cap.addedItems, fingerprint)
		}
		// Capture turn-state from response.metadata events (Codex WS path).
		if rawChunk != "" {
			captureTurnStateFromMetadataEvent(rawChunk, sessionKey)
		}
	})
}

// captureTurnStateFromMetadataEvent extracts the turn-state token from a
// response.metadata event chunk and stores it for sessionKey. Best-effort:
// any parse failure or absent header is a silent no-op.
func captureTurnStateFromMetadataEvent(rawChunk, sessionKey string) {
	var event struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(rawChunk), &event) != nil || event.Type != "response.metadata" {
		return
	}
	var meta struct {
		Headers map[string]string `json:"headers"`
	}
	if json.Unmarshal([]byte(rawChunk), &meta) != nil {
		return
	}
	if ts := meta.Headers[turnStateHeader]; ts != "" {
		captureTurnState(sessionKey, ts)
	}
}