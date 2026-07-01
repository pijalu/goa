// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvBuilder constructs hardened environment maps for sandboxed children.
type EnvBuilder struct {
	// ExtraKeep names are always copied from the parent environment even if
	// they match a secret marker (e.g. hardening flags).
	ExtraKeep []string
}

// BuildSafeEnv returns a minimal, credential-free environment for sandboxed
// subprocesses.  It mirrors Unsloth's _build_safe_env: whitelist-built from
// scratch with HOME/TMPDIR repointed at the workdir.
func (b *EnvBuilder) BuildSafeEnv(workdir string) map[string]string {
	path := buildPath()
	env := map[string]string{
		"PATH":             path,
		"HOME":             workdir,
		"TMPDIR":           workdir,
		"LANG":             getEnvDefault("LANG", "C.UTF-8"),
		"TERM":             "dumb",
		"PYTHONIOENCODING": "utf-8",
	}
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		env["VIRTUAL_ENV"] = venv
	}
	if isWindows() {
		env["SystemRoot"] = os.Getenv("SystemRoot")
		if env["SystemRoot"] == "" {
			env["SystemRoot"] = `C:\Windows`
		}
		env["TEMP"] = workdir
		env["TMP"] = workdir
	}
	return env
}

// BuildBypassEnv returns the full host environment minus credential-bearing
// variables, with HOME/TMPDIR repointed at the workdir.
func (b *EnvBuilder) BuildBypassEnv(workdir string) map[string]string {
	keep := make(map[string]bool, len(b.ExtraKeep))
	for _, k := range b.ExtraKeep {
		keep[strings.ToUpper(k)] = true
	}

	env := make(map[string]string)
	for k, v := range getOSEnv() {
		uk := strings.ToUpper(k)
		if keep[uk] {
			env[k] = v
			continue
		}
		if isSecretEnvName(k) || isCredLocationEnvName(k) || isSecretEnvValue(v) {
			continue
		}
		env[k] = v
	}
	env["HOME"] = workdir
	env["TMPDIR"] = workdir
	env["TEMP"] = workdir
	env["TMP"] = workdir
	if isWindows() {
		for _, v := range windowsProfileVars {
			if _, ok := env[v]; ok {
				env[v] = workdir
			}
		}
	}
	return env
}

// getOSEnv returns the current process environment as a map.
// It is overridden in tests.
var getOSEnv = func() map[string]string {
	m := make(map[string]string)
	for _, e := range os.Environ() {
		i := strings.IndexByte(e, '=')
		if i < 0 {
			continue
		}
		m[e[:i]] = e[i+1:]
	}
	return m
}

func buildPath() string {
	exeDir := filepath.Dir(os.Args[0])
	if exeDir == "" || exeDir == "." {
		exeDir = ""
	}
	var entries []string
	if exeDir != "" {
		entries = append(entries, exeDir)
	}
	if venv := os.Getenv("VIRTUAL_ENV"); venv != "" {
		venvBin := filepath.Join(venv, "bin")
		if isWindows() {
			venvBin = filepath.Join(venv, "Scripts")
		}
		entries = append(entries, venvBin)
	}
	if isWindows() {
		sysroot := os.Getenv("SystemRoot")
		if sysroot == "" {
			sysroot = `C:\Windows`
		}
		entries = append(entries, filepath.Join(sysroot, "System32"), sysroot)
	} else {
		entries = append(entries, "/usr/local/bin", "/usr/bin", "/bin")
	}
	seen := make(map[string]bool)
	var out []string
	for _, e := range entries {
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

var (
	// secretNames are exact env var names that always carry credentials.
	secretNames = map[string]bool{
		"HF_TOKEN":                 true,
		"HF_HUB_TOKEN":             true,
		"HUGGING_FACE_HUB_TOKEN":   true,
		"HUGGINGFACE_TOKEN":        true,
		"HUGGINGFACEHUB_API_TOKEN": true,
		"WANDB_API_KEY":            true,
		"GH_TOKEN":                 true,
		"GITHUB_TOKEN":             true,
		"OPENAI_API_KEY":           true,
		"ANTHROPIC_API_KEY":        true,
		"GEMINI_API_KEY":           true,
		"GOOGLE_API_KEY":           true,
		"GROQ_API_KEY":             true,
		"OPENROUTER_API_KEY":       true,
		"REPLICATE_API_TOKEN":      true,
		"COHERE_API_KEY":           true,
		"MISTRAL_API_KEY":          true,
		"NGC_API_KEY":              true,
		"KAGGLE_KEY":               true,
		"MYSQL_PWD":                true,
		"LD_PRELOAD":               true,
		"SSH_AUTH_SOCK":            true,
		"SSH_AGENT_PID":            true,
		"GPG_AGENT_INFO":           true,
		"GNUPGHOME":                true,
		"KUBECONFIG":               true,
		"DOCKER_HOST":              true,
	}

	// secretPrefixes match credential-bearing env namespaces.
	secretPrefixes = []string{"AWS_", "AZURE_", "GOOGLE_", "GCP_", "GCLOUD_", "DYLD_"}

	// secretMarkers match credential keywords in env var names.
	secretMarkers = []string{
		"TOKEN", "API_KEY", "APIKEY", "SECRET", "PASSWORD", "PASSWD",
		"CREDENTIAL", "PRIVATE_KEY", "AUTH", "CONNSTR", "CONNECTIONSTRING",
	}

	// keepNames are non-secret hardening flags that must survive stripping.
	keepNames = map[string]bool{
		"AWS_EC2_METADATA_DISABLED":    true,
		"AWS_EC2_METADATA_V1_DISABLED": true,
	}

	// credLocationNames point SDKs at real home/cache/config files.
	credLocationNames = map[string]bool{
		"HF_HOME":                 true,
		"HF_HUB_CACHE":            true,
		"HUGGINGFACE_HUB_CACHE":   true,
		"HF_XET_CACHE":            true,
		"TRANSFORMERS_CACHE":      true,
		"HF_DATASETS_CACHE":       true,
		"HF_ASSETS_CACHE":         true,
		"XDG_CONFIG_HOME":         true,
		"XDG_CACHE_HOME":          true,
		"XDG_DATA_HOME":           true,
		"NETRC":                   true,
		"PGPASSFILE":              true,
		"BOTO_CONFIG":             true,
		"PIP_CONFIG_FILE":         true,
		"CLOUDSDK_CONFIG":         true,
		"KAGGLE_CONFIG_DIR":       true,
		"DOCKER_CONFIG":           true,
		"WANDB_DIR":               true,
		"WANDB_CONFIG_DIR":        true,
		"WANDB_CACHE_DIR":         true,
		"NPM_CONFIG_USERCONFIG":   true,
		"NPM_CONFIG_GLOBALCONFIG": true,
		"YARN_RC_FILENAME":        true,
		"GIT_CONFIG_GLOBAL":       true,
		"GIT_CONFIG_SYSTEM":       true,
		"CARGO_HOME":              true,
		"RCLONE_CONFIG":           true,
		"GIT_ASKPASS":             true,
		"SSH_ASKPASS":             true,
		"BASH_ENV":                true,
		"HOMEDRIVE":               true,
		"HOMEPATH":                true,
	}

	windowsProfileVars = []string{"USERPROFILE", "APPDATA", "LOCALAPPDATA"}

	urlUserinfoRE = regexp.MustCompile(`://[^/\s@]+@`)
	secretValueRE = regexp.MustCompile(`(?i)(?:password|pwd|accountkey|accesskey)\s*=\s*[^\s;]+`)
)

func isSecretEnvName(name string) bool {
	u := strings.ToUpper(name)
	if keepNames[u] {
		return false
	}
	if secretNames[u] {
		return true
	}
	for _, p := range secretPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	for _, m := range secretMarkers {
		if strings.Contains(u, m) {
			return true
		}
	}
	return false
}

func isCredLocationEnvName(name string) bool {
	return credLocationNames[strings.ToUpper(name)]
}

func isSecretEnvValue(value string) bool {
	if value == "" {
		return false
	}
	return urlUserinfoRE.MatchString(value) || secretValueRE.MatchString(value)
}
