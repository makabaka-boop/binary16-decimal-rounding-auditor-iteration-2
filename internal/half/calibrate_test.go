package half_test

// Independent tests for the exact common-bias calibration machinery.
// The oracle here never calls the production intersection code: it
// enumerates bias candidates on an independently derived rational grid
// and tests membership directly through the certificates, then checks
// the production interval bounds and the minimum-absolute bias against
// that ground truth. Open endpoints, signed zeros, the subnormal/normal
// boundary and the infinity overflow tails are all exercised.

import (
	"math/big"
	"math/rand"
	"testing"

	"halfconv/internal/decimal"
	"halfconv/internal/half"
)

// oracleBiasSet recomputes the accepted bias interval of one item from
// scratch: it takes the target certificate, shifts every bound by
// -original itself, and then applies the zero convention directly.
func oracleBiasSet(t *testing.T, negLit bool, original *big.Rat, target half.Bits) half.Interval {
	t.Helper()
	iv := half.IntervalOf(target)
	shift := new(big.Rat).Neg(original)
	out := half.Interval{
		LowInclusive:  iv.LowInclusive,
		HighInclusive: iv.HighInclusive,
		LowInfinite:   iv.LowInfinite,
		HighInfinite:  iv.HighInfinite,
	}
	if iv.Low != nil {
		out.Low = new(big.Rat).Add(iv.Low, shift)
	}
	if iv.High != nil {
		out.High = new(big.Rat).Add(iv.High, shift)
	}

	zeroBias := new(big.Rat).Neg(original)
	if target.Class == half.ClassZero {
		if target.Sign == 0 {
			if negLit && original.Sign() == 0 &&
				out.Low != nil && out.Low.Cmp(zeroBias) == 0 {
				out.LowInclusive = false
			}
		} else {
			if out.High != nil && out.High.Cmp(zeroBias) == 0 {
				out.HighInclusive = negLit && original.Sign() == 0
			}
		}
	}
	return out
}

// oracleMember decides bias membership in a shifted bias interval. The
// interval is already on the bias axis, so it compares the bias
// directly; only signed-zero targets need the exact-zero sum sign
// adjudication of the calibration convention.
func oracleMember(iv half.Interval, target half.Bits, negLit bool, original, bias *big.Rat) bool {
	sum := new(big.Rat).Add(original, bias)
	if sum.Sign() == 0 && target.Class == half.ClassZero {
		// At an exact zero sum the convention alone decides the sign;
		// shifted bound positions are irrelevant.
		sumIsNegZero := negLit && bias.Sign() == 0
		return (target.Sign == 1) == sumIsNegZero
	}
	return biasMember(iv, bias)
}

// biasMember compares a bias against the interval's own geometry.
func biasMember(iv half.Interval, bias *big.Rat) bool {
	if !iv.LowInfinite {
		switch c := bias.Cmp(iv.Low); {
		case c < 0:
			return false
		case c == 0 && !iv.LowInclusive:
			return false
		}
	}
	if !iv.HighInfinite {
		switch c := bias.Cmp(iv.High); {
		case c > 0:
			return false
		case c == 0 && !iv.HighInclusive:
			return false
		}
	}
	return true
}

// oracleIntersect intersects intervals by selecting endpoints purely
// with *big.Rat comparisons (independent of the production max/min).
func oracleIntersect(a, b half.Interval) (half.Interval, bool) {
	type end struct {
		v         *big.Rat
		inf       int // -1 lower tail, +1 upper tail, 0 finite
		inclusive bool
	}
	lo := func(x half.Interval) end {
		if x.LowInfinite {
			return end{inf: -1}
		}
		return end{v: x.Low, inclusive: x.LowInclusive}
	}
	hi := func(x half.Interval) end {
		if x.HighInfinite {
			return end{inf: +1}
		}
		return end{v: x.High, inclusive: x.HighInclusive}
	}
	maxLo := func(p, q end) end {
		if p.inf == -1 {
			return q
		}
		if q.inf == -1 {
			return p
		}
		switch p.v.Cmp(q.v) {
		case 1:
			return p
		case -1:
			return q
		default:
			return end{v: p.v, inclusive: p.inclusive && q.inclusive}
		}
	}
	minHi := func(p, q end) end {
		if p.inf == +1 {
			return q
		}
		if q.inf == +1 {
			return p
		}
		switch p.v.Cmp(q.v) {
		case -1:
			return p
		case 1:
			return q
		default:
			return end{v: p.v, inclusive: p.inclusive && q.inclusive}
		}
	}
	l, h := maxLo(lo(a), lo(b)), minHi(hi(a), hi(b))
	c := half.Interval{LowInclusive: l.inclusive, HighInclusive: h.inclusive}
	if l.inf == -1 {
		c.LowInfinite = true
	} else {
		c.Low = l.v
	}
	if h.inf == +1 {
		c.HighInfinite = true
	} else {
		c.High = h.v
	}
	if l.inf != -1 && h.inf != +1 {
		switch l.v.Cmp(h.v) {
		case 1:
			return half.Interval{}, false
		case 0:
			if !l.inclusive || !h.inclusive {
				return half.Interval{}, false
			}
		}
	}
	return c, true
}

// assertIntersectionByProbes verifies the production intersection at
// every endpoint and a set of nearby/interior points purely by direct
// membership in the two operand intervals.
func assertIntersectionByProbes(t *testing.T, a, b, c half.Interval, ok bool) {
	t.Helper()
	points := map[string]*big.Rat{}
	add := func(p *big.Rat) { points[p.RatString()] = new(big.Rat).Set(p) }
	one := big.NewRat(1, 1)
	for _, iv := range []half.Interval{a, b} {
		for _, p := range []*big.Rat{iv.Low, iv.High} {
			if p == nil {
				continue
			}
			add(p)
			add(new(big.Rat).Sub(p, one))
			add(new(big.Rat).Add(p, one))
		}
	}
	mid := func(x, y *big.Rat) *big.Rat {
		m := new(big.Rat).Add(x, y)
		return m.Quo(m, big.NewRat(2, 1))
	}
	for _, x := range []*big.Rat{a.Low, b.Low} {
		for _, y := range []*big.Rat{a.High, b.High} {
			if x != nil && y != nil && x.Cmp(y) < 0 {
				add(mid(x, y))
			}
		}
	}
	for _, p := range points {
		want := biasMember(a, p) && biasMember(b, p)
		got := ok && biasMember(c, p)
		if got != want {
			t.Fatalf("probe %v: production membership=%v, per-interval=%v\n a=%+v\n b=%+v\n c=%+v ok=%v",
				p, got, want, a, b, c, ok)
		}
	}
}

func sameInterval(t *testing.T, tag string, got, want half.Interval) {
	t.Helper()
	eq := func(x, y *big.Rat) bool {
		if x == nil || y == nil {
			return x == nil && y == nil
		}
		return x.Cmp(y) == 0
	}
	if !eq(got.Low, want.Low) || !eq(got.High, want.High) ||
		got.LowInclusive != want.LowInclusive ||
		got.HighInclusive != want.HighInclusive ||
		got.LowInfinite != want.LowInfinite ||
		got.HighInfinite != want.HighInfinite {
		t.Fatalf("%s:\n got  %+v\n want %+v", tag, got, want)
	}
}

func parseRat(t *testing.T, s string) (bool, *big.Rat) {
	t.Helper()
	neg, r, err := decimal.Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return neg, r
}

// TestBiasIntervalAgainstOracle checks each per-item bias interval on a
// grid of originals against the independently constructed one, and
// probes both sides of every endpoint with 1/8 of the local gap.
func TestBiasIntervalAgainstOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))

	// Patterns to target: signed zeros, a sweep across the subnormal
	// grid including the normal boundary, and the two infinities.
	var patterns []uint16
	patterns = append(patterns, 0x0000, 0x8000)
	for F := uint16(1); F <= 1023; F++ {
		patterns = append(patterns, F, 0x8000|F) // positive/negative subnormals
	}
	// First normal binade (2^-14 .. 2^-13): edges and interior samples.
	for _, F := range []uint16{0, 1, 2, 3, 511, 512, 1021, 1022, 1023} {
		patterns = append(patterns, 0x0400|F, 0x8400|F)
	}
	patterns = append(patterns, 0x7BFF, 0xFBFF, 0x7C00, 0xFC00)
	patterns = append(patterns, 0x3C00, 0x3C01, 0x3E00, 0x7BFE)

	// Originals on a small rational grid: multiples of 2^-26 from
	// -2^-19 to 2^-19, plus decimal-tenth style values and zero literals.
	var originals []struct {
		neg bool
		r   *big.Rat
	}
	for k := int64(-128); k <= 128; k++ {
		originals = append(originals, struct {
			neg bool
			r   *big.Rat
		}{k < 0, ratPow2(k, 26)})
	}
	for _, s := range []string{"0", "-0", "+0", "0.1", "-0.1", "1", "-1", "0.000060975551605224609375", "65504", "70000", "-70000"} {
		neg, r := parseRat(t, s)
		originals = append(originals, struct {
			neg bool
			r   *big.Rat
		}{neg, r})
	}

	mkTarget := func(p uint16) half.Bits {
		b, ok := half.PatternBits(p)
		if !ok {
			t.Fatalf("pattern %04X rejected", p)
		}
		return b
	}

	for iter := 0; iter < 600; iter++ {
		p := patterns[rng.Intn(len(patterns))]
		og := originals[rng.Intn(len(originals))]
		target := mkTarget(p)

		got := half.BiasInterval(og.r, og.neg, target)
		want := oracleBiasSet(t, og.neg, og.r, target)
		sameInterval(t, "bias interval mismatch", got, want)

		// Membership must equal direct recomputation on the endpoint
		// values and 1/8-gap probes.
		var probes []*big.Rat
		add := func(r *big.Rat) { probes = append(probes, new(big.Rat).Set(r)) }
		if got.Low != nil {
			add(got.Low)
			if !got.LowInfinite && target.Class != half.ClassZero {
				gap := probeGap(got)
				add(new(big.Rat).Sub(got.Low, gap))
				add(new(big.Rat).Add(got.Low, gap))
			}
		}
		if got.High != nil {
			add(got.High)
			if !got.HighInfinite && target.Class != half.ClassZero {
				gap := probeGap(got)
				add(new(big.Rat).Sub(got.High, gap))
				add(new(big.Rat).Add(got.High, gap))
			}
		}
		// Zero bias and the bias making the sum zero.
		add(new(big.Rat))
		add(new(big.Rat).Neg(og.r))

		for _, b := range probes {
			m := gotMember(got, target, og.neg, og.r, b)
			om := oracleMember(want, target, og.neg, og.r, b)
			if m != om {
				t.Fatalf("pattern %04X original %s negLit=%v bias %s: member=%v oracle=%v",
					p, og.r, og.neg, b, m, om)
			}
			// Recomputed pattern agrees.
			rec := half.CalibratedConvert(og.neg, og.r, b)
			convOK := rec.Pattern() == p
			if m != convOK {
				t.Fatalf("pattern %04X original %s bias %s: interval member=%v but recompute gave %04X",
					p, og.r, b, m, rec.Pattern())
			}
		}
	}
}

// gotMember is membership in a bias interval for a constraint; it is
// identical to the independent oracle's geometry check.
func gotMember(iv half.Interval, target half.Bits, negLit bool, original, bias *big.Rat) bool {
	return oracleMember(iv, target, negLit, original, bias)
}

// probeGap returns an eighth of the span of a bounded interval (falling
// back to 2^-28 for a tail), guaranteed to stay just off an endpoint for
// dyadic-grid inputs.
func probeGap(iv half.Interval) *big.Rat {
	eighth := big.NewRat(8, 1)
	if iv.Low != nil && iv.High != nil {
		g := new(big.Rat).Sub(iv.High, iv.Low)
		if g.Sign() > 0 {
			return g.Quo(g, eighth)
		}
	}
	return new(big.Rat).SetFrac(big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), 28))
}

// TestIntersectionGrid pairs independently derived per-item intervals
// and compares the production intersection with the oracle, including
// open-endpoint emptiness.
func TestIntersectionGrid(t *testing.T) {
	o := newOracle(t)
	rng := rand.New(rand.NewSource(909090))
	mk := func(p uint16, orig struct {
		neg bool
		r   *big.Rat
	}) half.Interval {
		b, ok := half.PatternBits(p)
		if !ok {
			t.Fatalf("bad pattern %04X", p)
		}
		return half.BiasInterval(orig.r, orig.neg, b)
	}
	pickOrig := func() struct {
		neg bool
		r   *big.Rat
	} {
		k := int64(rng.Intn(511) - 255)
		return struct {
			neg bool
			r   *big.Rat
		}{k < 0, ratPow2(k, 26)}
	}
	allPats := o.pats
	allPats = append(append([]uint16{}, allPats...), 0x0000, 0x8000, 0x7C00, 0xFC00)

	for iter := 0; iter < 3000; iter++ {
		p1 := allPats[rng.Intn(len(allPats))]
		p2 := allPats[rng.Intn(len(allPats))]
		a := mk(p1, pickOrig())
		b := mk(p2, pickOrig())

		got, gok := half.IntersectIntervals(a, b)
		want, wok := oracleIntersect(a, b)
		if gok != wok {
			t.Fatalf("intersect feasibility mismatch:\n a=%+v\n b=%+v\n got ok=%v want ok=%v",
				a, b, gok, wok)
		}
		if gok {
			sameInterval(t, "intersection bounds", got, want)
		}
		assertIntersectionByProbes(t, a, b, got, gok)
	}

	// Hand cases: equal bounds, one open => empty.
	closed := func(lo, hi int64) half.Interval {
		return half.Interval{Low: big.NewRat(lo, 1), High: big.NewRat(hi, 1), LowInclusive: true, HighInclusive: true}
	}
	openLow := func(lo, hi int64) half.Interval {
		x := closed(lo, hi)
		x.LowInclusive = false
		return x
	}
	if _, ok := half.IntersectIntervals(closed(1, 1), openLow(1, 2)); ok {
		t.Fatalf("[1,1] intersect (1,2] must be empty")
	}
	if c, ok := half.IntersectIntervals(closed(1, 2), openLow(1, 3)); !ok ||
		c.LowInclusive || c.Low.Cmp(big.NewRat(1, 1)) != 0 ||
		c.High.Cmp(big.NewRat(2, 1)) != 0 || !c.HighInclusive {
		t.Fatalf("unexpected [1,2] n (1,3] = %+v ok=%v", c, ok)
	}
	// Tail intersection.
	tail := half.Interval{Low: big.NewRat(65520, 1), LowInclusive: true, HighInfinite: true}
	shifted := closed(60000, 70000)
	c, ok := half.IntersectIntervals(tail, shifted)
	if !ok || !c.LowInclusive || c.Low.Cmp(big.NewRat(65520, 1)) != 0 || c.HighInfinite {
		t.Fatalf("tail intersect finite = %+v ok=%v", c, ok)
	}
	if c.High == nil || c.High.Cmp(big.NewRat(70000, 1)) != 0 || !c.HighInclusive {
		t.Fatalf("tail intersect finite high bound = %+v", c.High)
	}
}

// TestSignedZeroCalibration is the exact zero overlap rule: bias zero
// is +0 always; only literal "-0" with zero bias keeps -0; the two
// certificates must not both accept the same bias at a batch
// intersection.
func TestSignedZeroCalibration(t *testing.T) {
	zero := new(big.Rat)
	pos, _ := half.PatternBits(0x0000)
	neg, _ := half.PatternBits(0x8000)

	// literal "0": bias 0 is in +0, out of -0.
	ivPos := half.BiasInterval(zero, false, pos)
	ivNeg := half.BiasInterval(zero, false, neg)
	if !gotMember(ivPos, pos, false, zero, zero) {
		t.Fatalf("0 + bias0 must be +0")
	}
	if gotMember(ivNeg, neg, false, zero, zero) {
		t.Fatalf("0 + bias0 must NOT be -0")
	}
	// literal "-0": bias 0 is in -0, out of +0.
	ivPosM := half.BiasInterval(zero, true, pos)
	ivNegM := half.BiasInterval(zero, true, neg)
	if gotMember(ivPosM, pos, true, zero, zero) {
		t.Fatalf("literal -0 + bias0 must NOT be +0")
	}
	if !gotMember(ivNegM, neg, true, zero, zero) {
		t.Fatalf("literal -0 + bias0 must be -0")
	}
	// A nonzero original cancelled exactly by its negative bias is
	// always +0, regardless of the literal's sign.
	one := big.NewRat(1, 1)
	cancel := big.NewRat(-1, 1)
	ivPosCancel := half.BiasInterval(one, false, pos)
	if !gotMember(ivPosCancel, pos, false, one, cancel) {
		t.Fatalf("1 + (-1) exact zero must be +0")
	}
	ivNegCancel := half.BiasInterval(one, false, neg)
	if gotMember(ivNegCancel, neg, false, one, cancel) {
		t.Fatalf("1 + (-1) exact zero must not be -0")
	}

	// Batch: ["0", "-0"] targets [+0, -0] => bias 0 is the only common
	// point; targets reversed => empty (no bias makes literal 0 a -0
	// while literal -0 becomes +0 at zero, and nonzero biases fail both).
	cal := half.Calibrate(
		[]*big.Rat{zero, zero},
		[]bool{false, true},
		[]half.Bits{pos, neg},
	)
	if !cal.Feasible || !cal.Attained || cal.Bias.Sign() != 0 {
		t.Fatalf("[0,-0]->[+0,-0] needs bias 0, got %+v", cal)
	}
	cal = half.Calibrate(
		[]*big.Rat{zero, zero},
		[]bool{false, true},
		[]half.Bits{neg, pos},
	)
	if cal.Feasible {
		t.Fatalf("[0,-0]->[-0,+0] must be infeasible, got %+v", cal)
	}
}

// TestSubnormalBoundaryCalibration exercises the 2^-14 normal join and
// the smallest subnormal, where ties are odd/even sensitive.
func TestSubnormalBoundaryCalibration(t *testing.T) {
	// Original exactly at the tie 2047/2^25 (rounds to largest
	// subnormal 03FF, F odd, tie excluded). Target the smallest normal
	// 0400: feasible biases must be strictly positive (tie excluded on
	// the 03FF side is irrelevant; the constraint interval for 0400
	// includes zero bias at the low end? original 2047/2^25 < 2^-14, and
	// 0400's low tie is exactly 2047/2^25 included since F=0 even).
	tie := ratPow2(2047, 25)
	t0400, _ := half.PatternBits(0x0400)
	iv := half.BiasInterval(tie, false, t0400)
	if iv.Low.Cmp(new(big.Rat)) != 0 || !iv.LowInclusive {
		t.Fatalf("tie->0400 lower bias bound should be 0 included, got %+v", iv)
	}

	// Conversely original at the tie targeting 03FF: bias 0 is excluded
	// (odd candidate loses the tie), so any feasible bias interval lies
	// strictly below 0 and 0 is not feasible.
	t03FF, _ := half.PatternBits(0x03FF)
	iv2 := half.BiasInterval(tie, false, t03FF)
	if iv2.High.Cmp(new(big.Rat)) != 0 || iv2.HighInclusive {
		t.Fatalf("tie->03FF upper bias bound should be 0 excluded, got %+v", iv2)
	}
	if gotMember(iv2, t03FF, false, tie, new(big.Rat)) {
		t.Fatalf("zero bias at odd tie must be rejected")
	}

	// Smallest subnormal tie at 2^-25: original = 2^-25 rounds to zero
	// (even), so targeting 0001 requires bias strictly > 0... the 0001
	// lower endpoint 2^-25 is excluded (F=1 odd), hence low bias bound 0
	// excluded.
	tinyTie := ratPow2(1, 25)
	t0001, _ := half.PatternBits(0x0001)
	iv3 := half.BiasInterval(tinyTie, false, t0001)
	if iv3.Low.Cmp(new(big.Rat)) != 0 || iv3.LowInclusive {
		t.Fatalf("2^-25->0001 lower bias bound should be 0 excluded, got %+v", iv3)
	}

	// Full batch feasibility: both constraints together are feasible with
	// a strictly positive minimum? iv low=0 incl (0400 even tie), iv3
	// low=0 excl (0001 odd tie): intersection low = 0 excluded, so the
	// infimum 0 is NOT attainable but positive biases exist.
	cal := half.Calibrate(
		[]*big.Rat{tie, tinyTie},
		[]bool{false, false},
		[]half.Bits{t0400, t0001},
	)
	if !cal.Feasible {
		t.Fatalf("batch should be feasible for positive biases: %+v", cal)
	}
	if cal.Attained {
		t.Fatalf("minimum must be the excluded endpoint 0, got attained bias %v", cal.Bias)
	}
	if cal.Infimum.Sign() != 0 {
		t.Fatalf("infimum must be 0, got %v", cal.Infimum)
	}
	if cal.WitnessBias == nil || cal.WitnessBias.Sign() <= 0 {
		t.Fatalf("witness must be strictly positive, got %v", cal.WitnessBias)
	}
	if i := half.MismatchAt(
		[]*big.Rat{tie, tinyTie}, []bool{false, false},
		[]half.Bits{t0400, t0001}, cal.WitnessBias); i != -1 {
		t.Fatalf("witness mismatches item %d", i)
	}
}

// TestOverflowTailCalibration covers infinity target tail intervals:
// a +inf target forces bias into a [65520-x, +inf) tail, and combining
// signs can leave one-sided feasible intervals.
func TestOverflowTailCalibration(t *testing.T) {
	posInf, _ := half.PatternBits(0x7C00)
	negInf, _ := half.PatternBits(0xFC00)

	// Original 70000 already +inf: bias interval is [65520-70000,+inf)
	// = [-4480,+inf), low included.
	hi := big.NewRat(70000, 1)
	iv := half.BiasInterval(hi, false, posInf)
	wantLo := big.NewRat(-4480, 1)
	if iv.LowInfinite || iv.Low.Cmp(wantLo) != 0 || !iv.LowInclusive || !iv.HighInfinite {
		t.Fatalf("70000->+inf bias interval = %+v", iv)
	}
	// Bias -4480 lands exactly on the tie 65520 which the even infinity
	// candidate wins: included.
	if rec := half.CalibratedConvert(false, hi, wantLo); rec.Pattern() != 0x7C00 {
		t.Fatalf("tie bias recomputed to %04X", rec.Pattern())
	}
	// Bias just below is rejected.
	below := new(big.Rat).Sub(wantLo, big.NewRat(1, 1))
	if rec := half.CalibratedConvert(false, hi, below); rec.Pattern() == 0x7C00 {
		t.Fatalf("bias %v must not reach +inf (got 7C00)", below)
	}

	// -70000 -> -inf: (-inf, 4480] tail.
	nhi := big.NewRat(-70000, 1)
	niv := half.BiasInterval(nhi, true, negInf)
	if !niv.LowInfinite || niv.High == nil || niv.High.Cmp(big.NewRat(4480, 1)) != 0 || !niv.HighInclusive {
		t.Fatalf("-70000->-inf bias interval = %+v", niv)
	}

	// Batch [70000 -> +inf, -70000 -> -inf] intersects to [-4480, 4480],
	// attained minimum bias 0.
	cal := half.Calibrate(
		[]*big.Rat{hi, nhi},
		[]bool{false, true},
		[]half.Bits{posInf, negInf},
	)
	if !cal.Feasible || !cal.Attained || cal.Bias.Sign() != 0 {
		t.Fatalf("two-sided infinity batch: %+v", cal)
	}
	if i := half.MismatchAt(
		[]*big.Rat{hi, nhi}, []bool{false, true},
		[]half.Bits{posInf, negInf}, cal.Bias); i != -1 {
		t.Fatalf("bias 0 must satisfy both tails, mismatch at %d", i)
	}

	// Incompatible: both originals large positive but one demands -inf.
	cal2 := half.Calibrate(
		[]*big.Rat{big.NewRat(70000, 1), big.NewRat(60000, 1)},
		[]bool{false, false},
		[]half.Bits{posInf, negInf},
	)
	if cal2.Feasible {
		t.Fatalf("+inf and -inf demands from huge positives must be infeasible: %+v", cal2)
	}
}

// TestCalibrateMinAbsBias builds feasible batches from a known bias and
// checks the reported interval and minimum bias on a small rational
// grid; when the minimum is an excluded endpoint the result must be
// unattained with an interior witness that satisfies every constraint.
func TestCalibrateMinAbsBias(t *testing.T) {
	rng := rand.New(rand.NewSource(31337))
	o := newOracle(t)

	for iter := 0; iter < 400; iter++ {
		m := 1 + rng.Intn(4)
		originals := make([]*big.Rat, m)
		negLits := make([]bool, m)
		targets := make([]half.Bits, m)

		// Pick a true bias on a fine dyadic grid so the shifted sums
		// land on or near grid ties; sometimes force it onto a tie.
		bias := ratScaled2(int64(rng.Intn(201)-100), -22)
		if rng.Intn(4) == 0 {
			bias = ratScaled2(int64(rng.Intn(41)-20), -25) // includes exact 2^-25 ties
		}
		feasible := true
		for i := 0; i < m; i++ {
			idx := rng.Intn(2049) // cover subnormals through first normals
			v := new(big.Rat).Set(o.vals[idx])
			// jitter by grid eighths to hit ties and interiors
			j := int64(rng.Intn(9) - 4)
			if idx < 1024 {
				v.Add(v, ratScaled2(j, -27))
			} else {
				v.Add(v, ratScaled2(j, -13))
			}
			if rng.Intn(2) == 0 {
				v.Neg(v)
			}
			originals[i] = v
			sum := new(big.Rat).Add(v, bias)
			if sum.Sign() == 0 {
				negLits[i] = rng.Intn(2) == 0
			} else {
				negLits[i] = v.Sign() < 0 && rng.Intn(2) == 0
			}
			targets[i] = half.CalibratedConvert(negLits[i], v, bias)
			if _, ok := half.PatternBits(targets[i].Pattern()); !ok {
				feasible = false
			}
		}
		if !feasible {
			continue
		}

		cal := half.Calibrate(originals, negLits, targets)
		if !cal.Feasible {
			t.Fatalf("iter %d: known bias %s declared infeasible\n originals=%v",
				iter, bias, originals)
		}

		// The known bias must belong to the reported interval.
		if !biasMember(cal.Interval, bias) {
			t.Fatalf("iter %d: known bias %s outside interval %+v", iter, bias, cal.Interval)
		}
		if i := half.MismatchAt(originals, negLits, targets, bias); i != -1 {
			t.Fatalf("iter %d: known bias mismatches item %d", iter, i)
		}

		if cal.Attained {
			if cal.Bias == nil {
				t.Fatalf("attained without bias")
			}
			if i := half.MismatchAt(originals, negLits, targets, cal.Bias); i != -1 {
				t.Fatalf("iter %d: claimed bias %v mismatches item %d",
					iter, cal.Bias, i)
			}
			// No feasible bias with a strictly smaller absolute value:
			// probe between 0 and the claimed minimum at eighth steps.
			if cal.Bias.Sign() != 0 {
				step := new(big.Rat).Quo(new(big.Rat).Set(cal.Bias), big.NewRat(8, 1))
				for k := int64(1); k < 8; k++ {
					probe := new(big.Rat).Mul(step, big.NewRat(k, 1))
					if cal.Bias.Sign() < 0 {
						probe.Neg(probe)
					}
					if half.MismatchAt(originals, negLits, targets, probe) == -1 {
						t.Fatalf("iter %d: bias %v smaller than claimed min %v is feasible",
							iter, probe, cal.Bias)
					}
				}
			}
		} else {
			if cal.Infimum == nil || cal.WitnessBias == nil {
				t.Fatalf("unattained result needs infimum and witness: %+v", cal)
			}
			// Infimum excluded from the interval.
			if biasMember(cal.Interval, cal.Infimum) {
				t.Fatalf("iter %d: infimum %v must be excluded", iter, cal.Infimum)
			}
			if half.MismatchAt(originals, negLits, targets, cal.Infimum) == -1 {
				t.Fatalf("iter %d: infimum %v unexpectedly recomputes targets", iter, cal.Infimum)
			}
			// Witness strictly inside and feasible.
			if !biasMember(cal.Interval, cal.WitnessBias) {
				t.Fatalf("iter %d: witness %v not inside %+v", iter, cal.WitnessBias, cal.Interval)
			}
			if i := half.MismatchAt(originals, negLits, targets, cal.WitnessBias); i != -1 {
				t.Fatalf("iter %d: witness mismatches item %d", iter, i)
			}
			// Witness lies on the correct side of the infimum.
			if new(big.Rat).Abs(cal.WitnessBias).Cmp(new(big.Rat).Abs(cal.Infimum)) <= 0 {
				t.Fatalf("iter %d: witness |%v| <= infimum |%v|", iter, cal.WitnessBias, cal.Infimum)
			}
		}
	}
}

// TestCalibrateExhaustiveBiasGrid builds batches whose bias intervals
// lie inside a fine dyadic range and brute-forces every grid bias in a
// wide window by recomputing the actual rounded pattern: a bias is
// feasible in the grid iff production declares it feasible and the
// geometry interval accepts it. Endpoint grid points (included and
// excluded ties) are deliberately included.
func TestCalibrateExhaustiveBiasGrid(t *testing.T) {
	rng := rand.New(rand.NewSource(20261008))

	// Work in the tiny region [-2^-21, 2^-21] at 2^-27 spacing, which
	// divides the smallest subnormal spacing 2^-24 and every tie, so
	// all per-item endpoints land exactly on grid points.
	grid := func(k int64) *big.Rat { return ratPow2(k, 27) }
	const halfRange = 64 // 2^-21

	makeItem := func() (*big.Rat, bool, half.Bits) {
		// Original k*2^-27, target pattern reached at an independently
		// chosen "true" grid bias; this guarantees a feasible item.
		ok := int64(rng.Intn(2*halfRange+1) - halfRange)
		bk := int64(rng.Intn(2*halfRange+1) - halfRange)
		v := grid(ok)
		b := grid(bk)
		negLit := false
		sum := new(big.Rat).Add(v, b)
		if sum.Sign() == 0 {
			negLit = rng.Intn(2) == 0
		}
		target := half.CalibratedConvert(negLit, v, b)
		return v, negLit, target
	}

	for iter := 0; iter < 500; iter++ {
		m := 1 + rng.Intn(3)
		orig := make([]*big.Rat, m)
		negLits := make([]bool, m)
		targets := make([]half.Bits, m)
		for i := range orig {
			orig[i], negLits[i], targets[i] = makeItem()
		}
		cal := half.Calibrate(orig, negLits, targets)

		for gk := int64(-halfRange * 32); gk <= halfRange*32; gk++ {
			b := grid(gk)
			gridFeasible := half.MismatchAt(orig, negLits, targets, b) == -1
			geomFeasible := cal.Feasible && biasMember(cal.Interval, b)
			if gridFeasible != geomFeasible {
				t.Fatalf("iter %d grid bias %v: brute-force feasible=%v, geometry=%v\n interval=%+v",
					iter, b, gridFeasible, geomFeasible, cal.Interval)
			}
			// A grid-feasible bias must lie on the correct side of the
			// claimed minimum absolute bias.
			if gridFeasible && cal.Feasible {
				switch {
				case cal.Attained && cal.Bias != nil:
					if new(big.Rat).Abs(b).Cmp(new(big.Rat).Abs(cal.Bias)) < 0 {
						t.Fatalf("iter %d: grid bias %v smaller than claimed min %v",
							iter, b, cal.Bias)
					}
				case !cal.Attained:
					if new(big.Rat).Abs(b).Cmp(new(big.Rat).Abs(cal.Infimum)) <= 0 {
						t.Fatalf("iter %d: grid-feasible bias %v not strictly beyond infimum %v",
							iter, b, cal.Infimum)
					}
				}
			}
		}

		if cal.Feasible {
			// Attained minimum must itself be grid-feasible.
			if cal.Attained {
				if i := half.MismatchAt(orig, negLits, targets, cal.Bias); i != -1 {
					t.Fatalf("iter %d: attained bias %v fails item %d", iter, cal.Bias, i)
				}
			} else {
				// Unattained infimum must fail at least one item.
				if half.MismatchAt(orig, negLits, targets, cal.Infimum) == -1 {
					t.Fatalf("iter %d: infimum %v unexpectedly feasible", iter, cal.Infimum)
				}
				if i := half.MismatchAt(orig, negLits, targets, cal.WitnessBias); i != -1 {
					t.Fatalf("iter %d: witness %v fails item %d", iter, cal.WitnessBias, i)
				}
			}
		}
	}
}

// TestPatternBitsRejectsNaN verifies only finite/infinity patterns are
// accepted.
func TestPatternBitsRejectsNaN(t *testing.T) {
	nanPats := []uint16{0x7C01, 0x7FFF, 0xFC01, 0xFFFF, 0x7E00}
	for _, p := range nanPats {
		if _, ok := half.PatternBits(p); ok {
			t.Fatalf("NaN pattern %04X must be rejected", p)
		}
	}
	for _, p := range []uint16{0x0000, 0x8000, 0x0001, 0x8001, 0x0400, 0x7BFF, 0xFBFF, 0x7C00, 0xFC00} {
		b, ok := half.PatternBits(p)
		if !ok {
			t.Fatalf("pattern %04X must be accepted", p)
		}
		if b.Pattern() != p {
			t.Fatalf("PatternBits(%04X).Pattern() = %04X", p, b.Pattern())
		}
	}
}
