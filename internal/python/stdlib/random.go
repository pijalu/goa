// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"math/rand"
	"sync"
	"time"

	"github.com/go-python/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

var (
	rngMu sync.Mutex
	rng   = rand.New(rand.NewSource(time.Now().UnixNano()))
)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "random",
			Doc:  "random — pseudo-random number generators",
		},
		Methods: []*py.Method{
			py.MustNewMethod("random", randomRandom, 0, `random() -> float

Return a random float in [0.0, 1.0).`),
			py.MustNewMethod("randint", randomRandint, 0, `randint(a, b) -> int

Return a random integer in [a, b].`),
			py.MustNewMethod("choice", randomChoice, 0, `choice(seq) -> item

Return a random element from a non-empty sequence.`),
			py.MustNewMethod("shuffle", randomShuffle, 0, `shuffle(seq)

Shuffle the sequence in place.`),
			py.MustNewMethod("sample", randomSample, 0, `sample(population, k) -> list

Return a k-length list of unique elements chosen from the population.`),
			py.MustNewMethod("uniform", randomUniform, 0, `uniform(a, b) -> float

Return a random float in [a, b).`),
			py.MustNewMethod("seed", randomSeed, 0, `seed(a)

Initialize the random number generator.`),
		},
	})
}

func randomRandom(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("random", 0)
	}
	rngMu.Lock()
	defer rngMu.Unlock()
	return py.Float(rng.Float64()), nil
}

func randomRandint(self py.Object, args py.Tuple) (py.Object, error) {
	var a, b py.Object
	if err := py.UnpackTuple(args, nil, "randint", 2, 2, &a, &b); err != nil {
		return nil, err
	}
	ai, err := compat.AsInt(a, "randint")
	if err != nil {
		return nil, err
	}
	bi, err := compat.AsInt(b, "randint")
	if err != nil {
		return nil, err
	}
	if ai > bi {
		return nil, py.ExceptionNewf(py.ValueError, "randint() empty range")
	}
	rngMu.Lock()
	defer rngMu.Unlock()
	return py.Int(rng.Intn(int(bi-ai+1)) + int(ai)), nil
}

func randomChoice(self py.Object, args py.Tuple) (py.Object, error) {
	var seq py.Object
	if err := py.UnpackTuple(args, nil, "choice", 1, 1, &seq); err != nil {
		return nil, err
	}
	items, err := compat.IterItems(seq)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, py.ExceptionNewf(py.IndexError, "choice() from empty sequence")
	}
	rngMu.Lock()
	defer rngMu.Unlock()
	return items[rng.Intn(len(items))], nil
}

func randomShuffle(self py.Object, args py.Tuple) (py.Object, error) {
	var seq py.Object
	if err := py.UnpackTuple(args, nil, "shuffle", 1, 1, &seq); err != nil {
		return nil, err
	}
	lst, ok := seq.(*py.List)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "shuffle() only supports list, not %s", seq.Type().Name)
	}
	rngMu.Lock()
	defer rngMu.Unlock()
	rng.Shuffle(len(lst.Items), func(i, j int) {
		lst.Items[i], lst.Items[j] = lst.Items[j], lst.Items[i]
	})
	return py.None, nil
}

func randomSample(self py.Object, args py.Tuple) (py.Object, error) {
	var population, k py.Object
	if err := py.UnpackTuple(args, nil, "sample", 2, 2, &population, &k); err != nil {
		return nil, err
	}
	ki, err := compat.AsInt(k, "sample")
	if err != nil {
		return nil, err
	}
	if ki < 0 {
		return nil, py.ExceptionNewf(py.ValueError, "sample() negative sample size")
	}
	items, err := compat.IterItems(population)
	if err != nil {
		return nil, err
	}
	if int(ki) > len(items) {
		return nil, py.ExceptionNewf(py.ValueError, "sample() sample size larger than population")
	}
	rngMu.Lock()
	defer rngMu.Unlock()
	rng.Shuffle(len(items), func(i, j int) {
		items[i], items[j] = items[j], items[i]
	})
	result := make([]py.Object, ki)
	for i := int64(0); i < ki; i++ {
		result[i] = items[i]
	}
	return py.NewListFromItems(result), nil
}

func randomUniform(self py.Object, args py.Tuple) (py.Object, error) {
	var a, b py.Object
	if err := py.UnpackTuple(args, nil, "uniform", 2, 2, &a, &b); err != nil {
		return nil, err
	}
	af, err := compat.AsFloat(a, "uniform")
	if err != nil {
		return nil, err
	}
	bf, err := compat.AsFloat(b, "uniform")
	if err != nil {
		return nil, err
	}
	rngMu.Lock()
	defer rngMu.Unlock()
	return py.Float(af + rng.Float64()*(bf-af)), nil
}

func randomSeed(self py.Object, args py.Tuple) (py.Object, error) {
	var s py.Object
	if err := py.UnpackTuple(args, nil, "seed", 1, 1, &s); err != nil {
		return nil, err
	}
	var seedVal int64
	switch v := s.(type) {
	case py.Int:
		seedVal = int64(v)
	case py.Float:
		seedVal = int64(v)
	case py.String:
		for _, r := range v {
			seedVal = seedVal*31 + int64(r)
		}
	case py.Bytes:
		for _, b := range v {
			seedVal = seedVal*31 + int64(b)
		}
	default:
		return nil, py.ExceptionNewf(py.TypeError, "seed() argument must be int, float, str, or bytes")
	}
	rngMu.Lock()
	defer rngMu.Unlock()
	rng.Seed(seedVal)
	return py.None, nil
}
