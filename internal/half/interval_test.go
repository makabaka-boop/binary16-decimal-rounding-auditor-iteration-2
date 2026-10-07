package half_test

// Tests for the exact rounding-preimage interval certificates. The
// independent oracle here is the exact midpoint between *adjacent*
// binary16 patterns: with roundTiesToEven that midpoint belongs to the
// neighbour whose stored fraction field is even. Every bound and probe
// is a *big.Rat (or a legal decimal parsed back through decimal.Parse),
// never host floating point.

import (
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"halfconv/internal/decimal"
	"halfconv/internal/half"
)

// independentMidpoint is (a+b)/2 computed without any production helper.
func independentMidpoint(a, b *big.Rat) *big.Rat {
	s := new(big.Rat).Add(a, b)
	return s.Quo(s, big.NewRat(2, 1))
}

// ratStep returns gap/8 of an adjacent-pair gap, used as a probe
// displacement that stays strictly on one side of the tie midpoint.
func eighthStep(gap *big.Rat) *big.Rat {
	return new(big.Rat).Quo(gap, big.NewRat(8, 1))
}

// assertReduced fails if r's fraction is not in lowest terms.
func assertReduced(t *testing.T, tag string, r *big.Rat) {
	t.Helper()
	g := new(big.Int).GCD(nil, nil, new(big.Int).Set(r.Num()), new(big.Int).Set(r.Denom()))
	if g.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("%s = %s is not reduced (gcd %s)", tag, r, g)
	}
}

// TestIntervalStructureAgainstAdjacentPatterns enumerates every
// positive finite magnitude in sorted order, appends the +infinity
// overflow boundary, and checks each certificate's endpoints against
// the independently computed exact midpoint with its neighbours, for
// both signs.
func TestIntervalStructureAgainstAdjacentPatterns(t *testing.T) {
	o := newOracle(t)
	n := len(o.vals) // includes 0 once; values strictly ascending
	if o.vals[0].Sign() != 0 || o.pats[0] != 0 {
		t.Fatalf("oracle must start at +0, got %v (%04X)", o.vals[0], o.pats[0])
	}
	infTie := big.NewRat(65520, 1)

	for sign := uint16(0); sign <= 1; sign++ {
		neg := sign == 1
		for i := 0; i < n; i++ {
			v := o.vals[i]
			pat := o.pats[i]
			F := pat & 0x3ff
			in := new(big.Rat).Set(v)
			if neg {
				in.Neg(in)
			}
			b := half.Convert(neg, in)
			if b.Pattern() != pat|(sign<<15) {
				t.Fatalf("oracle value %s converts to %04X, want %04X", in, b.Pattern(), pat)
			}
			iv := half.IntervalOf(b)

			// Independently expected magnitudes of the endpoints.
			var wantLo, wantHi *big.Rat
			wantLoInf, wantHiInf := false, false
			if i == 0 {
				wantLo = new(big.Rat) // signed zero meets 0
			} else {
				wantLo = independentMidpoint(o.vals[i-1], v)
			}
			if i == n-1 {
				wantHi = infTie // 65504/inf midpoint is 65520
			} else {
				wantHi = independentMidpoint(v, o.vals[i+1])
			}

			// Parity rule: the pattern itself wins the tie iff its
			// stored fraction field F is even. Zero (F=0) therefore
			// owns the 2^-25 tie; F=1023 never owns its upper tie.
			wantInclusive := F&1 == 0

			if neg {
				wantLo, wantHi = new(big.Rat).Neg(wantHi), new(big.Rat).Neg(wantLo)
			}
			if iv.Low == nil || iv.Low.Cmp(wantLo) != 0 {
				t.Fatalf("pattern %04X: low = %v, want %v", b.Pattern(), iv.Low, wantLo)
			}
			if iv.High == nil || iv.High.Cmp(wantHi) != 0 {
				t.Fatalf("pattern %04X: high = %v, want %v", b.Pattern(), iv.High, wantHi)
			}
			if iv.LowInclusive != wantInclusive || iv.HighInclusive != wantInclusive {
				t.Fatalf("pattern %04X (F=%d): inclusivity = %v/%v, want %v/%v",
					b.Pattern(), F, iv.LowInclusive, iv.HighInclusive, wantInclusive, wantInclusive)
			}
			if iv.LowInfinite != wantLoInf || iv.HighInfinite != wantHiInf {
				t.Fatalf("pattern %04X: finite bounds expected, got inf flags %v/%v",
					b.Pattern(), iv.LowInfinite, iv.HighInfinite)
			}
			assertReduced(t, "low", iv.Low)
			assertReduced(t, "high", iv.High)
		}

		// The infinity certificate: open outer tail, tie included.
		inf := half.Convert(neg, new(big.Rat).SetInt64(70000))
		if !inf.IsInf() {
			t.Fatalf("70000 must round to infinity")
		}
		iv := half.IntervalOf(inf)
		if neg {
			if !iv.LowInfinite || iv.High == nil || iv.High.Cmp(new(big.Rat).Neg(infTie)) != 0 || !iv.HighInclusive {
				t.Fatalf("negative infinity interval = %+v", iv)
			}
			if iv.Low != nil {
				t.Fatalf("negative infinity low must be nil")
			}
		} else {
			if !iv.HighInfinite || iv.Low == nil || iv.Low.Cmp(infTie) != 0 || !iv.LowInclusive {
				t.Fatalf("positive infinity interval = %+v", iv)
			}
			if iv.High != nil {
				t.Fatalf("positive infinity high must be nil")
			}
		}
	}
}

// TestEveryAdjacentMidpointAdjudication walks every exact tie
// midpoint (the independent oracle) and asserts that the certificate
// adjudicates the midpoint itself to the even-F neighbour, while
// decimal rendering + parsing + Convert agrees for both signs.
func TestEveryAdjacentMidpointAdjudication(t *testing.T) {
	o := newOracle(t)
	n := len(o.vals)

	for i := 1; i < n; i++ {
		m := independentMidpoint(o.vals[i-1], o.vals[i])
		loEven := o.pats[i-1]&1 == 0
		winnerIdx := i
		loserIdx := i - 1
		if loEven {
			winnerIdx, loserIdx = i-1, i
		}
		for sign := uint16(0); sign <= 1; sign++ {
			neg := sign == 1
			x := new(big.Rat).Set(m)
			if neg {
				x.Neg(x)
			}
			s := renderFinite(t, x)
			parsedNeg, r := mustParse(t, s)
			got := half.Convert(parsedNeg, r)

			wantPat := o.pats[winnerIdx] | (sign << 15)
			if got.Pattern() != wantPat {
				t.Fatalf("tie %s (%q): Convert gave %04X, even neighbour %04X",
					x, s, got.Pattern(), wantPat)
			}
			winIV := half.IntervalOf(got)
			if !winIV.Contains(parsedNeg, r) {
				t.Fatalf("tie %s: winner %04X certificate rejects it", s, wantPat)
			}
			loseVal := new(big.Rat).Set(o.vals[loserIdx])
			if neg {
				loseVal.Neg(loseVal)
			}
			loseIV := half.IntervalOf(half.Convert(neg, loseVal))
			if loseIV.Contains(parsedNeg, r) {
				t.Fatalf("tie %s: loser %04X certificate accepts it", s,
					o.pats[loserIdx]|(sign<<15))
			}
		}
	}

	// Overflow tie 65520: even infinity candidate wins over 65504.
	for _, c := range []struct {
		s         string
		infPat    uint16
		maxFinPat uint16
	}{
		{"65520", 0x7C00, 0x7BFF},
		{"-65520", 0xFC00, 0xFBFF},
	} {
		neg, r := mustParse(t, c.s)
		got := half.Convert(neg, r)
		if got.Pattern() != c.infPat || !got.IsInf() {
			t.Fatalf("%s -> %04X, want infinity %04X", c.s, got.Pattern(), c.infPat)
		}
		if !half.IntervalOf(got).Contains(neg, r) {
			t.Fatalf("%s must belong to its infinity certificate", c.s)
		}
		maxFin := half.Convert(false, big.NewRat(65504, 1))
		if c.infPat&0x8000 != 0 {
			maxFin = half.Convert(true, big.NewRat(-65504, 1))
		}
		if maxFin.Pattern() != c.maxFinPat {
			t.Fatalf("max finite pattern = %04X, want %04X", maxFin.Pattern(), c.maxFinPat)
		}
		if half.IntervalOf(maxFin).Contains(neg, r) {
			t.Fatalf("%s must be outside the max-finite interval", c.s)
		}
	}
}

// TestIntervalBothSides probes a deterministic set of adjacent-pair
// boundaries at +/- gap/8 (strictly inside each neighbour's interval),
// runs the probes through the real decimal parser, and checks
// certificate membership and conversion agree on both signs. All
// structurally special boundaries are included explicitly.
func TestIntervalBothSides(t *testing.T) {
	o := newOracle(t)
	n := len(o.vals)

	// Indices i mark the pair (vals[i-1], vals[i]); include every
	// special boundary and a fixed random sample of ordinary ones.
	special := map[int]bool{
		1:     true, // +0 / smallest subnormal (2^-25 tie)
		2:     true,
		1023:  true, // last pair fully inside subnormal grid
		1024:  true, // largest subnormal / smallest normal
		1025:  true,
		2048:  true, // a binade crossing inside normals
		n - 1: true, // 65488/65504 pair
	}
	rng := rand.New(rand.NewSource(20261004))
	idxs := make([]int, 0, len(special)+300)
	for i := range special {
		idxs = append(idxs, i)
	}
	for len(idxs) < len(special)+300 {
		idxs = append(idxs, 1+rng.Intn(n-1))
	}

	for _, i := range idxs {
		lo, hi := o.vals[i-1], o.vals[i]
		m := independentMidpoint(lo, hi)
		d := eighthStep(new(big.Rat).Sub(hi, lo))
		below := new(big.Rat).Sub(m, d) // strictly in lo's interval
		above := new(big.Rat).Add(m, d) // strictly in hi's interval

		for sign := uint16(0); sign <= 1; sign++ {
			neg := sign == 1
			for _, probe := range []*big.Rat{below, above} {
				x := new(big.Rat).Set(probe)
				if neg {
					x.Neg(x)
				}
				s := renderFinite(t, x)
				pneg, r := mustParse(t, s)
				got := half.Convert(pneg, r)
				iv := half.IntervalOf(got)
				if !iv.Contains(pneg, r) {
					t.Fatalf("probe %s converts to %04X whose interval rejects it: [%v,%v]",
						s, got.Pattern(), iv.Low, iv.High)
				}
				// The neighbour on the other side of the tie rejects it.
				var otherIdx int
				if probe == below {
					otherIdx = i
				} else {
					otherIdx = i - 1
				}
				otherVal := new(big.Rat).Set(o.vals[otherIdx])
				if neg {
					otherVal.Neg(otherVal)
				}
				other := half.Convert(neg, otherVal)
				if other.Pattern() != o.pats[otherIdx]|(sign<<15) {
					t.Fatalf("internal: neighbour pattern %04X want %04X",
						other.Pattern(), o.pats[otherIdx])
				}
				if half.IntervalOf(other).Contains(pneg, r) {
					t.Fatalf("probe %s wrongly accepted across tie by %04X", s,
						o.pats[otherIdx]|(sign<<15))
				}
			}
		}
	}

	// Overflow sides: 65519 + 65521, and the interior of the
	// [65504, 65520) max-finite interval.
	inf := half.Convert(false, big.NewRat(70000, 1))
	maxFin := half.Convert(false, big.NewRat(65504, 1))
	for _, c := range []struct {
		s        string
		neg      bool
		inInf    bool
		inMaxFin bool
	}{
		{"65504", false, false, true},
		{"65512", false, false, true},
		{"65519", false, false, true},
		{"65520", false, true, false},
		{"65521", false, true, false},
		{"100000", false, true, false},
		{"-65504", true, false, true},
		{"-65519", true, false, true},
		{"-65520", true, true, false},
		{"-100000", true, true, false},
	} {
		neg, r := mustParse(t, c.s)
		if neg != c.neg {
			t.Fatalf("%s sign flag = %v", c.s, neg)
		}
		candInf := inf
		candMax := maxFin
		if neg {
			candInf = half.Convert(true, big.NewRat(-70000, 1))
			candMax = half.Convert(true, big.NewRat(-65504, 1))
		}
		if got := half.IntervalOf(candInf).Contains(neg, r); got != c.inInf {
			t.Fatalf("%s in infinity interval = %v, want %v", c.s, got, c.inInf)
		}
		if got := half.IntervalOf(candMax).Contains(neg, r); got != c.inMaxFin {
			t.Fatalf("%s in max-finite interval = %v, want %v", c.s, got, c.inMaxFin)
		}
	}
}

// TestSignedZeroIntervals checks that the two zero patterns get
// sign-split certificates, with the 2^-25 tie included (zero is even)
// and the zero endpoint adjudicated from the input sign.
func TestSignedZeroIntervals(t *testing.T) {
	posZero := half.Convert(false, new(big.Rat))
	negZero := half.Convert(true, new(big.Rat))
	if posZero.Pattern() != 0x0000 || negZero.Pattern() != 0x8000 {
		t.Fatalf("zero patterns %04X/%04X", posZero.Pattern(), negZero.Pattern())
	}

	tie := ratPow2(1, 25) // 2^-25
	pi := half.IntervalOf(posZero)
	ni := half.IntervalOf(negZero)

	if pi.Low.Cmp(new(big.Rat)) != 0 || !pi.LowInclusive ||
		pi.High.Cmp(tie) != 0 || !pi.HighInclusive ||
		pi.LowInfinite || pi.HighInfinite {
		t.Fatalf("+0 interval = %+v, want [0, 2^-25] closed", pi)
	}
	if ni.High.Cmp(new(big.Rat)) != 0 || !ni.HighInclusive ||
		ni.Low.Cmp(new(big.Rat).Neg(tie)) != 0 || !ni.LowInclusive {
		t.Fatalf("-0 interval = %+v, want [-2^-25, 0] closed", ni)
	}

	// Zero-valued rationals are adjudicated by the input sign, exactly
	// as Parse preserves "-0".
	for _, c := range []struct {
		s       string
		neg     bool
		wantPos bool
		wantNeg bool
	}{
		{"0", false, true, false},
		{"+0", false, true, false},
		{"-0", true, false, true},
		{"-0.0", true, false, true},
		{"0e10", false, true, false},
	} {
		neg, r := mustParse(t, c.s)
		if neg != c.neg || r.Sign() != 0 {
			t.Fatalf("Parse(%q) = neg %v, r %v", c.s, neg, r)
		}
		if pi.Contains(neg, r) != c.wantPos || ni.Contains(neg, r) != c.wantNeg {
			t.Fatalf("%s: +0 membership %v (want %v), -0 membership %v (want %v)",
				c.s, pi.Contains(neg, r), c.wantPos, ni.Contains(neg, r), c.wantNeg)
		}
	}

	// Tiny nonzero magnitudes, including the exact ties.
	for _, c := range []struct {
		s       string
		neg     bool
		inPosIV bool
		inNegIV bool
	}{
		{"0.00000001", false, true, false},                  // 1e-8 < 2^-25
		{"0.0000000298023223876953125", false, true, false}, // exactly 2^-25
		{"0.00000003", false, false, false},                 // just above the tie
		{"-0.0000000298023223876953125", true, false, true},
		{"-0.00000003", true, false, false},
	} {
		neg, r := mustParse(t, c.s)
		if neg != c.neg {
			t.Fatalf("%s: sign flag = %v", c.s, neg)
		}
		if pi.Contains(neg, r) != c.inPosIV {
			t.Fatalf("%s: +0 interval membership = %v, want %v", c.s, pi.Contains(neg, r), c.inPosIV)
		}
		if ni.Contains(neg, r) != c.inNegIV {
			t.Fatalf("%s: -0 interval membership = %v, want %v", c.s, ni.Contains(neg, r), c.inNegIV)
		}
	}
}

// TestIntervalConsistentWithResult cross-checks that every enumerated
// pattern's interval exactly predicts Convert's pattern for inputs at
// the pattern value itself, and that the certificate's class matches.
func TestIntervalConsistentWithResult(t *testing.T) {
	o := newOracle(t)
	for sign := uint16(0); sign <= 1; sign++ {
		neg := sign == 1
		for _, v := range o.vals {
			x := new(big.Rat).Set(v)
			if neg {
				x.Neg(x)
			}
			b := half.Convert(neg, x)
			iv := half.IntervalOf(b)
			if !iv.Contains(neg, x) {
				t.Fatalf("pattern %04X at its own value %s rejected by its interval", b.Pattern(), x)
			}
			if b.Class != classOfPattern(b.Pattern()) {
				t.Fatalf("pattern %04X class %s", b.Pattern(), b.Class)
			}
		}
	}
}

func classOfPattern(p uint16) half.Class {
	E := (p >> 10) & 0x1f
	F := p & 0x3ff
	switch {
	case E == 31:
		return half.ClassInfinity
	case E == 0 && F == 0:
		return half.ClassZero
	case E == 0:
		return half.ClassSubnormal
	default:
		return half.ClassNormal
	}
}

// TestIntervalPartitionOnRandomDecimals feeds thousands of legal
// decimals (including factor-of-5 denominators and the 30-digit path)
// through the real parser and asserts the certificates form an exact
// partition: the converted pattern's interval accepts the value, while
// the intervals of the two magnitudes adjacent in the independently
// enumerated grid reject it. Signed zeros follow the input sign.
func TestIntervalPartitionOnRandomDecimals(t *testing.T) {
	o := newOracle(t)
	rng := rand.New(rand.NewSource(777777))
	for iter := 0; iter < 4000; iter++ {
		nd := 1 + rng.Intn(30)
		var b strings.Builder
		neg := rng.Intn(2) == 0
		if neg {
			b.WriteByte('-')
		}
		digits := make([]byte, nd)
		for i := range digits {
			digits[i] = byte('0' + rng.Intn(10))
		}
		digits[0] = byte('1' + rng.Intn(9))
		point := rng.Intn(nd + 1)
		b.Write(digits[:point])
		b.WriteByte('.')
		b.Write(digits[point:])
		b.WriteByte('e')
		exp := rng.Intn(101) - 50
		b.WriteString(big.NewInt(int64(exp)).String())
		s := b.String()

		pneg, r, err := decimal.Parse(s)
		if err != nil {
			t.Fatalf("generated %q: %v", s, err)
		}
		got := half.Convert(pneg, r)
		iv := half.IntervalOf(got)
		if !iv.Contains(pneg, r) {
			t.Fatalf("%s -> %04X but its own interval rejects it [%v,%v]",
				s, got.Pattern(), iv.Low, iv.High)
		}

		// The wrong-signed zero certificate must reject a zero input.
		if r.Sign() == 0 {
			wrong := half.Convert(!pneg, new(big.Rat))
			if half.IntervalOf(wrong).Contains(pneg, r) {
				t.Fatalf("%s: opposite-sign zero interval wrongly accepts it", s)
			}
			continue
		}

		a := new(big.Rat).Abs(r)
		rejectNeighbor := func(idx int) {
			if idx < 0 || idx >= len(o.vals) {
				return
			}
			nv := new(big.Rat).Set(o.vals[idx])
			if neg {
				nv.Neg(nv)
			}
			nb := half.Convert(neg, nv)
			if nb.Pattern() == got.Pattern() {
				return
			}
			if half.IntervalOf(nb).Contains(pneg, r) {
				t.Fatalf("%s (-> %04X) also fits adjacent %04X",
					s, got.Pattern(), nb.Pattern())
			}
		}

		if got.IsInf() {
			// The only finite neighbour is the largest finite pattern.
			rejectNeighbor(len(o.vals) - 1)
			continue
		}
		idx := sort.Search(len(o.vals), func(i int) bool { return o.vals[i].Cmp(a) >= 0 })
		rejectNeighbor(idx - 1)
		rejectNeighbor(idx)
		if idx < len(o.vals) && o.vals[idx].Cmp(a) == 0 {
			rejectNeighbor(idx + 1)
		}
	}
}
