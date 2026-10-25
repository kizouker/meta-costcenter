package costcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The Python version wrote some values to the database in Python's own formats: the collector's
// resume anchor is json.dumps of three record fields, and alert warning levels are json.dumps of
// a list. Reproducing those formats keeps an existing database valid; a different anchor would
// make every source look replaced on the first scan.

// decodeJSON parses one JSON value, keeping numbers as their literal text.
func decodeJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	// Python's json.loads rejects trailing data other than whitespace.
	if _, err := decoder.Token(); err == nil {
		return nil, fmt.Errorf("extra data after JSON value")
	}
	return value, nil
}

// isIntLiteral reports whether a JSON number is an integer, which Python reads as int, not float.
func isIntLiteral(n json.Number) bool { return !strings.ContainsAny(string(n), ".eE") }

// pyFloat is repr(float): the shortest round-tripping digits, fixed notation from 1e-4 to 1e16.
func pyFloat(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		return "nan"
	}
	sci := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	mantissa, expText, _ := strings.Cut(sci, "e")
	exp, _ := strconv.Atoi(expText)
	if exp < -4 || exp >= 16 {
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		return fmt.Sprintf("%se%s%02d", mantissa, sign, exp)
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.ContainsAny(s, ".") {
		s += ".0"
	}
	return s
}

// pyNumber renders a JSON number literal as Python would after json.loads.
func pyNumber(n json.Number) string {
	if isIntLiteral(n) {
		if i, ok := new(big.Int).SetString(string(n), 10); ok {
			return i.String()
		}
		return string(n)
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		return string(n)
	}
	return pyFloat(f)
}

// pyDumps is json.dumps with its default separators and ensure_ascii.
func pyDumps(value any) string {
	var b strings.Builder
	writePy(&b, value)
	return b.String()
}

func writePy(b *strings.Builder, value any) {
	switch v := value.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case json.Number:
		if isIntLiteral(v) {
			b.WriteString(pyNumber(v))
		} else if f, err := strconv.ParseFloat(string(v), 64); err == nil {
			switch {
			case math.IsInf(f, 1):
				b.WriteString("Infinity")
			case math.IsInf(f, -1):
				b.WriteString("-Infinity")
			default:
				b.WriteString(pyFloat(f))
			}
		} else {
			b.WriteString(string(v))
		}
	case int:
		b.WriteString(strconv.Itoa(v))
	case int64:
		b.WriteString(strconv.FormatInt(v, 10))
	case string:
		writePyString(b, v)
	case []any:
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteString(", ")
			}
			writePy(b, item)
		}
		b.WriteByte(']')
	case []int:
		items := make([]any, len(v))
		for i, n := range v {
			items[i] = n
		}
		writePy(b, items)
	case map[string]any:
		// Go maps have no order; sorted keys are the closest stable choice.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			writePyString(b, k)
			b.WriteString(": ")
			writePy(b, v[k])
		}
		b.WriteByte('}')
	default:
		writePyString(b, fmt.Sprint(v))
	}
}

func writePyString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r < 0x20 || (r > 0x7f && r <= 0xffff):
				fmt.Fprintf(b, `\u%04x`, r)
			case r > 0xffff:
				r -= 0x10000
				fmt.Fprintf(b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
			default:
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// pyStr is Python's str() of a value decoded from JSON, used where the Python version built
// identifiers with f-strings or str().
func pyStr(value any) string {
	switch v := value.(type) {
	case nil:
		return "None"
	case bool:
		if v {
			return "True"
		}
		return "False"
	case string:
		return v
	case json.Number:
		return pyNumber(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	}
	return pyDumps(value)
}

// pyRepr is repr() of a string: single quotes unless the string contains one and no double quote.
func pyRepr(s string) string {
	quote := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		quote = `"`
	}
	var b strings.Builder
	b.WriteString(quote)
	for _, r := range s {
		switch {
		case string(r) == quote || r == '\\':
			b.WriteRune('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(quote)
	return b.String()
}

// truthy is Python truthiness for a value decoded from JSON.
func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		f, err := strconv.ParseFloat(string(v), 64)
		return err != nil || f != 0
	case float64:
		return v != 0
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

// padRight and padLeft pad by characters, as Python's format widths do.
func padRight(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

func padLeft(s string, width int) string {
	if n := utf8.RuneCountInString(s); n < width {
		return strings.Repeat(" ", width-n) + s
	}
	return s
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func commas(n int64) string {
	if n < 0 {
		return "-" + groupThousands(strconv.FormatInt(-n, 10))
	}
	return groupThousands(strconv.FormatInt(n, 10))
}

// pyFloatJSON is a float written as Python's json module writes one: 0.0 rather than 0.
type pyFloatJSON float64

func (f pyFloatJSON) MarshalJSON() ([]byte, error) { return []byte(pyFloat(float64(f))), nil }
