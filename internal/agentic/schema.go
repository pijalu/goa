// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

// FieldError carries structured information about a single validation failure.
type FieldError struct {
	Field       string   // JSON path, e.g. "entity"
	Type        string   // e.g. "required", "enum", "type"
	Description string   // Human-readable description
	ValidValues []string // For enum failures; empty otherwise
}

// ValidationError carries structured information about schema validation failures.
type ValidationError struct {
	msg    string       // Flattened error string (backward compat)
	Fields []FieldError // Structured details per failing field
}

// Error implements the error interface for backward compatibility.
func (e *ValidationError) Error() string { return e.msg }

// extractEnumFromSchema looks up the "enum" array for a given property in the schema.
func extractEnumFromSchema(schema map[string]interface{}, property string) []string {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return nil
	}
	propDef, ok := props[property].(map[string]interface{})
	if !ok {
		return nil
	}
	// Handle both []interface{} and []string
	switch raw := propDef["enum"].(type) {
	case []interface{}:
		var result []string
		for _, v := range raw {
			if s, ok := v.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case []string:
		return raw
	}
	return nil
}

// Validate checks if the given input JSON conforms to the provided JSON Schema.
// Returns a *ValidationError describing all validation failures, or nil if valid.
//
// This function also rejects concatenated JSON (multiple objects/values in a
// single string), which some LLMs produce when confused about tool parameters.
func Validate(schema map[string]interface{}, input string) *ValidationError {
	// Pre-check: detect concatenated or malformed JSON.
	// gojsonschema.NewStringLoader only parses the first JSON value and
	// silently ignores trailing data, which causes llama.cpp to fail when
	// the malformed input is echoed back in conversation history.
	if err := validateSingleJSON(input); err != nil {
		return &ValidationError{msg: err.Error()}
	}

	schemaLoader := gojsonschema.NewGoLoader(schema)
	docLoader := gojsonschema.NewStringLoader(input)

	result, err := gojsonschema.Validate(schemaLoader, docLoader)
	if err != nil {
		return &ValidationError{msg: err.Error()}
	}

	if !result.Valid() {
		return buildValidationError(result, schema)
	}
	return nil
}

func buildValidationError(result *gojsonschema.Result, schema map[string]interface{}) *ValidationError {
	var msgs []string
	fields := make([]FieldError, 0, len(result.Errors()))
	for _, e := range result.Errors() {
		msgs = append(msgs, e.String())
		fe := FieldError{
			Field:       e.Field(),
			Type:        e.Type(),
			Description: e.Description(),
		}
		if fe.Type == "required" {
			fe.Field = requiredPropertyName(e)
		}
		if fe.Type == "enum" {
			fe.ValidValues = extractEnumFromSchema(schema, fe.Field)
		}
		fields = append(fields, fe)
	}
	return &ValidationError{
		msg:    strings.Join(msgs, "; "),
		Fields: fields,
	}
}

func requiredPropertyName(e gojsonschema.ResultError) string {
	if prop, ok := e.Details()["property"]; ok {
		if s, ok := prop.(string); ok {
			return s
		}
	}
	return ""
}

// validateSingleJSON checks that input contains exactly one JSON value with
// no trailing data. This catches the common LLM bug of concatenating multiple
// JSON objects (e.g. {"a":1}{"b":2}).
func validateSingleJSON(input string) error {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil // empty input is fine for tools with no required params
	}

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()

	var first interface{}
	if err := decoder.Decode(&first); err != nil {
		return fmt.Errorf("invalid JSON: %v", err)
	}

	// Check for trailing non-whitespace data after the first value.
	// decoder.More() is for arrays/objects; we check if there's another token.
	if decoder.More() {
		return fmt.Errorf("invalid JSON: multiple values detected (concatenated objects). Provide a single valid JSON object")
	}

	// Try to decode another token — if successful, there's trailing data.
	if decoder.Decode(&first) == nil {
		return fmt.Errorf("invalid JSON: trailing data after valid JSON object. Provide a single valid JSON object")
	}

	return nil
}
