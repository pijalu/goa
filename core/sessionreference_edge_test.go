// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"
)

func TestParseSessionReferenceMentions_EdgeCases(t *testing.T) {
	assertSessionParse(t, "goa-session:s1 and goa-session:s2", 2, "@s1 and @s2")
	assertSessionParse(t, "(@[x](goa-session:s1))", 1, "(@x)")
	assertSessionParse(t, "@[a](goa-session:s1) goa-session:s1", 1, "@a @s1")
	assertSessionParse(t, "path goa-session:../evil", 0, "path goa-session:../evil")
	refs, _, err := ParseSessionReferenceMentions("goa-session:missing1")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].ID != "missing1" {
		t.Errorf("refs=%+v", refs)
	}
}

func assertSessionParse(t *testing.T, input string, count int, want string) {
	refs, out, err := ParseSessionReferenceMentions(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != count || out != want {
		t.Errorf("input %q: refs=%+v out=%q", input, refs, out)
	}
}
