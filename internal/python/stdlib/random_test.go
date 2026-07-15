// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestRandomRandom(t *testing.T) {
	code := `
import random
random.seed(42)
r = random.random()
print(0.0 <= r < 1.0)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "True") {
		t.Errorf("expected True in output, got: %s", out)
	}
}

func TestRandomRandint(t *testing.T) {
	code := `
import random
random.seed(42)
r = random.randint(1, 10)
print(1 <= r <= 10)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "True") {
		t.Errorf("expected True in output, got: %s", out)
	}
}

func TestRandomChoice(t *testing.T) {
	code := `
import random
random.seed(42)
print(random.choice(["a", "b", "c"]))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "a") && !strings.Contains(out, "b") && !strings.Contains(out, "c") {
		t.Errorf("expected a choice in output, got: %s", out)
	}
}

func TestRandomShuffle(t *testing.T) {
	code := `
import random
lst = [1, 2, 3, 4, 5]
random.seed(42)
random.shuffle(lst)
print(len(lst))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "5") {
		t.Errorf("expected length 5 in output, got: %s", out)
	}
}

func TestRandomSample(t *testing.T) {
	code := `
import random
random.seed(42)
print(len(random.sample([1, 2, 3, 4, 5], 3)))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "3") {
		t.Errorf("expected sample size 3 in output, got: %s", out)
	}
}

func TestRandomUniform(t *testing.T) {
	code := `
import random
random.seed(42)
r = random.uniform(10, 20)
print(10 <= r < 20)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "True") {
		t.Errorf("expected True in output, got: %s", out)
	}
}

func TestRandomSeed(t *testing.T) {
	code := `
import random
random.seed(123)
a = random.random()
random.seed(123)
b = random.random()
print(a == b)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "True") {
		t.Errorf("expected True in output, got: %s", out)
	}
}

func TestRandomChoiceEmpty(t *testing.T) {
	code := `
import random
try:
    random.choice([])
    raise ValueError("expected error")
except IndexError:
    print("OK")
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "OK") {
		t.Errorf("expected OK in output, got: %s", out)
	}
}
