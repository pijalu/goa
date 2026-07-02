// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"io"
	"log"
	"os"
	"strings"
)

// Level represents a logging severity level.
type Level int

const (
	// Error is the most severe level.
	Error Level = iota
	// Warn indicates potentially harmful situations.
	Warn
	// Info provides general information.
	Info
	// Debug provides detailed diagnostic information.
	Debug
	// Trace provides very detailed execution flow information.
	Trace
)

// Logger provides leveled logging for the agentic SDK.
type Logger struct {
	level  Level
	logger *log.Logger
}

// NewLogger creates a logger that writes to os.Stderr.
func NewLogger(level Level) *Logger {
	return &Logger{
		level:  level,
		logger: log.New(os.Stderr, "", log.LstdFlags),
	}
}

// NewLoggerWithStdLogger creates a logger wrapping an existing standard library logger.
func NewLoggerWithStdLogger(stdLogger *log.Logger, level Level) *Logger {
	return &Logger{
		level:  level,
		logger: stdLogger,
	}
}

// Default returns a logger using the standard library's default output.
func Default() *Logger {
	return &Logger{
		level:  Info,
		logger: log.New(log.Writer(), "", log.LstdFlags),
	}
}

// SetOutput redirects the logger's output to the given writer.
func (l *Logger) SetOutput(w io.Writer) {
	if l == nil {
		return
	}
	if w == nil {
		w = io.Discard
	}
	l.logger = log.New(w, "", log.LstdFlags)
}

// Enabled reports whether the logger would emit messages at the given level.
func (l *Logger) Enabled(lv Level) bool {
	return l != nil && lv <= l.level
}

func (l *Logger) Log(lv Level, msg string, args ...interface{}) {
	if l != nil && lv <= l.level {
		levelStr := levelString(lv)
		msg = strings.TrimSpace(msg)
		if len(args) > 0 {
			l.logger.Printf("["+levelStr+"] "+msg, args...)
		} else {
			l.logger.Println("[" + levelStr + "] " + msg)
		}
	}
	// Always capture to the global ring so diagnostics are available even
	// when no file logger is configured or the level filters this line out.
	captureToRing(lv, msg, args...)
}

func levelString(lv Level) string {
	switch lv {
	case Error:
		return "ERROR"
	case Warn:
		return "WARN"
	case Info:
		return "INFO"
	case Debug:
		return "DEBUG"
	case Trace:
		return "TRACE"
	default:
		return "UNKNOWN"
	}
}

// String returns the human-readable name of the level.
func (lv Level) String() string {
	return levelString(lv)
}
