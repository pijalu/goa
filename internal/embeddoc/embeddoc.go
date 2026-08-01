// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package embeddoc provides helpers for loading embedded documentation files,
// including plain-text files and markdown files with YAML frontmatter.
package embeddoc

import (
	"bytes"
	"embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

// Document is a markdown file split into YAML frontmatter and a body.
type Document struct {
	// Frontmatter holds the parsed YAML metadata.
	Frontmatter map[string]any
	// Body is the markdown content after the frontmatter.
	Body string
}

// LoadText reads a plain-text file from an embedded filesystem and returns its
// trimmed content, dropping all HTML comments (e.g. SPDX headers). If the
// file is missing or empty, an empty string is returned.
func LoadText(fs embed.FS, name string) string {
	data, err := fs.ReadFile(name)
	if err != nil {
		return ""
	}
	return string(StripHTMLComments(data))
}

// LoadDocument reads a markdown file from an embedded filesystem and parses its
// YAML frontmatter. Files without frontmatter return an empty Frontmatter map
// and the full file body.
func LoadDocument(fs embed.FS, name string) (*Document, error) {
	data, err := fs.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", name, err)
	}
	return ParseDocument(data)
}

// ParseDocument splits raw markdown data into frontmatter and body.
// The body is returned with all HTML comments stripped (see StripHTMLComments):
// bodies are model-facing and comments must not consume LLM context.
func ParseDocument(data []byte) (*Document, error) {
	trimmed := bytes.TrimSpace(stripLeadingComment(data))

	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return &Document{Frontmatter: make(map[string]any), Body: string(StripHTMLComments(trimmed))}, nil
	}

	rest := trimmed[3:]
	endIdx := bytes.Index(rest, []byte("---"))
	if endIdx < 0 {
		return &Document{Frontmatter: make(map[string]any), Body: string(StripHTMLComments(trimmed))}, nil
	}

	fmBytes := bytes.TrimSpace(rest[:endIdx])
	bodyBytes := bytes.TrimSpace(rest[endIdx+3:])

	var fm map[string]any
	if len(fmBytes) > 0 {
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}
	if fm == nil {
		fm = make(map[string]any)
	}

	return &Document{Frontmatter: fm, Body: string(StripHTMLComments(bodyBytes))}, nil
}

// stripLeadingComment removes an optional HTML comment block at the start of a
// file. Embedded prompts include an SPDX license header wrapped in <!-- -->,
// which should not be treated as document content.
func stripLeadingComment(data []byte) []byte {
	return StripLeadingComment(data)
}

// StripLeadingComment removes optional leading HTML comment blocks (e.g. SPDX
// license headers) from data. Model-facing text must never carry them: they
// consume LLM context without meaning. Only comments before any content are
// removed; comments later in the document are preserved.
func StripLeadingComment(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	for bytes.HasPrefix(trimmed, []byte("<!--")) {
		endIdx := bytes.Index(trimmed, []byte("-->"))
		if endIdx < 0 {
			break
		}
		trimmed = bytes.TrimSpace(trimmed[endIdx+3:])
	}
	return trimmed
}

// StripHTMLComments removes ALL HTML comment blocks (<!-- ... -->) from
// markdown data, anywhere in the document — not just leading license headers.
// Model-facing markdown must never carry comments: they consume LLM context
// without meaning. Content inside fenced code blocks (``` or ~~~) is preserved
// verbatim, because comment-like text there is literal code, not a comment.
// An unterminated comment drops everything to EOF. The result is trimmed.
func StripHTMLComments(data []byte) []byte {
	var out bytes.Buffer
	inFence := false
	inComment := false
	var fence []byte
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i+1], data[i+1:]
		} else {
			line, data = data, nil
		}
		body := bytes.TrimSuffix(line, []byte("\n"))
		hasNL := len(body) < len(line)
		trimmed := bytes.TrimSpace(body)
		if inFence {
			out.Write(line)
			if fr := fenceRun(trimmed); fr != nil && fr[0] == fence[0] && len(fr) >= len(fence) {
				inFence = false
			}
			continue
		}
		if fr := fenceRun(trimmed); fr != nil && !inComment {
			inFence = true
			fence = fr
			out.Write(line)
			continue
		}
		// Outside fences: drop every HTML comment block (may span lines).
		var kept []byte
		content := body
		for len(content) > 0 {
			if inComment {
				end := bytes.Index(content, []byte("-->"))
				if end < 0 {
					content = nil
					break
				}
				content = content[end+3:]
				inComment = false
				continue
			}
			start := bytes.Index(content, []byte("<!--"))
			if start < 0 {
				kept = append(kept, content...)
				break
			}
			kept = append(kept, content[:start]...)
			content = content[start+4:]
			inComment = true
		}
		out.Write(kept)
		if hasNL {
			out.WriteByte('\n')
		}
	}
	return bytes.TrimSpace(out.Bytes())
}

// fenceRun returns the fence marker (``` or ~~~, 3+ chars) when the trimmed
// line opens or closes a fenced code block, else nil.
func fenceRun(trimmed []byte) []byte {
	if len(trimmed) < 3 {
		return nil
	}
	c := trimmed[0]
	if c != '`' && c != '~' {
		return nil
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == c {
		n++
	}
	if n < 3 {
		return nil
	}
	return trimmed[:n]
}
