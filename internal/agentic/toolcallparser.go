// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

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
	toolCallEndTagRE    = regexp.MustCompile(`\s*</tool_call>\s*`)
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
	calls := parseJSONToolCalls(content, idOffset, allowIncomplete)
	if len(calls) > 0 {
		return calls
	}
	return parseFunctionToolCalls(content, idOffset, allowIncomplete)
}

func parseJSONToolCalls(content string, idOffset int, allowIncomplete bool) []parsedToolCall {
	var calls []parsedToolCall
	for _, m := range toolCallJSONStartRE.FindAllStringIndex(content, -1) {
		pc, ok := extractJSONToolCall(content, m[1]-1, allowIncomplete)
		if !ok {
			continue
		}
		pc.id = fmt.Sprintf("call_%d", idOffset+len(calls))
		calls = append(calls, pc)
	}
	return calls
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

func parseFunctionToolCalls(content string, idOffset int, allowIncomplete bool) []parsedToolCall {
	var calls []parsedToolCall
	funcStarts := functionStartRE.FindAllStringSubmatchIndex(content, -1)
	for idx, fm := range funcStarts {
		if insideOpenParameter(content, fm[0]) {
			continue
		}
		pc, ok := extractFunctionToolCall(content, idx, funcStarts, allowIncomplete)
		if !ok {
			continue
		}
		pc.id = fmt.Sprintf("call_%d", idOffset+len(calls))
		calls = append(calls, pc)
	}
	return calls
}

func extractFunctionToolCall(content string, idx int, funcStarts [][]int, allowIncomplete bool) (parsedToolCall, bool) {
	fm := funcStarts[idx]
	name := content[fm[2]:fm[3]]
	body := extractFunctionBody(content, idx, funcStarts, allowIncomplete)
	if body == "" {
		return parsedToolCall{}, false
	}
	args, ok := parseFunctionParameters(body, allowIncomplete)
	if !ok {
		return parsedToolCall{}, false
	}
	return parsedToolCall{name: name, arguments: args}, true
}

func extractFunctionBody(content string, idx int, funcStarts [][]int, allowIncomplete bool) string {
	fm := funcStarts[idx]
	bodyStart := fm[1]
	nextFunc := len(content)
	if idx+1 < len(funcStarts) {
		nextFunc = funcStarts[idx+1][0]
	}
	bodyEnd := nextFunc
	if m := toolCallEndTagRE.FindStringIndex(content[bodyStart:]); m != nil {
		if candidate := bodyStart + m[0]; candidate < bodyEnd {
			bodyEnd = candidate
		}
	}
	body := content[bodyStart:bodyEnd]
	if !allowIncomplete {
		closeIdx := strings.LastIndex(body, "</function>")
		if closeIdx < 0 {
			return ""
		}
		return body[:closeIdx]
	}
	return functionCloseRE.ReplaceAllString(body, "")
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

func insideOpenParameter(content string, pos int) bool {
	lastParamStart := -1
	for _, m := range paramStartRE.FindAllStringIndex(content[:pos], -1) {
		lastParamStart = m[0]
	}
	if lastParamStart < 0 {
		return false
	}
	lastParamClose := strings.LastIndex(content[:pos], "</parameter>")
	lastFuncClose := strings.LastIndex(content[:pos], "</function>")
	return lastParamStart > maxInt(lastParamClose, lastFuncClose)
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
