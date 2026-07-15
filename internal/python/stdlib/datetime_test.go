// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib_test

import (
	"strings"
	"testing"
)

func TestDatetimeNow(t *testing.T) {
	code := `
import datetime
n = datetime.now()
print(n.year)
print(n.month)
print(n.day)
print(n.hour)
print(n.minute)
print(n.second)
print(n.isoformat())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "-") {
		t.Errorf("expected ISO format with dash, got: %s", out)
	}
}

func TestDatetimeClassMethods(t *testing.T) {
	code := `
import datetime
n = datetime.datetime.now()
print(n.year)
print(n.month)
print(n.day)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"20", "", "", ""} {
		_ = want
	}
	if !strings.Contains(out, "20") {
		t.Errorf("expected year in output, got: %s", out)
	}
}

func TestDatetimeFromTimestamp(t *testing.T) {
	code := `
import datetime
dt = datetime.datetime.fromtimestamp(1609459200)
print(dt.strftime("%Y-%m-%d"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "2021-01-01") {
		t.Errorf("expected 2021-01-01 in output, got: %s", out)
	}
}

func TestDatetimeStrptime(t *testing.T) {
	code := `
import datetime
dt = datetime.datetime.strptime("2024-03-15 14:30:45", "%Y-%m-%d %H:%M:%S")
print(dt.year)
print(dt.month)
print(dt.day)
print(dt.hour)
print(dt.minute)
print(dt.second)
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"2024", "3", "15", "14", "30", "45"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestDatetimeArithmetic(t *testing.T) {
	code := `
import datetime
dt = datetime.datetime(2024, 1, 1, 12, 0, 0)
future = dt + datetime.timedelta(days=1, hours=2)
print(future.strftime("%Y-%m-%d %H:%M:%S"))
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "2024-01-02 14:00:00") {
		t.Errorf("expected 2024-01-02 14:00:00 in output, got: %s", out)
	}
}

func TestDateToday(t *testing.T) {
	code := `
import datetime
d = datetime.date.today()
print(d.year)
print(d.month)
print(d.day)
print(d.isoformat())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "20") {
		t.Errorf("expected year in output, got: %s", out)
	}
}

func TestTimedelta(t *testing.T) {
	code := `
import datetime
td = datetime.timedelta(days=1, hours=2, minutes=30)
print(td.days)
print(td.seconds)
print(td.total_seconds())
`
	out, err := pyCode(t, code)
	if err != nil {
		t.Fatalf("error: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "95400") {
		t.Errorf("expected total_seconds in output, got: %s", out)
	}
}
