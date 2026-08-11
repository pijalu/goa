// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"
	"testing"
)

// TestPythonTool_Execute_BytesDecode replays the reported session failure:
// reading a UTF-8 JSONL log with open(path, 'rb').read().decode('utf-8')
// raised "AttributeError: 'bytes' has no attribute 'decode'" because gpython
// does not implement bytes.decode.
func TestPythonTool_Execute_BytesDecode(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{
			name: "reported transcript",
			code: "print(b\"abc\".decode('utf-8'))",
			want: "abc",
		},
		{
			name: "default encoding is utf-8",
			code: "print(b'hello'.decode())",
			want: "hello",
		},
		{
			name: "utf-8 decodes multibyte sequences",
			code: "print(b'h\\xc3\\xa9llo'.decode('utf-8'))",
			want: "héllo",
		},
		{
			name: "utf8 alias accepted",
			code: "print(b'abc'.decode('utf8'))",
			want: "abc",
		},
		{
			name: "non-utf8 bytes with errors=replace",
			code: "print(b'a\\xffb'.decode('utf-8', 'replace'))",
			want: "a\ufffdb",
		},
		{
			name: "non-utf8 bytes with errors=ignore",
			code: "print(b'a\\xff\\xfeb'.decode('utf-8', 'ignore'))",
			want: "ab",
		},
		{
			name: "empty bytes",
			code: "print(b''.decode())",
			want: "",
		},
	}
	tool := &PythonTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tt.code))
			if err != nil {
				t.Fatalf("Execute failed: %v\noutput: %s", err, out)
			}
			if !strings.Contains(out, tt.want) {
				t.Errorf("output = %q, want %q", out, tt.want)
			}
		})
	}
}

// TestPythonTool_Execute_BytesDecodeErrors covers the failure paths: an invalid
// sequence under the default 'strict' mode raises UnicodeDecodeError, an
// unknown encoding raises LookupError, and bad arguments raise TypeError.
func TestPythonTool_Execute_BytesDecodeErrors(t *testing.T) {
	tests := []struct {
		name string
		code string
		want string
	}{
		{"strict invalid sequence", "b'\\xff'.decode('utf-8')", "UnicodeDecodeError"},
		{"unknown encoding", "b'abc'.decode('latin-1')", "LookupError"},
		{"unknown errors mode", "b'abc'.decode('utf-8', 'bogus')", "LookupError"},
		{"too many args", "b'abc'.decode('utf-8', 'strict', 1)", "TypeError"},
		{"non-str encoding", "b'abc'.decode(8)", "TypeError"},
	}
	tool := &PythonTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Execute(fmt.Sprintf(`{"code": %q}`, tt.code))
			if err == nil {
				t.Fatalf("expected error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want %q", err.Error(), tt.want)
			}
		})
	}
}
