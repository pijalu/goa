// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package bm25

import (
	"testing"
)

func TestCodeTokenizer_CamelCase(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("getUserID")
	expectTokens(t, tokens, []string{"get", "user", "id"})
}

func TestCodeTokenizer_PascalCase(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("UserAuthenticationHandler")
	expectTokens(t, tokens, []string{"user", "authentication", "handler"})
}

func TestCodeTokenizer_SnakeCase(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("user_auth_token")
	expectTokens(t, tokens, []string{"user", "auth", "token"})
}

func TestCodeTokenizer_SCREAMING_SNAKE(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("MAX_RETRY_COUNT")
	expectTokens(t, tokens, []string{"max", "retry", "count"})
}

func TestCodeTokenizer_KebabCase(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("content-type-header")
	// "type" is a stop word in Go keywords, so it should be filtered.
	expectTokens(t, tokens, []string{"content", "header"})
}

func TestCodeTokenizer_DotPath(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("/home/user/project/main.go")
	// "go" is a stop word, filtered out.
	expectTokens(t, tokens, []string{"home", "user", "project", "main"})
}

func TestCodeTokenizer_GoSource(t *testing.T) {
	ct := NewCodeTokenizer()
	src := `package main

import "fmt"

// UserHandler processes user requests.
func UserHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Println("hello world")
}`
	tokens := ct.Tokenize(src)
	// Should include meaningful tokens: camelCase splits, comment words.
	expectTokensContain(t, tokens, []string{
		"user", "handler", "processes", "requests",
		"response", "writer", "request",
		"hello", "world",
	})
	// Should NOT include Go keywords.
	for _, kw := range []string{"package", "import", "func"} {
		if tokenInSlice(tokens, kw) {
			t.Errorf("unexpected keyword token: %s", kw)
		}
	}
}

func TestCodeTokenizer_StopWords(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("package main\nimport \"fmt\"")
	// Go keywords "package" and "import" are stop words and filtered.
	if tokenInSlice(tokens, "package") {
		t.Error("expected 'package' to be filtered as stop word")
	}
	if tokenInSlice(tokens, "import") {
		t.Error("expected 'import' to be filtered as stop word")
	}
	// "main" and "fmt" are meaningful identifiers, should remain.
	if !tokenInSlice(tokens, "main") {
		t.Error("expected 'main' to remain (not a stop word)")
	}
	if !tokenInSlice(tokens, "fmt") {
		t.Error("expected 'fmt' to remain (not a stop word)")
	}
}

func TestCodeTokenizer_MinTokenLen(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("a b c")
	if len(tokens) > 0 {
		t.Errorf("expected no single-char tokens, got %v", tokens)
	}
}

func TestCodeTokenizer_EmptyInput(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("")
	if len(tokens) != 0 {
		t.Errorf("expected empty result, got %v", tokens)
	}
}

func TestCodeTokenizer_CodeWithSymbols(t *testing.T) {
	ct := NewCodeTokenizer()
	src := `func (s *Service) CreateUser(ctx context.Context, req *CreateUserRequest) (*User, error) {`
	tokens := ct.Tokenize(src)
	expectTokensContain(t, tokens, []string{
		"service", "create", "user", "ctx", "context",
		"req", "request", "error",
	})
	// "create" and "user" should appear individually from PascalCase splitting.
	if !tokenInSlice(tokens, "create") {
		t.Error("expected 'create' from CreateUser")
	}
	if !tokenInSlice(tokens, "user") {
		t.Error("expected 'user' from CreateUser")
	}
}

func TestCodeTokenizer_Numbers(t *testing.T) {
	ct := NewCodeTokenizer()
	tokens := ct.Tokenize("http2 conn2 x86_64")
	// Numbers are part of the token.
	expectTokensContain(t, tokens, []string{"http2", "conn2", "x86", "64"})
}

// --- helpers ---

func expectTokens(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("got %v (len=%d), want %v (len=%d)", got, len(got), want, len(want))
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func expectTokensContain(t *testing.T, got, want []string) {
	t.Helper()
	gotSet := make(map[string]int)
	for _, g := range got {
		gotSet[g]++
	}
	for _, w := range want {
		if gotSet[w] == 0 {
			t.Errorf("expected token %q not found in %v", w, got)
		}
		gotSet[w]--
	}
}

func tokenInSlice(tokens []string, target string) bool {
	for _, t := range tokens {
		if t == target {
			return true
		}
	}
	return false
}
