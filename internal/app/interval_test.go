package app_test

// Tests for the optional exact interval certificate. When disabled the
// response shape must be unchanged; when enabled each successful item
// carries reduced rational bounds whose independent adjudication
// agrees with the produced hex/class/rounding_error.

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"halfconv/internal/app"
	"halfconv/internal/decimal"
)

// parseFrac parses a "p/q" JSON string value back into a *big.Rat.
func parseFrac(t *testing.T, raw json.RawMessage) *big.Rat {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("fraction bound is not a string: %s", string(raw))
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("cannot parse fraction %q", s)
	}
	return r
}

func TestIntervalsAbsentByDefault(t *testing.T) {
	body := `{"values": ["0.1", "-0", "65520", "NaN"]}`
	var out bytes.Buffer
	code := app.Execute(strings.NewReader(body), &out)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, out.String())
	}
	if bytes.Contains(out.Bytes(), []byte("interval")) {
		t.Fatalf("default output must not mention intervals:\n%s", out.String())
	}
	// And the parsed structure confirms it.
	resp := decodeResponse(t, &out)
	for _, it := range resp.Result {
		if it.Interval != nil {
			t.Fatalf("item %d carries an interval by default", it.Index)
		}
	}
}

func TestIntervalsEnabledInRequest(t *testing.T) {
	body := `{"with_intervals": true, "values": ["0.1", "-0", "65520", "0", "0.000060975551605224609375"]}`
	resp, code := execute(t, body)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, it := range resp.Result {
		if !it.OK {
			continue
		}
		if it.Interval == nil {
			t.Fatalf("item %d (%s): interval missing", it.Index, it.Input)
		}
	}

	// +0.1 -> 0x2E66 = 819/8192 = 1638/16384, in binade [1/16,1/8)
	// with ULP 2^-14: interval midpoints are 3275/32768 and 3277/32768.
	tenth := resp.Result[0]
	if *tenth.Hex != "2E66" || tenth.Interval == nil {
		t.Fatalf("0.1 item: %+v", tenth)
	}
	if lo := parseFrac(t, tenth.Interval.Low); lo.Cmp(big.NewRat(3275, 32768)) != 0 {
		t.Fatalf("0.1 low = %v, want 3275/32768", lo)
	}
	if hi := parseFrac(t, tenth.Interval.High); hi.Cmp(big.NewRat(3277, 32768)) != 0 {
		t.Fatalf("0.1 high = %v, want 3277/32768", hi)
	}
	// F = 0x266 = 614 even: both endpoints included.
	if !tenth.Interval.LowInclusive || !tenth.Interval.HighInclusive {
		t.Fatalf("0.1 endpoints must both be included: %+v", tenth.Interval)
	}

	// -0: [-2^-25, 0], both included.
	negZero := resp.Result[1]
	if *negZero.Hex != "8000" {
		t.Fatalf("-0 hex = %s", *negZero.Hex)
	}
	if lo := parseFrac(t, negZero.Interval.Low); lo.Cmp(big.NewRat(-1, 1<<25)) != 0 {
		t.Fatalf("-0 low = %v, want -1/2^25", lo)
	}
	if string(negZero.Interval.High) != `"0/1"` {
		t.Fatalf("-0 high = %s, want 0/1", negZero.Interval.High)
	}

	// +infinity: low = 65520/1 included, high null tail.
	inf := resp.Result[2]
	if *inf.Class != "infinity" || string(inf.RoundingError) != "null" {
		t.Fatalf("inf item: %+v", inf)
	}
	if lo := parseFrac(t, inf.Interval.Low); lo.Cmp(big.NewRat(65520, 1)) != 0 {
		t.Fatalf("inf low = %v, want 65520", lo)
	}
	// The outer tail side is open (HighInclusive=false is meaningless
	// for a null bound but kept false); the tie side is included.
	if !inf.Interval.LowInclusive || string(inf.Interval.High) != "null" || inf.Interval.HighInclusive {
		t.Fatalf("inf interval flags wrong: %+v", inf.Interval)
	}

	// +0: [0, 2^-25].
	posZero := resp.Result[3]
	if string(posZero.Interval.Low) != `"0/1"` {
		t.Fatalf("+0 low = %s", posZero.Interval.Low)
	}
	if hi := parseFrac(t, posZero.Interval.High); hi.Cmp(big.NewRat(1, 1<<25)) != 0 {
		t.Fatalf("+0 high = %v, want 1/2^25", hi)
	}

	// Largest subnormal 0x03FF: F odd -> both endpoints excluded;
	// upper bound is the subnormal/normal tie 2047/2 * 2^-24.
	ls := resp.Result[4]
	if *ls.Hex != "03FF" {
		t.Fatalf("largest subnormal hex = %s", *ls.Hex)
	}
	if ls.Interval.LowInclusive || ls.Interval.HighInclusive {
		t.Fatalf("F=1023 is odd: both ties must be excluded: %+v", ls.Interval)
	}
	if hi := parseFrac(t, ls.Interval.High); hi.Cmp(big.NewRat(2047, 1<<25)) != 0 {
		t.Fatalf("largest subnormal high = %v, want 2047/2^25", hi)
	}
}

// TestIntervalExplicitFlag exercises ExecuteWith and the bare-array
// envelope, independently of the request field.
func TestIntervalExplicitFlag(t *testing.T) {
	var off, on bytes.Buffer
	body := `["1"]`
	if code := app.ExecuteWith(strings.NewReader(body), &off, false); code != 0 {
		t.Fatalf("default code=%d", code)
	}
	if code := app.ExecuteWith(strings.NewReader(body), &on, true); code != 0 {
		t.Fatalf("flagged code=%d", code)
	}
	if bytes.Contains(off.Bytes(), []byte("interval")) {
		t.Fatalf("ExecuteWith(...,false) leaked intervals")
	}
	r := decodeResponse(t, &on)
	it := r.Result[0]
	if *it.Hex != "3C00" || it.Interval == nil {
		t.Fatalf("flagged conversion: %+v", it)
	}
	// 1.0: F=0 even so it wins both ties. Across the binade boundary
	// the neighbour spacings differ: predecessor 1-2^-11 gives the
	// lower tie 1-2^-12; successor 1+2^-10 gives the upper tie 1+2^-11.
	if lo := parseFrac(t, it.Interval.Low); lo.Cmp(big.NewRat(4095, 4096)) != 0 {
		t.Fatalf("1.0 low = %v, want 4095/4096", lo)
	}
	if hi := parseFrac(t, it.Interval.High); hi.Cmp(big.NewRat(2049, 2048)) != 0 {
		t.Fatalf("1.0 high = %v, want 2049/2048", hi)
	}
	if !it.Interval.LowInclusive || !it.Interval.HighInclusive {
		t.Fatalf("1.0 endpoints should be included")
	}
}

// TestIntervalAdjudicatesOtherDecimals is the certificate's core
// promise: parsed independently from the wire JSON, the rational
// interval decides whether another legal decimal encodes to the same
// pattern — and the answer matches hex/class/rounding_error exactly.
func TestIntervalAdjudicatesOtherDecimals(t *testing.T) {
	body := `{"with_intervals": true, "values": ["0.1", "0.000060975551605224609375", "65504", "-65520"]}`
	resp, code := execute(t, body)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}

	// wireMember decides membership directly from IntervalJSON using
	// only big.Rat (independent of the half package).
	wireMember := func(iv *app.IntervalJSON, neg bool, x *big.Rat) bool {
		cmp := func(raw json.RawMessage, x *big.Rat) int {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatalf("bound not string: %s", raw)
			}
			b, ok := new(big.Rat).SetString(s)
			if !ok {
				t.Fatalf("bad fraction %q", s)
			}
			return x.Cmp(b)
		}
		if x.Sign() == 0 {
			bound := iv.Low
			if neg {
				bound = iv.High
			}
			var s string
			_ = json.Unmarshal(bound, &s)
			return s == "0/1"
		}
		if string(iv.Low) != "null" {
			switch c := cmp(iv.Low, x); {
			case c < 0:
				return false
			case c == 0 && !iv.LowInclusive:
				return false
			}
		}
		if string(iv.High) != "null" {
			switch c := cmp(iv.High, x); {
			case c > 0:
				return false
			case c == 0 && !iv.HighInclusive:
				return false
			}
		}
		return true
	}

	cases := []struct {
		item    int
		probe   string
		wantHex string
		member  bool
	}{
		// 0.1 -> 2E66. The exact ties 3275/32768 and 3277/32768 both
		// resolve to the even F=614 (2E66); probes just beyond them
		// fall to the neighbouring patterns.
		{0, "0.099853515625", "2E64", false},     // previous grid point
		{0, "0.099945068359375", "2E66", true},   // exact lower tie
		{0, "0.100006103515625", "2E66", true},   // exact upper tie
		{0, "0.1000213623046875", "2E67", false}, // one eighth-ULP past
		{0, "0.1", "2E66", true},
		{0, "0.0999298095703125", "2E65", false}, // below the lower tie
		// Largest subnormal 0x03FF: exact upper tie 2047/2^25 is won by
		// the even smallest normal 0x0400, not by the odd F=1023.
		{1, "0.000060975551605224609375", "03FF", true},
		{1, "0.0000610053539276123046875", "0400", false},
		{1, "0.00006103515625", "0400", false}, // exactly 2^-14
		// 65504 max finite; 65520 tie goes to the even infinity.
		{2, "65504", "7BFF", true},
		{2, "65512", "7BFF", true},
		{2, "65520", "7C00", false},
		{2, "65521", "7C00", false},
		// -infinity tail (-inf, -65520] includes -65520/-100000 and
		// rejects -65519 (which rounds to the negative max finite).
		{3, "-65520", "FC00", true},
		{3, "-100000", "FC00", true},
		{3, "-65519", "FBFF", false},
	}
	for _, c := range cases {
		neg, r, err := decimal.Parse(c.probe)
		if err != nil {
			t.Fatalf("probe %q: %v", c.probe, err)
		}
		iv := resp.Result[c.item].Interval
		got := wireMember(iv, neg, r)
		if got != c.member {
			t.Fatalf("certificate of item %d on %q: membership %v, want %v",
				c.item, c.probe, got, c.member)
		}
		// Cross-check against an actual conversion: convert the probe
		// in its own batch and compare hex, class semantics.
		presp, pcode := execute(t, `{"values":["`+c.probe+`"]}`)
		if pcode != 0 {
			t.Fatalf("probe batch code %d", pcode)
		}
		pit := presp.Result[0]
		if !pit.OK || *pit.Hex != c.wantHex {
			t.Fatalf("probe %q converts to %+v, want hex %s", c.probe, pit, c.wantHex)
		}
		// Membership must be exactly "same pattern as the item".
		same := *pit.Hex == *resp.Result[c.item].Hex
		if same != c.member {
			t.Fatalf("probe %q: same-pattern=%v but certificate membership=%v",
				c.probe, same, c.member)
		}
	}
}

// TestIntervalsMixedBatch keeps per-item isolation: rejected entries
// carry no certificate, valid ones do.
func TestIntervalsMixedBatch(t *testing.T) {
	body := `{"with_intervals": true, "values": ["Infinity", "1e-50", "1e51", "", "1", "-0"]}`
	resp, code := execute(t, body)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(resp.Result) != 6 {
		t.Fatalf("count=%d", len(resp.Result))
	}
	for i, it := range resp.Result {
		switch i {
		case 0, 2, 3:
			if it.OK || it.Interval != nil || it.Hex != nil {
				t.Fatalf("item %d should be rejected with no certificate: %+v", i, it)
			}
		default:
			if !it.OK || it.Interval == nil {
				t.Fatalf("item %d should carry a certificate: %+v", i, it)
			}
		}
	}
	// 1e-50 -> +0 with error -1e-50; interval still the +0 one and
	// the error string stays consistent.
	tiny := resp.Result[1]
	if *tiny.Hex != "0000" || string(tiny.Interval.High) != `"1/33554432"` {
		t.Fatalf("1e-50 item: %+v", tiny)
	}
	if !strings.Contains(string(tiny.RoundingError),
		"-1/100000000000000000000000000000000000000000000000000") {
		t.Fatalf("1e-50 rounding error changed: %s", tiny.RoundingError)
	}
}
