package half_test

// Independent small-rational-grid tests for calibration's shifted
// preimage intervals, interval intersection, open-endpoint infimum
// handling, signed zeros, the subnormal/normal boundary and overflow
// tails.

import (
	"math/big"
	"testing"

	"halfconv/internal/half"
)

func biasInterval(t *testing.T, pattern uint16, originalNeg bool, original string) half.Interval {
	t.Helper()
	target, ok := half.DecodePattern(pattern)
	if !ok {
		t.Fatalf("target %04X is not finite or infinity", pattern)
	}
	neg, x := mustParse(t, original)
	if neg != originalNeg {
		t.Fatalf("Parse(%q) sign = %v, want %v", original, neg, originalNeg)
	}
	return half.AdditiveBiasInterval(target, neg, x)
}

func independentAddBiasMember(o *oracle, target half.Bits, originalNeg bool, original, bias *big.Rat) bool {
	y := new(big.Rat).Add(original, bias)
	if y.Sign() == 0 {
		negativeZero := originalNeg && original.Sign() == 0 && bias.Sign() == 0
		if target.Class != half.ClassZero {
			return false
		}
		return (target.Sign == 1) == negativeZero
	}
	pat, inf := o.round(y)
	if target.Class == half.ClassInfinity {
		return inf && pat == target.Pattern()
	}
	return !inf && pat == target.Pattern()
}

// TestAdditiveBiasIntervalsSmallGrid checks the shifted certificates on
// values y on a local binary grid and offsets b on another small binary
// grid, so both exact ties and points on either side are covered.
func TestAdditiveBiasIntervalsSmallGrid(t *testing.T) {
	o := newOracle(t)
	patterns := []uint16{
		0x0000, 0x8000,
		0x0001, 0x0002, 0x0003,
		0x03FE, 0x03FF, 0x0400, 0x0401,
		0x3C00, 0x3C01, 0x3C02, 0x3C03,
		0x4000, 0x4200, 0x7BFE, 0x7BFF,
		0x7C00, 0xFC00,
	}

	for _, pattern := range patterns {
		target, ok := half.DecodePattern(pattern)
		if !ok {
			t.Fatalf("bad target %04X", pattern)
		}
		t.Run(sprintfPattern(pattern), func(t *testing.T) {
			ys := additiveGridValues(t, target)
			bs := additiveGridBiases(t, target)
			for _, y := range ys {
				for _, b := range bs {
					x := new(big.Rat).Sub(y, b)
					// Test both signs of the original by using +/-x and
					// the corresponding sign-mirrored y/b where useful.
					for sign := 0; sign < 2; sign++ {
						xx := new(big.Rat).Set(x)
						yy := new(big.Rat).Set(y)
						bb := new(big.Rat).Set(b)
						neg := sign == 1
						if neg {
							xx.Neg(xx)
							yy.Neg(yy)
							bb.Neg(bb)
						}
						if xx.Sign() == 0 {
							// Exercise both signed zero literals.
							for _, zeroNeg := range []bool{false, true} {
								iv := half.AdditiveBiasInterval(target, zeroNeg, xx)
								got := half.IntervalContainsBias(iv, bb)
								want := independentAddBiasMember(o, target, zeroNeg, xx, bb)
								if got != want {
									t.Fatalf("target %04X zeroNeg=%v y=%s b=%s: member=%v want %v",
										pattern, zeroNeg, yy, bb, got, want)
								}
								rec := half.AddBias(zeroNeg, xx, bb)
								if got != independentAddBiasMember(o, target, zeroNeg, xx, bb) {
									t.Fatalf("internal membership mismatch")
								}
								if got && rec.Pattern() != pattern {
									t.Fatalf("accepted bias reconstructs %04X, want %04X", rec.Pattern(), pattern)
								}
							}
							continue
						}
						iv := half.AdditiveBiasInterval(target, neg, xx)
						got := half.IntervalContainsBias(iv, bb)
						want := independentAddBiasMember(o, target, neg, xx, bb)
						if got != want {
							t.Fatalf("target %04X x=%s b=%s y=%s: member=%v want %v",
								pattern, xx, bb, yy, got, want)
						}
						if got {
							rec := half.AddBias(neg, xx, bb)
							if rec.Pattern() != pattern {
								t.Fatalf("accepted bias reconstructs %04X, want %04X", rec.Pattern(), pattern)
							}
						}
					}
				}
			}
		})
	}
}

func additiveGridValues(t *testing.T, target half.Bits) []*big.Rat {
	t.Helper()
	switch target.Class {
	case half.ClassZero:
		return []*big.Rat{
			new(big.Rat),
			ratPow2(-1, 26), ratPow2(1, 26),
			ratPow2(-1, 25), ratPow2(1, 25),
			ratPow2(3, 26),
			new(big.Rat).Neg(ratPow2(1, 26)),
			new(big.Rat).Neg(ratPow2(1, 25)),
		}
	case half.ClassInfinity:
		if target.Sign == 0 {
			return []*big.Rat{big.NewRat(65519, 1), big.NewRat(65520, 1), big.NewRat(65521, 1), big.NewRat(70000, 1)}
		}
		return []*big.Rat{big.NewRat(-65519, 1), big.NewRat(-65520, 1), big.NewRat(-65521, 1), big.NewRat(-70000, 1)}
	case half.ClassSubnormal:
		mag := new(big.Rat).Set(target.Value)
		mag.Abs(mag)
		step := ratPow2(1, 26) // quarter subnormal ULP
		return signedMagnitudeGrid(mag, step, 4)
	default:
		mag := new(big.Rat).Set(target.Value)
		mag.Abs(mag)
		step := localUpperQuarterULP(target)
		// Keep close enough to remain in the tested local interval.
		return signedMagnitudeGrid(mag, step, 3)
	}
}

func localUpperQuarterULP(target half.Bits) *big.Rat {
	if target.Pattern() == 0x7BFF {
		return big.NewRat(4, 1) // quarter of the gap 65504 -> tie 65520
	}
	if target.F <= 1022 {
		next, ok := half.DecodePattern(target.E<<10 | (target.F + 1))
		if !ok {
			panic("invalid neighbor")
		}
		gap := new(big.Rat).Sub(next.Value, target.Value)
		return new(big.Rat).Quo(gap, big.NewRat(4, 1))
	}
	next, ok := half.DecodePattern((uint16(target.E)+1)<<10 | 0)
	if !ok {
		panic("invalid next binade")
	}
	gap := new(big.Rat).Sub(next.Value, target.Value)
	return new(big.Rat).Quo(gap, big.NewRat(4, 1))
}

func signedMagnitudeGrid(mag, step *big.Rat, steps int64) []*big.Rat {
	out := make([]*big.Rat, 0, 2*(2*int(steps)+1))
	for sign := 0; sign < 2; sign++ {
		for k := -steps; k <= steps; k++ {
			v := new(big.Rat).Add(mag, new(big.Rat).Mul(big.NewRat(k, 1), step))
			if sign == 1 {
				v.Neg(v)
			}
			out = append(out, v)
		}
	}
	return out
}

func additiveGridBiases(t *testing.T, target half.Bits) []*big.Rat {
	t.Helper()
	switch target.Class {
	case half.ClassZero:
		return []*big.Rat{
			new(big.Rat),
			ratPow2(-1, 26), ratPow2(1, 26),
			ratPow2(-1, 25), ratPow2(1, 25),
			ratPow2(3, 26),
			big.NewRat(1, 1),
			new(big.Rat).Neg(ratPow2(1, 26)),
			new(big.Rat).Neg(ratPow2(1, 25)),
		}
	case half.ClassInfinity:
		return []*big.Rat{
			new(big.Rat), big.NewRat(-1, 1), big.NewRat(1, 1),
			big.NewRat(-2, 2), big.NewRat(1, 2), big.NewRat(-1, 2),
			big.NewRat(100, 1), big.NewRat(-100, 1),
		}
	case half.ClassSubnormal:
		step := ratPow2(1, 26)
		return integerSteps(new(big.Rat), step, 8)
	default:
		step := localUpperQuarterULP(target)
		return integerSteps(new(big.Rat), step, 6)
	}
}

func integerSteps(center, step *big.Rat, n int64) []*big.Rat {
	out := make([]*big.Rat, 0, 2*int(n)+1)
	for k := -n; k <= n; k++ {
		out = append(out, new(big.Rat).Add(center, new(big.Rat).Mul(big.NewRat(k, 1), step)))
	}
	return out
}

func TestAdditiveIntervalIntersections(t *testing.T) {
	// Two adjacent normal target constraints: closed intervals touch at
	// the even target's tie. Here both targets 3C00 (F=0) and 3C01 (F=1)
	// share the tie 1+2^-11, but only 3C00 includes it.
	a := biasInterval(t, 0x3C00, false, "0")
	b := biasInterval(t, 0x3C01, false, "0")
	if got, ok := half.Intersect(a, b); ok {
		t.Fatalf("3C00 and 3C01 certificates must not overlap at odd tie: %+v", got)
	}

	// Largest subnormal (odd F) excludes 2047/2^25; smallest normal
	// (even F=0) includes it. Intersection of their ordinary intervals
	// must be empty.
	a = biasInterval(t, 0x03FF, false, "0")
	b = biasInterval(t, 0x0400, false, "0")
	if _, ok := half.Intersect(a, b); ok {
		t.Fatalf("largest subnormal/smallest normal odd tie must not overlap")
	}

	// Same target from originals 0001 (2^-24) and zero: the shifted
	// intervals meet at bias 3/2^25, the even-F=2 tie with 0003, which
	// 0002 owns.
	a = biasInterval(t, 0x0002, false, "0.000000059604644775390625") // 2^-24
	b = biasInterval(t, 0x0002, false, "0")
	got, ok := half.Intersect(a, b)
	if !ok {
		t.Fatalf("identical targets at x=2^-24 and x=0 should intersect")
	}
	shared := ratPow2(3, 25)
	if !half.IntervalContainsBias(got, shared) {
		t.Fatalf("bias 3/2^25 should fit both 0002 constraints, got %+v", got)
	}

	// Infinity tails meet the bounded max-finite interval at 65520, but
	// that tie belongs to infinity and not to the odd max-finite pattern.
	posInf := biasInterval(t, 0x7C00, false, "0")
	maxFinite := biasInterval(t, 0x7BFF, false, "0")
	if _, ok := half.Intersect(posInf, maxFinite); ok {
		t.Fatalf("+infinity and max finite preimages must not overlap")
	}
	negInf := biasInterval(t, 0xFC00, false, "0")
	if _, ok := half.Intersect(posInf, negInf); ok {
		t.Fatalf("positive and negative infinity tails must not overlap")
	}
}

func TestMinimumBiasOpenEndpoints(t *testing.T) {
	// The odd smallest subnormal excludes its lower tie: 2^-25 is the
	// unattained infimum.
	iv := biasInterval(t, 0x0001, false, "0")
	r, attainable := half.MinimumBias(iv)
	if attainable {
		t.Fatalf("0001 lower endpoint is excluded; minimum 2^-25 is only infimum, got %v", r)
	}
	if r.Cmp(ratPow2(1, 25)) != 0 {
		t.Fatalf("infimum = %v, want 2^-25", r)
	}

	// Even smallest normal owns the tie 2047/2^25, so that minimum is
	// attainable after shifting original up to just below it.
	iv = biasInterval(t, 0x0400, false, "0")
	r, attainable = half.MinimumBias(iv)
	if !attainable || r.Cmp(ratPow2(2047, 25)) != 0 {
		t.Fatalf("0400 tie minimum = %v attainable=%v, want 2047/2^25 true", r, attainable)
	}

	// Negative-side open endpoint returns signed negative infimum.
	iv = biasInterval(t, 0x8001, true, "-0") // negative smallest subnormal
	r, attainable = half.MinimumBias(iv)
	if attainable {
		t.Fatalf("negative 8001 near-zero endpoint must be excluded")
	}
	if r.Cmp(new(big.Rat).Neg(ratPow2(1, 25))) != 0 {
		t.Fatalf("negative infimum = %v, want -2^-25", r)
	}

	// Interval (0, ...) has unattained infimum zero.
	iv = half.Interval{Low: new(big.Rat), LowInclusive: false, HighInfinite: true}
	r, attainable = half.MinimumBias(iv)
	if attainable || r.Sign() != 0 {
		t.Fatalf("(0,+inf): got %v/%v, want unattained zero", r, attainable)
	}
}

func TestAdditiveSignedZeroCertificatesDoNotOverlap(t *testing.T) {
	zero := new(big.Rat)
	posTarget, _ := half.DecodePattern(0x0000)
	negTarget, _ := half.DecodePattern(0x8000)

	for _, c := range []struct {
		name string
		neg  bool
		x    *big.Rat
	}{
		{"+0", false, zero},
		{"-0", true, zero},
		{"positive tiny", false, ratPow2(1, 25)},
		{"negative tiny", true, new(big.Rat).Neg(ratPow2(1, 25))},
	} {
		t.Run(c.name, func(t *testing.T) {
			pos := half.AdditiveBiasInterval(posTarget, c.neg, c.x)
			neg := half.AdditiveBiasInterval(negTarget, c.neg, c.x)
			if _, ok := half.Intersect(pos, neg); ok {
				t.Fatalf("+0 and -0 bias intervals overlap")
			}
			if half.IntervalContainsBias(pos, zero) {
				if got := half.AddBias(c.neg, c.x, zero); got.Pattern() != 0x0000 {
					t.Fatalf("+0 certificate accepted bias but reconstruction = %04X", got.Pattern())
				}
			}
			if half.IntervalContainsBias(neg, zero) {
				if got := half.AddBias(c.neg, c.x, zero); got.Pattern() != 0x8000 {
					t.Fatalf("-0 certificate accepted bias but reconstruction = %04X", got.Pattern())
				}
			}
		})
	}

	// Explicitly: literal -0 + zero keeps -0; +0 + zero and every
	// nonzero cancellation produce +0.
	negZeroIV := half.AdditiveBiasInterval(negTarget, true, zero)
	if !half.IntervalContainsBias(negZeroIV, zero) {
		t.Fatalf("-0 target must accept literal -0 + 0")
	}
	posZeroIV := half.AdditiveBiasInterval(posTarget, true, zero)
	if half.IntervalContainsBias(posZeroIV, zero) {
		t.Fatalf("+0 target must reject literal -0 + 0")
	}
	x := big.NewRat(1, 2)
	negZeroIV = half.AdditiveBiasInterval(negTarget, false, x)
	cancel := big.NewRat(-1, 2)
	if half.IntervalContainsBias(negZeroIV, cancel) {
		t.Fatalf("nonzero exact cancellation must not produce -0")
	}
	posZeroIV = half.AdditiveBiasInterval(posTarget, false, x)
	if !half.IntervalContainsBias(posZeroIV, cancel) {
		t.Fatalf("nonzero exact cancellation must produce +0")
	}
}

func TestAdditiveOverflowTails(t *testing.T) {
	posInf, _ := half.DecodePattern(0x7C00)
	maxFinite, _ := half.DecodePattern(0x7BFF)
	x := big.NewRat(60000, 1)
	iv := half.AdditiveBiasInterval(posInf, false, x)
	if iv.LowInfinite || iv.Low.Cmp(big.NewRat(5520, 1)) != 0 || !iv.LowInclusive || !iv.HighInfinite {
		t.Fatalf("shifted +inf tail = %+v, want [5520,null)", iv)
	}
	fin := half.AdditiveBiasInterval(maxFinite, false, x)
	if _, ok := half.Intersect(iv, fin); ok {
		t.Fatalf("shifted infinity and max-finite constraints overlap at tie")
	}

	negInf, _ := half.DecodePattern(0xFC00)
	nx := big.NewRat(-60000, 1)
	niv := half.AdditiveBiasInterval(negInf, true, nx)
	if !niv.LowInfinite || niv.High.Cmp(big.NewRat(-5520, 1)) != 0 || !niv.HighInclusive {
		t.Fatalf("shifted -inf tail = %+v, want (null,-5520]", niv)
	}
}
