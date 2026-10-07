package app_test

// End-to-end tests for calibration mode. Enabled by an optional
// "targets" array of four-hex-digit binary16 patterns, the whole batch
// becomes one exact common-bias problem: any invalid original, target
// or mismatched length rejects the entire request, while ordinary
// batches keep their per-item behavior and fields.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"halfconv/internal/app"
)

func executeBody(t *testing.T, body string) (app.Response, []byte, int) {
	t.Helper()
	var out bytes.Buffer
	code := app.Execute(strings.NewReader(body), &out)
	var resp app.Response
	if code == 0 {
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("response not JSON (%v): %s", err, out.String())
		}
	}
	return resp, out.Bytes(), code
}

func rawFrac(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("expected JSON fraction string, got %s", raw)
	}
	return s
}

// TestCalibrationAttainedPoint: ["0","-0"] -> [+0,-0] has the single
// common bias 0; addition is exactly zero so 0 keeps -0 only for the
// literal "-0".
func TestCalibrationAttainedPoint(t *testing.T) {
	body := `{"values": ["0", "-0"], "targets": ["0000", "8000"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	if resp.Result != nil {
		t.Fatalf("calibration mode must not emit per-item results: %s", raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible || cal.Attained == nil || !*cal.Attained {
		t.Fatalf("expected attained feasible calibration: %s", raw)
	}
	if got := rawFrac(t, cal.Bias); got != "0/1" {
		t.Fatalf("bias = %s, want 0/1", got)
	}
	if cal.Infimum != nil || cal.Witness != nil || cal.WitnessRecomputed != nil {
		t.Fatalf("attained result must not carry infimum/witness fields: %s", raw)
	}
	if cal.Interval == nil ||
		rawFrac(t, cal.Interval.Low) != "0/1" || rawFrac(t, cal.Interval.High) != "0/1" ||
		!cal.Interval.LowInclusive || !cal.Interval.HighInclusive {
		t.Fatalf("interval should be the closed point [0,0]: %+v", cal.Interval)
	}
	want := []app.CalibResultJSON{{Index: 0, Hex: "0000", Class: "zero"}, {Index: 1, Hex: "8000", Class: "zero"}}
	if len(cal.Recomputed) != 2 || cal.Recomputed[0].Hex != "0000" || cal.Recomputed[1].Hex != "8000" {
		t.Fatalf("recomputed = %+v, want %+v", cal.Recomputed, want)
	}
}

// TestCalibrationUnattainedEndpoints: ["0","0"] -> [0001,0001]: the
// smallest-subnormal preimage is open at the 2^-25 tie (F=1 odd), so
// the infimum 2^-25 is not itself feasible; a strictly interior bias
// (midpoint 2^-24) must be reported with its recomputation.
func TestCalibrationUnattainedEndpoints(t *testing.T) {
	body := `{"values": ["0", "0"], "targets": ["0001", "0001"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible {
		t.Fatalf("expected feasible: %s", raw)
	}
	if cal.Attained == nil || *cal.Attained {
		t.Fatalf("minimum must be unattained (excluded tie): %s", raw)
	}
	if cal.Bias != nil || cal.Recomputed != nil {
		t.Fatalf("unattained result must not claim a feasible bias: %s", raw)
	}
	if got := rawFrac(t, cal.Infimum); got != "1/33554432" {
		t.Fatalf("infimum = %s, want 1/33554432 (2^-25)", got)
	}
	if got := rawFrac(t, cal.Witness); got != "1/16777216" {
		t.Fatalf("witness = %s, want 1/16777216 (2^-24)", got)
	}
	if len(cal.WitnessRecomputed) != 2 {
		t.Fatalf("witness_recomputed = %+v", cal.WitnessRecomputed)
	}
	for i, rc := range cal.WitnessRecomputed {
		if rc.Hex != "0001" || rc.Class != "subnormal" || rc.Index != i {
			t.Fatalf("witness recompute[%d] = %+v, want 0001 subnormal", i, rc)
		}
	}
	// Open endpoints survive on the wire.
	if cal.Interval == nil || cal.Interval.LowInclusive || cal.Interval.HighInclusive {
		t.Fatalf("both endpoints must be open: %+v", cal.Interval)
	}
}

// TestCalibrationAttainedTieEven: the minimum feasible bias sits on an
// included tie (even F) and must be reported as attained, never as an
// infimum approximation.
func TestCalibrationAttainedTieEven(t *testing.T) {
	// 1 -> 1.5 (3E00, F=512 even) and 2 -> 2.5 (4100, F=0 even).
	body := `{"values": ["1", "2"], "targets": ["3E00", "4100"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible || cal.Attained == nil || !*cal.Attained {
		t.Fatalf("attained tie expected: %s", raw)
	}
	// Lower tie of 1.5 is 1.5 - 2^-11: bias 1023/2048, included.
	if got := rawFrac(t, cal.Bias); got != "1023/2048" {
		t.Fatalf("bias = %s, want 1023/2048", got)
	}
	if cal.Interval == nil ||
		rawFrac(t, cal.Interval.Low) != "1023/2048" || !cal.Interval.LowInclusive {
		t.Fatalf("low bound should be included even tie: %+v", cal.Interval)
	}
	for i, wantHex := range []string{"3E00", "4100"} {
		if cal.Recomputed[i].Hex != wantHex || cal.Recomputed[i].Class != "normal" {
			t.Fatalf("recompute[%d] = %+v, want %s normal", i, cal.Recomputed[i], wantHex)
		}
	}
}

// TestCalibrationInfinityTail: 70000 -> +inf accepts biases from the
// included overflow tie 65520-70000 = -4480 onward; high side is the
// infinite tail (null).
func TestCalibrationInfinityTail(t *testing.T) {
	body := `{"values": ["70000"], "targets": ["7c00"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible || cal.Attained == nil || !*cal.Attained {
		t.Fatalf("feasible attained tail containing bias 0 expected: %s", raw)
	}
	if got := rawFrac(t, cal.Bias); got != "0/1" {
		t.Fatalf("minimum absolute bias = %s, want 0/1 (70000 is already +inf)", got)
	}
	if cal.Interval == nil || rawFrac(t, cal.Interval.Low) != "-4480/1" ||
		!cal.Interval.LowInclusive || string(cal.Interval.High) != "null" {
		t.Fatalf("tail interval = %+v", cal.Interval)
	}
	if cal.Recomputed[0].Hex != "7C00" || cal.Recomputed[0].Class != "infinity" {
		t.Fatalf("recompute = %+v, want 7C00 infinity", cal.Recomputed[0])
	}
}

// TestCalibrationInfeasible: demanding +0 from 0 forces a tiny bias
// while demanding +inf from 1 forces bias >= 65519; the response says
// infeasible and nothing else.
func TestCalibrationInfeasible(t *testing.T) {
	body := `{"values": ["0", "1"], "targets": ["0000", "7C00"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("infeasibility is not a transport error: code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || cal.Feasible {
		t.Fatalf("expected feasible=false: %s", raw)
	}
	if cal.Attained != nil || cal.Bias != nil || cal.Interval != nil ||
		cal.Recomputed != nil || cal.Infimum != nil {
		t.Fatalf("infeasible verdict must carry no suggestion: %s", raw)
	}
	if bytes.Contains(raw, []byte(`"results"`)) {
		t.Fatalf("calibration response must never embed results: %s", raw)
	}
}

// TestCalibrationNegativeZeroRequest: literal "-0" asked to become +0:
// zero bias is excluded (it keeps -0), so the infimum 0 is unattained
// but positive biases exist; asking it to remain -0 attains bias 0.
func TestCalibrationNegativeZeroRequest(t *testing.T) {
	body := `{"values": ["-0"], "targets": ["0000"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible {
		t.Fatalf("positive biases should make this feasible: %s", raw)
	}
	if cal.Attained == nil || *cal.Attained {
		t.Fatalf("zero bias must be excluded for -0 -> +0: %s", raw)
	}
	if got := rawFrac(t, cal.Infimum); got != "0/1" {
		t.Fatalf("infimum = %s, want 0/1", got)
	}
	if got := rawFrac(t, cal.Witness); got != "1/67108864" {
		t.Fatalf("witness = %s, want 1/67108864", got)
	}
	if cal.WitnessRecomputed[0].Hex != "0000" {
		t.Fatalf("witness recompute = %+v", cal.WitnessRecomputed[0])
	}

	body2 := `{"values": ["-0"], "targets": ["8000"]}`
	resp2, raw2, code := executeBody(t, body2)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw2)
	}
	c2 := resp2.Calibration
	if c2 == nil || !c2.Feasible || c2.Attained == nil || !*c2.Attained {
		t.Fatalf("-0 -> -0 must attain bias 0: %s", raw2)
	}
	if got := rawFrac(t, c2.Bias); got != "0/1" || c2.Recomputed[0].Hex != "8000" {
		t.Fatalf("-0 retention: bias=%s recomputed=%+v", got, c2.Recomputed[0])
	}
}

// TestCalibrationCancellationIsPositiveZero: a nonzero original that
// the bias cancels exactly always yields +0, never -0. For 1 -> -0 the
// biases [-1-2^-25, -1] reach the -0 region, but the cancellation bias
// -1 itself is excluded; the minimum-absolute feasible bias is thus an
// unattained infimum.
func TestCalibrationCancellationIsPositiveZero(t *testing.T) {
	negBody := `{"values": ["1"], "targets": ["8000"]}`
	resp, raw, code := executeBody(t, negBody)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible {
		t.Fatalf("small negative sums reaching -0 should be feasible: %s", raw)
	}
	if cal.Attained == nil || *cal.Attained {
		t.Fatalf("cancellation bias -1 must be excluded from the -0 interval: %s", raw)
	}
	if got := rawFrac(t, cal.Infimum); got != "-1/1" {
		t.Fatalf("infimum = %s, want -1/1 (excluded cancellation)", got)
	}
	if cal.Interval == nil ||
		rawFrac(t, cal.Interval.Low) != "-33554433/33554432" || !cal.Interval.LowInclusive ||
		rawFrac(t, cal.Interval.High) != "-1/1" || cal.Interval.HighInclusive {
		t.Fatalf("-0-from-1 interval = %+v", cal.Interval)
	}
	for _, rc := range cal.WitnessRecomputed {
		if rc.Hex != "8000" {
			t.Fatalf("witness recompute = %+v, want 8000", rc)
		}
	}
	// Directly: 1 -> +0 includes the exact cancellation bias -1; the
	// minimum-absolute feasible bias is -1+2^-25 (sum at the tie, which
	// rounds to even +0), also included.
	posBody := `{"values": ["1"], "targets": ["0000"]}`
	resp2, raw2, code := executeBody(t, posBody)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw2)
	}
	c := resp2.Calibration
	if c == nil || !c.Feasible || c.Attained == nil || !*c.Attained {
		t.Fatalf("1 -> +0 should be feasible: %s", raw2)
	}
	if got := rawFrac(t, c.Bias); got != "-33554431/33554432" {
		t.Fatalf("minimum bias = %s, want -33554431/33554432 (-1+2^-25)", got)
	}
	if c.Recomputed[0].Hex != "0000" {
		t.Fatalf("recompute at min bias = %+v, want 0000", c.Recomputed[0])
	}
	if rawFrac(t, c.Interval.Low) != "-1/1" || !c.Interval.LowInclusive {
		t.Fatalf("exact cancellation bias -1 must be included in +0: %+v", c.Interval)
	}
}

// TestCalibrationRejectsWholeBatch: every malformed calibration input
// aborts the entire request with an error envelope and no per-item
// suggestions.
func TestCalibrationRejectsWholeBatch(t *testing.T) {
	bodies := []string{
		`{"values": ["NaN"], "targets": ["0000"]}`,
		`{"values": ["1e51"], "targets": ["0000"]}`,
		`{"values": [""], "targets": ["0000"]}`,
		`{"values": ["x.y"], "targets": ["0000"]}`,
		`{"values": ["1"], "targets": ["7E00"]}`,  // NaN
		`{"values": ["1"], "targets": ["FFFF"]}`,  // NaN
		`{"values": ["1"], "targets": ["0x00"]}`,  // malformed
		`{"values": ["1"], "targets": ["000"]}`,   // too short
		`{"values": ["1"], "targets": ["00000"]}`, // too long
		`{"values": ["1"], "targets": ["-001"]}`,
		`{"values": ["1","2"], "targets": ["0000"]}`, // length mismatch
		`{"values": [], "targets": []}`,              // empty batch
		`{"values": [1], "targets": ["0000"]}`,       // non-string value
		`{"values": ["1"], "targets": [123]}`,        // non-string target
	}
	for _, b := range bodies {
		var out bytes.Buffer
		code := app.Execute(strings.NewReader(b), &out)
		if code == 0 {
			t.Errorf("body %s: expected rejection, got %s", b, out.String())
			continue
		}
		if !bytes.Contains(out.Bytes(), []byte(`"ok":false`)) {
			t.Errorf("body %s: expected error envelope, got %s", b, out.String())
		}
		if bytes.Contains(out.Bytes(), []byte(`"results"`)) {
			t.Errorf("body %s: rejected calibration must not emit results: %s", b, out.String())
		}
	}
}

// TestCalibrationErrorMessagesIndexTheBadField: the offending side and
// index are identified so the auditor can fix the request.
func TestCalibrationErrorMessagesIndexTheBadField(t *testing.T) {
	cases := []struct{ body, frag string }{
		{`{"values": ["1", "x"], "targets": ["0000", "0000"]}`, "values[1]"},
		{`{"values": ["1", "2"], "targets": ["0000", "zzzz"]}`, "targets[1]"},
		{`{"values": ["1", "2"], "targets": ["0000"]}`, "same length"},
	}
	for _, c := range cases {
		var out bytes.Buffer
		code := app.Execute(strings.NewReader(c.body), &out)
		if code == 0 || !bytes.Contains(out.Bytes(), []byte(c.frag)) {
			t.Fatalf("body %s: code=%d, want message containing %q, got %s",
				c.body, code, c.frag, out.String())
		}
	}
}

// TestOrdinaryModeUnchanged: targets absent => per-item behavior with
// invalid entries handled individually, and no calibration block.
func TestOrdinaryModeUnchanged(t *testing.T) {
	body := `{"values": ["1", "NaN", "0.1"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if resp.Calibration != nil {
		t.Fatalf("ordinary batch must not carry calibration: %s", raw)
	}
	if len(resp.Result) != 3 {
		t.Fatalf("results = %d, want 3", len(resp.Result))
	}
	if resp.Result[1].OK {
		t.Fatalf("NaN must still be a per-item rejection in ordinary mode")
	}
	if bytes.Contains(raw, []byte(`"calibration"`)) {
		t.Fatalf("output must be unchanged: %s", raw)
	}
}

// TestCalibrationBareArrayUnchanged: the array envelope cannot carry
// targets and stays ordinary.
func TestCalibrationBareArrayUnchanged(t *testing.T) {
	resp, raw, code := executeBody(t, `["1","2"]`)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	if resp.Calibration != nil || len(resp.Result) != 2 {
		t.Fatalf("bare array must stay ordinary: %s", raw)
	}
}

// TestCalibrationTwoSidedTails: +inf and -inf demands around huge
// magnitudes intersect in a closed bounded interval containing 0.
func TestCalibrationTwoSidedTails(t *testing.T) {
	body := `{"values": ["70000", "-70000"], "targets": ["7C00", "FC00"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible || cal.Attained == nil || !*cal.Attained {
		t.Fatalf("two-sided tails should attain bias 0: %s", raw)
	}
	if got := rawFrac(t, cal.Bias); got != "0/1" {
		t.Fatalf("bias = %s, want 0/1", got)
	}
	if cal.Interval == nil ||
		rawFrac(t, cal.Interval.Low) != "-4480/1" || !cal.Interval.LowInclusive ||
		rawFrac(t, cal.Interval.High) != "4480/1" || !cal.Interval.HighInclusive {
		t.Fatalf("interval = %+v, want [-4480,4480] closed", cal.Interval)
	}
	if cal.Recomputed[0].Hex != "7C00" || cal.Recomputed[1].Hex != "FC00" {
		t.Fatalf("recomputed = %+v", cal.Recomputed)
	}
}

// TestCalibrationSubnormalNormalJoin covers the 2^-14 boundary where
// the tie is won by the even smallest normal. A batch straddling the
// join (one item targeting the normal, a zero original targeting +0)
// intersects at bias 0: the tie is included for the even normal and
// the 2^-25 tie is included by even zero.
func TestCalibrationSubnormalNormalJoin(t *testing.T) {
	tie := "0.0000610053539276123046875" // 2047/2^25
	body := `{"values": ["` + tie + `", "0"], "targets": ["0400", "0000"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	cal := resp.Calibration
	if cal == nil || !cal.Feasible || cal.Attained == nil || !*cal.Attained {
		t.Fatalf("join batch should attain bias 0: %s", raw)
	}
	if got := rawFrac(t, cal.Bias); got != "0/1" {
		t.Fatalf("bias = %s, want 0/1", got)
	}
	if cal.Interval == nil ||
		rawFrac(t, cal.Interval.Low) != "0/1" || !cal.Interval.LowInclusive ||
		rawFrac(t, cal.Interval.High) != "1/33554432" || !cal.Interval.HighInclusive {
		t.Fatalf("interval = %+v, want [0, 1/33554432] closed", cal.Interval)
	}
	if cal.Recomputed[0].Hex != "0400" || cal.Recomputed[0].Class != "normal" ||
		cal.Recomputed[1].Hex != "0000" || cal.Recomputed[1].Class != "zero" {
		t.Fatalf("recompute = %+v, want 0400 normal and 0000 zero", cal.Recomputed)
	}
}

// TestCalibrationMixedSignZeroNoOverlap: no bias can make literal "0"
// a -0 and literal "-0" a +0 simultaneously.
func TestCalibrationMixedSignZeroNoOverlap(t *testing.T) {
	body := `{"values": ["0", "-0"], "targets": ["8000", "0000"]}`
	resp, raw, code := executeBody(t, body)
	if code != 0 {
		t.Fatalf("code=%d out=%s", code, raw)
	}
	if resp.Calibration == nil || resp.Calibration.Feasible {
		t.Fatalf("the two zero certificates must not overlap at bias 0: %s", raw)
	}
}
