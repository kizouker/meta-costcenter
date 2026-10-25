package costcenter

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Dec is an exact decimal amount of money. Rates and limits are decimals and token counts are
// integers, so every amount Cost Center computes is exact; nothing is rounded until it is shown.
// exp is the exponent Python's Decimal would carry, so String prints what the Python version
// printed, trailing zeros included (0.0, not 0). The zero value is 0.
type Dec struct {
	r   *big.Rat
	exp int
}

func decInt(n int64) Dec { return Dec{r: new(big.Rat).SetInt64(n)} }

func (d Dec) rat() *big.Rat {
	if d.r == nil {
		return new(big.Rat)
	}
	return d.r
}

// Add keeps the smaller exponent, as Decimal addition does.
func (d Dec) Add(o Dec) Dec { return Dec{new(big.Rat).Add(d.rat(), o.rat()), min(d.exp, o.exp)} }

func (d Dec) Mul(o Dec) Dec { return Dec{new(big.Rat).Mul(d.rat(), o.rat()), d.exp + o.exp} }

func (d Dec) MulInt(n int64) Dec {
	return Dec{new(big.Rat).Mul(d.rat(), new(big.Rat).SetInt64(n)), d.exp}
}

// DivInt divides exactly. Decimal strips trailing zeros from an exact quotient only down to the
// ideal exponent (the dividend's), so the result keeps whichever exponent is smaller.
func (d Dec) DivInt(n int64) Dec {
	q := new(big.Rat).Quo(d.rat(), new(big.Rat).SetInt64(n))
	return Dec{q, min(d.exp, -decimalPlaces(q))}
}

func (d Dec) Cmp(o Dec) int    { return d.rat().Cmp(o.rat()) }
func (d Dec) Sign() int        { return d.rat().Sign() }
func (d Dec) Float64() float64 { f, _ := d.rat().Float64(); return f }

// decimalPlaces is the fewest places that show r exactly; a decimal's denominator is 2^a·5^b.
func decimalPlaces(r *big.Rat) int {
	den := new(big.Int).Set(r.Denom())
	places := 0
	for _, p := range []*big.Int{big.NewInt(2), big.NewInt(5)} {
		n := 0
		for {
			q, m := new(big.Int).QuoRem(den, p, new(big.Int))
			if m.Sign() != 0 {
				break
			}
			den, n = q, n+1
		}
		places = max(places, n)
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		return 28 // not a decimal; Decimal's default precision
	}
	return places
}

// String is Python's str(Decimal) of the amount, such as 0.000102, 30, 0.0, or 2E-9.
func (d Dec) String() string {
	r := d.rat()
	exp := min(d.exp, -decimalPlaces(r))
	if exp > 0 {
		exp = 0
	}
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-exp)), nil)))
	coefficient := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	negative := coefficient.Sign() < 0
	return decimalText(negative, new(big.Int).Abs(coefficient).String(), exp)
}

// MarshalJSON writes the amount as a string, as the Python version did, so no precision is lost.
func (d Dec) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

// Fixed rounds half to even to the given places, optionally with thousands separators,
// matching Python's format(Decimal, ',.6f').
func (d Dec) Fixed(places int, grouping bool) string {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(places)), nil)
	scaled := new(big.Rat).Mul(d.rat(), new(big.Rat).SetInt(scale))
	negative := scaled.Sign() < 0
	scaled.Abs(scaled)
	q, rem := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
	// Compare twice the remainder with the denominator to decide the rounding.
	switch new(big.Int).Mul(rem, big.NewInt(2)).Cmp(scaled.Denom()) {
	case 1:
		q.Add(q, big.NewInt(1))
	case 0:
		if q.Bit(0) == 1 {
			q.Add(q, big.NewInt(1))
		}
	}
	digits := q.String()
	if len(digits) <= places {
		digits = strings.Repeat("0", places-len(digits)+1) + digits
	}
	whole, frac := digits[:len(digits)-places], digits[len(digits)-places:]
	if grouping {
		whole = groupThousands(whole)
	}
	s := whole
	if places > 0 {
		s += "." + frac
	}
	if negative && q.Sign() != 0 {
		s = "-" + s
	}
	return s
}

func groupThousands(digits string) string {
	var b strings.Builder
	for i, c := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// Decimal is a parsed decimal literal: its exact value, its exponent (the number of places is
// -Exponent), and its canonical text as Python's str(Decimal) prints it.
type Decimal struct {
	Value    Dec
	Exponent int
	Text     string
	Finite   bool
}

var decimalLiteral = regexp.MustCompile(`^([+-]?)(?:(\d+)(?:\.(\d*))?|\.(\d+))(?:[eE]([+-]?\d+))?$`)
var specialLiteral = regexp.MustCompile(`^[+-]?(?i:inf|infinity|nan|snan)\d*$`)

var errNotDecimal = errors.New("not a decimal number")

// ParseDecimal reads a decimal as Python's Decimal() does, including surrounding whitespace,
// exponents, and the non-finite values Infinity and NaN, which callers reject.
func ParseDecimal(text string) (Decimal, error) {
	text = strings.TrimSpace(text)
	if specialLiteral.MatchString(text) {
		return Decimal{Text: text}, nil
	}
	m := decimalLiteral.FindStringSubmatch(strings.ReplaceAll(text, "_", ""))
	if m == nil || strings.Contains(text, "_") {
		return Decimal{}, errNotDecimal
	}
	intPart, fracPart := m[2], m[3]
	if m[4] != "" {
		fracPart = m[4]
	}
	exponent := 0
	if m[5] != "" {
		e, err := strconv.Atoi(m[5])
		if err != nil {
			return Decimal{}, errNotDecimal
		}
		exponent = e
	}
	exponent -= len(fracPart)
	coefficient := strings.TrimLeft(intPart+fracPart, "0")
	if coefficient == "" {
		coefficient = "0"
	}
	value, _ := new(big.Rat).SetString(coefficient)
	if exponent != 0 {
		scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(abs(exponent))), nil))
		if exponent > 0 {
			value.Mul(value, scale)
		} else {
			value.Quo(value, scale)
		}
	}
	negative := m[1] == "-"
	if negative {
		value.Neg(value)
	}
	return Decimal{Value: Dec{value, exponent}, Exponent: exponent, Text: decimalText(negative, coefficient, exponent), Finite: true}, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// decimalText is Python's Decimal.__str__ (scientific notation only for very small or large exponents).
func decimalText(negative bool, coefficient string, exponent int) string {
	leftdigits := exponent + len(coefficient)
	dotplace := 1
	if exponent <= 0 && leftdigits > -6 {
		dotplace = leftdigits
	}
	var intpart, fracpart string
	switch {
	case dotplace <= 0:
		intpart, fracpart = "0", "."+strings.Repeat("0", -dotplace)+coefficient
	case dotplace >= len(coefficient):
		intpart = coefficient + strings.Repeat("0", dotplace-len(coefficient))
	default:
		intpart, fracpart = coefficient[:dotplace], "."+coefficient[dotplace:]
	}
	exp := ""
	if leftdigits != dotplace {
		exp = fmt.Sprintf("E%+d", leftdigits-dotplace)
	}
	sign := ""
	if negative {
		sign = "-"
	}
	return sign + intpart + fracpart + exp
}

// mustDec parses a decimal that is known to be valid, such as a rate already in the database.
func mustDec(text string) Dec {
	d, err := ParseDecimal(text)
	if err != nil || !d.Finite {
		return Dec{}
	}
	return d.Value
}
