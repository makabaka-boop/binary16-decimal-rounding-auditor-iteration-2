// Package decimal parses decimal measurement strings into exact rationals.
//
// No host floating point is involved: the mantissa digits and the decimal
// exponent are combined with integer arithmetic into a *big.Rat, so the
// later rounding decision can never observe a binary double-rounding.
package decimal

import (
	"errors"
	"math/big"
	"strconv"
	"strings"
)

const (
	// MaxSigDigits is the maximum number of significant digits accepted.
	MaxSigDigits = 30
	// MinExponent / MaxExponent bound the decimal exponent.
	MinExponent = -50
	MaxExponent = 50
)

var (
	// ErrFormat is returned for anything that is not a plain finite
	// decimal number (NaN, Infinity, hex, empty string, ...).
	ErrFormat = errors.New("invalid decimal format")
	// ErrTooManyDigits is returned when the mantissa carries more than
	// 30 significant digits.
	ErrTooManyDigits = errors.New("more than 30 significant digits")
	// ErrExponentRange is returned when the decimal exponent is outside
	// the inclusive range [-50, 50].
	ErrExponentRange = errors.New("decimal exponent outside [-50, 50]")
)

// Parse converts a decimal string such as "1.5", "-2e3" or "00.12500"
// into an exact rational. The boolean reports whether the input carried
// a minus sign (so callers can distinguish -0 from +0).
func Parse(s string) (negative bool, r *big.Rat, err error) {
	if s == "" {
		return false, nil, ErrFormat
	}

	p := 0
	switch s[p] {
	case '+':
		p++
	case '-':
		negative = true
		p++
	}
	if p == len(s) {
		return false, nil, ErrFormat
	}

	// Split into mantissa and optional exponent, at the same time
	// validating every character. A second 'e'/'E' fails like any
	// other character in its position.
	rest := s[p:]
	mantissa := rest
	expPart := ""
	if i := strings.IndexAny(rest, "eE"); i >= 0 {
		mantissa = rest[:i]
		expPart = rest[i+1:]
		if expPart == "" || strings.ContainsAny(expPart, "eE") {
			return false, nil, ErrFormat
		}
	}

	if mantissa == "" || strings.Count(mantissa, ".") > 1 {
		return false, nil, ErrFormat
	}

	intDigits := mantissa
	fracDigits := ""
	if dot := strings.IndexByte(mantissa, '.'); dot >= 0 {
		intDigits = mantissa[:dot]
		fracDigits = mantissa[dot+1:]
	}
	if intDigits == "" && fracDigits == "" {
		return false, nil, ErrFormat
	}
	for _, d := range intDigits {
		if d < '0' || d > '9' {
			return false, nil, ErrFormat
		}
	}
	for _, d := range fracDigits {
		if d < '0' || d > '9' {
			return false, nil, ErrFormat
		}
	}

	// Collect the digit sequence and locate the decimal point.
	digits := intDigits + fracDigits
	if digits == "" {
		return false, nil, ErrFormat
	}
	pointOffset := len(fracDigits) // digits moved left past the point

	// Leading zeros do not count as significant digits.
	significant := strings.TrimLeft(digits, "0")
	if len(significant) > MaxSigDigits {
		return false, nil, ErrTooManyDigits
	}

	exp := 0
	if expPart != "" {
		if expPart == "+" || expPart == "-" {
			return false, nil, ErrFormat
		}
		for i, d := range expPart {
			if d == '+' || d == '-' {
				if i != 0 {
					return false, nil, ErrFormat
				}
				continue
			}
			if d < '0' || d > '9' {
				return false, nil, ErrFormat
			}
		}
		exp, err = strconv.Atoi(expPart)
		if err != nil {
			// Overflow of the integer exponent is certainly out
			// of range.
			return false, nil, ErrExponentRange
		}
	}
	if exp < MinExponent || exp > MaxExponent {
		return false, nil, ErrExponentRange
	}

	if significant == "" {
		// An all-zero mantissa is exactly zero; keep the sign so
		// "-0" survives (Neg on a zero rational would erase it).
		return negative, new(big.Rat), nil
	}

	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return false, nil, ErrFormat
	}
	// Value = n * 10^exp / 10^pointOffset. The decimal exponent and
	// the fractional point shift are independent powers of ten.
	ten := big.NewInt(10)
	r = new(big.Rat)
	if exp >= 0 {
		r.SetInt(new(big.Int).Mul(n,
			new(big.Int).Exp(ten, big.NewInt(int64(exp)), nil)))
	} else {
		r.SetFrac(n, new(big.Int).Exp(ten, big.NewInt(int64(-exp)), nil))
	}
	if pointOffset > 0 {
		r.Quo(r, new(big.Rat).SetInt(
			new(big.Int).Exp(ten, big.NewInt(int64(pointOffset)), nil)))
	}
	if negative {
		r.Neg(r)
	}
	return negative, r, nil
}
