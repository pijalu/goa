package bm25

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"strings"
)

// CodeRegion is a best-effort semantic region extracted from a source file.
type CodeRegion struct {
	Kind      string
	Name      string
	Parent    string
	Signature string
	StartLine int
	EndLine   int
}

type CodeAnalyzer interface {
	Languages() []string
	Analyze(path string, source string) ([]CodeRegion, error)
}

type AnalyzerRegistry struct {
	byLanguage map[string]CodeAnalyzer
	fallback   CodeAnalyzer
}

func NewAnalyzerRegistry() *AnalyzerRegistry {
	r := &AnalyzerRegistry{byLanguage: make(map[string]CodeAnalyzer), fallback: lexicalAnalyzer{}}
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
	if r != nil {
		if a := r.byLanguage[strings.ToLower(language)]; a != nil {
			return a
		}
	}
	return lexicalAnalyzer{}
}

// LanguageForPath returns a stable language label for common source formats.
func LanguageForPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	languages := map[string]string{
		".go": "go", ".js": "javascript", ".jsx": "javascript", ".mjs": "javascript", ".cjs": "javascript",
		".ts": "typescript", ".tsx": "typescript", ".mts": "typescript", ".cts": "typescript", ".py": "python", ".pyi": "python",
		".rs": "rust", ".java": "java", ".c": "c", ".h": "c", ".cc": "cpp", ".cpp": "cpp", ".cxx": "cpp", ".hpp": "cpp",
		".cs": "csharp", ".rb": "ruby", ".php": "php", ".kt": "kotlin", ".swift": "swift", ".dart": "dart", ".scala": "scala",
		".lua": "lua", ".sh": "shell", ".bash": "shell", ".zsh": "shell", ".sql": "sql", ".json": "json", ".yaml": "yaml", ".yml": "yaml",
		".toml": "toml", ".html": "html", ".css": "css",
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

// Patterns intentionally describe declarations common to many languages. The
// balanced/indentation range calculation below makes them useful on malformed
// files too, unlike a parser that rejects the whole file.
var declarationRE = regexp.MustCompile(`(?m)^[ \t]*(?:(?:export|public|private|protected|internal|async|static|final|abstract|override|virtual|sealed|const|let|var)[ \t]+)*(?:(func|fn|def|function|class|struct|interface|trait|impl|enum|type|namespace|module|object|record|constructor)[ \t]+([A-Za-z_$][\w$]*)|(?:func|function)[ \t]*\([^\n]*\)[ \t]*([A-Za-z_$][\w$]*))`)
var importRE = regexp.MustCompile(`(?m)^[ \t]*(?:import|from|using|include|require|require_relative|use|เปิด)\b[^\n]*`)

func (lexicalAnalyzer) Analyze(path, source string) ([]CodeRegion, error) {
	lines := strings.Split(source, "\n")
	regions := make([]CodeRegion, 0)
	for _, m := range declarationRE.FindAllStringSubmatchIndex(source, -1) {
		name := capture(source, m, 2)
		if name == "" {
			name = capture(source, m, 3)
		}
		kind := capture(source, m, 1)
		if kind == "" {
			kind = "function"
		}
		start := lineNumber(source, m[0])
		end := declarationEnd(lines, start, lines[start-1])
		regions = append(regions, CodeRegion{Kind: strings.ToLower(kind), Name: name, Signature: strings.TrimSpace(lines[start-1]), StartLine: start, EndLine: end})
	}
	for _, m := range importRE.FindAllStringIndex(source, -1) {
		line := lineNumber(source, m[0])
		regions = append(regions, CodeRegion{Kind: "import", Signature: strings.TrimSpace(source[m[0]:m[1]]), StartLine: line, EndLine: line})
	}
	return regions, nil
}
func capture(source string, m []int, group int) string {
	i := 2 * group
	if i+1 >= len(m) || m[i] < 0 {
		return ""
	}
	return source[m[i]:m[i+1]]
}
func lineNumber(source string, offset int) int { return 1 + strings.Count(source[:offset], "\n") }

func declarationEnd(lines []string, start int, signature string) int {
	if start < 1 || start > len(lines) {
		return start
	}
	// Brace languages: count braces while ignoring the common one-line case.
	depth := strings.Count(signature, "{") - strings.Count(signature, "}")
	if depth > 0 {
		for i := start; i < len(lines); i++ {
			depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			if depth <= 0 {
				return i + 1
			}
		}
		return len(lines)
	}
	// Python/YAML-like blocks end before the next non-blank line at the same or
	// lesser indentation. This also gives sensible ranges for incomplete code.
	indent := len(lines[start-1]) - len(strings.TrimLeft(lines[start-1], " \t"))
	last := start
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		n := len(line) - len(strings.TrimLeft(line, " \t"))
		if n <= indent {
			return last
		}
		last = i + 1
	}
	return len(lines)
}

// chunkParams are the bounded window settings used when no declaration can
// be extracted.
type chunkParams struct {
	window  int
	overlap int
}

// normalizeChunkParams applies the fallbacks: window defaults to 120 lines
// and overlap to 20 lines when unset or not strictly smaller than window.
func normalizeChunkParams(window, overlap int) chunkParams {
	if window <= 0 {
		window = 120
	}
	if overlap < 0 || overlap >= window {
		overlap = 20
	}
	return chunkParams{window: window, overlap: overlap}
}

// regionChunks converts analyzer regions into documents clamped to source
// bounds; ranges outside the file are skipped.
func regionChunks(path, source string, lines []string, regions []CodeRegion) []DocumentMeta {
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
		doc := makeDocument(path, LanguageForPath(path), r.Kind, r.Name, r.Parent, r.Signature, r.StartLine, end, strings.Join(lines[r.StartLine-1:end], "\n"))
		doc.Imports = importsForSource(source)
		chunks = append(chunks, doc)
	}
	return chunks
}

// windowChunks slides a fixed-size overlapping window across all lines; this
// is the fallback for sources with no extractable declarations.
func windowChunks(path string, lines []string, window, overlap int) []DocumentMeta {
	chunks := make([]DocumentMeta, 0)
	step := window - overlap
	for start := 1; start <= len(lines); start += step {
		end := min(start+window-1, len(lines))
		chunks = append(chunks, makeDocument(path, LanguageForPath(path), "window", "", "", "", start, end, strings.Join(lines[start-1:end], "\n")))
		if end == len(lines) {
			break
		}
	}
	return chunks
}

// ChunkSource returns semantic regions and bounded windows when no declaration
// can be extracted. Every returned range is clamped to the source.
func ChunkSource(path, source string, analyzer CodeAnalyzer, window, overlap int) []DocumentMeta {
	params := normalizeChunkParams(window, overlap)
	if analyzer == nil {
		analyzer = lexicalAnalyzer{}
	}
	regions, _ := analyzer.Analyze(path, source)
	lines := strings.Split(source, "\n")
	chunks := regionChunks(path, source, lines, regions)
	if len(chunks) > 0 {
		return chunks
	}
	return windowChunks(path, lines, params.window, params.overlap)
}
func importsForSource(source string) string {
	matches := importRE.FindAllString(source, -1)
	return strings.Join(matches, " ")
}

func makeDocument(path, language, kind, symbol, parent, signature string, start, end int, content string) DocumentMeta {
	h := sha1.Sum([]byte(path + "\x00" + string(rune(start)) + "\x00" + content))
	return DocumentMeta{ID: hex.EncodeToString(h[:]), Path: path, Language: language, Kind: kind, Symbol: symbol, Parent: parent, Signature: signature, StartLine: start, EndLine: end, Content: content}
}
