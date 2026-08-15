// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"os"
	"testing"

	"github.com/pijalu/goa/internal"
)

// TestMain pins a hermetic goa home for the whole provider test package. The
// canonical PurposeHeadersInterceptor calls internal.AnonymousUserID() on
// every protocol-backed request, which would otherwise create the identity
// file in the developer's real ~/.goa during tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "goa-provider-test-*")
	if err != nil {
		panic(err)
	}
	internal.SetGoaHome(dir)
	code := m.Run()
	internal.SetGoaHome("")
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
