// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"reflect"
	"testing"
)

func TestExpandCommandParams(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", []string{}, []string{}},
		{"single", []string{"show", "memo1"}, []string{"show", "memo1"}},
		{"multiple", []string{"show", "a;b;c"}, []string{"show", "a", "b", "c"}},
		{"mixed", []string{"show", "a;b", "c"}, []string{"show", "a", "b", "c"}},
		{"drops empty", []string{"show", "a;"}, []string{"show", "a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandCommandParams(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("expandCommandParams(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
