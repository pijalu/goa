// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"strings"
	"testing"
)

func TestValidateSingleJSON_Valid(t *testing.T) {
	tests := []string{
		`{"key": "value"}`,
		`{}`,
		`{"nested": {"a": 1}}`,
		`[]`,
		`{"array": [1, 2, 3]}`,
		` `, // whitespace only
		"",  // empty
	}
	for _, input := range tests {
		if err := validateSingleJSON(input); err != nil {
			t.Errorf("validateSingleJSON(%q) unexpected error: %v", input, err)
		}
	}
}

func TestValidateSingleJSON_Concatenated(t *testing.T) {
	tests := []struct {
		input   string
		wantErr string
	}{
		{
			input:   `{"a": 1}{"b": 2}`,
			wantErr: "multiple values",
		},
		{
			input:   `{"members": []}{"projects": []}`,
			wantErr: "multiple values",
		},
		{
			input:   `{}{}`,
			wantErr: "multiple values",
		},
		{
			input:   `{"key": "value"} {"key2": "value2"}`,
			wantErr: "multiple values",
		},
		{
			input:   `{"key": "value"}garbage`,
			wantErr: "multiple values",
		},
	}
	for _, tc := range tests {
		err := validateSingleJSON(tc.input)
		if err == nil {
			t.Errorf("validateSingleJSON(%q) expected error containing %q, got nil", tc.input, tc.wantErr)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("validateSingleJSON(%q) error = %q, want containing %q", tc.input, err.Error(), tc.wantErr)
		}
	}
}

func TestValidateSingleJSON_Invalid(t *testing.T) {
	tests := []string{
		`{invalid}`,
		`{"unclosed": "string}`,
		`not json`,
	}
	for _, input := range tests {
		if err := validateSingleJSON(input); err == nil {
			t.Errorf("validateSingleJSON(%q) expected error, got nil", input)
		}
	}
}

func TestValidate_ConcatenatedJSON(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"members": map[string]interface{}{"type": "array"},
		},
	}

	// First object is valid, but concatenated with another — should fail
	input := `{"members": []}{"projects": []}`

	err := Validate(schema, input)
	if err == nil {
		t.Fatal("Validate() expected error for concatenated JSON, got nil")
	}
	if !strings.Contains(err.Error(), "multiple values") {
		t.Errorf("Validate() error = %q, want containing 'multiple values'", err.Error())
	}
}
