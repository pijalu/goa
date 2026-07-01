// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
)

// BuildOptions controls what goes into a diagnostic bundle.
type BuildOptions struct {
	// OutputPath is the destination zip file. If empty, a timestamped file
	// under .goa/exports is created.
	OutputPath string

	// IssueDescription is the user-provided problem statement.
	IssueDescription string

	// IncludeGlobalLog bundles the agent log even if it lives outside the
	// project directory.
	IncludeGlobalLog bool

	// ProjectDir is the current working directory.
	ProjectDir string

	// ConfigDir is the user's goa config directory (~/.goa).
	ConfigDir string

	// SessionID is the ID of the session to export. Empty means the current
	// session recorded by ctx.SessionStore.
	SessionID string
}

// BuildResult is returned after a successful bundle build.
type BuildResult struct {
	Path       string
	Size       int64
	EntryCount int
	Manifest   Manifest
}

// BuildBundle creates a diagnostic zip bundle for the given context.
func BuildBundle(ctx core.Context, opts BuildOptions) (*BuildResult, error) {
	if opts.ProjectDir == "" {
		opts.ProjectDir = resolveProjectDir(ctx)
	}
	if opts.ConfigDir == "" {
		opts.ConfigDir = resolveConfigDir(ctx)
	}

	outputPath := opts.OutputPath
	if outputPath == "" {
		exportDir := filepath.Join(opts.ProjectDir, ".goa", "exports")
		ts := time.Now().Format("20060102-150405")
		outputPath = filepath.Join(exportDir, fmt.Sprintf("goa-export-%s.zip", ts))
	}

	zb, err := NewZipBuilder(outputPath)
	if err != nil {
		return nil, err
	}
	defer zb.Close()

	present, missing := collectArtifacts(zb, ctx, opts)

	manifest := BuildManifest(buildMetadata(ctx, opts), present, missing)
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid manifest: %w", err)
	}
	manifestBytes, err := manifest.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	if err := zb.AddBytes("manifest.json", manifestBytes); err != nil {
		return nil, fmt.Errorf("add manifest: %w", err)
	}
	present = append(present, "manifest.json")

	readme := renderReadme(manifest)
	if err := zb.AddBytes("README.md", []byte(readme)); err != nil {
		return nil, fmt.Errorf("add readme: %w", err)
	}
	present = append(present, "README.md")

	path, err := zb.Close()
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat bundle: %w", err)
	}

	sort.Strings(present)
	return &BuildResult{
		Path:       path,
		Size:       info.Size(),
		EntryCount: len(present),
		Manifest:   manifest,
	}, nil
}

func collectArtifacts(zb *ZipBuilder, ctx core.Context, opts BuildOptions) (present, missing []string) {
	collector := &artifactCollector{zb: zb}

	// Session events.
	collector.addFile(resolveSessionPath(ctx, opts), "session/events.jsonl", nil)

	// Logs.
	agentLogPath := resolveAgentLogPath(ctx, opts)
	keyLogPath := resolveKeyLogPath(ctx, opts)
	collector.addFile(agentLogPath, "logs/goa.log", RedactText)
	if keyLogPath != "" && keyLogPath != agentLogPath {
		collector.addFile(keyLogPath, "logs/keys.log", RedactText)
	}

	// Configs (redacted).
	collector.addFile(filepath.Join(opts.ProjectDir, ".goa", "config.yaml"), "config/project.yaml", RedactYAML)
	collector.addFile(filepath.Join(opts.ConfigDir, "config.yaml"), "config/user.yaml", RedactYAML)
	collector.addFile(filepath.Join(opts.ProjectDir, ".goa", "config.local.yaml"), "config/local.yaml", RedactYAML)

	// User-modifiable modes (prompts/mode/).
	collector.addDir(filepath.Join(opts.ProjectDir, ".goa", "prompts", "mode"), "prompts/mode")

	// System info.
	collector.addJSON(buildSystemInfo(ctx, opts), "system/info.json")

	// HTTP request/response log (captures last N LLM API calls).
	collector.addJSONLog(transport.GlobalHTTPLog.Snapshot(), "logs/http.jsonl")

	// Issue description.
	if issue := strings.TrimSpace(opts.IssueDescription); issue != "" {
		collector.addBytes("issue.md", []byte(issue))
	}

	// Session markdown summary.
	if summary := RenderSessionMarkdown(ctx); summary != "" {
		collector.addBytes("session.md", []byte(summary))
	}

	return collector.present, collector.missing
}

type artifactCollector struct {
	zb      *ZipBuilder
	present []string
	missing []string
}

func (c *artifactCollector) addBytes(target string, data []byte) {
	if err := c.zb.AddBytes(target, data); err == nil {
		c.present = append(c.present, target)
	} else {
		c.missing = append(c.missing, target)
	}
}

func (c *artifactCollector) addFile(sourcePath, target string, redact func(string) string) {
	if sourcePath == "" {
		c.missing = append(c.missing, target)
		return
	}
	info, err := os.Stat(sourcePath)
	if err != nil || info.IsDir() || info.Size() == 0 {
		c.missing = append(c.missing, target)
		return
	}
	if err := c.zb.AddFile(sourcePath, target, redact); err != nil {
		c.missing = append(c.missing, target)
		return
	}
	c.present = append(c.present, target)
}

func (c *artifactCollector) addJSON(v interface{}, target string) {
	data, err := marshalJSON(v)
	if err != nil {
		c.missing = append(c.missing, target)
		return
	}
	c.addBytes(target, data)
}

func (c *artifactCollector) addJSONLog(entries []transport.HTTPLogEntry, target string) {
	if len(entries) == 0 {
		c.missing = append(c.missing, target)
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			c.missing = append(c.missing, target)
			return
		}
	}
	data := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	if err := c.zb.AddBytes(target, data); err != nil {
		c.missing = append(c.missing, target)
		return
	}
	c.present = append(c.present, target)
}

func (c *artifactCollector) addDir(sourceDir, baseTarget string) {
	info, err := os.Stat(sourceDir)
	if err != nil || !info.IsDir() {
		c.missing = append(c.missing, baseTarget)
		return
	}
	if err := c.zb.AddDir(sourceDir, baseTarget); err != nil {
		c.missing = append(c.missing, baseTarget)
		return
	}
	c.present = append(c.present, baseTarget)
}

type systemInfo struct {
	GoaVersion string            `json:"goaVersion"`
	GoVersion  string            `json:"goVersion"`
	OS         string            `json:"os"`
	Arch       string            `json:"arch"`
	PID        int               `json:"pid"`
	Workspace  string            `json:"workspace"`
	ConfigDir  string            `json:"configDir"`
	Env        map[string]string `json:"env,omitempty"`
}

func buildSystemInfo(ctx core.Context, opts BuildOptions) systemInfo {
	env := map[string]string{}
	for _, k := range []string{"SHELL", "TERM", "TERM_PROGRAM", "GOA_HOME", "HOME", "USER"} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	return systemInfo{
		GoaVersion: internal.Version,
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		PID:        os.Getpid(),
		Workspace:  opts.ProjectDir,
		ConfigDir:  opts.ConfigDir,
		Env:        env,
	}
}

func buildMetadata(ctx core.Context, opts BuildOptions) Metadata {
	cfg := ctx.Config
	mode, provider, model := "", "", ""
	if cfg != nil {
		mode = cfg.ActiveMajor()
		provider = cfg.ActiveProvider
		model = cfg.ActiveModel
	}
	return NewMetadata(
		opts.ProjectDir,
		opts.ConfigDir,
		resolveSessionID(ctx, opts),
		strings.TrimSpace(opts.IssueDescription),
		mode,
		provider,
		model,
	)
}

func resolveProjectDir(ctx core.Context) string {
	if ctx.ProjectDir != "" {
		return ctx.ProjectDir
	}
	if ctx.Config != nil && ctx.Config.ConfigDir != "" {
		if d := filepath.Dir(ctx.Config.ConfigDir); d != "" && d != "." {
			return d
		}
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return "."
}

func resolveConfigDir(ctx core.Context) string {
	if ctx.Config != nil && ctx.Config.ConfigDir != "" {
		return ctx.Config.ConfigDir
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".goa")
	}
	return filepath.Join(resolveProjectDir(ctx), ".goa")
}

func resolveSessionID(ctx core.Context, opts BuildOptions) string {
	if opts.SessionID != "" {
		return opts.SessionID
	}
	if ctx.SessionStore != nil {
		return ctx.SessionStore.SessionID()
	}
	return ""
}

func resolveSessionPath(ctx core.Context, opts BuildOptions) string {
	id := resolveSessionID(ctx, opts)
	if id == "" {
		return ""
	}
	if ctx.SessionStore != nil {
		if p := ctx.SessionStore.CurrentSessionPath(); p != "" {
			return p
		}
	}
	return filepath.Join(opts.ProjectDir, ".goa", "sessions", id+".jsonl")
}

func resolveAgentLogPath(ctx core.Context, opts BuildOptions) string {
	if ctx.Config != nil && ctx.Config.Logging.File != "" {
		return ctx.Config.Logging.File
	}
	return ""
}

func resolveKeyLogPath(ctx core.Context, opts BuildOptions) string {
	cfg := ctx.Config
	if cfg != nil && cfg.Logging.File != "" {
		return cfg.Logging.File
	}
	if cd, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cd, "goa", "keys.log")
	}
	return filepath.Join(opts.ProjectDir, ".goa", "keys.log")
}

func marshalJSON(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
