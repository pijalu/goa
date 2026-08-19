// Command gosplit safely partitions a Go source file into cohesive declaration files.
// It emits complete package/import headers so generated files can be compiled before
// the source file is replaced by a caller.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type declaration struct {
	start, end int
}

func main() {
	input := flag.String("file", "", "Go source file to split")
	output := flag.String("out", "", "directory for generated files")
	maxLines := flag.Int("max-lines", 500, "target maximum lines per generated file")
	flag.Parse()
	if *input == "" || *output == "" || *maxLines < 1 {
		fmt.Fprintln(os.Stderr, "usage: gosplit -file source.go -out directory [-max-lines 500]")
		os.Exit(2)
	}
	if err := split(*input, *output, *maxLines); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func split(input, output string, maxLines int) error {
	src, file, fset, err := parseSource(input)
	if err != nil {
		return err
	}
	header, decls := sourceParts(src, file, fset)
	groups := groupDeclarations(src, decls, maxLines-len(strings.Split(header, "\n")))
	return writeGroups(output, filepath.Base(input), header, src, groups)
}

func parseSource(input string) ([]byte, *ast.File, *token.FileSet, error) {
	src, err := os.ReadFile(input)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read %s: %w", input, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, input, src, parser.ParseComments)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse %s: %w", input, err)
	}
	if len(file.Decls) == 0 {
		return nil, nil, nil, fmt.Errorf("%s has no declarations", input)
	}
	return src, file, fset, nil
}

func sourceParts(src []byte, file *ast.File, fset *token.FileSet) (string, []declaration) {
	headerEnd := fset.Position(file.Name.End()).Offset
	decls := make([]declaration, 0, len(file.Decls))
	for _, d := range file.Decls {
		if isImport(d) {
			headerEnd = maxOffset(headerEnd, fset.Position(d.End()).Offset)
			continue
		}
		start := fset.Position(d.Pos()).Offset
		if gd, ok := d.(*ast.GenDecl); ok && gd.Doc != nil {
			start = fset.Position(gd.Doc.Pos()).Offset
		}
		decls = append(decls, declaration{start: start, end: fset.Position(d.End()).Offset})
	}
	return strings.TrimSpace(string(src[:headerEnd])) + "\n\n", decls
}

func isImport(d ast.Decl) bool {
	gd, ok := d.(*ast.GenDecl)
	return ok && gd.Tok == token.IMPORT
}

func maxOffset(a, b int) int {
	if b > a {
		return b
	}
	return a
}

func writeGroups(output, inputBase, header string, src []byte, groups [][]declaration) error {
	if err := os.MkdirAll(output, 0o755); err != nil {
		return err
	}
	base := strings.TrimSuffix(inputBase, ".go")
	for _, group := range groups {
		var b strings.Builder
		b.WriteString(header)
		for _, d := range group {
			b.WriteString(strings.TrimSpace(string(src[d.start:d.end])))
			b.WriteString("\n\n")
		}
		name := filepath.Join(output, fmt.Sprintf("%s_%s.go", base, declarationName(src, group[0])))
		if err := os.WriteFile(name, []byte(b.String()), 0o644); err != nil {
			return err
		}
		if err := formatGenerated(name); err != nil {
			return err
		}
	}
	return nil
}

func groupDeclarations(src []byte, decls []declaration, maxLines int) [][]declaration {
	if maxLines < 1 {
		maxLines = 1
	}
	var groups [][]declaration
	var current []declaration
	lines := 0
	for _, d := range decls {
		n := lineCount(src[d.start:d.end])
		if len(current) > 0 && lines+n > maxLines {
			groups = append(groups, current)
			current = nil
			lines = 0
		}
		current = append(current, d)
		lines += n
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func lineCount(b []byte) int { return 1 + strings.Count(string(b), "\n") }

func declarationName(src []byte, d declaration) string {
	text := strings.TrimSpace(string(src[d.start:d.end]))
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && (fields[0] == "func" || fields[0] == "type") {
			name := strings.Trim(fields[1], "(*{")
			if i := strings.IndexByte(name, '('); i >= 0 {
				name = name[:i]
			}
			if name != "" {
				return strings.ToLower(name)
			}
		}
	}
	return "features"
}

func formatGenerated(name string) error {
	if tool, err := exec.LookPath("goimports"); err == nil {
		cmd := exec.Command(tool, "-w", name)
		if output, runErr := cmd.CombinedOutput(); runErr == nil {
			return nil
		} else {
			return fmt.Errorf("goimports %s: %w: %s", name, runErr, strings.TrimSpace(string(output)))
		}
	}
	cmd := exec.Command("gofmt", "-w", name)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gofmt %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	return nil
}
