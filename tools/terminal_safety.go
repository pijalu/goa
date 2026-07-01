// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// blockedCommandsCommon are commands that should never run at command position.
var blockedCommandsCommon = map[string]bool{
	"rm": true, "dd": true, "chmod": true, "chown": true, "mkfs": true,
	"mount": true, "umount": true, "fdisk": true, "sudo": true, "su": true,
	"doas": true, "pkexec": true, "shutdown": true, "reboot": true,
	"halt": true, "poweroff": true, "kill": true, "killall": true,
	"pkill": true, "passwd": true, "curl": true, "wget": true,
	"nc": true, "ncat": true, "netcat": true, "socat": true,
	"ssh": true, "scp": true, "sftp": true, "rsync": true,
	"eval": true, "source": true,
}

var blockedCommandsWin = map[string]bool{
	"rmdir": true, "takeown": true, "icacls": true, "runas": true,
	"powershell": true, "pwsh": true,
}

func blockedCommands() map[string]bool {
	if runtime.GOOS != "windows" {
		return blockedCommandsCommon
	}
	m := make(map[string]bool, len(blockedCommandsCommon)+len(blockedCommandsWin))
	for k, v := range blockedCommandsCommon {
		m[k] = v
	}
	for k, v := range blockedCommandsWin {
		m[k] = v
	}
	return m
}

var (
	shellSeparators    = map[string]bool{";": true, "&&": true, "||": true, "|": true, "&": true, "\n": true, "(": true, ")": true, "`": true, "{": true, "}": true}
	shellKeywordsAsSep = map[string]bool{"then": true, "do": true, "else": true, "elif": true}
	commandPrefixes    = map[string]bool{"env": true, "command": true, "builtin": true, "exec": true, "time": true, "nohup": true, "nice": true, "setsid": true, "stdbuf": true, "timeout": true, "ionice": true, "chroot": true, "sudo": true, "doas": true, "su": true, "xargs": true}
	findExecFlags      = map[string]bool{"-exec": true, "-execdir": true, "-ok": true, "-okdir": true}
	assignmentRE       = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	unixShells         = map[string]bool{"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "csh": true, "tcsh": true, "fish": true}
	winShells          = map[string]bool{"cmd": true, "cmd.exe": true}
)

// findBlockedCommands detects blocked commands at shell command position only.
// It mirrors Unsloth's _find_blocked_commands logic.
func findBlockedCommands(command string, extraBlocked []string) []string {
	allBlocked := mergeBlocked(extraBlocked)
	if len(allBlocked) == 0 {
		return nil
	}

	tokens := tokenizeShell(command)
	blocked := scanCommandPosition(tokens, allBlocked)

	for b := range findBlockedByRegex(command, allBlocked) {
		blocked[b] = true
	}
	for b := range findNestedBlocked(command, tokens, allBlocked) {
		blocked[b] = true
	}

	out := make([]string, 0, len(blocked))
	for b := range blocked {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

func mergeBlocked(extra []string) map[string]bool {
	all := blockedCommands()
	for _, b := range extra {
		all[strings.ToLower(b)] = true
	}
	return all
}

func scanCommandPosition(tokens []string, allBlocked map[string]bool) map[string]bool {
	blocked := make(map[string]bool)
	state := cmdState{expectCommand: true}

	for i, token := range tokens {
		state.handleToken(token, i, tokens, allBlocked, blocked)
	}
	return blocked
}

type cmdState struct {
	expectCommand bool
	prefixPending bool
}

func (s *cmdState) handleToken(token string, i int, tokens []string, allBlocked, blocked map[string]bool) {
	if isSepOrKeyword(token) {
		s.reset()
		return
	}
	if findExecFlags[token] && i+1 < len(tokens) {
		checkBlocked(tokens[i+1], allBlocked, blocked)
	}
	if strings.HasPrefix(token, "-") {
		s.handleFlag()
		return
	}
	if !s.expectCommand {
		return
	}
	if assignmentRE.MatchString(token) || s.isWrapperArg(token) {
		return
	}
	s.handleCommand(token, allBlocked, blocked)
}

func (s *cmdState) reset() {
	s.expectCommand = true
	s.prefixPending = false
}

func (s *cmdState) handleFlag() {
	if !s.prefixPending {
		s.expectCommand = false
	}
}

func (s *cmdState) isWrapperArg(token string) bool {
	return s.prefixPending && isNumeric(token)
}

func (s *cmdState) handleCommand(token string, allBlocked, blocked map[string]bool) {
	base := tokenBasename(token)
	if allBlocked[base] {
		blocked[base] = true
	}
	if commandPrefixes[base] {
		s.prefixPending = true
		return
	}
	s.expectCommand = false
	s.prefixPending = false
}

func checkBlocked(token string, allBlocked, blocked map[string]bool) {
	base := tokenBasename(token)
	if allBlocked[base] {
		blocked[base] = true
	}
}

func tokenizeShell(cmd string) []string {
	if runtime.GOOS == "windows" {
		return tokenizeWindows(cmd)
	}
	return tokenizeUnix(cmd)
}

func tokenizeUnix(cmd string) []string {
	return tokenize(cmd, isSepChar, readUnixWord)
}

func tokenizeWindows(cmd string) []string {
	return tokenize(cmd, isWinSepChar, readWindowsWord)
}

func tokenize(cmd string, isSep func(byte) bool, readWord func(string, int) (string, int)) []string {
	var tokens []string
	i := 0
	for i < len(cmd) {
		i = skipSpaces(cmd, i)
		if i >= len(cmd) {
			break
		}
		if isSep(cmd[i]) {
			tok, next := readSeparator(cmd, i)
			tokens = append(tokens, tok)
			i = next
			continue
		}
		word, next := readWord(cmd, i)
		if word != "" {
			tokens = append(tokens, unquote(word))
		}
		i = next
	}
	return tokens
}

func readSeparator(cmd string, i int) (string, int) {
	start := i
	if i+1 < len(cmd) {
		two := cmd[i : i+2]
		if two == "&&" || two == "||" {
			return two, i + 2
		}
	}
	return string(cmd[start]), i + 1
}

func readUnixWord(cmd string, i int) (string, int) {
	start := i
	for i < len(cmd) && !isShellSpace(cmd[i]) && !isSepChar(cmd[i]) {
		if q := cmd[i]; q == '"' || q == '\'' {
			i = skipShellQuoted(cmd, i+1, q)
			if i < len(cmd) && cmd[i] == q {
				i++
			}
		} else if q == '\\' && i+1 < len(cmd) {
			i += 2
		} else {
			i++
		}
	}
	return cmd[start:i], i
}

func readWindowsWord(cmd string, i int) (string, int) {
	start := i
	inQuote := byte(0)
	for i < len(cmd) {
		c := cmd[i]
		if inQuote == 0 && (isShellSpace(c) || isWinSepChar(c)) {
			break
		}
		if inQuote == 0 && (c == '"' || c == '\'') {
			inQuote = c
			i++
			continue
		}
		if inQuote != 0 && c == inQuote {
			inQuote = 0
			i++
			continue
		}
		if c == '\\' && i+1 < len(cmd) {
			i += 2
			continue
		}
		i++
	}
	return cmd[start:i], i
}

func isSepChar(c byte) bool {
	return c == ';' || c == '&' || c == '|' || c == '\n' || c == '(' || c == ')' || c == '`' || c == '{' || c == '}'
}

func isWinSepChar(c byte) bool {
	return c == '&' || c == '|' || c == '\n' || c == '(' || c == ')'
}

func isShellSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r'
}

func skipSpaces(cmd string, i int) int {
	for i < len(cmd) && isShellSpace(cmd[i]) {
		i++
	}
	return i
}

func skipShellQuoted(cmd string, i int, quote byte) int {
	for i < len(cmd) && cmd[i] != quote {
		if cmd[i] == '\\' && quote == '"' && i+1 < len(cmd) {
			i += 2
			continue
		}
		i++
	}
	return i
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func isSepOrKeyword(token string) bool {
	return shellSeparators[token] || shellKeywordsAsSep[strings.ToLower(token)]
}

func isNumeric(s string) bool {
	if s == "" || s == "-" {
		return false
	}
	if s[0] == '-' {
		s = s[1:]
	}
	_, err := strconv.Atoi(s)
	return err == nil
}

func tokenBasename(tok string) string {
	tok = strings.Trim(tok, ";&|()`{}")
	base := filepath.Base(tok)
	ext := filepath.Ext(base)
	if runtime.GOOS == "windows" {
		if isWindowsExt(ext) {
			return strings.ToLower(strings.TrimSuffix(base, ext))
		}
	}
	return strings.ToLower(base)
}

func isWindowsExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".exe", ".com", ".bat", ".cmd":
		return true
	}
	return false
}

func findBlockedByRegex(command string, allBlocked map[string]bool) map[string]bool {
	blocked := make(map[string]bool)
	if len(allBlocked) == 0 {
		return blocked
	}
	words := make([]string, 0, len(allBlocked))
	for w := range allBlocked {
		words = append(words, regexp.QuoteMeta(w))
	}
	sort.Strings(words)
	alt := strings.Join(words, "|")
	pattern := fmt.Sprintf(`(?:^|[;&|`+"`"+`\n(]\s*|[$]\(\s*|<\(\s*)(?:[\w./\\-]*/|[a-zA-Z]:[/\\][\w./\\-]*)?(%s)(?:\.(?:exe|com|bat|cmd))?\b`, alt)
	re := regexp.MustCompile(pattern)
	for _, m := range re.FindAllStringSubmatch(strings.ToLower(command), -1) {
		if len(m) > 1 && m[1] != "" {
			blocked[m[1]] = true
		}
	}
	return blocked
}

func findNestedBlocked(command string, tokens []string, allBlocked map[string]bool) map[string]bool {
	blocked := make(map[string]bool)
	for i, token := range tokens {
		isUnixC, isWinC := classifyShellFlag(token)
		if !(isUnixC || isWinC) || i < 1 || i+1 >= len(tokens) {
			continue
		}
		if shell := findShellBase(tokens[:i], isWinC); shell != "" {
			for _, b := range findBlockedCommands(tokens[i+1], nil) {
				if allBlocked[b] {
					blocked[b] = true
				}
			}
		}
	}
	return blocked
}

func classifyShellFlag(token string) (isUnixC, isWinC bool) {
	lower := strings.ToLower(token)
	if lower == "-c" {
		return true, false
	}
	if strings.HasPrefix(lower, "-") && strings.HasSuffix(lower, "c") && !strings.HasPrefix(lower, "--") {
		return true, false
	}
	if strings.EqualFold(lower, "/c") {
		return false, true
	}
	return false, false
}

func findShellBase(prevTokens []string, isWinC bool) string {
	for j := len(prevTokens) - 1; j >= 0; j-- {
		prev := prevTokens[j]
		if strings.HasPrefix(prev, "-") {
			continue
		}
		if isWinC && strings.HasPrefix(prev, "/") && len(prev) <= 3 {
			continue
		}
		base := tokenBasename(prev)
		if isWinC {
			if winShells[base] {
				return base
			}
			return ""
		}
		if unixShells[base] {
			return base
		}
		return ""
	}
	return ""
}

// getShellCmd returns the platform-appropriate shell invocation.
func getShellCmd(command string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", command}
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return []string{shell, "-c", command}
}
