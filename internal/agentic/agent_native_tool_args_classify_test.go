// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import "testing"

func TestClassifyNativeToolArguments(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want nativeArgumentClass
	}{
		{name: "empty", raw: "", want: nativeArgumentsValid},
		{name: "whitespace", raw: " \n\t", want: nativeArgumentsValid},
		{name: "object", raw: `{"path":"x","edits":[]}`, want: nativeArgumentsValid},
		{name: "object with trailing whitespace", raw: "{\"x\":1}\n", want: nativeArgumentsValid},
		{name: "open string", raw: `{"path":"x","new_content":"abc`, want: nativeArgumentsTruncated},
		{name: "open object", raw: `{"path":"x"`, want: nativeArgumentsTruncated},
		{name: "open array", raw: `{"edits":[1,2`, want: nativeArgumentsTruncated},
		{name: "trailing escape", raw: "{\"path\":\"x\\", want: nativeArgumentsTruncated},
		{name: "mismatched closer", raw: `{"x":1]}`, want: nativeArgumentsMalformed},
		{name: "trailing value", raw: `{} {}`, want: nativeArgumentsMalformed},
		{name: "trailing garbage", raw: `{"x":1} garbage`, want: nativeArgumentsMalformed},
		{name: "array", raw: `[]`, want: nativeArgumentsWrongShape},
		{name: "scalar", raw: `42`, want: nativeArgumentsWrongShape},
		{name: "string encoded object", raw: `"{\"x\":1}"`, want: nativeArgumentsWrongShape},
		{name: "null", raw: `null`, want: nativeArgumentsWrongShape},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyNativeToolArguments(tt.raw); got != tt.want {
				t.Fatalf("classifyNativeToolArguments(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
