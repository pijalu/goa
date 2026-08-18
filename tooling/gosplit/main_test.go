package main

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitPreservesExistingOutputAndProducesParsableGroups(t *testing.T) {
	dir := t.TempDir()
	source := writeSample(t, dir)
	out := filepath.Join(dir, "generated")
	generateTwice(t, source, out)
	assertGenerated(t, out)
	if got := string(mustRead(t, source)); got != sampleContents {
		t.Fatal("split modified original source")
	}
}

const sampleContents = "package sample\n\nimport \"fmt\"\n\n// first declaration\nfunc First() { fmt.Println(\"first\") }\n\nfunc Second() { fmt.Println(\"second\") }\n"

func writeSample(t *testing.T, dir string) string {
	t.Helper()
	source := filepath.Join(dir, "sample.go")
	if err := os.WriteFile(source, []byte(sampleContents), 0o644); err != nil {
		t.Fatal(err)
	}
	return source
}

func generateTwice(t *testing.T, source, out string) {
	t.Helper()
	if err := split(source, out, 8); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(out, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := split(source, out, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("split removed unrelated output: %v", err)
	}
}

func assertGenerated(t *testing.T, out string) {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(out, "*.go"))
	if err != nil || len(files) != 1 {
		t.Fatalf("generated files = %d, err=%v", len(files), err)
	}
	if strings.Contains(filepath.Base(files[0]), "_split_") {
		t.Fatalf("generated file retains split suffix: %s", files[0])
	}
	for _, file := range files {
		assertGeneratedFile(t, file)
	}
}

func assertGeneratedFile(t *testing.T, file string) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("generated %s does not parse: %v", file, err)
	}
	if f.Name.Name != "sample" || !strings.Contains(string(mustRead(t, file)), "import \"fmt\"") {
		t.Fatalf("generated %s lacks package/import header", file)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
