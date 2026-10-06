package api

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// FlexString decodes a JSON string, number, bool or null into a string. The
// HSE API is inconsistent about ID types (e.g. program_id is a number in
// ratings and a zero-padded string in grades), so every identifier uses it.
type FlexString string

func (f *FlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || string(b) == "null":
		*f = ""
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = FlexString(s)
	case b[0] == '{' || b[0] == '[':
		// Unexpected shape: keep decoding the rest of the document.
		*f = ""
	default:
		*f = FlexString(string(b))
	}
	return nil
}

func (f FlexString) String() string { return string(f) }

// Num is an optional number. It accepts numbers, numeric strings (with a
// comma or dot decimal separator) and null; anything else decodes as "unset"
// instead of failing the whole response.
type Num struct {
	V  float64
	OK bool
}

func (n *Num) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" || s == "" {
		*n = Num{}
		return nil
	}
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal([]byte(s), &str); err != nil {
			*n = Num{}
			return nil
		}
		s = strings.ReplaceAll(strings.TrimSpace(str), ",", ".")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		// "NaN"/"Infinity" strings parse but can't be shown or re-encoded.
		*n = Num{}
		return nil
	}
	*n = Num{V: v, OK: true}
	return nil
}

func (n Num) MarshalJSON() ([]byte, error) {
	if !n.OK {
		return []byte("null"), nil
	}
	return []byte(strconv.FormatFloat(n.V, 'f', -1, 64)), nil
}

// Int returns the value truncated to an int (0 when unset).
func (n Num) Int() int { return int(n.V) }

// String formats the number compactly ("7", "6.73"), or "—" when unset.
func (n Num) String() string {
	if !n.OK {
		return "—"
	}
	return strconv.FormatFloat(n.V, 'f', -1, 64)
}

// Fixed formats with exactly prec decimals, or "—" when unset.
func (n Num) Fixed(prec int) string {
	if !n.OK {
		return "—"
	}
	return strconv.FormatFloat(n.V, 'f', prec, 64)
}

func NewNum(v float64) Num { return Num{V: v, OK: true} }

// FlexTime parses the handful of date formats the API uses. Unparseable or
// null values decode to the zero time instead of failing.
type FlexTime struct{ time.Time }

var timeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000Z0700",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

func (t *FlexTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		t.Time = time.Time{}
		return nil
	}
	t.Time = ParseTime(s)
	return nil
}

func (t FlexTime) MarshalJSON() ([]byte, error) {
	if t.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(t.Time.Format(time.RFC3339Nano))
}

// ParseTime parses s with the known API layouts. Values without a zone are
// interpreted as Moscow time, which is what the HSE backend means by them.
func ParseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range timeLayouts {
		var (
			t   time.Time
			err error
		)
		if strings.Contains(l, "Z07") || l == time.RFC3339 || l == time.RFC3339Nano {
			t, err = time.Parse(l, s)
		} else {
			t, err = time.ParseInLocation(l, s, Moscow)
		}
		if err == nil {
			return t
		}
	}
	return time.Time{}
}
