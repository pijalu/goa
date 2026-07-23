// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package compat

import (
	"testing"

	"github.com/pijalu/gpython/py"
)

func TestAsString(t *testing.T) {
	s, err := AsString(py.String("hello"), "test")
	if err != nil || s != "hello" {
		t.Fatalf("unexpected result: %v, %v", s, err)
	}
	s, err = AsString(py.Bytes("world"), "test")
	if err != nil || s != "world" {
		t.Fatalf("unexpected result: %v, %v", s, err)
	}
	_, err = AsString(py.Int(1), "test")
	if err == nil {
		t.Fatal("expected error for int")
	}
}

func TestAsStringOrDefault(t *testing.T) {
	if got := AsStringOrDefault(py.String("x"), "def"); got != "x" {
		t.Errorf("got %q, want x", got)
	}
	if got := AsStringOrDefault(py.Int(1), "def"); got != "def" {
		t.Errorf("got %q, want def", got)
	}
}

func TestAsBytes(t *testing.T) {
	b, err := AsBytes(py.String("ab"), "test")
	if err != nil || string(b) != "ab" {
		t.Fatalf("unexpected result: %v, %v", b, err)
	}
	b, err = AsBytes(py.Bytes([]byte{1, 2}), "test")
	if err != nil || len(b) != 2 {
		t.Fatalf("unexpected result: %v, %v", b, err)
	}
	_, err = AsBytes(py.Int(1), "test")
	if err == nil {
		t.Fatal("expected error for int")
	}
}

func TestAsInt(t *testing.T) {
	n, err := AsInt(py.Int(42), "test")
	if err != nil || n != 42 {
		t.Fatalf("unexpected result: %v, %v", n, err)
	}
	n, err = AsInt(py.Bool(true), "test")
	if err != nil || n != 1 {
		t.Fatalf("unexpected result: %v, %v", n, err)
	}
	_, err = AsInt(py.String("x"), "test")
	if err == nil {
		t.Fatal("expected error for string")
	}
}

func TestAsIntOrDefault(t *testing.T) {
	if got := AsIntOrDefault(py.Int(5), 0); got != 5 {
		t.Errorf("got %d, want 5", got)
	}
	if got := AsIntOrDefault(py.String("x"), 7); got != 7 {
		t.Errorf("got %d, want 7", got)
	}
}

func TestAsBool(t *testing.T) {
	if !AsBool(py.Bool(true)) {
		t.Error("expected true")
	}
	if AsBool(py.Int(1)) {
		t.Error("expected false for int")
	}
}

func TestAsFloat(t *testing.T) {
	f, err := AsFloat(py.Float(3.14), "test")
	if err != nil || f != 3.14 {
		t.Fatalf("unexpected result: %v, %v", f, err)
	}
	f, err = AsFloat(py.Int(2), "test")
	if err != nil || f != 2 {
		t.Fatalf("unexpected result: %v, %v", f, err)
	}
	_, err = AsFloat(py.String("x"), "test")
	if err == nil {
		t.Fatal("expected error for string")
	}
}

func TestPyToGo(t *testing.T) {
	tests := []struct {
		in   py.Object
		want any
	}{
		{nil, nil},
		{py.None, nil},
		{py.Bool(true), true},
		{py.Int(42), int64(42)},
		{py.Float(1.5), 1.5},
		{py.String("x"), "x"},
		{py.Bytes([]byte{1, 2}), []byte{1, 2}},
		{py.NewListFromItems([]py.Object{py.Int(1), py.String("a")}), []any{int64(1), "a"}},
		{py.Tuple{py.Int(2)}, []any{int64(2)}},
		{py.StringDict{"k": py.Int(3)}, map[string]any{"k": int64(3)}},
	}
	for _, tc := range tests {
		got, err := PyToGo(tc.in)
		if err != nil {
			t.Fatalf("PyToGo error: %v", err)
		}
		assertGoValue(t, tc.in, got, tc.want)
	}
}

func assertGoValue(t *testing.T, in py.Object, got, want any) {
	t.Helper()
	switch want := want.(type) {
	case []byte:
		gotBytes, ok := got.([]byte)
		if !ok || string(gotBytes) != string(want) {
			t.Errorf("PyToGo(%v) = %v, want %v", in, got, want)
		}
	case []any:
		gotSlice, ok := got.([]any)
		if !ok || len(gotSlice) != len(want) {
			t.Errorf("PyToGo(%v) = %v, want %v", in, got, want)
		}
	case map[string]any:
		gotMap, ok := got.(map[string]any)
		if !ok || len(gotMap) != len(want) {
			t.Errorf("PyToGo(%v) = %v, want %v", in, got, want)
		}
	default:
		if got != want {
			t.Errorf("PyToGo(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestPyToGoUnsupported(t *testing.T) {
	_, err := PyToGo(py.NewType("X", ""))
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestGoToPy(t *testing.T) {
	tests := []struct {
		in   any
		want py.Object
	}{
		{nil, py.None},
		{true, py.Bool(true)},
		{false, py.Bool(false)},
		{int(1), py.Int(1)},
		{int8(2), py.Int(2)},
		{int64(3), py.Int(3)},
		{uint(4), py.Int(4)},
		{float64(2.0), py.Int(2)},
		{float64(2.5), py.Float(2.5)},
		{float32(1.5), py.Float(1.5)},
		{"s", py.String("s")},
		{[]byte{1, 2}, py.Bytes{1, 2}},
		{[]any{int64(1), "a"}, py.NewListFromItems([]py.Object{py.Int(1), py.String("a")})},
		{map[string]any{"k": int64(2)}, py.StringDict{"k": py.Int(2)}},
	}
	for _, tc := range tests {
		got, err := GoToPy(tc.in)
		if err != nil {
			t.Fatalf("GoToPy error: %v", err)
		}
		equal, err := py.Eq(got, tc.want)
		if err != nil || equal != py.True {
			t.Errorf("GoToPy(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGoToPyUnsupported(t *testing.T) {
	_, err := GoToPy(struct{}{})
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

func TestFormatError(t *testing.T) {
	err := FormatError("fn", "int", py.String("x"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestRequiredArgError(t *testing.T) {
	err := RequiredArgError("fn", "arg")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTooFewArgsError(t *testing.T) {
	err := TooFewArgsError("fn", 1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestTooManyArgsError(t *testing.T) {
	err := TooManyArgsError("fn", 1)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMustString(t *testing.T) {
	if err := MustString(py.String("x"), "fn"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := MustString(py.Int(1), "fn"); err == nil {
		t.Error("expected error")
	}
}

func TestMustInt(t *testing.T) {
	if err := MustInt(py.Int(1), "fn"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := MustInt(py.String("x"), "fn"); err == nil {
		t.Error("expected error")
	}
}

func TestPyObjectOrNone(t *testing.T) {
	if PyObjectOrNone(nil) != py.None {
		t.Error("expected None for nil")
	}
	if PyObjectOrNone(py.Int(1)) != py.Int(1) {
		t.Error("expected value")
	}
}

func TestIterItems(t *testing.T) {
	items, err := IterItems(py.NewListFromItems([]py.Object{py.Int(1)}))
	if err != nil || len(items) != 1 {
		t.Fatalf("unexpected result: %v, %v", items, err)
	}
	items, err = IterItems(py.Tuple{py.Int(2)})
	if err != nil || len(items) != 1 {
		t.Fatalf("unexpected result: %v, %v", items, err)
	}
	items, err = IterItems(py.String("ab"))
	if err != nil || len(items) != 2 {
		t.Fatalf("unexpected result: %v, %v", items, err)
	}
	items, err = IterItems(py.Bytes([]byte{1, 2}))
	if err != nil || len(items) != 2 {
		t.Fatalf("unexpected result: %v, %v", items, err)
	}
	_, err = IterItems(py.Int(1))
	if err == nil {
		t.Fatal("expected error for int")
	}
}

func TestNewPyList(t *testing.T) {
	lst, err := NewPyList([]any{int64(1), "a"})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	l, ok := lst.(*py.List)
	if !ok || len(l.Items) != 2 {
		t.Fatalf("unexpected list: %v", lst)
	}
}

func TestNewPyStringDict(t *testing.T) {
	d, err := NewPyStringDict(map[string]any{"k": int64(1)})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(d) != 1 {
		t.Fatalf("unexpected dict: %v", d)
	}
}
