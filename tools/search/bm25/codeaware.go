package bm25

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// CodeRegion is a best-effort semantic region extracted from a source file.
// An analyzer may return declarations, imports, or structural blocks; callers
// must be prepared for partial results when source is malformed.
type CodeRegion struct {
	Kind      string
	Name      string
	Parent    string
	Signature string
	StartLine int
	EndLine   int
}

// CodeAnalyzer extracts semantic regions for one or more languages.
type CodeAnalyzer interface {
	Languages() []string
	Analyze(path string, source string) ([]CodeRegion, error)
}

// AnalyzerRegistry selects a language analyzer and always falls back to the
// language-agnostic chunker. Parser failures therefore never remove a file
// from retrieval.
type AnalyzerRegistry struct {
	byLanguage map[string]CodeAnalyzer
	fallback   CodeAnalyzer
}

func NewAnalyzerRegistry() *AnalyzerRegistry {
	r := &AnalyzerRegistry{byLanguage: make(map[string]CodeAnalyzer), fallback: lexicalAnalyzer{}}
	// The lexical analyzer is deliberately shared: declaration syntax is similar
	// enough across these languages and malformed code remains searchable.
	r.Register(lexicalAnalyzer{})
	return r
}

func (r *AnalyzerRegistry) Register(a CodeAnalyzer) {
	if r == nil || a == nil {
		return
	}
	for _, lang := range a.Languages() {
		r.byLanguage[strings.ToLower(lang)] = a
	}
}

func (r *AnalyzerRegistry) Analyzer(language string) CodeAnalyzer {
	if r == nil {
		return lexicalAnalyzer{}
	}
	if a := r.byLanguage[strings.ToLower(language)]; a != nil {
		return a
	}
	return r.fallback
}

// LanguageForPath returns a stable language label for common source formats.
func LanguageForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	languages := map[string]string{
		".go": "go", ".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
		".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript",
		".py": "python", ".pyi": "python", ".rs": "rust", ".java": "java", ".c": "c", ".h": "c",
		".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp", ".cs": "csharp", ".rb": "ruby",
		".php": "php", ".kt": "kotlin", ".swift": "swift", ".dart": "dart", ".scala": "scala",
		".lua": "lua", ".sh": "shell", ".bash": "shell", ".zsh": "shell", ".sql": "sql",
		".json": "json", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml", ".html": "html", ".css": "css",
	}
	if lang := languages[ext]; lang != "" {
		return lang
	}
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" {
		return "dockerfile"
	}
	if base == "makefile" {
		return "makefile"
	}
	return "text"
}

type lexicalAnalyzer struct{}

func (lexicalAnalyzer) Languages() []string {
	return []string{"go", "javascript", "typescript", "python", "rust", "java", "c", "cpp", "csharp", "ruby", "php", "kotlin", "swift", "dart", "scala", "lua", "shell", "sql", "json", "yaml", "toml", "html", "css", "dockerfile", "makefile", "text"}
}

var declarationRE = regexp.MustCompile(`(?m)^\s*(?:(?:export|public|private|protected|async|static|func|fn|def|class|struct|type|interface|trait|impl|enum|namespace|module|object|record)\s+)?(?:func|fn|def|class|struct|interface|trait|impl|enum|type|namespace|module|object|record|function)\s+([A-Za-z_$][\w$]*)`)
var importRE = regexp.MustCompile(`(?m)^\s*(?:import|from|using|include|require|use)\b[^\n]*`)

func (lexicalAnalyzer) Analyze(path, source string) ([]CodeRegion, error) {
	lines := strings.Split(source, "\n")
	regions := make([]CodeRegion, 0)
	for _, m := range declarationRE.FindAllStringSubmatchIndex(source, -1) {
		name := source[m[2]:m[3]]
		start := lineNumber(source, m[0])
		end := regionEnd(lines, start)
		sig := strings.TrimSpace(lines[start-1])
		regions = append(regions, CodeRegion{Kind: "declaration", Name: name, Signature: sig, StartLine: start, EndLine: end})
	}
	for _, m := range importRE.FindAllStringIndex(source, -1) {
		line := lineNumber(source, m[0])
		regions = append(regions, CodeRegion{Kind: "import", Signature: strings.TrimSpace(source[m[0]:m[1]]), StartLine: line, EndLine: line})
	}
	return regions, nil
}

func lineNumber(source string, offset int) int { return 1 + strings.Count(source[:offset], "\n") }
func regionEnd(lines []string, start int) int {
	if start < 1 {
		return 1
	}
	end := start + 79
	if end > len(lines) {
		return len(lines)
	}
	return end
}

// ChunkSource returns semantic regions plus bounded fallback windows. It is the
// foundation used by later chunk indexes and is independently testable.
func ChunkSource(path, source string, analyzer CodeAnalyzer, window, overlap int) []DocumentMeta {
	if window <= 0 {
		window = 120
	}
	if overlap < 0 || overlap >= window {
		overlap = 20
	}
	if analyzer == nil {
		analyzer = lexicalAnalyzer{}
	}
	regions, _ := analyzer.Analyze(path, source)
	lines := strings.Split(source, "\n")
	chunks := make([]DocumentMeta, 0, len(regions)+1)
	for _, r := range regions {
		if r.StartLine < 1 || r.StartLine > len(lines) {
			continue
		}
		end := r.EndLine
		if end < r.StartLine {
			end = r.StartLine
		}
		if end > len(lines) {
			end = len(lines)
		}
		chunks = append(chunks, makeDocument(path, LanguageForPath(path), r.Kind, r.Name, r.Parent, r.Signature, r.StartLine, end, strings.Join(lines[r.StartLine-1:end], "\n")))
	}
	if len(chunks) > 0 {
		return chunks
	}
	for start := 1; start <= len(lines); start += window - overlap {
		end := start + window - 1
		if end > len(lines) {
			end = len(lines)
		}
		chunks = append(chunks, makeDocument(path, LanguageForPath(path), "window", "", "", "", start, end, strings.Join(lines[start-1:end], "\n")))
		if end == len(lines) {
			break
		}
	}
	return chunks
}

func makeDocument(path, language, kind, symbol, parent, signature string, start, end int, content string) DocumentMeta {
	h := sha1.Sum([]byte(path + "\x00" + string(rune(start)) + "\x00" + content))
	return DocumentMeta{ID: hex.EncodeToString(h[:]), Path: path, Language: language, Kind: kind, Symbol: symbol, Parent: parent, Signature: signature, StartLine: start, EndLine: end, Content: content}
}
