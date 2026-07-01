// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"bufio"
	"io"
	"strings"
)

// ParseSSE reads Server-Sent Events from r and calls emit for each payload.
// It handles the "data: " prefix and stops when it encounters "[DONE]".
// Returns nil on normal completion (either [DONE] or clean EOF), or the
// scanner error if the stream was interrupted by an I/O error.
func ParseSSE(r io.Reader, emit func(string)) error {
	scanner := bufio.NewScanner(r)
	// Increase buffer from default 64KB to 1MB to handle large SSE lines
	// (e.g., long tool call arguments, large content chunks).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")

			if payload == "[DONE]" {
				return nil
			}

			emit(payload)
		}
	}

	return scanner.Err()
}
