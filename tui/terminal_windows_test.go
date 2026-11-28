//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"errors"
	"testing"
)

// stubConsoleCP replaces the console code page syscalls with fakes and
// returns a pointer to the fake state plus a cleanup restoring the real
// syscalls.
func stubConsoleCP(t *testing.T, get func() (uint32, error), set func(uint32) error) (calls *[]uint32) {
	t.Helper()
	setCalls := &[]uint32{}
	origGet, origSet := getConsoleOutputCP, setConsoleOutputCP
	getConsoleOutputCP = get
	setConsoleOutputCP = func(cp uint32) error {
		*setCalls = append(*setCalls, cp)
		return set(cp)
	}
	t.Cleanup(func() {
		getConsoleOutputCP, setConsoleOutputCP = origGet, origSet
	})
	return setCalls
}

func TestSetWindowsUTF8OutputCP_SetsAndRestores(t *testing.T) {
	setCalls := stubConsoleCP(t,
		func() (uint32, error) { return 850, nil }, // legacy conhost OEM page
		func(uint32) error { return nil },
	)

	restore := setWindowsUTF8OutputCP()
	if restore == nil {
		t.Fatal("expected a restore function when switching from a non-UTF-8 page")
	}
	if len(*setCalls) != 1 || (*setCalls)[0] != cpUTF8 {
		t.Fatalf("start: want one set call with %d, got %v", cpUTF8, *setCalls)
	}

	restore()
	if len(*setCalls) != 2 || (*setCalls)[1] != 850 {
		t.Fatalf("stop: want restore call with previous page 850, got %v", *setCalls)
	}
}

func TestSetWindowsUTF8OutputCP_AlreadyUTF8(t *testing.T) {
	setCalls := stubConsoleCP(t,
		func() (uint32, error) { return cpUTF8, nil },
		func(uint32) error { return nil },
	)

	restore := setWindowsUTF8OutputCP()
	if restore != nil {
		t.Fatal("expected nil restore when the console is already UTF-8")
	}
	if len(*setCalls) != 0 {
		t.Fatalf("expected no set calls, got %v", *setCalls)
	}
}

func TestSetWindowsUTF8OutputCP_GetFails(t *testing.T) {
	setCalls := stubConsoleCP(t,
		func() (uint32, error) { return 0, errors.New("not a console") },
		func(uint32) error { return nil },
	)

	if restore := setWindowsUTF8OutputCP(); restore != nil {
		t.Fatal("expected nil restore when GetConsoleOutputCP fails")
	}
	if len(*setCalls) != 0 {
		t.Fatalf("expected no set calls, got %v", *setCalls)
	}
}

func TestSetWindowsUTF8OutputCP_SetFails(t *testing.T) {
	setCalls := stubConsoleCP(t,
		func() (uint32, error) { return 437, nil },
		func(uint32) error { return errors.New("access denied") },
	)

	restore := setWindowsUTF8OutputCP()
	if restore != nil {
		t.Fatal("expected nil restore when SetConsoleOutputCP fails (nothing to restore)")
	}
	if len(*setCalls) != 1 || (*setCalls)[0] != cpUTF8 {
		t.Fatalf("want one attempted set with %d, got %v", cpUTF8, *setCalls)
	}
}
