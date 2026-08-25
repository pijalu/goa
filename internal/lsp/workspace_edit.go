package lsp

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// WorkspaceEditPolicy controls safe application of server-provided edits.
type WorkspaceEditPolicy struct {
	Root           string
	AllowProtected bool
	BackupDir      string
}

// WorkspaceEditPreview is a deterministic summary before mutation.
type WorkspaceEditPreview struct {
	Files     []string
	EditCount int
}

type workspaceFileEdits struct {
	path  string
	edits []TextEdit
}

// PreviewWorkspaceEdit validates paths and summarizes an edit without changing files.
func PreviewWorkspaceEdit(edit *WorkspaceEdit, policy WorkspaceEditPolicy) (WorkspaceEditPreview, error) {
	files, err := normalizeWorkspaceEdit(edit, policy)
	if err != nil {
		return WorkspaceEditPreview{}, err
	}
	paths := make([]string, 0, len(files))
	count := 0
	for _, f := range files {
		paths = append(paths, f.path)
		count += len(f.edits)
	}
	sort.Strings(paths)
	return WorkspaceEditPreview{Files: paths, EditCount: count}, nil
}

// ApplyWorkspaceEdit backs up and applies text edits atomically per file.
func ApplyWorkspaceEdit(edit *WorkspaceEdit, policy WorkspaceEditPolicy) (WorkspaceEditPreview, error) {
	files, err := normalizeWorkspaceEdit(edit, policy)
	if err != nil {
		return WorkspaceEditPreview{}, err
	}
	preview, _ := previewFiles(files)
	if policy.BackupDir == "" {
		policy.BackupDir = filepath.Join(policy.Root, ".goa", "backups")
	}
	if err := os.MkdirAll(policy.BackupDir, 0700); err != nil {
		return WorkspaceEditPreview{}, err
	}
	for _, f := range files {
		if err := applyWorkspaceFile(f, policy.BackupDir); err != nil {
			return WorkspaceEditPreview{}, err
		}
	}
	return preview, nil
}

func applyWorkspaceFile(f workspaceFileEdits, backupDir string) error {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return fmt.Errorf("read %s: %w", f.path, err)
	}
	backup := filepath.Join(backupDir, filepath.Base(f.path)+".bak")
	if err := os.WriteFile(backup, data, 0600); err != nil {
		return fmt.Errorf("backup %s: %w", f.path, err)
	}
	updated, err := applyTextEdits(string(data), f.edits)
	if err != nil {
		return fmt.Errorf("edit %s: %w", f.path, err)
	}
	return replaceFile(f.path, updated)
}

func replaceFile(path, content string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".goa-edit-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	_, err = tmp.WriteString(content)
	if err == nil {
		err = tmp.Chmod(0600)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func previewFiles(files []workspaceFileEdits) (WorkspaceEditPreview, error) {
	paths := make([]string, 0, len(files))
	count := 0
	for _, f := range files {
		paths = append(paths, f.path)
		count += len(f.edits)
	}
	sort.Strings(paths)
	return WorkspaceEditPreview{Files: paths, EditCount: count}, nil
}

func normalizeWorkspaceEdit(edit *WorkspaceEdit, policy WorkspaceEditPolicy) ([]workspaceFileEdits, error) {
	if edit == nil {
		return nil, fmt.Errorf("nil workspace edit")
	}
	root, err := filepath.Abs(policy.Root)
	if err != nil {
		return nil, err
	}
	if resolved, e := filepath.EvalSymlinks(root); e == nil {
		root = resolved
	}
	byPath := make(map[string][]TextEdit)
	add := func(uri string, edits []TextEdit) error {
		path, err := validatedEditPath(uri, root, policy.AllowProtected)
		if err != nil {
			return err
		}
		byPath[path] = append(byPath[path], edits...)
		return nil
	}
	for uri, edits := range edit.Changes {
		if err := add(uri, edits); err != nil {
			return nil, err
		}
	}
	for _, change := range edit.DocumentChanges {
		if err := add(change.TextDocument.URI, change.Edits); err != nil {
			return nil, err
		}
	}
	files := make([]workspaceFileEdits, 0, len(byPath))
	for path, edits := range byPath {
		files = append(files, workspaceFileEdits{path, edits})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

func validatedEditPath(uri, root string, allowProtected bool) (string, error) {
	path, err := workspaceURIPath(uri)
	if err != nil {
		return "", err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace edit path outside root: %s", path)
	}
	if !allowProtected && (rel == ".goa" || strings.HasPrefix(rel, ".goa"+string(filepath.Separator))) {
		return "", fmt.Errorf("protected workspace edit path: %s", path)
	}
	return path, nil
}

func workspaceURIPath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid workspace URI %q: %w", raw, err)
	}
	if u.Scheme != "file" && u.Scheme != "" {
		return "", fmt.Errorf("unsupported workspace URI %q", raw)
	}
	if u.Scheme == "" {
		return filepath.FromSlash(raw), nil
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", err
	}
	if u.Host != "" && u.Host != "localhost" {
		p = "//" + u.Host + p
	}
	return filepath.FromSlash(p), nil
}

func applyTextEdits(content string, edits []TextEdit) (string, error) {
	type span struct {
		start, end int
		text       string
	}
	spans := make([]span, 0, len(edits))
	for _, e := range edits {
		start, err := positionOffset(content, e.Range.Start)
		if err != nil {
			return "", err
		}
		end, err := positionOffset(content, e.Range.End)
		if err != nil {
			return "", err
		}
		if start > end {
			return "", fmt.Errorf("invalid text edit range")
		}
		spans = append(spans, span{start, end, e.NewText})
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
	for i := 1; i < len(spans); i++ {
		if spans[i].end > spans[i-1].start {
			return "", fmt.Errorf("overlapping text edits")
		}
	}
	for _, s := range spans {
		content = content[:s.start] + s.text + content[s.end:]
	}
	return content, nil
}

func positionOffset(content string, p Position) (int, error) {
	if p.Line < 0 || p.Character < 0 {
		return 0, fmt.Errorf("invalid text edit position")
	}
	lineStart := 0
	for line := 0; line < p.Line; line++ {
		i := strings.IndexByte(content[lineStart:], '\n')
		if i < 0 {
			return 0, fmt.Errorf("text edit line out of range")
		}
		lineStart += i + 1
	}
	lineEnd := strings.IndexByte(content[lineStart:], '\n')
	if lineEnd < 0 {
		lineEnd = len(content) - lineStart
	}
	lineEnd += lineStart
	line := content[lineStart:lineEnd]
	byteOffset := 0
	units := 0
	for _, r := range line {
		n := 1
		if r > 0xffff {
			n = 2
		}
		if units+n > p.Character {
			break
		}
		units += n
		byteOffset += len(string(r))
	}
	if units != p.Character {
		return 0, fmt.Errorf("text edit character out of range")
	}
	return lineStart + byteOffset, nil
}
