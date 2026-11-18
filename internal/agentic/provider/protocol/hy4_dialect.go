// SPDX-License-Identifier: GPL-3.0-or-later

package protocol

import (
	"encoding/json"
	"strings"
)

// hy4ToolCall is a tagged call recovered from a complete wrapper, or an
// explicitly non-executable diagnostic shape for an incomplete wrapper.
type hy4ToolCall struct {
	Name      string
	Arguments string
	Complete  bool
}

type hy4Result struct {
	Text  string
	Calls []hy4ToolCall
}

// hy4Normalizer owns independent streaming state for content and reasoning.
// A wrapper is withheld until its matching close arrives so malformed or
// truncated calls can never become executable midway through a stream.
type hy4Normalizer struct {
	content  hy4Channel
	thinking hy4Channel
}

func (n *hy4Normalizer) Content(delta string) hy4Result {
	return n.content.normalize(delta, false)
}

func (n *hy4Normalizer) Thinking(delta string) hy4Result {
	return n.thinking.normalize(delta, false)
}

func (n *hy4Normalizer) Finish() (content, thinking hy4Result) {
	content = n.content.normalize("", true)
	thinking = n.thinking.normalize("", true)
	return content, thinking
}

type hy4Channel struct {
	pending string
}

type hy4Marker struct {
	Name    string
	Suffix  string
	Closing bool
	Length  int
}

type hy4MarkerStatus int

const (
	hy4MarkerInvalid hy4MarkerStatus = iota
	hy4MarkerPartial
	hy4MarkerValid
)

var hy4MarkerNames = []string{"think", "tool_calls", "tool_call", "arg_key", "arg_value"}

func (c *hy4Channel) normalize(delta string, eof bool) hy4Result {
	input := c.pending + delta
	c.pending = ""
	var result hy4Result
	var clean strings.Builder

	for len(input) > 0 {
		rel := strings.IndexByte(input, '<')
		if rel < 0 {
			clean.WriteString(input)
			break
		}
		clean.WriteString(input[:rel])
		input = input[rel:]
		step := nextHy4Step(input, eof)
		clean.WriteString(step.Text)
		result.Calls = append(result.Calls, step.Calls...)
		if step.Hold {
			c.pending = input
			break
		}
		input = input[step.Consumed:]
	}
	result.Text = clean.String()
	return result
}

type hy4Step struct {
	Text     string
	Calls    []hy4ToolCall
	Consumed int
	Hold     bool
}

func nextHy4Step(input string, eof bool) hy4Step {
	marker, status := scanHy4Marker(input)
	switch status {
	case hy4MarkerInvalid:
		return hy4Step{Text: "<", Consumed: 1}
	case hy4MarkerPartial:
		return partialHy4Step(input, eof)
	}
	if marker.Name == "tool_calls" && !marker.Closing {
		return wrapperHy4Step(input, marker, eof)
	}
	// Every other known suffixed marker is protocol framing. Unknown XML never
	// reaches this branch and is copied byte-for-byte by the invalid step.
	return hy4Step{Consumed: marker.Length}
}

func partialHy4Step(input string, eof bool) hy4Step {
	if !eof {
		return hy4Step{Hold: true}
	}
	if confirmedHy4MarkerPrefix(input) {
		return hy4Step{Consumed: len(input)}
	}
	return hy4Step{Text: input, Consumed: len(input)}
}

func wrapperHy4Step(input string, marker hy4Marker, eof bool) hy4Step {
	consumed, calls, complete := consumeHy4Wrapper(input, marker)
	if !complete && !eof {
		return hy4Step{Hold: true}
	}
	return hy4Step{Calls: calls, Consumed: consumed}
}

// consumeHy4Wrapper finds only the close carrying the opening tag's suffix.
// At EOF an unclosed wrapper is consumed as control bytes, but all identifiable
// calls inside are marked incomplete for the native non-executing gate.
func consumeHy4Wrapper(input string, marker hy4Marker) (consumed int, calls []hy4ToolCall, complete bool) {
	closeTag := "</tool_calls:" + marker.Suffix + ">"
	rel := strings.Index(input[marker.Length:], closeTag)
	if rel < 0 {
		calls = parseHy4Calls(input[marker.Length:], marker.Suffix, false)
		return len(input), calls, false
	}
	bodyEnd := marker.Length + rel
	calls = parseHy4Calls(input[marker.Length:bodyEnd], marker.Suffix, true)
	return bodyEnd + len(closeTag), calls, true
}

func scanHy4Marker(input string) (hy4Marker, hy4MarkerStatus) {
	end := strings.IndexByte(input, '>')
	if end < 0 {
		if possibleHy4Marker(input) {
			return hy4Marker{}, hy4MarkerPartial
		}
		return hy4Marker{}, hy4MarkerInvalid
	}
	raw := input[1:end]
	closing := strings.HasPrefix(raw, "/")
	raw = strings.TrimPrefix(raw, "/")
	name, suffix, ok := strings.Cut(raw, ":")
	if !ok || suffix == "" || !knownHy4MarkerName(name) || !hexOnly(suffix) {
		return hy4Marker{}, hy4MarkerInvalid
	}
	return hy4Marker{Name: name, Suffix: suffix, Closing: closing, Length: end + 1}, hy4MarkerValid
}

func knownHy4MarkerName(name string) bool {
	for _, known := range hy4MarkerNames {
		if name == known {
			return true
		}
	}
	return false
}

func possibleHy4Marker(input string) bool {
	for _, name := range hy4MarkerNames {
		for _, closing := range []bool{false, true} {
			prefix := hy4MarkerPrefix(name, closing)
			if len(input) <= len(prefix) && strings.HasPrefix(prefix, input) {
				return true
			}
			if strings.HasPrefix(input, prefix) && hexOnly(input[len(prefix):]) {
				return true
			}
		}
	}
	return false
}

func confirmedHy4MarkerPrefix(input string) bool {
	for _, name := range hy4MarkerNames {
		for _, closing := range []bool{false, true} {
			prefix := hy4MarkerPrefix(name, closing)
			if len(input) >= len(prefix) && strings.HasPrefix(input, prefix) && hexOnly(input[len(prefix):]) {
				return true
			}
		}
	}
	return false
}

func hy4MarkerPrefix(name string, closing bool) string {
	if closing {
		return "</" + name + ":"
	}
	return "<" + name + ":"
}

func hexOnly(value string) bool {
	for _, b := range []byte(value) {
		if !((b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')) {
			return false
		}
	}
	return true
}

type hy4Argument struct {
	Key   string
	Value string
}

// parseHy4Calls parses call boundaries separately from argument conversion.
// wrapperComplete is deliberately applied to every call: a closed inner call
// inside a truncated outer wrapper must still not execute.
func parseHy4Calls(body, suffix string, wrapperComplete bool) []hy4ToolCall {
	open := "<tool_call:" + suffix + ">"
	closeTag := "</tool_call:" + suffix + ">"
	var calls []hy4ToolCall
	for pos := 0; pos < len(body); {
		rel := strings.Index(body[pos:], open)
		if rel < 0 {
			break
		}
		start := pos + rel + len(open)
		closeRel := strings.Index(body[start:], closeTag)
		nextRel := strings.Index(body[start:], open)
		if closeRel < 0 || (nextRel >= 0 && nextRel < closeRel) {
			end := len(body)
			if nextRel >= 0 {
				end = start + nextRel
			}
			if call, ok := makeHy4Call(body[start:end], suffix, false); ok {
				calls = append(calls, call)
			}
			pos = end
			continue
		}
		end := start + closeRel
		if call, ok := makeHy4Call(body[start:end], suffix, wrapperComplete); ok {
			calls = append(calls, call)
		}
		pos = end + len(closeTag)
	}
	return calls
}

func makeHy4Call(body, suffix string, wrapperComplete bool) (hy4ToolCall, bool) {
	args, name, argumentsComplete := parseHy4Arguments(body, suffix)
	name = strings.TrimSpace(name)
	if name == "" {
		return hy4ToolCall{}, false
	}
	if !wrapperComplete || !argumentsComplete {
		// Keep the call identifiable while forcing the existing native argument
		// classifier to emit its typed, non-executing truncated-call diagnostic.
		return hy4ToolCall{Name: name, Arguments: "{", Complete: false}, true
	}
	return hy4ToolCall{Name: name, Arguments: encodeHy4Arguments(args), Complete: true}, true
}

func parseHy4Arguments(body, suffix string) (args []hy4Argument, name string, complete bool) {
	keyOpen := "<arg_key:" + suffix + ">"
	keyClose := "</arg_key:" + suffix + ">"
	valueOpen := "<arg_value:" + suffix + ">"
	valueClose := "</arg_value:" + suffix + ">"
	complete = true
	firstKey := strings.Index(body, keyOpen)
	if firstKey < 0 {
		return nil, body, true
	}
	name = body[:firstKey]
	pos := firstKey
	for pos < len(body) {
		rel := strings.Index(body[pos:], keyOpen)
		if rel < 0 {
			break
		}
		keyStart := pos + rel + len(keyOpen)
		keyEndRel := strings.Index(body[keyStart:], keyClose)
		if keyEndRel < 0 {
			return args, name, false
		}
		keyEnd := keyStart + keyEndRel
		key := strings.TrimSpace(body[keyStart:keyEnd])
		afterKey := keyEnd + len(keyClose)
		valueStartRel := strings.Index(body[afterKey:], valueOpen)
		nextKeyRel := strings.Index(body[afterKey:], keyOpen)
		if valueStartRel < 0 || (nextKeyRel >= 0 && nextKeyRel < valueStartRel) {
			return args, name, false
		}
		valueStart := afterKey + valueStartRel + len(valueOpen)
		valueEndRel := strings.Index(body[valueStart:], valueClose)
		nextKeyRel = strings.Index(body[valueStart:], keyOpen)
		if valueEndRel < 0 || (nextKeyRel >= 0 && nextKeyRel < valueEndRel) {
			return args, name, false
		}
		valueEnd := valueStart + valueEndRel
		args = append(args, hy4Argument{Key: key, Value: body[valueStart:valueEnd]})
		pos = valueEnd + len(valueClose)
	}
	return args, name, complete
}

func encodeHy4Arguments(args []hy4Argument) string {
	if len(args) == 0 {
		return "{}"
	}
	var out strings.Builder
	out.WriteByte('{')
	for i, arg := range args {
		if i > 0 {
			out.WriteByte(',')
		}
		key, _ := json.Marshal(arg.Key)
		out.Write(key)
		out.WriteByte(':')
		out.WriteString(hy4ArgumentValue(arg.Value))
	}
	out.WriteByte('}')
	return out.String()
}

func hy4ArgumentValue(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" && json.Valid([]byte(trimmed)) {
		return trimmed
	}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
