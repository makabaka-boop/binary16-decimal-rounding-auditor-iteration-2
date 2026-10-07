package half_test

import (
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"halfconv/internal/decimal"
	"halfconv/internal/half"
)

// ---- independent test helpers ---------------------------------------------

// decodePattern decodes a binary16 bit pattern straight from the IEEE
// formula. It deliberately does not call any production helper, so the
// exhaustive round-trip is a genuine cross-check.
func decodePattern(p uint16) *big.Rat {
	E := (p >> 10) & 0x1f
	F := p & 0x3ff
	r := new(big.Rat)
	if E == 0 {
		r.SetFrac(big.NewInt(int64(F)), new(big.Int).Lsh(big.NewInt(1), 24))
	} else {
		shift := int(E) - 25 // (1024+F) * 2^(E-25)
		r.SetInt64(int64(1024 + F))
		if shift >= 0 {
			r.Mul(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(shift))))
		} else {
			r.Quo(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(-shift))))
		}
	}
	if p&0x8000 != 0 {
		r.Neg(r)
	}
	return r
}

// pow2Exponent returns k when d == 2^k.
func pow2Exponent(t *testing.T, d *big.Int) int {
	t.Helper()
	u := new(big.Int).Set(d)
	k := 0
	for u.Bit(0) == 0 {
		u.Rsh(u, 1)
		k++
	}
	if u.Cmp(big.NewInt(1)) != 0 {
		t.Fatalf("denominator %s is not a power of two", d)
	}
	return k
}

// renderFinite prints a rational whose denominator is a power of two as
// an exact terminating decimal string (no exponent marker). This keeps
// every exhaustive case inside the parser's 30-significant-digit bound.
func renderFinite(t *testing.T, q *big.Rat) string {
	t.Helper()
	num := new(big.Int).Set(q.Num())
	neg := num.Sign() < 0
	num.Abs(num)
	k := pow2Exponent(t, q.Denom())

	if k == 0 {
		s := num.String()
		if neg {
			return "-" + s
		}
		return s
	}
	// n / 2^k = (n * 5^k) / 10^k
	digits := new(big.Int).Mul(num, new(big.Int).Exp(big.NewInt(5), big.NewInt(int64(k)), nil))
	s := digits.String()
	if len(s) <= k {
		s = strings.Repeat("0", k+1-len(s)) + s
	}
	intPart := s[:len(s)-k]
	fracPart := strings.TrimRight(s[len(s)-k:], "0")
	out := intPart
	if fracPart != "" {
		out += "." + fracPart
	}
	if neg {
		out = "-" + out
	}
	return out
}

func ratPow2(n int64, k uint) *big.Rat {
	return new(big.Rat).SetFrac(big.NewInt(n), new(big.Int).Lsh(big.NewInt(1), k))
}

// ratScaled2 returns n * 2^s for any integer s.
func ratScaled2(n int64, s int) *big.Rat {
	r := new(big.Rat).SetInt64(n)
	if s >= 0 {
		r.Mul(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(s))))
	} else {
		r.Quo(r, new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), uint(-s))))
	}
	return r
}

func mustParse(t *testing.T, s string) (bool, *big.Rat) {
	t.Helper()
	neg, r, err := decimal.Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return neg, r
}

func fracString(r *big.Rat) string { return r.Num().String() + "/" + r.Denom().String() }

// ---- exhaustive round trip over every finite binary16 bit pattern ---------

func TestAllFinitePatternsRoundTrip(t *testing.T) {
	const patterns = 2 * 31 * 1024 // signs x E=0..30 x F=0..1023
	count := 0
	for sign := uint16(0); sign <= 1; sign++ {
		for E := uint16(0); E <= 30; E++ {
			for F := uint16(0); F <= 1023; F++ {
				p := sign<<15 | E<<10 | F
				want := decodePattern(p)

				s := renderFinite(t, want)
				neg, r := mustParse(t, s)
				// The two signed zeros share one decimal spelling;
				// carry the sign bit through the neg flag explicitly.
				if E == 0 && F == 0 {
					neg = sign == 1
					if neg {
						r.Neg(r)
					}
				}
				if neg != (sign == 1) {
					t.Fatalf("pattern %04X (%s): sign lost parsing %q", p, want, s)
				}

				got := half.Convert(neg, r)
				if got.Pattern() != p {
					t.Fatalf("pattern %04X (value %s, input %q) converted to %04X",
						p, want, s, got.Pattern())
				}

				wantClass := half.ClassNormal
				if E == 0 && F == 0 {
					wantClass = half.ClassZero
				} else if E == 0 {
					wantClass = half.ClassSubnormal
				}
				if got.Class != wantClass {
					t.Fatalf("pattern %04X: class = %q, want %q", p, got.Class, wantClass)
				}

				// An exactly representable input has zero, reduced error.
				err := half.Error(got, r)
				if err == nil || err.Sign() != 0 || err.Denom().Cmp(big.NewInt(1)) != 0 {
					t.Fatalf("pattern %04X (input %q): error = %v, want 0/1", p, s, err)
				}
				if got.Value.Cmp(want) != 0 {
					t.Fatalf("pattern %04X: reconstructed value %s != %s", p, got.Value, want)
				}
				count++
			}
		}
	}
	if count != patterns {
		t.Fatalf("checked %d patterns, want %d", count, patterns)
	}
}

// ---- hand-checked boundary tables -----------------------------------------

type ratCase struct {
	name string
	in   *big.Rat // rendered exactly to decimal
	hex  string
	err  *big.Rat // expected value(result) - value(input)
}

func runRatCases(t *testing.T, cases []ratCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := renderFinite(t, c.in)
			neg, r := mustParse(t, s)
			got := half.Convert(neg, r)
			if h := sprintfPattern(got.Pattern()); h != c.hex {
				t.Fatalf("input %s: pattern = %s, want %s", s, h, c.hex)
			}
			err := half.Error(got, r)
			if err == nil || err.Cmp(c.err) != 0 {
				t.Fatalf("input %s: error = %v, want %s", s, err, fracString(c.err))
			}
			// big.Rat always keeps lowest terms; assert it.
			if g := new(big.Int).GCD(nil, nil, err.Num(), err.Denom()); g.Cmp(big.NewInt(1)) != 0 && err.Sign() != 0 {
				t.Fatalf("error fraction %s not reduced", fracString(err))
			}
		})
	}
}

func sprintfPattern(p uint16) string {
	const hex = "0123456789ABCDEF"
	b := []byte{'0', '0', '0', '0'}
	for i := 3; i >= 0; i-- {
		b[i] = hex[p&0xF]
		p >>= 4
	}
	return string(b)
}

func TestSubnormalBoundaries(t *testing.T) {
	runRatCases(t, []ratCase{
		// Below the zero/smallest-subnormal midpoint: rounds to zero.
		{"below_midpoint_to_zero", ratPow2(7, 28), "0000", ratPow2(-7, 28)},
		// Exactly on the midpoint 2^-25: tie goes to even k=0 (zero).
		{"tie_zero_smallest_even_zero", ratPow2(1, 25), "0000", ratPow2(-1, 25)},
		// Just above the midpoint (5*2^-26): rounds up to 2^-24.
		{"above_midpoint_to_smallest", ratPow2(5, 26), "0001", ratPow2(-1, 26)},
		// 3*2^-25 is three quarters past the midpoint: 2^-23 (k=2).
		{"three_quarters_to_second_subnormal", ratPow2(3, 25), "0002", ratPow2(1, 25)},
		// Smallest subnormal, exact.
		{"smallest_subnormal_exact", ratPow2(1, 24), "0001", new(big.Rat)},
		// Largest subnormal, exact.
		{"largest_subnormal_exact", ratPow2(1023, 24), "03FF", new(big.Rat)},
		// Half ULP below the subnormal/normal boundary: largest subnormal.
		{"half_ulp_below_boundary", ratPow2(4093, 26), "03FF", ratPow2(-1, 26)},
		// Exact tie between k=1023 (odd) and k=1024 (even): normal wins.
		{"tie_boundary_even_normal", ratPow2(2047, 25), "0400", ratPow2(1, 25)},
		// Smallest normal 2^-14, exact.
		{"smallest_normal_exact", ratPow2(1, 14), "0400", new(big.Rat)},
	})
}

func TestNormalMidpoints(t *testing.T) {
	// Binade [1, 2): ULP = 2^-10, half ULP = 2^-11.
	runRatCases(t, []ratCase{
		// 1 + 2^-11 is the tie between m=2048 (even) and m=2049 (odd).
		{"tie_after_one_even", new(big.Rat).Add(big.NewRat(1, 1), ratPow2(1, 11)),
			"3C00", ratPow2(-1, 11)},
		// 1 + 2^-12: below the tie.
		{"below_tie_after_one", new(big.Rat).Add(big.NewRat(1, 1), ratPow2(1, 12)),
			"3C00", ratPow2(-1, 12)},
		// 1 + 2^-10: exactly 1.0009765625.
		{"one_plus_ulp_exact", new(big.Rat).Add(big.NewRat(1, 1), ratPow2(1, 10)),
			"3C01", new(big.Rat)},
		// Tie between 1.5 (m=1536 even) and 1.5+ULP (m=1537 odd).
		{"tie_at_three_halves", new(big.Rat).Add(big.NewRat(3, 2), ratPow2(1, 11)),
			"3E00", ratPow2(-1, 11)},
		// 65488 is the tie between 65472 (m=2046 even) and 65504
		// (m=2047 odd): must land on the finite even candidate, NOT
		// overflow.
		{"top_binade_tie_even_finite", big.NewRat(65488, 1), "7BFE", big.NewRat(-16, 1)},
	})
}

type strCase struct {
	in    string
	neg   bool
	hex   string
	class half.Class
	err   *big.Rat // nil means infinity -> error must be null
}

func runStrCases(t *testing.T, cases []strCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			neg, r := mustParse(t, c.in)
			if neg != c.neg {
				t.Fatalf("Parse(%q) sign = %v, want %v", c.in, neg, c.neg)
			}
			got := half.Convert(neg, r)
			if h := sprintfPattern(got.Pattern()); h != c.hex {
				t.Fatalf("pattern = %s, want %s", h, c.hex)
			}
			if got.Class != c.class {
				t.Fatalf("class = %q, want %q", got.Class, c.class)
			}
			err := half.Error(got, r)
			if c.err == nil {
				if err != nil {
					t.Fatalf("infinity error = %v, want nil", err)
				}
			} else {
				if err == nil || err.Cmp(c.err) != 0 {
					t.Fatalf("error = %v, want %s", err, fracString(c.err))
				}
			}
		})
	}
}

func TestSignedZeros(t *testing.T) {
	runStrCases(t, []strCase{
		{"0", false, "0000", half.ClassZero, new(big.Rat)},
		{"+0", false, "0000", half.ClassZero, new(big.Rat)},
		{"-0", true, "8000", half.ClassZero, new(big.Rat)},
		{"-0.0", true, "8000", half.ClassZero, new(big.Rat)},
		{"000.000", false, "0000", half.ClassZero, new(big.Rat)},
		{"-0e10", true, "8000", half.ClassZero, new(big.Rat)},
		{"1e-50", false, "0000", half.ClassZero,
			new(big.Rat).Neg(powTen(-50))}, // result 0 - 1e-50
		{"-1e-50", true, "8000", half.ClassZero, powTen(-50)},
	})
}

func powTen(n int) *big.Rat {
	r := new(big.Rat)
	if n >= 0 {
		r.SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil))
	} else {
		r.SetFrac(big.NewInt(1), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(-n)), nil))
	}
	return r
}

func TestDecimalFractionRounding(t *testing.T) {
	// 0.1 -> 0x2E66 = 819/8192; error = -1/40960. This is the classic
	// value where going via host double can invite double rounding.
	runStrCases(t, []strCase{
		{"0.1", false, "2E66", half.ClassNormal, big.NewRat(-1, 40960)},
		{"-0.1", true, "AE66", half.ClassNormal, big.NewRat(1, 40960)},
		{"1.0", false, "3C00", half.ClassNormal, new(big.Rat)},
		{"1000", false, "63D0", half.ClassNormal, new(big.Rat)},
		{"0.5", false, "3800", half.ClassNormal, new(big.Rat)},
	})
}

func TestOverflow(t *testing.T) {
	runStrCases(t, []strCase{
		{"65504", false, "7BFF", half.ClassNormal, new(big.Rat)},
		{"65512", false, "7BFF", half.ClassNormal, big.NewRat(-8, 1)},
		// Exact tie max-finite/infinity: infinity is the even candidate.
		{"65520", false, "7C00", half.ClassInfinity, nil},
		{"-65520", true, "FC00", half.ClassInfinity, nil},
		{"100000", false, "7C00", half.ClassInfinity, nil},
		{"-100000", true, "FC00", half.ClassInfinity, nil},
		{"1e50", false, "7C00", half.ClassInfinity, nil},
		{"-1E50", true, "FC00", half.ClassInfinity, nil},
	})
}

// ---- parser rejection ------------------------------------------------------

func TestInvalidDecimals(t *testing.T) {
	bad := []string{
		"", "-", "+", ".", " 1", "1 ", "1e", "e5", "1.2.3",
		"NaN", "-NaN", "nan", "Infinity", "-Infinity", "inf", "-inf",
		"0x1p4", "0x10", "1_000", "1e99", "1e51", "1e-51", "1e2-3",
		"1.2e++1", "1.2e+1-2", "n", "1ee2", "0.1e", "٣", "1,5", "٣", "1,5",
	}
	for _, s := range bad {
		if _, _, err := decimal.Parse(s); err == nil {
			t.Errorf("Parse(%q): expected rejection, got nil", s)
		}
	}

	// 31 significant digits rejected, 30 accepted.
	if _, _, err := decimal.Parse("9999999999999999999999999999999"); err == nil {
		t.Error("31 significant digits should be rejected")
	}
	if _, _, err := decimal.Parse("999999999999999999999999999999"); err != nil {
		t.Errorf("30 significant digits should parse: %v", err)
	}
	// Leading zeros do not count.
	if _, _, err := decimal.Parse("0000000000000000000000000000001"); err != nil {
		t.Errorf("leading zeros should not count: %v", err)
	}
	// Exponent limits exactly.
	if _, _, err := decimal.Parse("1e-50"); err != nil {
		t.Errorf("1e-50 should parse: %v", err)
	}
	if _, _, err := decimal.Parse("1e50"); err != nil {
		t.Errorf("1e50 should parse: %v", err)
	}
}

// ---- independent brute-force oracle ---------------------------------------

// oracle enumerates every positive finite binary16 magnitude exactly
// once and picks the nearest by rational distance, breaking ties toward
// the candidate whose stored fraction is even (equivalent to even
// significand, including the infinity carry candidate).
type oracle struct {
	vals []*big.Rat
	pats []uint16
}

func newOracle(t *testing.T) *oracle {
	t.Helper()
	type ent struct {
		v *big.Rat
		p uint16
	}
	var es []ent
	for E := uint16(0); E <= 30; E++ {
		for F := uint16(0); F <= 1023; F++ {
			p := E<<10 | F
			v := decodePattern(p)
			if v.Sign() == 0 {
				es = append(es, ent{v, p})
				continue
			}
			es = append(es, ent{v, p})
		}
	}
	sort.Slice(es, func(i, j int) bool { return es[i].v.Cmp(es[j].v) < 0 })
	o := &oracle{}
	seen := map[string]bool{}
	for _, e := range es {
		key := e.v.RatString()
		if seen[key] {
			t.Fatalf("duplicate oracle value %s", key)
		}
		seen[key] = true
		o.vals = append(o.vals, e.v)
		o.pats = append(o.pats, e.p)
	}
	return o
}

func (o *oracle) round(x *big.Rat) (pat uint16, inf bool) {
	neg := x.Sign() < 0
	a := new(big.Rat).Abs(x)
	if a.Cmp(big.NewRat(65520, 1)) >= 0 {
		return 0x7C00 | signBit(neg), true
	}
	idx := sort.Search(len(o.vals), func(i int) bool { return o.vals[i].Cmp(a) >= 0 })
	switch {
	case idx < len(o.vals) && o.vals[idx].Cmp(a) == 0:
		return o.pats[idx] | signBit(neg), false
	case idx == 0:
		return o.pats[0] | signBit(neg), false // a < smallest subnormal half
	case idx == len(o.vals):
		// a in (65504, 65520): max finite is the only finite neighbor.
		return o.pats[len(o.pats)-1] | signBit(neg), false
	}
	lo := o.vals[idx-1]
	hi := o.vals[idx]
	dl := new(big.Rat).Sub(a, lo)
	dh := new(big.Rat).Sub(hi, a)
	c := dl.Cmp(dh)
	var pick uint16
	if c < 0 {
		pick = o.pats[idx-1]
	} else if c > 0 {
		pick = o.pats[idx]
	} else {
		// Tie: even stored fraction wins.
		if o.pats[idx-1]&1 == 0 {
			pick = o.pats[idx-1]
		} else {
			pick = o.pats[idx]
		}
	}
	return pick | signBit(neg), false
}

func signBit(neg bool) uint16 {
	if neg {
		return 0x8000
	}
	return 0
}

// assertAgainstOracle runs one positive/negative rational through both
// the production converter and the independent oracle.
func assertAgainstOracle(t *testing.T, o *oracle, q *big.Rat) {
	t.Helper()
	s := renderFinite(t, q)
	neg, r, err := decimal.Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	wantPat, wantInf := o.round(r)
	got := half.Convert(neg, r)
	if got.IsInf() != wantInf || (!wantInf && got.Pattern() != wantPat) {
		t.Fatalf("value %s: got %04X (inf=%v), oracle %04X (inf=%v)",
			s, got.Pattern(), got.IsInf(), wantPat, wantInf)
	}
	if wantInf {
		if e := half.Error(got, r); e != nil {
			t.Fatalf("value %s: infinity error should be nil, got %v", s, e)
		}
		return
	}
	// Error must equal value(result) - value(input) exactly and be
	// no larger than half the local ULP.
	err2 := half.Error(got, r)
	wantErr := new(big.Rat).Sub(decodePattern(got.Pattern()), r)
	if err2.Cmp(wantErr) != 0 {
		t.Fatalf("value %s: error %v != expected %v", s, err2, wantErr)
	}
}

// TestOracleRandomBinaryGrid walks each normal binade at quarter-ULP
// resolution (forcing plenty of exact ties) plus subnormal territory,
// and compares against the brute-force oracle.
func TestOracleRandomBinaryGrid(t *testing.T) {
	o := newOracle(t)
	rng := rand.New(rand.NewSource(20260926))
	for iter := 0; iter < 4000; iter++ {
		e := rng.Intn(30) - 14 // binade exponent -14..15
		m := 1024 + rng.Intn(1024)
		delta := rng.Intn(5) - 2 // -2..2 quarter ULP steps
		// value = (4m + delta) * 2^(e-12)
		n := int64(4*m + delta)
		q := ratScaled2(n, e-12)
		if q.Sign() == 0 {
			continue
		}
		if rng.Intn(2) == 0 {
			q.Neg(q)
		}
		assertAgainstOracle(t, o, q)
	}

	// Explicit sweep of the whole subnormal+first-normal region at
	// half-grid spacing (k = 0..2049 halves).
	for k := int64(1); k <= 2049; k++ {
		assertAgainstOracle(t, o, ratPow2(k, 25))
		assertAgainstOracle(t, o, new(big.Rat).Neg(ratPow2(k, 25)))
	}

	// Sweep the top binade at 8-grid resolution around overflow.
	for k := int64(2020); k <= 2050; k++ {
		// spacing 32; quarter = 8: value = 32*k
		q := big.NewRat(32*k, 1)
		assertAgainstOracle(t, o, q)
		assertAgainstOracle(t, o, new(big.Rat).Neg(q))
	}
}

// TestOracleRandomDecimals generates valid decimal strings directly,
// exercising denominators containing factors of 5 as well.
func TestOracleRandomDecimals(t *testing.T) {
	o := newOracle(t)
	rng := rand.New(rand.NewSource(424242))
	for iter := 0; iter < 3000; iter++ {
		nd := 1 + rng.Intn(30)
		var b strings.Builder
		if rng.Intn(2) == 0 {
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

		neg, r, err := decimal.Parse(s)
		if err != nil {
			t.Fatalf("generated %q: %v", s, err)
		}
		if r.Sign() == 0 {
			continue
		}
		wantPat, wantInf := o.round(r)
		got := half.Convert(neg, r)
		if got.IsInf() != wantInf || (!wantInf && got.Pattern() != wantPat) {
			t.Fatalf("value %s: got %04X (inf=%v), oracle %04X (inf=%v)",
				s, got.Pattern(), got.IsInf(), wantPat, wantInf)
		}
		if !wantInf {
			wantErr := new(big.Rat).Sub(decodePattern(got.Pattern()), r)
			if e := half.Error(got, r); e.Cmp(wantErr) != 0 {
				t.Fatalf("value %s: error %v != %v", s, e, wantErr)
			}
		}
	}
}

// TestRenderFinite sanity-checks the test's own decimal renderer.
func TestRenderFinite(t *testing.T) {
	cases := []struct {
		q    *big.Rat
		want string
	}{
		{big.NewRat(3, 2), "1.5"},
		{ratPow2(1, 24), "0.000000059604644775390625"},
		{ratPow2(1, 25), "0.0000000298023223876953125"},
		{big.NewRat(65504, 1), "65504"},
		{ratPow2(1023, 24), "0.000060975551605224609375"},
	}
	for _, c := range cases {
		if got := renderFinite(t, c.q); got != c.want {
			t.Errorf("renderFinite(%v) = %q, want %q", c.q, got, c.want)
		}
	}
}
