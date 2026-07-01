// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package client

import (
	"testing"
)

func TestToolInfo(t *testing.T) {
	ti := ToolInfo{Name: "read", Description: "read file"}
	if ti.Name != "read" {
		t.Errorf("name = %q", ti.Name)
	}
}

func TestRPCError(t *testing.T) {
	e := &rpcError{Code: -1, Message: "fail"}
	if e.Error() == "" {
		t.Error("expected error string")
	}
}
