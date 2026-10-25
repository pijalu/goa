// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package startplan

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal"
)

// deviceMid is the persistent per-install device identity the plan backend
// requires as X-Device-Mid. ZCode derives it from its telemetry state file;
// goa keeps its own value so the two installs never share a device identity.
//
// The file is one line of hex inside GoaHomeDir()/device-mid. It is created
// lazily on first plan request, NOT at startup: a goa run that never touches
// Start Plan must not write state.

const deviceMidFile = "device-mid"

var (
	deviceMidMu     sync.Mutex
	deviceMidCached string
	// deviceMidDir is overridable in tests; empty means GoaHomeDir().
	deviceMidDir string
)

// DeviceMid returns the persistent device identity, creating it on first use.
// An empty result means no home directory could be resolved — callers must
// send requests without the header in that case and let the backend's
// classification (400 parameter error → unknown availability, not outage)
// apply, instead of failing locally.
func DeviceMid() string {
	deviceMidMu.Lock()
	defer deviceMidMu.Unlock()
	if deviceMidCached != "" {
		return deviceMidCached
	}
	v := loadDeviceMid()
	if v == "" {
		v = newDeviceMid()
		if v == "" {
			return ""
		}
		if err := saveDeviceMid(v); err != nil {
			// Unwritable state must not break the request path: keep the
			// value in memory so this process still identifies itself
			// consistently, and retry persistence on the next call.
			deviceMidCached = v
			return v
		}
	}
	deviceMidCached = v
	return v
}

// SetDeviceMidDir overrides the state directory (tests only).
func SetDeviceMidDir(dir string) {
	deviceMidMu.Lock()
	defer deviceMidMu.Unlock()
	deviceMidDir = dir
	deviceMidCached = ""
}

// ResetDeviceMid clears the cached value (tests only).
func ResetDeviceMid() {
	deviceMidMu.Lock()
	defer deviceMidMu.Unlock()
	deviceMidCached = ""
}

func deviceMidPath() string {
	dir := deviceMidDir
	if dir == "" {
		dir = internal.GoaHomeDir()
	}
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, deviceMidFile)
}

func loadDeviceMid() string {
	path := deviceMidPath()
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return normalizeDeviceMid(string(raw))
}

func saveDeviceMid(v string) error {
	path := deviceMidPath()
	if path == "" {
		return os.ErrNotExist
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(v+"\n"), 0o600)
}

// normalizeDeviceMid accepts the value only when it is printable ASCII —
// the header must survive wire encoding, and a corrupted file must yield
// "regenerate" rather than a malformed header.
func normalizeDeviceMid(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 128 {
		return ""
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x20 || v[i] > 0x7e {
			return ""
		}
	}
	return v
}

// newDeviceMid mints 32 random bytes as hex — the same entropy shape ZCode
// uses, and enough that two goa installs never collide.
func newDeviceMid() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return ""
	}
	return hex.EncodeToString(buf)
}
