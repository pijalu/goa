package bm25

import (
	"strings"
	"testing"
)

func TestLanguageForPath(t *testing.T) {
	cases := map[string]string{"main.go": "go", "app.tsx": "typescript", "server.py": "python", "main.rs": "rust", "Dockerfile": "dockerfile", "README.md": "text"}
	for path, want := range cases {
		if got := LanguageForPath(path); got != want {
			t.Errorf("%s: got %q want %q", path, got, want)
		}
	}
}

func TestChunkSourceExtractsDeclarationAndImports(t *testing.T) {
	source := "package main\nimport \"fmt\"\n\nfunc Greeting() string {\n return fmt.Sprint(\"hi\")\n}\n"
	chunks := ChunkSource("main.go", source, lexicalAnalyzer{}, 20, 4)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want declaration and import", len(chunks))
	}
	var sawDecl, sawImport bool
	for _, c := range chunks {
		sawDecl = sawDecl || c.Symbol == "Greeting"
		sawImport = sawImport || c.Kind == "import"
		if c.StartLine < 1 || c.EndLine < c.StartLine {
			t.Errorf("invalid range: %+v", c)
		}
	}
	if !sawDecl || !sawImport {
		t.Fatalf("chunks missing declaration/import: %+v", chunks)
	}
}

func TestChunkSourceFallbackWindows(t *testing.T) {
	source := strings.Repeat("plain text\n", 5)
	chunks := ChunkSource("notes.txt", source, lexicalAnalyzer{}, 2, 1)
	if len(chunks) < 3 {
		t.Fatalf("got %d fallback chunks", len(chunks))
	}
	for _, c := range chunks {
		if c.Kind != "window" || c.Content == "" {
			t.Errorf("invalid fallback chunk: %+v", c)
		}
	}
}
