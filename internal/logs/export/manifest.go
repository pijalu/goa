// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package export

import (
	"encoding/json"
	"fmt"
	"runtime"
	"time"

	"github.com/pijalu/goa/internal"
)

// FilePaths records the relative zip paths for each bundled artifact.
type FilePaths struct {
	SessionEvents string `json:"sessionEvents,omitempty"`
	AgentLog      string `json:"agentLog,omitempty"`
	HTTPLog       string `json:"httpLog,omitempty"`
	KeyLog        string `json:"keyLog,omitempty"`
	ProjectConfig string `json:"projectConfig,omitempty"`
	UserConfig    string `json:"userConfig,omitempty"`
	LocalConfig   string `json:"localConfig,omitempty"`
	Modes         string `json:"modes,omitempty"`
	SystemInfo    string `json:"systemInfo,omitempty"`
	Issue         string `json:"issue,omitempty"`
	Session       string `json:"session,omitempty"`
	Readme        string `json:"readme,omitempty"`
	Trace         string `json:"trace,omitempty"`
}

// Manifest is the structured metadata for a diagnostic bundle.
type Manifest struct {
	SessionID        string    `json:"sessionId,omitempty"`
	ExportedAt       string    `json:"exportedAt"`
	GoaVersion       string    `json:"goaVersion"`
	GoVersion        string    `json:"goVersion"`
	OS               string    `json:"os"`
	Arch             string    `json:"arch"`
	WorkspaceDir     string    `json:"workspaceDir"`
	ConfigDir        string    `json:"configDir"`
	ActiveMode       string    `json:"activeMode,omitempty"`
	ActiveProvider   string    `json:"activeProvider,omitempty"`
	ActiveModel      string    `json:"activeModel,omitempty"`
	IssueDescription string    `json:"issueDescription,omitempty"`
	Files            FilePaths `json:"files"`
	MissingFiles     []string  `json:"missingFiles,omitempty"`
}

// Metadata is the runtime metadata captured for a bundle.
type Metadata struct {
	SessionID        string
	ExportedAt       string
	GoaVersion       string
	GoVersion        string
	OS               string
	Arch             string
	WorkspaceDir     string
	ConfigDir        string
	ActiveMode       string
	ActiveProvider   string
	ActiveModel      string
	IssueDescription string
}

// NewMetadata builds a Metadata from collected runtime values.
func NewMetadata(workspaceDir, configDir, sessionID, issue, profile, provider, model string) Metadata {
	return Metadata{
		SessionID:        sessionID,
		ExportedAt:       time.Now().UTC().Format(time.RFC3339),
		GoaVersion:       internal.Version,
		GoVersion:        runtime.Version(),
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		WorkspaceDir:     workspaceDir,
		ConfigDir:        configDir,
		ActiveMode:       profile,
		ActiveProvider:   provider,
		ActiveModel:      model,
		IssueDescription: issue,
	}
}

// BuildManifest creates a Manifest with populated file paths.
func BuildManifest(m Metadata, present, missing []string) Manifest {
	files := buildFilePaths(present)
	return Manifest{
		SessionID:        m.SessionID,
		ExportedAt:       m.ExportedAt,
		GoaVersion:       m.GoaVersion,
		GoVersion:        m.GoVersion,
		OS:               m.OS,
		Arch:             m.Arch,
		WorkspaceDir:     m.WorkspaceDir,
		ConfigDir:        m.ConfigDir,
		ActiveMode:       m.ActiveMode,
		ActiveProvider:   m.ActiveProvider,
		ActiveModel:      m.ActiveModel,
		IssueDescription: m.IssueDescription,
		Files:            files,
		MissingFiles:     missing,
	}
}

func buildFilePaths(present []string) FilePaths {
	files := FilePaths{}
	setters := map[string]func(string){
		"session/events.jsonl": func(p string) { files.SessionEvents = p },
		"logs/goa.log":         func(p string) { files.AgentLog = p },
		"logs/http.jsonl":      func(p string) { files.HTTPLog = p },
		"logs/keys.log":        func(p string) { files.KeyLog = p },
		"config/project.yaml":  func(p string) { files.ProjectConfig = p },
		"config/user.yaml":     func(p string) { files.UserConfig = p },
		"config/local.yaml":    func(p string) { files.LocalConfig = p },
		"prompts/mode":         func(p string) { files.Modes = p },
		"system/info.json":     func(p string) { files.SystemInfo = p },
		"diagnostics/trace.json": func(p string) { files.Trace = p },
		"issue.md":             func(p string) { files.Issue = p },
		"session.md":           func(p string) { files.Session = p },
		"README.md":            func(p string) { files.Readme = p },
	}
	for _, p := range present {
		if set, ok := setters[p]; ok {
			set(p)
		}
	}
	return files
}

// MarshalJSON returns pretty-printed JSON.
func (m Manifest) MarshalJSON() ([]byte, error) {
	type alias Manifest
	return json.MarshalIndent(alias(m), "", "  ")
}

// Validate checks that required fields are populated.
func (m Manifest) Validate() error {
	if m.ExportedAt == "" {
		return fmt.Errorf("exportedAt is required")
	}
	if m.GoaVersion == "" {
		return fmt.Errorf("goaVersion is required")
	}
	return nil
}
