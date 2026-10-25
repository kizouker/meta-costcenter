package costcenter

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"
)

// Times are stored as Python's datetime.isoformat() wrote them, for example
// 2026-10-25T17:23:37.630946+00:00, because the database orders and compares them as text.

// now is replaceable in tests.
var now = time.Now

func isoformat(t time.Time) string {
	t = t.UTC()
	base := t.Format("2006-01-02T15:04:05")
	if micro := t.Nanosecond() / 1000; micro != 0 {
		base += fmt.Sprintf(".%06d", micro)
	}
	return base + "+00:00"
}

func nowISO() string { return isoformat(now()) }

var isoPattern = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2})(?::(\d{2})(?::(\d{2})(?:[.,](\d+))?)?)?` +
	`(Z|[+-]\d{2}(?::?\d{2}(?::?\d{2}(?:\.\d+)?)?)?)?)?$`)

var errNaive = errors.New("timestamp requires timezone")

// parseISO reads the forms of ISO 8601 that Python's datetime.fromisoformat accepts in practice.
// A timestamp without a timezone is an error, as the Python collector required.
func parseISO(text string) (time.Time, error) {
	m := isoPattern.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, fmt.Errorf("invalid isoformat string: %q", text)
	}
	num := func(s string) int { n, _ := strconv.Atoi(s); return n }
	nanos := 0
	if m[7] != "" {
		digits := (m[7] + "000000000")[:9]
		nanos = num(digits)
		// Python keeps microseconds only.
		nanos -= nanos % 1000
	}
	if m[8] == "" {
		return time.Time{}, errNaive
	}
	location := time.UTC
	if m[8] != "Z" {
		offset, err := parseOffset(m[8])
		if err != nil {
			return time.Time{}, err
		}
		location = time.FixedZone("", offset)
	}
	t := time.Date(num(m[1]), time.Month(num(m[2])), num(m[3]), num(m[4]), num(m[5]), num(m[6]), nanos, location)
	if t.Day() != num(m[3]) || t.Hour() != num(m[4]) || t.Minute() != num(m[5]) || t.Second() != num(m[6]) {
		return time.Time{}, fmt.Errorf("invalid isoformat string: %q", text)
	}
	return t, nil
}

var offsetPattern = regexp.MustCompile(`^([+-])(\d{2}):?(\d{2})?(?::?(\d{2}))?`)

func parseOffset(text string) (int, error) {
	m := offsetPattern.FindStringSubmatch(text)
	if m == nil {
		return 0, fmt.Errorf("invalid offset %q", text)
	}
	h, _ := strconv.Atoi(m[2])
	mm, _ := strconv.Atoi(m[3])
	ss, _ := strconv.Atoi(m[4])
	if h > 23 || mm > 59 || ss > 59 {
		return 0, fmt.Errorf("invalid offset %q", text)
	}
	seconds := h*3600 + mm*60 + ss
	if m[1] == "-" {
		seconds = -seconds
	}
	return seconds, nil
}

// localTime parses a stored timestamp into the local timezone.
func localTime(stored string) time.Time {
	t, err := parseISO(stored)
	if err != nil {
		return time.Time{}
	}
	return t.Local()
}

// localDay is the local calendar date of a stored timestamp, as YYYY-MM-DD.
func localDay(stored string) string { return localTime(stored).Format("2006-01-02") }

// minYear and maxYear are the range Python's datetime supports.
const minYear, maxYear = 1, 9999

// timestampFromNumber converts a numeric record timestamp: Unix seconds, milliseconds, or Muse's
// native microseconds, told apart by magnitude.
func timestampFromNumber(integer bool, i int64, f float64) (string, error) {
	var t time.Time
	if integer {
		switch {
		case i > 100_000_000_000_000:
			t = time.UnixMicro(i)
		case i > 100_000_000_000:
			t = time.UnixMilli(i)
		default:
			if i > math.MaxInt64/1_000_000 || i < math.MinInt64/1_000_000 {
				return "", errors.New("timestamp out of range")
			}
			t = time.Unix(i, 0)
		}
	} else {
		switch {
		case f > 100_000_000_000_000:
			f /= 1_000_000
		case f > 100_000_000_000:
			f /= 1000
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > 1e12 {
			return "", errors.New("timestamp out of range")
		}
		seconds := math.Floor(f)
		// Python rounds the fraction to microseconds, half to even.
		micro := math.RoundToEven((f - seconds) * 1e6)
		t = time.Unix(int64(seconds), 0).Add(time.Duration(micro) * time.Microsecond)
	}
	if y := t.UTC().Year(); y < minYear || y > maxYear {
		return "", errors.New("timestamp out of range")
	}
	return isoformat(t), nil
}

// parseDate reads YYYY-MM-DD (or YYYYMMDD), as date.fromisoformat does.
func parseDate(text string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "20060102"} {
		if len(text) == len(layout) {
			if d, err := time.ParseInLocation(layout, text, time.Local); err == nil {
				return d, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("Invalid isoformat string: %s", pyRepr(text))
}

// today is the local date at midnight.
func today() time.Time {
	t := now()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

func dateString(t time.Time) string { return t.Format("2006-01-02") }
