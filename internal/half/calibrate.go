// Calibration support: find one common additive bias b such that adding
// it to every original measurement v_i and rounding the exact rational
// sum v_i+b to binary16 produces the auditor-supplied target pattern.
//
// Every accepted bias set is an interval with exact rational bounds
// (the target pattern's rounding-preimage interval shifted by -v_i), so
// the whole batch is feasible exactly when all shifted intervals
// intersect. All arithmetic is exact: no host floating point, no
// decimal approximation of any bias.

package half

import "math/big"

// PatternBits decodes a raw binary16 bit pattern into Bits. It accepts
// finite patterns and the two infinities but reports ok=false for every
// NaN encoding (E==31 with F!=0).
func PatternBits(p uint16) (Bits, bool) {
	sign := p >> 15
	E := (p >> 10) & 0x1f
	F := p & 0x3ff
	if E == 31 {
		if F != 0 {
			return Bits{}, false
		}
		return Bits{Sign: sign, E: 31, F: 0, Value: nil, Class: ClassInfinity}, true
	}
	b := Bits{Sign: sign, E: E, F: F, Class: classOf(E, F)}
	if b.Class == ClassZero {
		b.Value = new(big.Rat)
	} else {
		b.Value = finiteMagnitude(E, F)
		if sign == 1 {
			b.Value.Neg(b.Value)
		}
	}
	return b, true
}

// CalibratedConvert rounds the exact sum original+bias to binary16
// under roundTiesToEven, applying the calibration zero convention:
//
//   - an exact zero sum is +0;
//   - the sole exception is an original literal "-0" with bias exactly
//     zero, which keeps -0.
//
// A zero reached by cancellation of a nonzero original and a nonzero
// bias is therefore always +0.
func CalibratedConvert(negativeLiteral bool, original, bias *big.Rat) Bits {
	x := new(big.Rat).Add(original, bias)
	if x.Sign() == 0 {
		sign := uint16(0)
		if negativeLiteral && bias.Sign() == 0 {
			sign = 1
		}
		return Bits{Sign: sign, E: 0, F: 0, Value: new(big.Rat), Class: ClassZero}
	}
	// x is nonzero, so Convert derives the sign from the rational.
	return Convert(false, x)
}

// Shift translates an interval on sums x = original+bias into the
// interval on biases b = x-original: every finite bound moves by
// -original; infinite tails and endpoint inclusion are preserved.
func Shift(iv Interval, original *big.Rat) Interval {
	out := Interval{
		LowInclusive:  iv.LowInclusive,
		HighInclusive: iv.HighInclusive,
		LowInfinite:   iv.LowInfinite,
		HighInfinite:  iv.HighInfinite,
	}
	shift := new(big.Rat).Neg(original)
	if iv.Low != nil {
		out.Low = new(big.Rat).Add(iv.Low, shift)
	}
	if iv.High != nil {
		out.High = new(big.Rat).Add(iv.High, shift)
	}
	return out
}

// BiasInterval returns the exact set of biases for which
// CalibratedConvert(negativeLiteral, original, b) encodes to target:
// the target's rounding preimage interval shifted by -original, with
// the single exact-zero endpoint recut per the zero convention.
//
// Recutting only endpoint flags (never punching a hole) keeps the
// result an interval and is what stops the two zero certificates from
// overlapping at the intersection point: the +0 interval owns the zero
// sum except for (literal "-0", bias 0), which the -0 interval owns
// exclusively.
func BiasInterval(original *big.Rat, negativeLiteral bool, target Bits) Interval {
	iv := Shift(IntervalOf(target), original)
	if target.Class != ClassZero {
		return iv
	}
	zeroBias := new(big.Rat).Neg(original) // bias making the sum exactly zero
	if target.Sign == 0 {
		// +0 owns every exact-zero sum except literal "-0" + bias 0;
		// that exception coincides with the low endpoint only when the
		// original itself is zero.
		if negativeLiteral && original.Sign() == 0 &&
			iv.Low != nil && iv.Low.Cmp(zeroBias) == 0 {
			iv.LowInclusive = false
		}
		return iv
	}
	// -0 also owns every nonzero negative sum down to the included
	// -2^-25 tie (smaller magnitudes round to zero, tie resolves to the
	// even zero). Only the exact-zero high endpoint is convention-bound:
	// it is -0 solely for literal "-0" with zero bias; a cancellation by
	// a nonzero bias (e.g. 1 + (-1)) is +0.
	if iv.High != nil && iv.High.Cmp(zeroBias) == 0 {
		iv.HighInclusive = negativeLiteral && original.Sign() == 0
	}
	return iv
}

func cloneRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}

// IntersectIntervals returns the exact intersection of two bias
// intervals, carrying open/closed endpoints and infinite tails through.
// The second result is false when the intersection is empty: bounds in
// strict order, or equal bounds that one side excludes.
func IntersectIntervals(a, b Interval) (Interval, bool) {
	var c Interval

	// Lower bound is the larger of the two; inclusion is the AND of the
	// two flags only when the bounds coincide.
	switch {
	case a.LowInfinite && b.LowInfinite:
		c.LowInfinite = true
	case a.LowInfinite:
		c.Low, c.LowInclusive = cloneRat(b.Low), b.LowInclusive
	case b.LowInfinite:
		c.Low, c.LowInclusive = cloneRat(a.Low), a.LowInclusive
	default:
		switch a.Low.Cmp(b.Low) {
		case 1:
			c.Low, c.LowInclusive = cloneRat(a.Low), a.LowInclusive
		case -1:
			c.Low, c.LowInclusive = cloneRat(b.Low), b.LowInclusive
		default:
			c.Low, c.LowInclusive = cloneRat(a.Low), a.LowInclusive && b.LowInclusive
		}
	}

	// Upper bound is the smaller of the two, mirroring the lower rule.
	switch {
	case a.HighInfinite && b.HighInfinite:
		c.HighInfinite = true
	case a.HighInfinite:
		c.High, c.HighInclusive = cloneRat(b.High), b.HighInclusive
	case b.HighInfinite:
		c.High, c.HighInclusive = cloneRat(a.High), a.HighInclusive
	default:
		switch a.High.Cmp(b.High) {
		case -1:
			c.High, c.HighInclusive = cloneRat(a.High), a.HighInclusive
		case 1:
			c.High, c.HighInclusive = cloneRat(b.High), b.HighInclusive
		default:
			c.High, c.HighInclusive = cloneRat(a.High), a.HighInclusive && b.HighInclusive
		}
	}

	if !c.LowInfinite && !c.HighInfinite {
		switch c.Low.Cmp(c.High) {
		case 1:
			return Interval{}, false
		case 0:
			if !c.LowInclusive || !c.HighInclusive {
				return Interval{}, false
			}
		}
	}
	return c, true
}

// Calibration is the exact common-bias verdict for one batch.
type Calibration struct {
	// Feasible reports whether the shifted preimage intervals intersect.
	Feasible bool
	// Interval is the exact feasible bias interval when Feasible.
	Interval Interval
	// Attained reports whether the minimum-absolute bias is actually a
	// member of Interval; when false it is only an infimum.
	Attained bool
	// Bias is the minimum-absolute bias when Attained.
	Bias *big.Rat
	// Infimum is the excluded endpoint when !Attained (its absolute
	// value is the greatest lower bound of |b|).
	Infimum *big.Rat
	// WitnessBias is a strictly interior feasible bias given only when
	// !Attained, proving the interval itself is nonempty.
	WitnessBias *big.Rat
}

// BiasMember reports whether bias b lies in a bias interval, honoring
// open endpoints and infinite tails. It is pure geometry: BiasInterval
// has already applied the signed-zero recut to the endpoint flags.
func BiasMember(iv Interval, b *big.Rat) bool {
	if !iv.LowInfinite {
		switch c := b.Cmp(iv.Low); {
		case c < 0:
			return false
		case c == 0 && !iv.LowInclusive:
			return false
		}
	}
	if !iv.HighInfinite {
		switch c := b.Cmp(iv.High); {
		case c > 0:
			return false
		case c == 0 && !iv.HighInclusive:
			return false
		}
	}
	return true
}

func containsZero(iv Interval) bool {
	return BiasMember(iv, new(big.Rat))
}

// interiorPoint returns a rational strictly inside the interval on the
// side of the excluded endpoint closest to zero.
func interiorPoint(iv Interval) *big.Rat {
	two := big.NewRat(2, 1)
	one := big.NewRat(1, 1)
	if iv.High != nil && iv.High.Sign() < 0 {
		// Negative side: the excluded endpoint is the high bound.
		if iv.LowInfinite {
			return new(big.Rat).Sub(iv.High, one)
		}
		m := new(big.Rat).Add(iv.Low, iv.High)
		return m.Quo(m, two)
	}
	// Positive side: the excluded endpoint is the low bound.
	if iv.HighInfinite {
		return new(big.Rat).Add(iv.Low, one)
	}
	m := new(big.Rat).Add(iv.Low, iv.High)
	return m.Quo(m, two)
}

// Calibrate intersects the per-item bias intervals and selects the
// minimum-absolute feasible bias. originals, negativeLiteral and
// targets must have equal length.
func Calibrate(originals []*big.Rat, negativeLiteral []bool, targets []Bits) Calibration {
	n := len(originals)
	if n == 0 || len(negativeLiteral) != n || len(targets) != n {
		return Calibration{Feasible: false}
	}
	iv := BiasInterval(originals[0], negativeLiteral[0], targets[0])
	for i := 1; i < n; i++ {
		next := BiasInterval(originals[i], negativeLiteral[i], targets[i])
		var ok bool
		iv, ok = IntersectIntervals(iv, next)
		if !ok {
			return Calibration{Feasible: false}
		}
	}

	cal := Calibration{Feasible: true, Interval: iv}
	if containsZero(iv) {
		cal.Attained = true
		cal.Bias = new(big.Rat)
		return cal
	}

	if iv.High != nil && iv.High.Sign() < 0 {
		// Entirely negative: the smallest |b| sits at the high bound.
		cal.Infimum = cloneRat(iv.High)
		cal.Attained = iv.HighInclusive
	} else {
		// Entirely positive: the smallest |b| sits at the low bound;
		// the low bound is finite because zero is not contained.
		cal.Infimum = cloneRat(iv.Low)
		cal.Attained = iv.LowInclusive
	}
	if cal.Attained {
		cal.Bias = cloneRat(cal.Infimum)
	} else {
		cal.WitnessBias = interiorPoint(iv)
	}
	return cal
}

// MismatchAt verifies the bias against every constraint and returns the
// index of the first item whose recomputed pattern differs from its
// target, or -1 when all match.
func MismatchAt(originals []*big.Rat, negativeLiteral []bool, targets []Bits, bias *big.Rat) int {
	for i := range originals {
		got := CalibratedConvert(negativeLiteral[i], originals[i], bias)
		if got.Pattern() != targets[i].Pattern() {
			return i
		}
	}
	return -1
}
