// Package half converts exact rational numbers to IEEE 754 binary16
// (half precision) bit patterns using only integer/rational arithmetic.
//
// binary16 layout: 1 sign bit, 5 exponent bits, 10 fraction bits,
// bias 15. Rounding uses roundTiesToEven ("nearest, ties to even").
package half

import (
	"math/big"
)

// Class is the IEEE category of a binary16 result.
type Class string

const (
	ClassZero      Class = "zero"
	ClassSubnormal Class = "subnormal"
	ClassNormal    Class = "normal"
	ClassInfinity  Class = "infinity"
)

// Bits is a complete binary16 conversion result.
type Bits struct {
	// Sign is the raw sign bit (0 positive, 1 negative).
	Sign uint16
	// E is the stored 5-bit exponent field (0 for zero/subnormal,
	// 31 for infinity).
	E uint16
	// F is the stored 10-bit fraction field.
	F uint16
	// Value is the exact rational represented by the bit pattern
	// (nil for infinity).
	Value *big.Rat
	Class Class
}

// Pattern returns the 16-bit encoding, formatted as four hex digits
// by callers.
func (b Bits) Pattern() uint16 {
	return (b.Sign << 15) | (b.E << 10) | b.F
}

// IsInf reports whether the result is an infinity.
func (b Bits) IsInf() bool { return b.Class == ClassInfinity }

// powersOfTwo caches 2^e for the small range visited during binade
// searches (-25..16).
var powersOfTwo = map[int]*big.Rat{}

// p2 returns the exact rational 2^e. The range needed is -25..16.
func p2(e int) *big.Rat {
	if v, ok := powersOfTwo[e]; ok {
		// Return a copy: callers never mutate cached rationals in
		// practice, but keep the map values shared-read only.
		return new(big.Rat).Set(v)
	}
	r := new(big.Rat)
	if e >= 0 {
		r.SetInt(new(big.Int).Lsh(big.NewInt(1), uint(e)))
	} else {
		r.SetFrac(big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), uint(-e)))
	}
	powersOfTwo[e] = r
	return new(big.Rat).Set(r)
}

// roundScale rounds r * 2^s (r >= 0) to the nearest integer, with ties
// breaking toward the even integer. The result fits in int64 for every
// caller, but big.Int is used throughout to keep the arithmetic honest.
func roundScale(r *big.Rat, s int) *big.Int {
	z := new(big.Rat).Set(r)
	if s >= 0 {
		z.Mul(z, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(s))))
	} else {
		z.Quo(z, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(-s))))
	}

	num := z.Num()
	den := z.Denom()
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	twiceRem := new(big.Int).Lsh(rem, 1)
	cmp := twiceRem.Cmp(den)
	if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	return q
}

func cmpRat(x *big.Rat, y *big.Rat) int { return x.Cmp(y) }

// Convert rounds the exact rational r to binary16 with roundTiesToEven.
// The neg argument carries the sign (r may be a negative rational, but
// Parse also reports the sign of an input zero).
func Convert(neg bool, r *big.Rat) Bits {
	zero := func(sign uint16) Bits {
		return Bits{Sign: sign, E: 0, F: 0, Value: new(big.Rat), Class: ClassZero}
	}
	if r.Sign() == 0 {
		var sign uint16
		if neg {
			sign = 1
		}
		return zero(sign)
	}

	neg = neg || r.Sign() < 0
	x := new(big.Rat).Abs(r)

	var sign uint16
	if neg {
		sign = 1
	}

	mkInf := func() Bits { return Bits{Sign: sign, E: 31, F: 0, Value: nil, Class: ClassInfinity} }

	// Largest finite is 0x7BFF = 65504; next binade midpoint is 65520.
	maxFinite := new(big.Rat).SetInt64(65504)
	infMidpoint := new(big.Rat).SetInt64(65520)

	var E, F uint16

	switch {
	case cmpRat(x, infMidpoint) >= 0:
		// x == 65520 is the exact tie between 65504 and infinity;
		// the infinity candidate has stored field 0 (even), so the
		// tie resolves to infinity.
		return mkInf()

	case cmpRat(x, maxFinite) >= 0:
		// [65504, 65520): strictly closer to 65504.
		E, F = 30, 1023

	case cmpRat(x, p2(-14)) < 0:
		// Below the smallest normal 2^-14: subnormal spacing is
		// 2^-24. k rounds to 0..1023, except that the tie between
		// k=1023 and k=1024 rounds even to 1024, which is exactly
		// the smallest normal.
		k := roundScale(x, 24)
		switch kv := k.Int64(); {
		case kv == 0:
			return zero(sign)
		case kv == 1024:
			E, F = 1, 0
		default:
			E, F = 0, uint16(kv)
		}

	default:
		// Normal region: 2^-14 <= x < 65520. Find the binade
		// [2^e, 2^(e+1)) containing x; an exact power of two is the
		// left endpoint and ties resolve to it via the even m.
		e := -14
		for cmpRat(x, p2(e+1)) >= 0 && e < 15 {
			e++
		}
		// m = x / 2^e * 1024, rounded ties to even.
		m := roundScale(x, 10-e)
		mi := uint16(m.Int64())
		if mi == 2048 {
			E, F = uint16(e+1+15), 0
		} else {
			E, F = uint16(e+15), mi-1024
		}
	}

	val := finiteMagnitude(E, F)
	if neg {
		val.Neg(val)
	}
	return Bits{Sign: sign, E: E, F: F, Value: val, Class: classOf(E, F)}
}

// finiteMagnitude returns the exact nonnegative rational encoded by the
// given stored fields.
func finiteMagnitude(E, F uint16) *big.Rat {
	if E == 0 {
		if F == 0 {
			return new(big.Rat)
		}
		// F * 2^-24
		return new(big.Rat).SetFrac(
			new(big.Int).SetInt64(int64(F)),
			new(big.Int).Lsh(big.NewInt(1), 24),
		)
	}
	// significand = 1024 + F, value = significand * 2^(E-25)
	sig := big.NewInt(int64(1024 + F))
	v := new(big.Rat).SetInt(sig)
	shift := int(E) - 25
	if shift >= 0 {
		v.Mul(v, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(shift))))
	} else {
		v.Quo(v, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(-shift))))
	}
	return v
}

func classOf(E, F uint16) Class {
	switch {
	case E == 0 && F == 0:
		return ClassZero
	case E == 0:
		return ClassSubnormal
	default:
		return ClassNormal
	}
}

// Error returns the exact rounding error value(result) - original,
// reduced to lowest terms. It is nil for infinite results.
func Error(b Bits, original *big.Rat) *big.Rat {
	if b.IsInf() {
		return nil
	}
	return new(big.Rat).Sub(b.Value, original)
}

// Interval is the exact certificate of the set of signed real values
// that round (roundTiesToEven) to the bit pattern carried by the Bits
// passed to IntervalOf. Every bound is a reduced *big.Rat; no host
// floating point is used anywhere.
//
// A nil Low with LowInfinite set means the -infinity tail; a nil High
// with HighInfinite set means the +infinity tail. The inclusivity flags
// record how the exact tie midpoints are resolved (ties to even).
//
// The two signed zeros get separate, sign-specific intervals that meet
// at the rational 0: [0, 2^-25] for +0 and [-2^-25, 0] for -0. Because
// the rational 0 itself does not remember a sign, membership at 0 is
// adjudicated from the input sign via Contains, exactly as Parse does.
type Interval struct {
	Low           *big.Rat
	High          *big.Rat
	LowInclusive  bool
	HighInclusive bool
	LowInfinite   bool
	HighInfinite  bool
}

// midpoint returns the exact reduced arithmetic mean (a+b)/2.
func midpoint(a, b *big.Rat) *big.Rat {
	m := new(big.Rat).Add(a, b)
	return m.Quo(m, big.NewRat(2, 1))
}

// IntervalOf returns the exact rounding preimage interval of b: every
// legal decimal whose parsed value (with its sign) lies in the interval
// encodes to b.Pattern().
func IntervalOf(b Bits) Interval {
	// Overflow tails: the tie 65520 between 65504 and infinity resolves
	// to the even infinity candidate, so the tail endpoint is included.
	if b.Class == ClassInfinity {
		threshold := new(big.Rat).SetInt64(65520)
		if b.Sign == 0 {
			return Interval{Low: threshold, LowInclusive: true, HighInfinite: true}
		}
		return Interval{LowInfinite: true, High: new(big.Rat).Neg(threshold), HighInclusive: true}
	}

	// Signed zero preimages. The tie magnitude 2^-25 between zero
	// (significand 0, even) and the smallest subnormal (significand 1,
	// odd) rounds to zero, so the outer endpoint is included.
	if b.Class == ClassZero {
		tie := p2(-25)
		zero := new(big.Rat)
		if b.Sign == 0 {
			return Interval{Low: zero, LowInclusive: true, High: tie, HighInclusive: true}
		}
		return Interval{Low: new(big.Rat).Neg(tie), LowInclusive: true, High: zero, HighInclusive: true}
	}

	// Finite nonzero result. Locate the adjacent representable
	// magnitudes purely from the stored fields: neighbors differ by one
	// fraction step, wrapping F=1023 to the next binade's F=0 (and
	// vice versa), with E=0 denoting the subnormal grid.
	v := finiteMagnitude(b.E, b.F)
	var pred *big.Rat
	if b.F >= 1 {
		pred = finiteMagnitude(b.E, b.F-1)
	} else {
		// Power of two: predecessor is the previous binade's top value
		// (E-1 == 0 is the largest subnormal, neighbor of 2^-14).
		pred = finiteMagnitude(b.E-1, 1023)
	}
	low := midpoint(pred, v)

	var high *big.Rat
	switch {
	case b.E == 30 && b.F == 1023:
		// 65504: half an ULP beyond is the overflow tie 65520.
		high = new(big.Rat).SetInt64(65520)
	case b.F <= 1022:
		high = midpoint(v, finiteMagnitude(b.E, b.F+1))
	default: // F == 1023: next binade power of two
		high = midpoint(v, finiteMagnitude(b.E+1, 0))
	}

	// At either midpoint the two candidates are consecutive integer
	// significands in the common fine grid; the even one wins. The
	// result's own candidate is even exactly when its stored fraction
	// field F is even (m = 1024+F has the same parity; subnormal F is
	// the significand directly).
	inclusive := b.F&1 == 0
	if b.Sign == 0 {
		return Interval{Low: low, High: high, LowInclusive: inclusive, HighInclusive: inclusive}
	}
	// Mirror across zero: [-high, -low], swapping endpoint roles.
	return Interval{
		Low:           new(big.Rat).Neg(high),
		High:          new(big.Rat).Neg(low),
		LowInclusive:  inclusive,
		HighInclusive: inclusive,
	}
}

// Contains is the independent adjudicator: it reports whether a parsed
// decimal (the sign flag plus its exact rational value) encodes to the
// bit pattern whose certificate is iv. A zero value matches only the
// certificate of the input-sign-matching signed zero.
func (iv Interval) Contains(negativeInput bool, x *big.Rat) bool {
	if x.Sign() == 0 {
		if negativeInput {
			return iv.High != nil && iv.High.Sign() == 0
		}
		return iv.Low != nil && iv.Low.Sign() == 0
	}
	if !iv.LowInfinite {
		switch c := x.Cmp(iv.Low); {
		case c < 0:
			return false
		case c == 0 && !iv.LowInclusive:
			return false
		}
	}
	if !iv.HighInfinite {
		switch c := x.Cmp(iv.High); {
		case c > 0:
			return false
		case c == 0 && !iv.HighInclusive:
			return false
		}
	}
	return true
}
