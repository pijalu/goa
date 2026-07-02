// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	toolCallJSONStartRE = regexp.MustCompile(`<tool_call>\s*\{`)
	functionStartRE     = regexp.MustCompile(`<function=([\w-]+)>\s*`)
	toolCallEndTagRE    = regexp.MustCompile(`\s*</tool_call>\s*$`)
	functionCloseRE     = regexp.MustCompile(`\s*</function>\s*$`)
	paramStartRE        = regexp.MustCompile(`<parameter=([\w-]+)>\s*`)
	paramCloseRE        = regexp.MustCompile(`\s*</parameter>\s*$`)

	toolClosedPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`),
		regexp.MustCompile(`(?s)<function=[\w-]+>.*?</function>`),
	}
	toolAllPatterns = append([]*regexp.Regexp(nil), toolClosedPatterns...)

	toolXMLSignals = []string{"<tool_call>", "<function="}
)

func init() {
	toolAllPatterns = append(toolAllPatterns,
		regexp.MustCompile(`(?s)<tool_call>.*$`),
		regexp.MustCompile(`(?s)<function=[\w-]+>.*$`),
	)
}

// hasToolSignal reports whether text contains any tool-call XML signal.
func hasToolSignal(text string) bool {
	for _, s := range toolXMLSignals {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

// stripToolMarkup removes tool-call XML from text.
// When final is false, only fully closed blocks are removed.
// When final is true, trailing incomplete markup is also removed.
func stripToolMarkup(text string, final bool) string {
	pats := toolClosedPatterns
	if final {
		pats = toolAllPatterns
	}
	for _, p := range pats {
		text = p.ReplaceAllString(text, "")
	}
	return text
}

// parsedToolCall is the internal representation extracted from XML.
type parsedToolCall struct {
	id        string
	name      string
	arguments string
}

// parseToolCallsFromText parses OpenAI-style tool_calls from model text.
// It handles <tool_call>{json}</tool_call> and <function=name><parameter=k>v.
// When allowIncomplete is true, missing closing tags are tolerated.
func parseToolCallsFromText(content string, idOffset int, allowIncomplete bool) []parsedToolCall {
	sc := &toolCallScanner{
		content:         content,
		idOffset:        idOffset,
		allowIncomplete: allowIncomplete,
	}
	if calls := sc.allJSONCalls(); len(calls) > 0 {
		return calls
	}
	// Reset the cursor + emitted counter before scanning the function form.
	sc.pos = 0
	sc.emitted = 0
	return sc.allFunctionCalls()
}

// toolCallScanner parses tool-call XML from a content buffer with a single
// forward cursor. Advancing the cursor past each consumed call means a
// <function= or <parameter= token embedded inside a parameter value is
// consumed as part of that value and never mistaken for a top-level marker —
// which removes the repeated full-buffer rescans (and the O(n²)
// insideOpenParameter check) of a pure find-all-then-filter approach.
type toolCallScanner struct {
	content         string
	pos             int // cursor into content
	idOffset        int // base for synthesized call ids
	emitted         int // calls emitted so far (for id assignment)
	allowIncomplete bool
}

func (sc *toolCallScanner) nextID() string {
	id := fmt.Sprintf("call_%d", sc.idOffset+sc.emitted)
	sc.emitted++
	return id
}

// allJSONCalls extracts every <tool_call>{json}</tool_call> block.
func (sc *toolCallScanner) allJSONCalls() []parsedToolCall {
	var calls []parsedToolCall
	for {
		pc, ok := sc.nextJSONCall()
		if !ok {
			return calls
		}
		calls = append(calls, pc)
	}
}

func (sc *toolCallScanner) nextJSONCall() (parsedToolCall, bool) {
	for {
		rel := toolCallJSONStartRE.FindStringIndex(sc.content[sc.pos:])
		if rel == nil {
			return parsedToolCall{}, false
		}
		braceStart := sc.pos + rel[1] - 1
		// Advance past this opening tag so a failed extraction cannot rematch.
		sc.pos += rel[1]
		pc, ok := extractJSONToolCall(sc.content, braceStart, sc.allowIncomplete)
		if ok {
			pc.id = sc.nextID()
			return pc, true
		}
	}
}

// allFunctionCalls extracts every <function=name>...</function> block.
func (sc *toolCallScanner) allFunctionCalls() []parsedToolCall {
	var calls []parsedToolCall
	for {
		pc, ok := sc.nextFunctionCall()
		if !ok {
			return calls
		}
		calls = append(calls, pc)
	}
}

func (sc *toolCallScanner) nextFunctionCall() (parsedToolCall, bool) {
	for {
		rel := functionStartRE.FindStringSubmatchIndex(sc.content[sc.pos:])
		if rel == nil {
			return parsedToolCall{}, false
		}
		nameStart := sc.pos + rel[2]
		nameEnd := sc.pos + rel[3]
		bodyStart := sc.pos + rel[1]
		name := sc.content[nameStart:nameEnd]

		args, end, ok := sc.parseFunctionBody(bodyStart)
		if !ok {
			// Not a valid call at this tag; advance past it and keep scanning.
			sc.pos = bodyStart
			continue
		}
		sc.pos = end
		return parsedToolCall{id: sc.nextID(), name: name, arguments: args}, true
	}
}

// parseFunctionBody scans the body of a function call starting at bodyStart,
// returning the JSON arguments and the absolute cursor position one past the
// consumed body. Because the cursor only moves forward through consumed
// parameter values, a nested <function= token inside a value is absorbed and
// never treated as a top-level boundary.
func (sc *toolCallScanner) parseFunctionBody(bodyStart int) (args string, end int, ok bool) {
	if !sc.allowIncomplete {
		// A complete call must own a </function> close. The body is the text
		// before it; the cursor lands just past the close.
		closeRel := strings.Index(sc.content[bodyStart:], "</function>")
		if closeRel < 0 {
			return "", 0, false
		}
		body := sc.content[bodyStart : bodyStart+closeRel]
		end = bodyStart + closeRel + len("</function>")
		parsed, ok := parseFunctionParameters(body, false)
		if !ok {
			return "", 0, false
		}
		return parsed, end, true
	}

	// Incomplete (streaming): bound the body at the next top-level <function=
	// start (or a trailing </tool_call>) so a subsequent call is not swallowed;
	// otherwise run to end of content. Then strip any partial </function>.
	bodyEnd := len(sc.content)
	if m := functionStartRE.FindStringIndex(sc.content[bodyStart:]); m != nil {
		bodyEnd = bodyStart + m[0]
	}
	if m := toolCallEndTagRE.FindStringIndex(sc.content[bodyStart:]); m != nil {
		if c := bodyStart + m[0]; c < bodyEnd {
			bodyEnd = c
		}
	}
	body := functionCloseRE.ReplaceAllString(sc.content[bodyStart:bodyEnd], "")
	parsed, ok := parseFunctionParameters(body, true)
	if !ok {
		return "", 0, false
	}
	return parsed, bodyEnd, true
}

func extractJSONToolCall(content string, start int, allowIncomplete bool) (parsedToolCall, bool) {
	end, ok := findBalancedBrace(content, start)
	if !ok {
		return parsedToolCall{}, false
	}
	if !allowIncomplete {
		tail := strings.TrimSpace(content[end+1:])
		if !strings.HasPrefix(tail, "</tool_call>") {
			return parsedToolCall{}, false
		}
	}
	jsonStr := content[start : end+1]
	var obj map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &obj); err != nil {
		return parsedToolCall{}, false
	}
	name, _ := obj["name"].(string)
	return parsedToolCall{
		name:      name,
		arguments: marshalArgs(obj["arguments"]),
	}, true
}

func marshalArgs(v any) string {
	if v == nil {
		return "{}"
	}
	switch a := v.(type) {
	case string:
		return a
	case map[string]any:
		b, _ := json.Marshal(a)
		return string(b)
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func findBalancedBrace(s string, start int) (int, bool) {
	depth := 0
	inString := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		switch {
		case inString:
			inString, i = updateStringState(s, i, ch, inString)
		case ch == '"':
			inString = true
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func updateStringState(s string, i int, ch byte, inString bool) (bool, int) {
	if ch == '\\' && i+1 < len(s) {
		return true, i + 1
	}
	if ch == '"' {
		return false, i
	}
	return true, i
}

func parseFunctionParameters(body string, allowIncomplete bool) (string, bool) {
	paramStarts := paramStartRE.FindAllStringSubmatchIndex(body, -1)
	if len(paramStarts) == 0 {
		return "{}", true
	}

	args := make(map[string]string)
	if len(paramStarts) == 1 {
		ok := parseSingleParameter(body, paramStarts[0], args, allowIncomplete)
		if !ok {
			return "", false
		}
	} else {
		ok := parseMultipleParameters(body, paramStarts, args, allowIncomplete)
		if !ok {
			return "", false
		}
	}

	b, _ := json.Marshal(args)
	return string(b), true
}

func parseSingleParameter(body string, pm []int, args map[string]string, allowIncomplete bool) bool {
	key := body[pm[2]:pm[3]]
	val := body[pm[1]:]
	val, ok := finalizeParameterValue(val, allowIncomplete)
	if !ok {
		return false
	}
	args[key] = val
	return true
}

func parseMultipleParameters(body string, paramStarts [][]int, args map[string]string, allowIncomplete bool) bool {
	for pidx, pm := range paramStarts {
		key := body[pm[2]:pm[3]]
		valStart := pm[1]
		nextParam := len(body)
		if pidx+1 < len(paramStarts) {
			nextParam = paramStarts[pidx+1][0]
		}
		val, ok := finalizeParameterValue(body[valStart:nextParam], allowIncomplete)
		if !ok {
			return false
		}
		args[key] = val
	}
	return true
}

func finalizeParameterValue(val string, allowIncomplete bool) (string, bool) {
	if !allowIncomplete {
		val = strings.TrimSpace(val)
		if !strings.HasSuffix(val, "</parameter>") {
			return "", false
		}
		val = val[:len(val)-len("</parameter>")]
		return strings.TrimSpace(val), true
	}
	return strings.TrimSpace(paramCloseRE.ReplaceAllString(val, "")), true
}
