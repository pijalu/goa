// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"strings"
)

// maxErrorLengthForHint is the threshold above which we don't append a schema
// hint to plain Go errors, to avoid flooding the LLM context.
const maxErrorLengthForHint = 200

// EnrichError takes a tool schema and an error (from Validate or tool.Execute)
// and returns a formatted string with the error + a compact schema hint.
// For *ValidationError, it includes the failing field and required fields.
// For plain Go errors, the hint is only appended if the error is short.
func EnrichError(schema ToolSchema, err error) string {
	// Handle both plain nil and typed nil (*ValidationError)(nil)
	if err == nil {
		return ""
	}
	if v, ok := err.(*ValidationError); ok && v == nil {
		return ""
	}

	baseMsg := err.Error()

	// Build compact schema hint
	hint := buildCompactHint(schema, err)
	if hint == "" {
		return baseMsg
	}

	// For plain Go errors (not ValidationError), suppress hint if error is long
	if _, ok := err.(*ValidationError); !ok {
		if len(baseMsg) >= maxErrorLengthForHint {
			return baseMsg
		}
	}

	return baseMsg + "\nHint: " + hint
}

// buildCompactHint produces a one-line schema summary.
// It only handles flat schemas (properties + required + enum).
func buildCompactHint(schema ToolSchema, err error) string {
	var parts []string

	parts = append(parts, requiredPart(schema))
	if hint := validationErrorHint(err); hint != "" {
		parts = append(parts, hint)
	}
	if hint := enumHint(schema); hint != "" {
		parts = append(parts, hint)
	}

	return strings.Join(parts, ". ")
}

// requiredPart returns the tool name/required-fields summary.
func requiredPart(schema ToolSchema) string {
	required := extractStringSlice(schema.Schema, "required")
	if len(required) > 0 {
		return fmt.Sprintf("Tool '%s' requires: %s", schema.Name, strings.Join(required, ", "))
	}
	return fmt.Sprintf("Tool '%s'", schema.Name)
}

// validationErrorHint formats failing field details from a ValidationError.
func validationErrorHint(err error) string {
	vErr, ok := err.(*ValidationError)
	if !ok || len(vErr.Fields) == 0 {
		return ""
	}
	var hints []string
	for _, f := range vErr.Fields {
		hints = append(hints, fieldHint(f))
	}
	return strings.Join(hints, ", ")
}

// fieldHint returns a human-readable hint for a single validation failure.
func fieldHint(f FieldError) string {
	switch f.Type {
	case "required":
		return fmt.Sprintf("%s is required", f.Field)
	case "enum":
		if len(f.ValidValues) > 0 {
			return fmt.Sprintf("%s must be one of: %s", f.Field, strings.Join(f.ValidValues, ", "))
		}
	case "invalid_type":
		return fmt.Sprintf("%s has wrong type (%s)", f.Field, f.Description)
	}
	return fmt.Sprintf("%s: %s", f.Field, f.Description)
}

// enumHint returns a compact "Fields: ..." hint for property enums.
func enumHint(schema ToolSchema) string {
	props, ok := schema.Schema["properties"].(map[string]interface{})
	if !ok {
		return ""
	}
	var hints []string
	for name, prop := range props {
		if vals := enumValues(prop); len(vals) > 0 {
			hints = append(hints, fmt.Sprintf("%s (enum: %s)", name, strings.Join(vals, ", ")))
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "Fields: " + strings.Join(hints, ", ")
}

// enumValues extracts string enum values from a property schema.
func enumValues(prop interface{}) []string {
	propMap, ok := prop.(map[string]interface{})
	if !ok {
		return nil
	}
	raw, ok := propMap["enum"].([]interface{})
	if !ok {
		return nil
	}
	var vals []string
	for _, v := range raw {
		if s, ok := v.(string); ok {
			vals = append(vals, s)
		}
	}
	return vals
}

func extractStringSlice(m map[string]interface{}, key string) []string {
	if raw, ok := m[key]; ok {
		if arr, ok := raw.([]interface{}); ok {
			var result []string
			for _, v := range arr {
				if s, ok := v.(string); ok {
					result = append(result, s)
				}
			}
			return result
		}
		if arr, ok := raw.([]string); ok {
			return arr
		}
	}
	return nil
}
