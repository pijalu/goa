// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// nativeArgumentClass describes provider-supplied native tool arguments before
// any concrete tool sees them. Only a complete JSON object is executable.
type nativeArgumentClass int

const (
	nativeArgumentsValid nativeArgumentClass = iota
	nativeArgumentsTruncated
	nativeArgumentsMalformed
	nativeArgumentsWrongShape
)

// maxNativeArgumentReissues bounds provider-error recovery independently of
// optional loop/round guards. The initial rejection plus two reissue rounds may
// continue; the next malformed native call ends the turn.
const maxNativeArgumentReissues = 2

// ToolProtocolResultPrefix identifies provider-protocol failures that never
// reached a concrete tool. Renderers must not present these as successful runs.
const ToolProtocolResultPrefix = "Error: [provider error:"

type toolProtocolRejection struct {
	message  string
	terminal bool
}

func classifyNativeToolArguments(raw string) nativeArgumentClass {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nativeArgumentsValid
	}
	if json.Valid([]byte(trimmed)) {
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &object); err != nil || object == nil {
			return nativeArgumentsWrongShape
		}
		return nativeArgumentsValid
	}

	// Decoder distinguishes unexpected EOF from other syntax failures. Merely
	// checking json.Valid cannot separate provider truncation from malformed
	// JSON, and repairing before execution could turn a partial edit into a write.
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	var value any
	err := decoder.Decode(&value)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return nativeArgumentsTruncated
	}
	return nativeArgumentsMalformed
}

func nativeArgumentErrorCode(class nativeArgumentClass) string {
	switch class {
	case nativeArgumentsTruncated:
		return "truncated_tool_arguments"
	case nativeArgumentsWrongShape:
		return "invalid_tool_argument_shape"
	default:
		return "invalid_tool_arguments"
	}
}

func nativeArgumentGuidance(class nativeArgumentClass) string {
	if class == nativeArgumentsWrongShape {
		return "Native tool arguments must be one complete JSON object. The tool was not executed. Reissue one complete native tool call with an arguments object."
	}
	return "The provider did not supply one complete, valid JSON arguments object. The tool was not executed. Reissue one complete native tool call with full JSON arguments."
}

func newNativeArgumentRejection(class nativeArgumentClass, failures int) toolProtocolRejection {
	code := nativeArgumentErrorCode(class)
	message := fmt.Sprintf("%s %s]\n%s", ToolProtocolResultPrefix, code, nativeArgumentGuidance(class))
	terminal := failures > maxNativeArgumentReissues
	if terminal {
		message = fmt.Sprintf("%s %s: recovery_limit]\nNative tool arguments remained invalid after %d reissue opportunities. The tool was not executed; recovery stopped to prevent an unbounded turn.", ToolProtocolResultPrefix, code, maxNativeArgumentReissues)
	}
	return toolProtocolRejection{message: message, terminal: terminal}
}

// rejectNativeToolCall buffers a non-executing result so assistant/tool history
// remains paired and the model can reissue a complete call. No scheduler task or
// EventToolStart is created for this call.
func (a *Agent) rejectNativeToolCall(tc provider.ContentBlock, class nativeArgumentClass) {
	a.mu.Lock()
	a.nativeArgumentFailures++
	failures := a.nativeArgumentFailures
	rejection := newNativeArgumentRejection(class, failures)
	if a.toolProtocolRejections == nil {
		a.toolProtocolRejections = make(map[string]toolProtocolRejection)
	}
	if tc.ToolCallID != "" {
		a.toolProtocolRejections[tc.ToolCallID] = rejection
	}
	a.bufferedToolCallCount++
	a.mu.Unlock()

	a.emitSyntheticToolResult(tc, rejection.message)
	a.bufferedToolCalls = append(a.bufferedToolCalls, tc)
	delete(a.streamingToolCalls, tc.ToolCallID)
}

func (a *Agent) noteValidNativeToolCall() {
	a.mu.Lock()
	a.nativeArgumentFailures = 0
	a.mu.Unlock()
}
