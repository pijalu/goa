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

func TestChunkSourceBroadLanguagesAndAccurateRanges(t *testing.T) {
	cases := []struct {
		path, source, symbol, kind string
		start, end                 int
	}{
		{"handler.py", "import os\n\ndef handle(x):\n    value = x + 1\n    return value\n\ndef next():\n    return 2\n", "handle", "def", 3, 5},
		{"app.js", "import {x} from 'm'\n\nexport function run() {\n  return x\n}\n", "run", "function", 3, 5},
		{"lib.rs", "fn compute() {\n  let x = 1;\n}\n", "compute", "fn", 1, 3},
	}
	for _, tc := range cases {
		chunks := ChunkSource(tc.path, tc.source, lexicalAnalyzer{}, 20, 4)
		var found bool
		for _, c := range chunks {
			if c.Symbol == tc.symbol {
				found = true
				if c.Kind != tc.kind || c.StartLine != tc.start || c.EndLine != tc.end {
					t.Errorf("%s: got %+v", tc.path, c)
				}
			}
		}
		if !found {
			t.Errorf("%s: symbol %q not found in %+v", tc.path, tc.symbol, chunks)
		}
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
