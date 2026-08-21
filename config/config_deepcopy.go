// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import "reflect"

func (c *Config) DeepCopy() *Config {
	out := *c
	deepCopyRefFields(reflect.ValueOf(&out).Elem(), reflect.ValueOf(c).Elem())
	return &out
}

// deepCopyRefFields replaces reference-kind fields in dst with freshly
// allocated copies of src's, recursively. Value fields must already have
// been shallow-copied into dst by the caller (struct assignment, Copy, or
// Set), so after this walk no mutable state is aliased. Unexported struct
// fields (not settable via reflection) keep the shallow copy.
func deepCopyRefFields(dst, src reflect.Value) {
	switch src.Kind() {
	case reflect.Pointer:
		deepCopyPtr(dst, src)
	case reflect.Map:
		deepCopyMap(dst, src)
	case reflect.Slice:
		deepCopySlice(dst, src)
	case reflect.Struct:
		deepCopyStructFields(dst, src)
	case reflect.Interface:
		deepCopyInterface(dst, src)
	}
}

func deepCopyPtr(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}
	nv := reflect.New(src.Type().Elem())
	nv.Elem().Set(src.Elem()) // shallow copy first; reallocate references below
	dst.Set(nv)
	deepCopyRefFields(nv.Elem(), src.Elem())
}

func deepCopyMap(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}
	nm := reflect.MakeMapWithSize(src.Type(), src.Len())
	dst.Set(nm)
	for _, k := range src.MapKeys() {
		v := src.MapIndex(k)
		nv := reflect.New(v.Type()).Elem()
		nv.Set(v) // shallow copy first; reallocate references below
		deepCopyRefFields(nv, v)
		nm.SetMapIndex(k, nv)
	}
}

func deepCopySlice(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}
	ns := reflect.MakeSlice(src.Type(), src.Len(), src.Len())
	reflect.Copy(ns, src) // shallow copy first; reallocate references below
	dst.Set(ns)
	for i := range src.Len() {
		deepCopyRefFields(ns.Index(i), src.Index(i))
	}
}

func deepCopyStructFields(dst, src reflect.Value) {
	for i := range src.NumField() {
		if dst.Field(i).CanSet() {
			deepCopyRefFields(dst.Field(i), src.Field(i))
		}
	}
}

// deepCopyInterface copies the dynamic value behind an `any` field (e.g.
// MCP/LSP option maps) so reference kinds inside it are reallocated too.
func deepCopyInterface(dst, src reflect.Value) {
	if src.IsNil() {
		return
	}
	v := src.Elem()
	nv := reflect.New(v.Type()).Elem()
	nv.Set(v)
	deepCopyRefFields(nv, v)
	dst.Set(nv)
}
