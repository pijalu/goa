// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"
)

func TestParseSessionReferenceMentions_EdgeCases(t *testing.T) {
	// Bare URIs at start and end of text.
	refs, out, err := ParseSessionReferenceMentions("goa-session:s1 and goa-session:s2")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || out != "@s1 and @s2" {
		t.Errorf("refs=%+v out=%q", refs, out)
	}

	// Markdown mention inside a sentence with no surrounding spaces.
	_, out2, err := ParseSessionReferenceMentions("(@[x](goa-session:s1))")
	if err != nil {
		t.Fatal(err)
	}
	if out2 != "(@x)" {
		t.Errorf("out2=%q", out2)
	}

	// Dedup across markdown + bare forms keeps one reference.
	refs3, out3, err := ParseSessionReferenceMentions("@[a](goa-session:s1) goa-session:s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs3) != 1 || out3 != "@a @s1" {
		t.Errorf("refs3=%+v out3=%q", refs3, out3)
	}

	// Bare URI with traversal id shape is left as text (not a reference).
	refs4, out4, err := ParseSessionReferenceMentions("path goa-session:../evil")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs4) != 0 || out4 != "path goa-session:../evil" {
		t.Errorf("refs4=%+v out4=%q", refs4, out4)
	}

	// Bare URI with a plausible id parses; resolution fails when absent.
	refs5, _, err := ParseSessionReferenceMentions("goa-session:missing1")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs5) != 1 || refs5[0].ID != "missing1" {
		t.Errorf("refs5=%+v", refs5)
	}
}
