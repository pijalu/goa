// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// SSHHostConfig holds SSH connection parameters.
type SSHHostConfig struct {
	ID      string `yaml:"id"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	User    string `yaml:"user"`
	KeyFile string `yaml:"key_file"`
}

// SSHBashTool executes commands on remote hosts via the system ssh binary.
type SSHBashTool struct {
	Hosts []SSHHostConfig
}

// Schema returns the tool schema for ssh_bash.
func (t *SSHBashTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "ssh_bash",
		Description: "Execute a command on a remote host via SSH.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"host_id": map[string]any{
					"type": "string", "description": "Host ID from SSH configuration",
				},
				"command": map[string]any{
					"type": "string", "description": "Command to execute on the remote host",
				},
				"timeout": map[string]any{
					"type": "integer", "description": "Timeout in seconds",
				},
				"workdir": map[string]any{
					"type": "string", "description": "Working directory on the remote host",
				},
			},
			"required": []string{"host_id", "command"},
		},
	}
}

// sshBashParams holds the parsed input for SSHBashTool.
type sshBashParams struct {
	HostID  string `json:"host_id"`
	Command string `json:"command"`
	Workdir string `json:"workdir"`
	Timeout int    `json:"timeout"`
}

// Execute runs a command on a remote host via SSH.
func (t *SSHBashTool) Execute(input string) (string, error) {
	var p sshBashParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "ssh_bash", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Ensure your input is valid JSON with the required fields.",
		}
	}
	hostID := p.HostID
	cmdStr := p.Command
	workdir := p.Workdir
	timeoutSec := p.Timeout

	if hostID == "" || cmdStr == "" {
		return "", &internal.ToolError{
			Tool: "ssh_bash", Type: "missing_parameter",
			Detail:   "Both host_id and command are required",
			HintText: "Specify host_id and command parameters.",
		}
	}

	// Look up host config
	hostConfig := t.findHost(hostID)
	if hostConfig == nil {
		return "", &internal.ToolError{
			Tool: "ssh_bash", Type: "host_not_found",
			Detail:   fmt.Sprintf("Host %q not found in SSH configuration", hostID),
			HintText: "Check the host ID or add it to tools.ssh.hosts in the config.",
		}
	}

	// Build SSH command
	sshArgs := t.buildSSHArgs(hostConfig, cmdStr, workdir)
	if len(sshArgs) == 0 {
		return "", &internal.ToolError{
			Tool: "ssh_bash", Type: "config_error",
			Detail:   "Failed to build SSH arguments",
			HintText: "Check the SSH host configuration.",
		}
	}

	// Execute
	start := time.Now()
	cmd := exec.Command("ssh", sshArgs...)

	// Handle timeout with goroutine + select
	resultCh := make(chan execResult, 1)
	go func() {
		output, err := cmd.CombinedOutput()
		resultCh <- execResult{output: output, err: err}
	}()

	var result execResult
	if timeoutSec > 0 {
		select {
		case r := <-resultCh:
			result = r
		case <-time.After(time.Duration(timeoutSec) * time.Second):
			cmd.Process.Kill()
			// Wait for goroutine to finish after kill
			r := <-resultCh
			result = r
			return "", &internal.ToolError{
				Tool: "ssh_bash", Type: "timeout",
				Detail:   fmt.Sprintf("SSH command timed out after %ds", timeoutSec),
				HintText: "Increase the timeout value or check connectivity to the remote host.",
			}
		}
	} else {
		result = <-resultCh
	}

	duration := time.Since(start)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "[ssh_bash: %s — %s]\n", hostID, truncateCommand(cmdStr, 60))
	fmt.Fprintf(&buf, "Exit: %d\n", exitCode(result.err))
	fmt.Fprintf(&buf, "Duration: %.2fs\n", duration.Seconds())
	if len(result.output) > 0 {
		fmt.Fprintf(&buf, "Output:\n%s\n", string(result.output))
	}

	return buf.String(), nil
}

func (t *SSHBashTool) IsRetryable(err error) bool { return false }

//go:embed sshbash.short.md sshbash.long.md
var sshbashDocs embed.FS

func (t *SSHBashTool) ShortDoc() string { return readDoc(sshbashDocs, "sshbash.short.md") }
func (t *SSHBashTool) LongDoc() string  { return readDoc(sshbashDocs, "sshbash.long.md") }

func (t *SSHBashTool) Examples() []string {
	return []string{
		`{"host_id": "prod-web", "command": "uptime"}`,
		`{"host_id": "dev-server", "command": "ls -la /var/log", "workdir": "/home/app"}`,
	}
}

// findHost looks up a host by ID.
func (t *SSHBashTool) findHost(id string) *SSHHostConfig {
	for i := range t.Hosts {
		if t.Hosts[i].ID == id {
			return &t.Hosts[i]
		}
	}
	return nil
}

// execResult holds the SSH command execution result.
type execResult struct {
	output []byte
	err    error
}

// buildSSHArgs constructs the SSH command arguments.
func (t *SSHBashTool) buildSSHArgs(host *SSHHostConfig, cmdStr, workdir string) []string {
	var args []string
	args = append(args, "-q")
	args = append(args, "-o", "StrictHostKeyChecking=yes")
	args = append(args, "-o", "ConnectTimeout=5")
	args = append(args, "-o", "ServerAliveInterval=5")

	if host.Port > 0 && host.Port != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", host.Port))
	}
	if host.KeyFile != "" {
		args = append(args, "-i", host.KeyFile)
	}

	target := host.Host
	if host.User != "" {
		target = host.User + "@" + target
	}
	args = append(args, target)

	remoteCmd := cmdStr
	if workdir != "" {
		remoteCmd = fmt.Sprintf("cd %s && %s", workdir, cmdStr)
	}
	args = append(args, remoteCmd)

	return args
}
