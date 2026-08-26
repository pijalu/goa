package bm25

import (
	"fmt"
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

// TestChunkSourceWindowTable pins the windowing arithmetic for the boundary
// cases: source shorter than the window, length an exact multiple of the
// step, and a stride with overlap.
func TestChunkSourceWindowTable(t *testing.T) {
	linesOf := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("line%d", i+1)
		}
		return out
	}
	cases := []struct {
		name            string
		lines           int
		window, overlap int
		want            [][2]int
	}{
		{"window exceeds length clamps to source", 3, 10, 4, [][2]int{{1, 3}}},
		{"exact multiple ends without extra chunk", 6, 3, 0, [][2]int{{1, 3}, {4, 6}}},
		{"overlapping stride covers all lines", 7, 3, 1, [][2]int{{1, 3}, {3, 5}, {5, 7}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowChunks("f.txt", linesOf(tc.lines), tc.window, tc.overlap)
			if len(got) != len(tc.want) {
				t.Fatalf("chunk ranges %v want %v", formatRanges(got), tc.want)
			}
			for i, c := range got {
				if c.Kind != "window" || c.StartLine != tc.want[i][0] || c.EndLine != tc.want[i][1] {
					t.Fatalf("chunk %d = %d:%d want %v", i, c.StartLine, c.EndLine, tc.want[i])
				}
			}
		})
	}
}

// TestNormalizeChunkParamsFallbacks pins every fallback branch of the window
// parameter normalization.
func TestNormalizeChunkParamsFallbacks(t *testing.T) {
	cases := []struct {
		window, overlap int
		wantW, wantO    int
	}{
		{-5, -1, 120, 20},
		{0, 0, 120, 0},
		{-5, 4, 120, 4},
		{30, 30, 30, 20},
		{10, 25, 10, 20},
		{12, 5, 12, 5},
	}
	for _, tc := range cases {
		got := normalizeChunkParams(tc.window, tc.overlap)
		if got.window != tc.wantW || got.overlap != tc.wantO {
			t.Errorf("normalize(%d,%d) = {%d %d}, want {%d %d}", tc.window, tc.overlap, got.window, got.overlap, tc.wantW, tc.wantO)
		}
	}
}

func formatRanges(chunks []DocumentMeta) [][2]int {
	out := make([][2]int, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, [2]int{c.StartLine, c.EndLine})
	}
	return out
}
