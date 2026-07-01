// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// secretKeyPattern matches YAML/JSON keys that likely hold credentials.
var secretKeyPattern = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|auth)$`)

// bearerPattern matches Authorization: Bearer or api-key: headers in log lines.
var bearerPattern = regexp.MustCompile(`(?i)(Authorization:\s*Bearer\s+|api-key:\s*)(\S+)`)

// RedactYAML replaces secret values in YAML-like text with [REDACTED].
// It is intentionally simple and line-oriented; it does not parse full YAML.
func RedactYAML(input string) string {
	lines := strings.Split(input, "\n")
	for i, line := range lines {
		lines[i] = redactYAMLLine(line)
	}
	return strings.Join(lines, "\n")
}

func redactYAMLLine(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return line
	}
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return line
	}
	key := trimmed[:idx]
	rest := trimmed[idx+1:]
	if !secretKeyPattern.MatchString(key) {
		return line
	}
	// Preserve indentation and inline comments.
	leading := line[:len(line)-len(trimmed)]
	commentIdx := strings.Index(rest, " #")
	if commentIdx < 0 {
		commentIdx = strings.Index(rest, "\t#")
	}
	if commentIdx >= 0 {
		return leading + key + ": [REDACTED]" + rest[commentIdx:]
	}
	return leading + key + ": [REDACTED]"
}

// RedactJSON replaces secret string values in JSON text with [REDACTED].
func RedactJSON(input string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(input), &v); err != nil {
		// Not valid JSON: fall back to text redaction.
		return RedactText(input)
	}
	redactValue(v)
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return RedactText(input)
	}
	return string(out)
}

func redactValue(v interface{}) {
	switch node := v.(type) {
	case map[string]interface{}:
		for key, val := range node {
			if secretKeyPattern.MatchString(key) {
				node[key] = redactedValue(val)
				continue
			}
			redactValue(val)
		}
	case []interface{}:
		for i := range node {
			redactValue(node[i])
		}
	}
}

func redactedValue(val interface{}) interface{} {
	if val == nil {
		return nil
	}
	return "[REDACTED]"
}

// RedactText redacts bearer tokens and api-key headers in arbitrary text.
func RedactText(input string) string {
	return bearerPattern.ReplaceAllString(input, "${1}[REDACTED]")
}

// RedactBytes is a convenience wrapper around RedactText.
func RedactBytes(input []byte) []byte {
	return bytes.ReplaceAll([]byte(RedactText(string(input))), []byte("\r\n"), []byte("\n"))
}
