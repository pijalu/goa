// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"github.com/go-python/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

// countIterator implements itertools.count.
type countIterator struct {
	cur  int64
	step int64
}

var countIteratorType = py.NewType("count", "count iterator")

// cycleIterator implements itertools.cycle.
type cycleIterator struct {
	items []py.Object
	idx   int
}

var cycleIteratorType = py.NewType("cycle", "cycle iterator")

// repeatIterator implements itertools.repeat.
type repeatIterator struct {
	value py.Object
	times int64
	count int64
}

var repeatIteratorType = py.NewType("repeat", "repeat iterator")

// chainIterator implements itertools.chain.
type chainIterator struct {
	iterables []py.Object
	cur       int
	current   py.Object
}

var chainIteratorType = py.NewType("chain", "chain iterator")

// isliceIterator implements itertools.islice.
type isliceIterator struct {
	iter  py.Object
	cur   int64
	start int64
	stop  int64
	step  int64
}

var isliceIteratorType = py.NewType("islice", "islice iterator")

// combIterator implements itertools.combinations.
type combIterator struct {
	items   []py.Object
	indices []int
	r       int
	done    bool
}

var combIteratorType = py.NewType("combinations", "combinations iterator")

// permIterator implements itertools.permutations.
type permIterator struct {
	items   []py.Object
	indices []int
	r       int
	done    bool
}

var permIteratorType = py.NewType("permutations", "permutations iterator")

// productIterator implements itertools.product.
type productIterator struct {
	pools [][]py.Object
	idx   []int
	done  bool
}

var productIteratorType = py.NewType("product", "product iterator")

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "itertools",
			Doc:  "itertools — functions creating iterators for efficient looping",
		},
		Methods: []*py.Method{
			py.MustNewMethod("count", itertoolsCount, 0, `count(start=0, step=1) -> iterator

Return an iterator that counts up from start by step.`),
			py.MustNewMethod("cycle", itertoolsCycle, 0, `cycle(iterable) -> iterator

Return an iterator cycling over iterable forever.`),
			py.MustNewMethod("repeat", itertoolsRepeat, 0, `repeat(element, times=None) -> iterator

Repeat element times times or forever.`),
			py.MustNewMethod("chain", itertoolsChain, 0, `chain(*iterables) -> iterator

Return an iterator chained from multiple iterables.`),
			py.MustNewMethod("islice", itertoolsISlice, 0, `islice(iterable, stop) / islice(iterable, start, stop, step=1) -> iterator

Return an iterator over a slice of iterable.`),
			py.MustNewMethod("combinations", itertoolsCombinations, 0, `combinations(iterable, r) -> iterator

Return r-length combinations of elements from iterable.`),
			py.MustNewMethod("permutations", itertoolsPermutations, 0, `permutations(iterable, r) -> iterator

Return r-length permutations of elements from iterable.`),
			py.MustNewMethod("product", itertoolsProduct, 0, `product(*iterables) -> iterator

Return the Cartesian product of the input iterables.`),
		},
	})
}

// --- count ---

func (c *countIterator) Type() *py.Type { return countIteratorType }
func (c *countIterator) M__iter__() (py.Object, error) { return c, nil }
func (c *countIterator) M__next__() (py.Object, error) {
	val := c.cur
	c.cur += c.step
	return py.Int(val), nil
}

func itertoolsCount(self py.Object, args py.Tuple) (py.Object, error) {
	var start, step py.Object = py.Int(0), py.Int(1)
	if err := py.UnpackTuple(args, nil, "count", 0, 2, &start, &step); err != nil {
		return nil, err
	}
	s, err := compat.AsInt(start, "count")
	if err != nil {
		return nil, err
	}
	p, err := compat.AsInt(step, "count")
	if err != nil {
		return nil, err
	}
	return &countIterator{cur: s, step: p}, nil
}

// --- cycle ---

func (c *cycleIterator) Type() *py.Type { return cycleIteratorType }
func (c *cycleIterator) M__iter__() (py.Object, error) { return c, nil }
func (c *cycleIterator) M__next__() (py.Object, error) {
	if len(c.items) == 0 {
		return nil, py.StopIteration
	}
	val := c.items[c.idx]
	c.idx = (c.idx + 1) % len(c.items)
	return val, nil
}

func itertoolsCycle(self py.Object, args py.Tuple) (py.Object, error) {
	var iterable py.Object
	if err := py.UnpackTuple(args, nil, "cycle", 1, 1, &iterable); err != nil {
		return nil, err
	}
	items, err := compat.IterItems(iterable)
	if err != nil {
		return nil, err
	}
	return &cycleIterator{items: items}, nil
}

// --- repeat ---

func (r *repeatIterator) Type() *py.Type { return repeatIteratorType }
func (r *repeatIterator) M__iter__() (py.Object, error) { return r, nil }
func (r *repeatIterator) M__next__() (py.Object, error) {
	if r.times >= 0 && r.count >= r.times {
		return nil, py.StopIteration
	}
	r.count++
	return r.value, nil
}

func itertoolsRepeat(self py.Object, args py.Tuple) (py.Object, error) {
	var value py.Object
	var times py.Object
	if err := py.UnpackTuple(args, nil, "repeat", 1, 2, &value, &times); err != nil {
		return nil, err
	}
	var n int64 = -1
	if times != nil {
		v, err := compat.AsInt(times, "repeat")
		if err != nil {
			return nil, err
		}
		n = v
	}
	return &repeatIterator{value: value, times: n}, nil
}

// --- chain ---

func (c *chainIterator) Type() *py.Type { return chainIteratorType }
func (c *chainIterator) M__iter__() (py.Object, error) { return c, nil }
func (c *chainIterator) M__next__() (py.Object, error) {
	for {
		if c.current == nil {
			if c.cur >= len(c.iterables) {
				return nil, py.StopIteration
			}
			it, err := py.Iter(c.iterables[c.cur])
			if err != nil {
				return nil, py.ExceptionNewf(py.TypeError, "chain() expected iterable, got %s", c.iterables[c.cur].Type().Name)
			}
			c.current = it
			c.cur++
		}
		val, err := py.Next(c.current)
		if err != nil {
			if err == py.StopIteration {
				c.current = nil
				continue
			}
			return nil, err
		}
		return val, nil
	}
}

func itertoolsChain(self py.Object, args py.Tuple) (py.Object, error) {
	items := make([]py.Object, len(args))
	copy(items, args)
	return &chainIterator{iterables: items}, nil
}

// --- islice ---

func (i *isliceIterator) Type() *py.Type { return isliceIteratorType }
func (i *isliceIterator) M__iter__() (py.Object, error) { return i, nil }
func (i *isliceIterator) M__next__() (py.Object, error) {
	if i.shouldStop() {
		return nil, py.StopIteration
	}
	if i.cur < i.start {
		return i.skipToStart()
	}
	return i.yieldNext()
}

func (i *isliceIterator) shouldStop() bool {
	return i.stop >= 0 && i.cur >= i.stop
}

func (i *isliceIterator) skipToStart() (py.Object, error) {
	for i.cur < i.start {
		if _, err := i.nextFromSource(); err != nil {
			if err == py.StopIteration {
				return nil, py.StopIteration
			}
			return nil, err
		}
		i.cur++
	}
	return i.yieldNext()
}

func (i *isliceIterator) yieldNext() (py.Object, error) {
	if i.shouldStop() {
		return nil, py.StopIteration
	}
	val, err := i.nextFromSource()
	if err != nil {
		if err == py.StopIteration {
			return nil, py.StopIteration
		}
		return nil, err
	}
	i.advanceStep()
	i.cur += i.step
	return val, nil
}

func (i *isliceIterator) advanceStep() {
	for s := int64(0); s < i.step-1; s++ {
		if _, err := i.nextFromSource(); err == py.StopIteration {
			i.cur = i.stop
			return
		}
	}
}

func (i *isliceIterator) nextFromSource() (py.Object, error) {
	nextIt, ok := i.iter.(py.I__next__)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "islice() expected iterator")
	}
	return nextIt.M__next__()
}

func itertoolsISlice(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) < 2 || len(args) > 4 {
		return nil, py.ExceptionNewf(py.TypeError, "islice() expected 2 to 4 arguments")
	}
	iterable := args[0]
	it, err := iterable.(py.I__iter__).M__iter__()
	if err != nil {
		return nil, err
	}
	start, stop, step, err := parseISliceArgs(args[1:])
	if err != nil {
		return nil, err
	}
	if step <= 0 {
		return nil, py.ExceptionNewf(py.ValueError, "islice() step must be >= 1")
	}
	return &isliceIterator{iter: it, start: start, stop: stop, step: step, cur: 0}, nil
}

func parseISliceArgs(args py.Tuple) (start, stop, step int64, err error) {
	switch len(args) {
	case 1:
		start = 0
		stop, err = compat.AsInt(args[0], "islice")
		step = 1
	case 2:
		start, err = compat.AsInt(args[0], "islice")
		if err != nil {
			return 0, 0, 0, err
		}
		stop, err = compat.AsInt(args[1], "islice")
		step = 1
	case 3:
		start, err = compat.AsInt(args[0], "islice")
		if err != nil {
			return 0, 0, 0, err
		}
		stop, err = compat.AsInt(args[1], "islice")
		if err != nil {
			return 0, 0, 0, err
		}
		step, err = compat.AsInt(args[2], "islice")
	}
	return start, stop, step, err
}

// --- combinations ---

func (c *combIterator) Type() *py.Type { return combIteratorType }
func (c *combIterator) M__iter__() (py.Object, error) { return c, nil }
func (c *combIterator) M__next__() (py.Object, error) {
	if c.done {
		return nil, py.StopIteration
	}
	result := make([]py.Object, c.r)
	for i := 0; i < c.r; i++ {
		result[i] = c.items[c.indices[i]]
	}
	// advance indices
	i := c.r - 1
	for i >= 0 && c.indices[i] == i+len(c.items)-c.r {
		i--
	}
	if i < 0 {
		c.done = true
	} else {
		c.indices[i]++
		for j := i + 1; j < c.r; j++ {
			c.indices[j] = c.indices[j-1] + 1
		}
	}
	return py.NewListFromItems(result), nil
}

func itertoolsCombinations(self py.Object, args py.Tuple) (py.Object, error) {
	var iterable, r py.Object
	if err := py.UnpackTuple(args, nil, "combinations", 2, 2, &iterable, &r); err != nil {
		return nil, err
	}
	ri, err := compat.AsInt(r, "combinations")
	if err != nil {
		return nil, err
	}
	items, err := compat.IterItems(iterable)
	if err != nil {
		return nil, err
	}
	if ri < 0 {
		return nil, py.ExceptionNewf(py.ValueError, "combinations() r must be non-negative")
	}
	if int(ri) > len(items) {
		return &combIterator{done: true}, nil
	}
	indices := make([]int, ri)
	for i := 0; i < int(ri); i++ {
		indices[i] = i
	}
	return &combIterator{items: items, indices: indices, r: int(ri)}, nil
}

// --- permutations ---

func (p *permIterator) Type() *py.Type { return permIteratorType }
func (p *permIterator) M__iter__() (py.Object, error) { return p, nil }
func (p *permIterator) M__next__() (py.Object, error) {
	if p.done {
		return nil, py.StopIteration
	}
	result := make([]py.Object, p.r)
	for i := 0; i < p.r; i++ {
		result[i] = p.items[p.indices[i]]
	}
	// next permutation
	i := len(p.indices) - 2
	for i >= 0 && p.indices[i] >= p.indices[i+1] {
		i--
	}
	if i < 0 {
		p.done = true
	} else {
		j := len(p.indices) - 1
		for p.indices[j] <= p.indices[i] {
			j--
		}
		p.indices[i], p.indices[j] = p.indices[j], p.indices[i]
		for left, right := i+1, len(p.indices)-1; left < right; left, right = left+1, right-1 {
			p.indices[left], p.indices[right] = p.indices[right], p.indices[left]
		}
	}
	return py.NewListFromItems(result), nil
}

func itertoolsPermutations(self py.Object, args py.Tuple) (py.Object, error) {
	var iterable, r py.Object
	if err := py.UnpackTuple(args, nil, "permutations", 2, 2, &iterable, &r); err != nil {
		return nil, err
	}
	ri, err := compat.AsInt(r, "permutations")
	if err != nil {
		return nil, err
	}
	items, err := compat.IterItems(iterable)
	if err != nil {
		return nil, err
	}
	if ri < 0 {
		return nil, py.ExceptionNewf(py.ValueError, "permutations() r must be non-negative")
	}
	indices := make([]int, ri)
	for i := 0; i < int(ri); i++ {
		indices[i] = i
	}
	return &permIterator{items: items, indices: indices, r: int(ri)}, nil
}

// --- product ---

func (p *productIterator) Type() *py.Type { return productIteratorType }
func (p *productIterator) M__iter__() (py.Object, error) { return p, nil }
func (p *productIterator) M__next__() (py.Object, error) {
	if p.done {
		return nil, py.StopIteration
	}
	result := make([]py.Object, len(p.pools))
	for i, pool := range p.pools {
		result[i] = pool[p.idx[i]]
	}
	// advance
	for i := len(p.idx) - 1; i >= 0; i-- {
		p.idx[i]++
		if p.idx[i] < len(p.pools[i]) {
			break
		}
		p.idx[i] = 0
		if i == 0 {
			p.done = true
		}
	}
	return py.NewListFromItems(result), nil
}

func itertoolsProduct(self py.Object, args py.Tuple) (py.Object, error) {
	pools := make([][]py.Object, len(args))
	for i, arg := range args {
		items, err := compat.IterItems(arg)
		if err != nil {
			return nil, err
		}
		pools[i] = items
	}
	idx := make([]int, len(pools))
	return &productIterator{pools: pools, idx: idx}, nil
}

// Ensure all iterators implement the required interfaces.
var (
	_ py.I__iter__ = (*countIterator)(nil)
	_ py.I__next__ = (*countIterator)(nil)
	_ py.Object      = (*countIterator)(nil)

	_ py.I__iter__ = (*cycleIterator)(nil)
	_ py.I__next__ = (*cycleIterator)(nil)
	_ py.Object      = (*cycleIterator)(nil)

	_ py.I__iter__ = (*repeatIterator)(nil)
	_ py.I__next__ = (*repeatIterator)(nil)
	_ py.Object      = (*repeatIterator)(nil)

	_ py.I__iter__ = (*chainIterator)(nil)
	_ py.I__next__ = (*chainIterator)(nil)
	_ py.Object      = (*chainIterator)(nil)

	_ py.I__iter__ = (*isliceIterator)(nil)
	_ py.I__next__ = (*isliceIterator)(nil)
	_ py.Object      = (*isliceIterator)(nil)

	_ py.I__iter__ = (*combIterator)(nil)
	_ py.I__next__ = (*combIterator)(nil)
	_ py.Object      = (*combIterator)(nil)

	_ py.I__iter__ = (*permIterator)(nil)
	_ py.I__next__ = (*permIterator)(nil)
	_ py.Object      = (*permIterator)(nil)

	_ py.I__iter__ = (*productIterator)(nil)
	_ py.I__next__ = (*productIterator)(nil)
	_ py.Object      = (*productIterator)(nil)
)
