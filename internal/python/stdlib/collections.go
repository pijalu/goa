// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"sort"

	"github.com/pijalu/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

// Counter implements a multiset of string keys.
type Counter struct {
	counts map[string]int64
}

var counterType = py.NewTypeX("Counter", `Counter(iterable)

A counter for string keys.`, counterNew, counterInit)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "collections",
			Doc:  "collections — specialized container datatypes",
		},
		Methods: []*py.Method{
			py.MustNewMethod("Counter", collectionsCounter, 0, `Counter(iterable) -> Counter

Create a Counter from an iterable of hashable keys.`),
		},
	})

	counterType.Dict["update"] = py.MustNewMethod("update", counterUpdate, 0, `update(iterable)

Add counts from the iterable.`)
	counterType.Dict["subtract"] = py.MustNewMethod("subtract", counterSubtract, 0, `subtract(iterable)

Subtract counts from the iterable.`)
	counterType.Dict["elements"] = py.MustNewMethod("elements", counterElements, 0, `elements() -> iterator

Return an iterator over elements repeating each as many times as its count.`)
	counterType.Dict["most_common"] = py.MustNewMethod("most_common", counterMostCommon, 0, `most_common(n=None) -> list

Return a list of the n most common (key, count) pairs.`)
	counterType.Dict["keys"] = py.MustNewMethod("keys", counterKeys, 0, `keys() -> list

Return a list of keys.`)
	counterType.Dict["values"] = py.MustNewMethod("values", counterValues, 0, `values() -> list

Return a list of counts.`)
	counterType.Dict["items"] = py.MustNewMethod("items", counterItems, 0, `items() -> list

Return a list of (key, count) pairs.`)
}

// Type returns the Counter type.
func (c *Counter) Type() *py.Type { return counterType }

// M__getitem__ returns the count for a key (default 0).
func (c *Counter) M__getitem__(key py.Object) (py.Object, error) {
	k, err := compat.AsString(key, "Counter")
	if err != nil {
		return nil, err
	}
	return py.Int(c.counts[k]), nil
}

// M__setitem__ sets the count for a key.
func (c *Counter) M__setitem__(key, value py.Object) (py.Object, error) {
	k, err := compat.AsString(key, "Counter")
	if err != nil {
		return nil, err
	}
	v, err := compat.AsInt(value, "Counter")
	if err != nil {
		return nil, err
	}
	c.counts[k] = v
	return py.None, nil
}

// M__contains__ reports whether the key has a positive count.
func (c *Counter) M__contains__(key py.Object) (py.Object, error) {
	k, err := compat.AsString(key, "Counter")
	if err != nil {
		return nil, err
	}
	return py.Bool(c.counts[k] > 0), nil
}

// M__iter__ returns an iterator over the keys.
func (c *Counter) M__iter__() (py.Object, error) {
	keys := make([]py.Object, 0, len(c.counts))
	for k := range c.counts {
		keys = append(keys, py.String(k))
	}
	return py.NewIterator(py.Tuple(keys)), nil
}

// M__len__ returns the number of keys.
func (c *Counter) M__len__() (py.Object, error) {
	return py.Int(len(c.counts)), nil
}

// M__bool__ returns whether any count is positive.
func (c *Counter) M__bool__() (py.Object, error) {
	return py.Bool(len(c.counts) > 0), nil
}

// --- Type construction ---

func counterNew(t *py.Type, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	return &Counter{counts: make(map[string]int64)}, nil
}

func counterInit(self py.Object, args py.Tuple, kwargs py.StringDict) error {
	c, ok := self.(*Counter)
	if !ok {
		return py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	var iterable py.Object
	if err := py.UnpackTuple(args, nil, "Counter", 0, 1, &iterable); err != nil {
		return err
	}
	if iterable != nil {
		if err := c.addFromIterable(iterable, 1); err != nil {
			return err
		}
	}
	return nil
}

func collectionsCounter(self py.Object, args py.Tuple) (py.Object, error) {
	obj, err := counterNew(counterType, args, nil)
	if err != nil {
		return nil, err
	}
	if err := counterInit(obj, args, nil); err != nil {
		return nil, err
	}
	return obj, nil
}

// --- Instance methods ---

func counterUpdate(self py.Object, args py.Tuple) (py.Object, error) {
	var iterable py.Object
	if err := py.UnpackTuple(args, nil, "update", 1, 1, &iterable); err != nil {
		return nil, err
	}
	c, ok := self.(*Counter)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	if err := c.addFromIterable(iterable, 1); err != nil {
		return nil, err
	}
	return py.None, nil
}

func counterSubtract(self py.Object, args py.Tuple) (py.Object, error) {
	var iterable py.Object
	if err := py.UnpackTuple(args, nil, "subtract", 1, 1, &iterable); err != nil {
		return nil, err
	}
	c, ok := self.(*Counter)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	if err := c.addFromIterable(iterable, -1); err != nil {
		return nil, err
	}
	return py.None, nil
}

func counterElements(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("elements", 0)
	}
	c, ok := self.(*Counter)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	var items []py.Object
	for k, n := range c.counts {
		for i := int64(0); i < n; i++ {
			items = append(items, py.String(k))
		}
	}
	return py.NewIterator(py.Tuple(items)), nil
}

func counterMostCommon(self py.Object, args py.Tuple) (py.Object, error) {
	var n py.Object
	if err := py.UnpackTuple(args, nil, "most_common", 0, 1, &n); err != nil {
		return nil, err
	}
	c, ok := self.(*Counter)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	limit := int64(len(c.counts))
	if n != nil {
		v, err := compat.AsInt(n, "most_common")
		if err != nil {
			return nil, err
		}
		limit = v
	}
	type pair struct {
		key   string
		count int64
	}
	pairs := make([]pair, 0, len(c.counts))
	for k, v := range c.counts {
		pairs = append(pairs, pair{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].count > pairs[j].count
	})
	result := make([]py.Object, 0, len(pairs))
	for i, p := range pairs {
		if int64(i) >= limit {
			break
		}
		result = append(result, py.Tuple{py.String(p.key), py.Int(p.count)})
	}
	return py.NewListFromItems(result), nil
}

func counterKeys(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("keys", 0)
	}
	c, ok := self.(*Counter)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	items := make([]py.Object, 0, len(c.counts))
	for k := range c.counts {
		items = append(items, py.String(k))
	}
	return py.NewListFromItems(items), nil
}

func counterValues(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("values", 0)
	}
	c, ok := self.(*Counter)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	items := make([]py.Object, 0, len(c.counts))
	for _, v := range c.counts {
		items = append(items, py.Int(v))
	}
	return py.NewListFromItems(items), nil
}

func counterItems(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("items", 0)
	}
	c, ok := self.(*Counter)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected Counter, got %s", self.Type().Name)
	}
	items := make([]py.Object, 0, len(c.counts))
	for k, v := range c.counts {
		items = append(items, py.Tuple{py.String(k), py.Int(v)})
	}
	return py.NewListFromItems(items), nil
}

func (c *Counter) addFromIterable(iterable py.Object, delta int64) error {
	items, err := compat.IterItems(iterable)
	if err != nil {
		// Counters can also be initialized from a dict of counts.
		if d, ok := iterable.(py.StringDict); ok {
			for k, v := range d {
				count, err := compat.AsInt(v, "Counter")
				if err != nil {
					return err
				}
				c.counts[k] += count * delta
			}
			return nil
		}
		return err
	}
	for _, item := range items {
		k, err := compat.AsString(item, "Counter")
		if err != nil {
			return err
		}
		c.counts[k] += delta
	}
	return nil
}

// Ensure Counter implements required py interfaces.
var (
	_ py.I__getitem__  = (*Counter)(nil)
	_ py.I__setitem__  = (*Counter)(nil)
	_ py.I__contains__ = (*Counter)(nil)
	_ py.I__iter__     = (*Counter)(nil)
	_ py.I__len__      = (*Counter)(nil)
	_ py.I__bool__     = (*Counter)(nil)
	_ py.Object         = (*Counter)(nil)
)
