// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package stdlib

import (
	"strings"
	"time"

	"github.com/go-python/gpython/py"

	"github.com/pijalu/goa/internal/python/compat"
)

// DateTime represents a Python datetime object.
type DateTime struct {
	t time.Time
}

// Date represents a Python date object.
type Date struct {
	t time.Time
}

// TimeDelta represents a Python timedelta object.
type TimeDelta struct {
	d time.Duration
}

var (
	datetimeType = py.NewTypeX("datetime", `datetime(year, month, day, hour=0, minute=0, second=0, microsecond=0)

Date/time type.`, datetimeNew, datetimeInit)
	dateType = py.NewTypeX("date", `date(year, month, day)

Date type.`, dateNew, dateInit)
	timedeltaType = py.NewTypeX("timedelta", `timedelta(days=0, seconds=0, microseconds=0, milliseconds=0, minutes=0, hours=0, weeks=0)

Duration type.`, timedeltaNew, timedeltaInit)
)

func init() {
	py.RegisterModule(&py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: "datetime",
			Doc:  "datetime — date and time types",
		},
		Methods: []*py.Method{
			py.MustNewMethod("now", datetimeNow, 0, `now() -> datetime

Return the current local date and time.`),
			py.MustNewMethod("today", datetimeNow, 0, `today() -> datetime

Return the current local date and time.`),
			py.MustNewMethod("fromtimestamp", datetimeFromTimestamp, 0, `fromtimestamp(timestamp) -> datetime

Return a datetime from a POSIX timestamp.`),
			py.MustNewMethod("strptime", datetimeStrptime, 0, `strptime(date_string, format) -> datetime

Return a datetime parsed from the given string and format.`),
		},
		Globals: py.StringDict{
			"datetime":  datetimeType,
			"date":      dateType,
			"timedelta": timedeltaType,
		},
	})

	// datetime class methods
	datetimeType.Dict["now"] = py.MustNewMethod("now", datetimeNow, 0, `now() -> datetime

Return the current local date and time.`)
	datetimeType.Dict["today"] = py.MustNewMethod("today", datetimeNow, 0, `today() -> datetime

Return the current local date and time.`)
	datetimeType.Dict["fromtimestamp"] = py.MustNewMethod("fromtimestamp", datetimeFromTimestamp, 0, `fromtimestamp(timestamp) -> datetime

Return a datetime from a POSIX timestamp.`)
	datetimeType.Dict["strptime"] = py.MustNewMethod("strptime", datetimeStrptime, 0, `strptime(date_string, format) -> datetime

Return a datetime parsed from the given string and format.`)
	datetimeType.Dict["isoformat"] = py.MustNewMethod("isoformat", datetimeIsoformat, 0, `isoformat() -> str

Return a string in ISO 8601 format.`)
	datetimeType.Dict["strftime"] = py.MustNewMethod("strftime", datetimeStrftime, 0, `strftime(format) -> str

Return a string formatted by format.`)

	// date class methods
	dateType.Dict["today"] = py.MustNewMethod("today", dateToday, 0, `today() -> date

Return the current local date.`)
	dateType.Dict["isoformat"] = py.MustNewMethod("isoformat", dateIsoformat, 0, `isoformat() -> str

Return a string in ISO 8601 format.`)

	// timedelta methods
	timedeltaType.Dict["total_seconds"] = py.MustNewMethod("total_seconds", timedeltaTotalSeconds, 0, `total_seconds() -> float

Return the total seconds in the duration.`)
}

// Type returns the datetime type.
func (d *DateTime) Type() *py.Type { return datetimeType }

// M__getattr__ exposes datetime fields as read-only attributes.
func (d *DateTime) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "year":
		return py.Int(d.t.Year()), nil
	case "month":
		return py.Int(int(d.t.Month())), nil
	case "day":
		return py.Int(d.t.Day()), nil
	case "hour":
		return py.Int(d.t.Hour()), nil
	case "minute":
		return py.Int(d.t.Minute()), nil
	case "second":
		return py.Int(d.t.Second()), nil
	case "microsecond":
		return py.Int(d.t.Nanosecond() / 1000), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'datetime' object has no attribute '%s'", name)
}

// M__add__ adds a timedelta to a datetime.
func (d *DateTime) M__add__(other py.Object) (py.Object, error) {
	td, ok := other.(*TimeDelta)
	if !ok {
		return py.NotImplemented, nil
	}
	return &DateTime{t: d.t.Add(td.d)}, nil
}

// M__sub__ subtracts a timedelta from a datetime, or datetime from datetime.
func (d *DateTime) M__sub__(other py.Object) (py.Object, error) {
	switch v := other.(type) {
	case *TimeDelta:
		return &DateTime{t: d.t.Add(-v.d)}, nil
	case *DateTime:
		return &TimeDelta{d: d.t.Sub(v.t)}, nil
	}
	return py.NotImplemented, nil
}

// Type returns the date type.
func (d *Date) Type() *py.Type { return dateType }

// M__getattr__ exposes date fields as read-only attributes.
func (d *Date) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "year":
		return py.Int(d.t.Year()), nil
	case "month":
		return py.Int(int(d.t.Month())), nil
	case "day":
		return py.Int(d.t.Day()), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'date' object has no attribute '%s'", name)
}

// Type returns the timedelta type.
func (td *TimeDelta) Type() *py.Type { return timedeltaType }

// M__getattr__ exposes timedelta fields as read-only attributes.
func (td *TimeDelta) M__getattr__(name string) (py.Object, error) {
	switch name {
	case "days":
		return py.Int(int(td.d / (24 * time.Hour))), nil
	case "seconds":
		secs := int(td.d.Seconds()) % 86400
		return py.Int(secs), nil
	case "microseconds":
		us := (td.d % time.Second) / time.Microsecond
		return py.Int(us), nil
	}
	return nil, py.ExceptionNewf(py.AttributeError, "'timedelta' object has no attribute '%s'", name)
}

// M__add__ adds two timedeltas, or a timedelta to a datetime.
func (td *TimeDelta) M__add__(other py.Object) (py.Object, error) {
	switch v := other.(type) {
	case *TimeDelta:
		return &TimeDelta{d: td.d + v.d}, nil
	case *DateTime:
		return v.M__add__(td)
	}
	return py.NotImplemented, nil
}

// M__sub__ subtracts two timedeltas.
func (td *TimeDelta) M__sub__(other py.Object) (py.Object, error) {
	if v, ok := other.(*TimeDelta); ok {
		return &TimeDelta{d: td.d - v.d}, nil
	}
	return py.NotImplemented, nil
}

// M__radd__ adds a timedelta to a datetime (right-hand side).
func (td *TimeDelta) M__radd__(other py.Object) (py.Object, error) {
	if v, ok := other.(*DateTime); ok {
		return v.M__add__(td)
	}
	return py.NotImplemented, nil
}

// M__rsub__ subtracts a timedelta from a datetime (right-hand side).
func (td *TimeDelta) M__rsub__(other py.Object) (py.Object, error) {
	if v, ok := other.(*DateTime); ok {
		return v.M__sub__(td)
	}
	return py.NotImplemented, nil
}

// M__mul__ multiplies a timedelta by a number.
func (td *TimeDelta) M__mul__(other py.Object) (py.Object, error) {
	n, err := numberToFloat(other, "timedelta")
	if err != nil {
		return py.NotImplemented, nil
	}
	return &TimeDelta{d: time.Duration(float64(td.d) * n)}, nil
}

// M__rmul__ multiplies a number by a timedelta.
func (td *TimeDelta) M__rmul__(other py.Object) (py.Object, error) {
	return td.M__mul__(other)
}

// M__truediv__ divides a timedelta by a number.
func (td *TimeDelta) M__truediv__(other py.Object) (py.Object, error) {
	n, err := numberToFloat(other, "timedelta")
	if err != nil {
		return py.NotImplemented, nil
	}
	if n == 0 {
		return nil, py.ExceptionNewf(py.ZeroDivisionError, "timedelta division by zero")
	}
	return &TimeDelta{d: time.Duration(float64(td.d) / n)}, nil
}

// --- Type construction ---

func datetimeNew(t *py.Type, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	return &DateTime{}, nil
}

func datetimeInit(self py.Object, args py.Tuple, kwargs py.StringDict) error {
	d, ok := self.(*DateTime)
	if !ok {
		return py.ExceptionNewf(py.TypeError, "expected datetime, got %s", self.Type().Name)
	}
	obj, err := initDateTime(args, false)
	if err != nil {
		return err
	}
	*d = *(obj.(*DateTime))
	return nil
}

func dateNew(t *py.Type, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	return &Date{}, nil
}

func dateInit(self py.Object, args py.Tuple, kwargs py.StringDict) error {
	d, ok := self.(*Date)
	if !ok {
		return py.ExceptionNewf(py.TypeError, "expected date, got %s", self.Type().Name)
	}
	obj, err := initDateTime(args, true)
	if err != nil {
		return err
	}
	*d = *(obj.(*Date))
	return nil
}

func timedeltaNew(t *py.Type, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	return &TimeDelta{}, nil
}

func timedeltaInit(self py.Object, args py.Tuple, kwargs py.StringDict) error {
	td, ok := self.(*TimeDelta)
	if !ok {
		return py.ExceptionNewf(py.TypeError, "expected timedelta, got %s", self.Type().Name)
	}
	obj, err := newTimeDelta(args, kwargs)
	if err != nil {
		return err
	}
	*td = *(obj.(*TimeDelta))
	return nil
}

// --- Module/class functions ---

func datetimeNow(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("now", 0)
	}
	return &DateTime{t: time.Now()}, nil
}

func datetimeFromTimestamp(self py.Object, args py.Tuple) (py.Object, error) {
	var ts py.Object
	if err := py.UnpackTuple(args, nil, "fromtimestamp", 1, 1, &ts); err != nil {
		return nil, err
	}
	secs, err := compat.AsFloat(ts, "fromtimestamp")
	if err != nil {
		return nil, err
	}
	return &DateTime{t: time.Unix(int64(secs), int64((secs-float64(int64(secs)))*1e9))}, nil
}

func datetimeStrptime(self py.Object, args py.Tuple) (py.Object, error) {
	var dateStr, format py.Object
	if err := py.UnpackTuple(args, nil, "strptime", 2, 2, &dateStr, &format); err != nil {
		return nil, err
	}
	s, err := compat.AsString(dateStr, "strptime")
	if err != nil {
		return nil, err
	}
	f, err := compat.AsString(format, "strptime")
	if err != nil {
		return nil, err
	}
	layout := pythonToGoFormat(f)
	t, err := time.Parse(layout, s)
	if err != nil {
		return nil, py.ExceptionNewf(py.ValueError, "strptime() failed: %v", err)
	}
	return &DateTime{t: t}, nil
}

func datetimeIsoformat(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("isoformat", 0)
	}
	d, ok := self.(*DateTime)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected datetime, got %s", self.Type().Name)
	}
	return py.String(d.t.Format(time.RFC3339Nano)), nil
}

func datetimeStrftime(self py.Object, args py.Tuple) (py.Object, error) {
	var format py.Object
	if err := py.UnpackTuple(args, nil, "strftime", 1, 1, &format); err != nil {
		return nil, err
	}
	f, err := compat.AsString(format, "strftime")
	if err != nil {
		return nil, err
	}
	d, ok := self.(*DateTime)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected datetime, got %s", self.Type().Name)
	}
	return py.String(d.t.Format(pythonToGoFormat(f))), nil
}

func dateToday(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("today", 0)
	}
	return &Date{t: time.Now().Truncate(24 * time.Hour)}, nil
}

func dateIsoformat(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("isoformat", 0)
	}
	d, ok := self.(*Date)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected date, got %s", self.Type().Name)
	}
	return py.String(d.t.Format("2006-01-02")), nil
}

func timedeltaTotalSeconds(self py.Object, args py.Tuple) (py.Object, error) {
	if len(args) != 0 {
		return nil, compat.TooManyArgsError("total_seconds", 0)
	}
	td, ok := self.(*TimeDelta)
	if !ok {
		return nil, py.ExceptionNewf(py.TypeError, "expected timedelta, got %s", self.Type().Name)
	}
	return py.Float(td.d.Seconds()), nil
}

// --- Internal helpers ---

func initDateTime(args py.Tuple, dateOnly bool) (py.Object, error) {
	minArgs, maxArgs := 3, 7
	if dateOnly {
		maxArgs = 3
	}
	if len(args) < minArgs {
		return nil, compat.TooFewArgsError("__init__", minArgs)
	}
	if len(args) > maxArgs {
		return nil, compat.TooManyArgsError("__init__", maxArgs)
	}
	y, mo, d, h, mi, s, us, err := parseDateTimeArgs(args, maxArgs)
	if err != nil {
		return nil, err
	}
	t := time.Date(int(y), time.Month(mo), int(d), int(h), int(mi), int(s), int(us)*1000, time.Local)
	if dateOnly {
		return &Date{t: t}, nil
	}
	return &DateTime{t: t}, nil
}

func parseDateTimeArgs(args py.Tuple, maxArgs int) (y, mo, d, h, mi, s, us int64, err error) {
	var year, month, day, hour, minute, second, microsecond py.Object = py.Int(0), py.Int(0), py.Int(0), py.Int(0), py.Int(0), py.Int(0), py.Int(0)
	if err = py.UnpackTuple(args, nil, "__init__", 3, maxArgs, &year, &month, &day, &hour, &minute, &second, &microsecond); err != nil {
		return 0, 0, 0, 0, 0, 0, 0, err
	}
	objs := []py.Object{year, month, day, hour, minute, second, microsecond}
	vals := make([]int64, len(objs))
	for i, obj := range objs {
		vals[i], err = compat.AsInt(obj, "__init__")
		if err != nil {
			return 0, 0, 0, 0, 0, 0, 0, err
		}
	}
	return vals[0], vals[1], vals[2], vals[3], vals[4], vals[5], vals[6], nil
}

func newTimeDelta(args py.Tuple, kwargs py.StringDict) (py.Object, error) {
	if len(args) > 0 {
		return nil, py.ExceptionNewf(py.TypeError, "timedelta() takes no positional arguments")
	}
	values := map[string]int64{
		"days": 0, "seconds": 0, "microseconds": 0,
		"milliseconds": 0, "minutes": 0, "hours": 0, "weeks": 0,
	}
	for k, def := range values {
		if v, ok := kwargs[k]; ok {
			val, err := compat.AsInt(v, "timedelta")
			if err != nil {
				return nil, err
			}
			values[k] = val
		} else {
			values[k] = def
		}
	}
	for k := range kwargs {
		if _, ok := values[k]; !ok {
			return nil, py.ExceptionNewf(py.TypeError, "timedelta() got an unexpected keyword argument '%s'", k)
		}
	}
	d := time.Duration(values["days"])*24*time.Hour +
		time.Duration(values["seconds"])*time.Second +
		time.Duration(values["microseconds"])*time.Microsecond +
		time.Duration(values["milliseconds"])*time.Millisecond +
		time.Duration(values["minutes"])*time.Minute +
		time.Duration(values["hours"])*time.Hour +
		time.Duration(values["weeks"])*7*24*time.Hour
	return &TimeDelta{d: d}, nil
}

func numberToFloat(o py.Object, fn string) (float64, error) {
	switch v := o.(type) {
	case py.Int:
		return float64(v), nil
	case py.Float:
		return float64(v), nil
	case py.Bool:
		if v {
			return 1, nil
		}
		return 0, nil
	}
	return 0, compat.FormatError(fn, "number", o)
}

// pythonToGoFormat converts Python strftime/strptime directives to Go layout.
func pythonToGoFormat(format string) string {
	replacements := []struct{ from, to string }{
		{"%Y", "2006"},
		{"%m", "01"},
		{"%d", "02"},
		{"%H", "15"},
		{"%M", "04"},
		{"%S", "05"},
		{"%f", "000000"},
	}
	for _, r := range replacements {
		format = strings.ReplaceAll(format, r.from, r.to)
	}
	return format
}

// Ensure types implement py interfaces.
var (
	_ py.I__getattr__ = (*DateTime)(nil)
	_ py.I__add__     = (*DateTime)(nil)
	_ py.I__sub__     = (*DateTime)(nil)
	_ py.Object        = (*DateTime)(nil)

	_ py.I__getattr__ = (*Date)(nil)
	_ py.Object        = (*Date)(nil)

	_ py.I__getattr__ = (*TimeDelta)(nil)
	_ py.I__add__     = (*TimeDelta)(nil)
	_ py.I__sub__     = (*TimeDelta)(nil)
	_ py.I__radd__    = (*TimeDelta)(nil)
	_ py.I__rsub__    = (*TimeDelta)(nil)
	_ py.I__mul__     = (*TimeDelta)(nil)
	_ py.I__rmul__    = (*TimeDelta)(nil)
	_ py.I__truediv__ = (*TimeDelta)(nil)
	_ py.Object        = (*TimeDelta)(nil)
)
