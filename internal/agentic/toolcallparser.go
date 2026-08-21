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

	// DSML (DeepSeek Markup Language) uses the full-width vertical bar
	// (U+FF5C "｜") as its delimiter: <｜｜DSML｜｜tool_calls> wraps one or more
	// <｜｜DSML｜｜invoke name="fn">…</｜｜DSML｜｜invoke> blocks, each holding
	// <｜｜DSML｜｜parameter name="k" string="true|false">value</｜｜DSML｜｜parameter>
	// entries. DeepSeek-family models fall back to this native text format when
	// the request suppresses structured tool calls (tool_choice "none") — the
	// exact collapse-round case where a dropped call loses user work.
	dsmlInvokeRE = regexp.MustCompile(`<｜｜DSML｜｜invoke\s+name="([^"]+)"\s*>`)
	dsmlParamRE  = regexp.MustCompile(`<｜｜DSML｜｜parameter\s+name="([^"]+)"(?:\s+string="(?:true|false)")?\s*>`)
	// dsmlInvokeClose terminates one invoke block.
	dsmlInvokeClose = "</｜｜DSML｜｜invoke>"

	// Anthropic-legacy tool-use dialect: <invoke name="fn">…</invoke> wrapping
	// <parameter name="k">value</parameter> entries. Models trained on the
	// classic tool-use SFT format (GLM included) degrade into emitting it as
	// plain content when they fall out of native function calling mid-turn —
	// observed live 2026-08-19 (export goa-export-20260819-004622: a goal
	// create arrived as text after a garbled token, displayed verbatim, never
	// executed). The recovery path must recognize it like the other dialects.
	invokeStartRE = regexp.MustCompile(`<invoke\s+name="([^"]+)"\s*>`)
	invokeParamRE = regexp.MustCompile(`<parameter\s+name="([^"]+)"[^>]*>`)
	// invokeClose terminates one invoke block.
	invokeClose = "</invoke>"

	toolClosedPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`),
		regexp.MustCompile(`(?s)<function=[\w-]+>.*?</function>`),
		regexp.MustCompile(`(?s)<｜｜DSML｜｜tool_calls>.*?</｜｜DSML｜｜tool_calls>`),
		regexp.MustCompile(`(?s)<｜｜DSML｜｜invoke\s+name="[^"]+"\s*>.*?</｜｜DSML｜｜invoke>`),
		regexp.MustCompile(`(?s)<invoke\s+name="[^"]+"\s*>.*?</invoke>`),
	}
	toolAllPatterns = append([]*regexp.Regexp(nil), toolClosedPatterns...)

	toolXMLSignals = []string{"<tool_call>", "<function=", "<invoke name", "<｜｜DSML｜｜invoke", "<｜｜DSML｜｜tool_calls>"}
)

func init() {
	toolAllPatterns = append(toolAllPatterns,
		regexp.MustCompile(`(?s)<tool_call>.*$`),
		regexp.MustCompile(`(?s)<function=[\w-]+>.*$`),
		regexp.MustCompile(`(?s)<｜｜DSML｜｜tool_calls>.*$`),
		regexp.MustCompile(`(?s)<｜｜DSML｜｜invoke\s+name="[^"]+"\s*>.*$`),
		regexp.MustCompile(`(?s)<invoke\s+name="[^"]+"\s*>.*$`),
		// Orphan closers: a close tag whose open was already stripped/flushed on
		// an earlier delta must not leak into the display.
		regexp.MustCompile(`(?s)</｜｜DSML｜｜tool_calls>`),
		regexp.MustCompile(`(?s)</｜｜DSML｜｜invoke>`),
		regexp.MustCompile(`(?s)</invoke>`),
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

// hasDSMLSignal reports whether text contains DeepSeek DSML tool-call markup.
// Tracked separately because DSML is recovered unconditionally (it is a
// first-class provider format surfaced when structured calls are suppressed),
// unlike the generic XML forms gated behind AutoHealToolCalls.
func hasDSMLSignal(text string) bool {
	return strings.Contains(text, "<｜｜DSML｜｜invoke") || strings.Contains(text, "<｜｜DSML｜｜tool_calls>")
}

// parseDSMLToolCallsFromText recovers only DSML tool calls (used when the
// generic XML auto-heal opt-in is off but a DeepSeek native call must still be
// honored). It never touches the <tool_call>/<function=name> forms.
func parseDSMLToolCallsFromText(content string, idOffset int, allowIncomplete bool) []parsedToolCall {
	sc := &toolCallScanner{content: content, idOffset: idOffset, allowIncomplete: allowIncomplete}
	return sc.allDSMLCalls()
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
	if calls := sc.allFunctionCalls(); len(calls) > 0 {
		return calls
	}
	// Anthropic-legacy invoke form (see invokeStartRE): GLM-class models
	// degrade into it as plain content when they fall out of native function
	// calling — recover it like the other text dialects.
	sc.pos = 0
	sc.emitted = 0
	if calls := sc.allInvokeCalls(); len(calls) > 0 {
		return calls
	}
	// DSML fallback: DeepSeek-family models emit their native markup when
	// structured tool calls are suppressed (tool_choice "none") — recover it so
	// a well-formed call is never silently dropped.
	sc.pos = 0
	sc.emitted = 0
	return sc.allDSMLCalls()
}

// allDSMLCalls extracts every <｜｜DSML｜｜invoke name="fn">…</｜｜DSML｜｜invoke>
// block (with or without the surrounding tool_calls wrapper).
func (sc *toolCallScanner) allDSMLCalls() []parsedToolCall {
	var calls []parsedToolCall
	for {
		pc, ok := sc.nextDSMLCall()
		if !ok {
			return calls
		}
		calls = append(calls, pc)
	}
}

// allInvokeCalls extracts every <invoke name="fn">…</invoke> block of the
// Anthropic-legacy dialect.
func (sc *toolCallScanner) allInvokeCalls() []parsedToolCall {
	var calls []parsedToolCall
	for {
		pc, ok := sc.nextInvokeCall()
		if !ok {
			return calls
		}
		calls = append(calls, pc)
	}
}

// nextInvokeCall mirrors nextDSMLCall: forward cursor, body bounded at the
// </invoke> close (or, incomplete, at the next invoke open / end of content).
func (sc *toolCallScanner) nextInvokeCall() (parsedToolCall, bool) {
	for {
		rel := invokeStartRE.FindStringSubmatchIndex(sc.content[sc.pos:])
		if rel == nil {
			return parsedToolCall{}, false
		}
		name := sc.content[sc.pos+rel[2] : sc.pos+rel[3]]
		bodyStart := sc.pos + rel[1]

		bodyEnd := len(sc.content)
		end := len(sc.content)
		if ci := strings.Index(sc.content[bodyStart:], invokeClose); ci >= 0 {
			bodyEnd = bodyStart + ci
			end = bodyEnd + len(invokeClose)
		} else if !sc.allowIncomplete {
			// Complete parse requires a close tag; skip this candidate.
			sc.pos = bodyStart
			continue
		} else if m := invokeStartRE.FindStringIndex(sc.content[bodyStart:]); m != nil {
			bodyEnd = bodyStart + m[0]
			end = bodyEnd
		}

		args, ok := parseInvokeParameters(sc.content[bodyStart:bodyEnd])
		if !ok {
			sc.pos = bodyStart
			continue
		}
		sc.pos = end
		return parsedToolCall{id: sc.nextID(), name: name, arguments: args}, true
	}
}

// parseInvokeParameters folds the <parameter name="k">v</parameter> entries
// of one invoke body into a JSON argument object. Values pass through
// verbatim (the receiving tool decodes JSON arrays/objects itself); an empty
// body maps to an empty argument object — same semantics as the DSML form.
func parseInvokeParameters(body string) (string, bool) {
	matches := invokeParamRE.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return "{}", true
	}
	args := make(map[string]string, len(matches))
	for i, pm := range matches {
		key := body[pm[2]:pm[3]]
		valEnd := len(body)
		if i+1 < len(matches) {
			valEnd = matches[i+1][0]
		}
		val := strings.TrimSpace(body[pm[1]:valEnd])
		val = strings.TrimSuffix(val, "</parameter>")
		args[key] = strings.TrimSpace(val)
	}
	b, _ := json.Marshal(args)
	return string(b), true
}

func (sc *toolCallScanner) nextDSMLCall() (parsedToolCall, bool) {
	for {
		rel := dsmlInvokeRE.FindStringSubmatchIndex(sc.content[sc.pos:])
		if rel == nil {
			return parsedToolCall{}, false
		}
		name := sc.content[sc.pos+rel[2] : sc.pos+rel[3]]
		bodyStart := sc.pos + rel[1]

		// Bound the body at the invoke close tag, or (incomplete stream) at the
		// next invoke / tool_calls open, or end of content.
		bodyEnd := len(sc.content)
		end := len(sc.content)
		if ci := strings.Index(sc.content[bodyStart:], dsmlInvokeClose); ci >= 0 {
			bodyEnd = bodyStart + ci
			end = bodyEnd + len(dsmlInvokeClose)
		} else if !sc.allowIncomplete {
			// Complete parse requires a close tag; skip this candidate.
			sc.pos = bodyStart
			continue
		} else if m := dsmlInvokeRE.FindStringIndex(sc.content[bodyStart:]); m != nil {
			bodyEnd = bodyStart + m[0]
			end = bodyEnd
		}

		args, ok := parseDSMLParameters(sc.content[bodyStart:bodyEnd])
		if !ok {
			sc.pos = bodyStart
			continue
		}
		sc.pos = end
		return parsedToolCall{id: sc.nextID(), name: name, arguments: args}, true
	}
}

// parseDSMLParameters folds the DSML parameter entries of one invoke body into
// a JSON argument object. The optional string="true|false" attribute only marks
// whether the value is a quoted string; values are passed through verbatim (the
// receiving tool decodes JSON arrays/objects itself).
func parseDSMLParameters(body string) (string, bool) {
	matches := dsmlParamRE.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		// An invoke with no parameters maps to an empty argument object.
		if strings.TrimSpace(body) == "" {
			return "{}", true
		}
		return "{}", true
	}
	args := make(map[string]string, len(matches))
	for i, pm := range matches {
		key := body[pm[2]:pm[3]]
		valStart := pm[1]
		valEnd := len(body)
		if i+1 < len(matches) {
			valEnd = matches[i+1][0]
		}
		val := strings.TrimSpace(body[valStart:valEnd])
		val = strings.TrimSuffix(val, "</｜｜DSML｜｜parameter>")
		args[key] = strings.TrimSpace(val)
	}
	b, _ := json.Marshal(args)
	return string(b), true
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
