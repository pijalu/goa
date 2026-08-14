// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// genmodels fetches the models.dev catalog and rewrites the embedded
// api.json snapshot consumed by internal/agentic/provider/models
// (go:embed). The snapshot is slimmed: provider-level documentation
// fields and verbose per-model metadata (descriptions, release dates,
// families, ...) are dropped, keeping only the fields the registry
// parses. This keeps the embedded fallback small while letting every
// build embed the latest published models.
//
// Usage from the repo root:
//
//	go run ./cmd/genmodels
//	go generate ./internal/agentic/provider/models/
//
// Flags:
//
//	-out     target file (default: the embedded snapshot path)
//	-url     catalog URL  (default: https://models.dev/api.json)
//	-check   do not write; exit 1 when the committed snapshot is stale
//	-strict  fail when the fetch fails (default: keep the existing file)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	// defaultURL is the canonical models.dev catalog endpoint (mirrors
	// models.ModelsDevURL; duplicated so the generator stays standalone).
	defaultURL = "https://models.dev/api.json"
	// defaultOut is the embedded snapshot path relative to the repo root.
	defaultOut = "internal/agentic/provider/models/api.json"
	// fetchTimeout bounds the catalog download.
	fetchTimeout = 30 * time.Second
)

// slimProvider keeps only the provider-level fields the registry consumes:
// identity (id/name), fallback endpoint synthesis (api/npm), credential
// hints (env), and the models map. models.dev's doc URL and any future
// marketing fields are dropped.
type slimProvider struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	API    string              `json:"api,omitempty"`
	NPM    string              `json:"npm,omitempty"`
	Env    []string            `json:"env,omitempty"`
	Models map[string]slimModel `json:"models"`
}

// slimModel keeps the fields the registry parses (see modelsDevModel):
// capabilities, limits, costs, and input modalities. Pointer fields preserve
// explicit zero values from upstream (e.g. cache_write: 0) while dropping
// absent keys.
type slimModel struct {
	Name       string          `json:"name"`
	ToolCall   *bool           `json:"tool_call,omitempty"`
	Reasoning  *bool           `json:"reasoning,omitempty"`
	Limit      *slimLimit      `json:"limit,omitempty"`
	Cost       *slimCost       `json:"cost,omitempty"`
	Modalities *slimModalities `json:"modalities,omitempty"`
}

type slimLimit struct {
	Context *int `json:"context,omitempty"`
	Output  *int `json:"output,omitempty"`
}

type slimCost struct {
	Input      *float64 `json:"input,omitempty"`
	Output     *float64 `json:"output,omitempty"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
}

type slimModalities struct {
	Input []string `json:"input,omitempty"`
}

// slimCatalog decodes the raw models.dev document and re-encodes it with
// only the whitelisted fields. Decoding into the slim structs drops every
// unknown key; re-marshalling emits map keys in sorted order, giving a
// deterministic, diff-friendly single-line document.
func slimCatalog(raw []byte) ([]byte, error) {
	var providers map[string]slimProvider
	if err := json.Unmarshal(raw, &providers); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("catalog has no providers (upstream schema changed?)")
	}
	out, err := json.Marshal(providers)
	if err != nil {
		return nil, fmt.Errorf("encode slim catalog: %w", err)
	}
	return out, nil
}

// fetchCatalog downloads the models.dev document.
func fetchCatalog(url string) ([]byte, error) {
	client := &http.Client{Timeout: fetchTimeout}
	resp, err := client.Get(url) //nolint:gosec // URL is the fixed catalog endpoint (or an explicit flag).
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	return body, nil
}

// writeFile atomic-write data to path (tmp file + rename) so a failed run
// never leaves a truncated snapshot behind.
func writeFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func main() {
	out := flag.String("out", defaultOut, "output path for the slimmed catalog")
	url := flag.String("url", defaultURL, "models.dev catalog URL")
	check := flag.Bool("check", false, "exit 1 when the committed snapshot differs from upstream (no write)")
	strict := flag.Bool("strict", false, "fail when the fetch fails instead of keeping the existing file")
	flag.Parse()

	if err := run(*out, *url, *check, *strict); err != nil {
		fmt.Fprintln(os.Stderr, "genmodels:", err)
		os.Exit(1)
	}
}

func run(out, url string, check, strict bool) error {
	raw, err := fetchCatalog(url)
	if err != nil {
		if strict || check {
			return err
		}
		if _, statErr := os.Stat(out); statErr == nil {
			fmt.Fprintf(os.Stderr, "genmodels: warning: %v — keeping existing %s\n", err, out)
			return nil
		}
		return fmt.Errorf("%w (no existing %s to keep)", err, out)
	}

	slim, err := slimCatalog(raw)
	if err != nil {
		return err
	}

	if check {
		existing, readErr := os.ReadFile(out)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", out, readErr)
		}
		if !bytes.Equal(existing, slim) {
			return fmt.Errorf("%s is stale — run `go generate ./internal/agentic/provider/models/`", out)
		}
		fmt.Printf("genmodels: %s is up to date\n", out)
		return nil
	}

	if err := writeFile(out, slim); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}

	var providers map[string]slimProvider
	if err := json.Unmarshal(slim, &providers); err != nil {
		return err
	}
	models := 0
	for _, p := range providers {
		models += len(p.Models)
	}
	fmt.Printf("genmodels: wrote %s (%d providers, %d models, %d bytes)\n", out, len(providers), models, len(slim))
	return nil
}
