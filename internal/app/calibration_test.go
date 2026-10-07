package app_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"halfconv/internal/app"
)

func parseCalibration(t *testing.T, body string) (app.Response, int, string) {
	t.Helper()
	var out bytes.Buffer
	code := app.Execute(strings.NewReader(body), &out)
	var resp app.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON (%v): %s", err, out.String())
	}
	return resp, code, out.String()
}

func ratString(r *big.Rat) string { return r.Num().String() + "/" + r.Denom().String() }

func TestCalibrationRejectsBadWholeRequest(t *testing.T) {
	bodies := []string{
		`{"values":["1","2"],"targets":["3C00"]}`,
		`{"values":["NaN"],"targets":["3C00"]}`,
		`{"values":["1"],"targets":["3C0"]}`,
		`{"values":["1"],"targets":["7C01"]}`, // NaN
		`{"values":["1"],"targets":["NaN"]}`,
		`{"values":["1"],"targets":[1234]}`,
		`{"values":["1e51"],"targets":["7C00"]}`,
	}
	for _, body := range bodies {
		var out bytes.Buffer
		code := app.Execute(strings.NewReader(body), &out)
		if code != 1 {
			t.Fatalf("body %s: code=%d, want 1; output=%s", body, code, out.String())
		}
		if !bytes.Contains(out.Bytes(), []byte(`"ok":false`)) {
			t.Fatalf("body %s: expected request error envelope, got %s", body, out.String())
		}
		if bytes.Contains(out.Bytes(), []byte(`"results"`)) {
			t.Fatalf("body %s: rejected calibration must not emit partial results: %s", body, out.String())
		}
	}
}

func TestCalibrationDefaultModeStillProcessesItems(t *testing.T) {
	resp, code := execute(t, `{"values":["1","NaN"]}`)
	if code != 0 {
		t.Fatalf("ordinary conversion code=%d", code)
	}
	if resp.Calibration != nil || len(resp.Result) != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Result[0].Target != nil || resp.Result[1].OK {
		t.Fatalf("targets mode fields leaked into ordinary response: %+v", resp.Result)
	}
}

func TestCalibrationZeroBiasAndRecalculation(t *testing.T) {
	body := `{"values":["0","1"],"targets":["0000","3C00"]}`
	resp, code, raw := parseCalibration(t, body)
	if code != 0 {
		t.Fatalf("code=%d raw=%s", code, raw)
	}
	if resp.Result != nil || resp.Calibration == nil {
		t.Fatalf("calibration response shape wrong: %+v", resp)
	}
	cal := resp.Calibration
	if !cal.Feasible {
		t.Fatalf("expected feasible: %s", raw)
	}
	if string(cal.Bias) != `"0/1"` || string(cal.MinimumAbsoluteBias) != `"0/1"` {
		t.Fatalf("bias fields = %s/%s", cal.Bias, cal.MinimumAbsoluteBias)
	}
	if cal.MinimumAttainable == nil || !*cal.MinimumAttainable {
		t.Fatalf("zero bias must be attainable")
	}
	if len(cal.Results) != 2 {
		t.Fatalf("recalculation results = %d", len(cal.Results))
	}
	for i, wantHex := range []string{"0000", "3C00"} {
		it := cal.Results[i]
		if it.Hex == nil || *it.Hex != wantHex || it.Target == nil || *it.Target != wantHex {
			t.Fatalf("result %d = %+v, want target/hex %s", i, it, wantHex)
		}
	}
	// Intersection includes zero; +0's upper tie 2^-25 is tighter than
	// the shifted 1.0 upper tie 1/2048.
	if lo := parseFrac(t, cal.Interval.Low); lo.Sign() != 0 {
		t.Fatalf("low = %v, want zero", lo)
	}
	if hi := parseFrac(t, cal.Interval.High); hi.Cmp(big.NewRat(1, 1<<25)) != 0 {
		t.Fatalf("high = %v, want 2^-25", hi)
	}
}

func TestCalibrationLiteralNegativeZeroRetained(t *testing.T) {
	body := `{"values":["-0"],"targets":["8000"]}`
	resp, code, raw := parseCalibration(t, body)
	if code != 0 || !resp.Calibration.Feasible {
		t.Fatalf("code=%d response=%s", code, raw)
	}
	cal := resp.Calibration
	if string(cal.Bias) != `"0/1"` || cal.MinimumAttainable == nil || !*cal.MinimumAttainable {
		t.Fatalf("-0 zero bias must be attainable: %+v", cal)
	}
	if got := *cal.Results[0].Hex; got != "8000" {
		t.Fatalf("-0 + 0 recomputed to %s", got)
	}

	// Asking literal -0 to become +0 is possible only for a strictly
	// positive bias; zero is only an excluded infimum.
	body = `{"values":["-0"],"targets":["0000"]}`
	resp, code, raw = parseCalibration(t, body)
	if code != 0 {
		t.Fatalf("code=%d raw=%s", code, raw)
	}
	cal = resp.Calibration
	if !cal.Feasible {
		t.Fatalf("(0,2^-25] should be feasible")
	}
	if cal.Bias != nil || cal.Results != nil {
		t.Fatalf("unattained infimum must not be presented as a feasible bias: %+v", cal)
	}
	if cal.MinimumAttainable == nil || *cal.MinimumAttainable {
		t.Fatalf("minimum_attainable must be false")
	}
	if m := parseFrac(t, cal.MinimumAbsoluteBias); m.Sign() != 0 {
		t.Fatalf("minimum_absolute_bias = %v, want zero infimum", m)
	}
	if cal.Interval.LowInclusive {
		t.Fatalf("zero endpoint should be excluded for literal -0 -> +0")
	}
}

func TestCalibrationOpenEndpointInfimum(t *testing.T) {
	body := `{"values":["0"],"targets":["0001"]}`
	resp, code, raw := parseCalibration(t, body)
	if code != 0 || !resp.Calibration.Feasible {
		t.Fatalf("code=%d raw=%s", code, raw)
	}
	cal := resp.Calibration
	if cal.Bias != nil || cal.Results != nil {
		t.Fatalf("excluded endpoint must not produce bias/results: %+v", cal)
	}
	wantInf := big.NewRat(1, 1<<25)
	if m := parseFrac(t, cal.MinimumAbsoluteBias); m.Cmp(wantInf) != 0 {
		t.Fatalf("infimum = %v, want %s", m, ratString(wantInf))
	}
	if cal.Interval.LowInclusive {
		t.Fatalf("lower tie for odd F=1 must be open")
	}

	// That endpoint is owned by +0, so combining the two requirements is
	// empty.
	body = `{"values":["0","0"],"targets":["0001","0000"]}`
	resp, code, raw = parseCalibration(t, body)
	if code != 0 {
		t.Fatalf("code=%d raw=%s", code, raw)
	}
	if resp.Calibration.Feasible || resp.Calibration.Interval != nil || resp.Calibration.Results != nil {
		t.Fatalf("intersection at open endpoint must report only feasible=false: %+v", resp.Calibration)
	}
}

func TestCalibrationSubnormalBoundaryTie(t *testing.T) {
	body := `{"values":["0"],"targets":["0400"]}`
	resp, code, raw := parseCalibration(t, body)
	if code != 0 {
		t.Fatalf("code=%d raw=%s", code, raw)
	}
	cal := resp.Calibration
	if !cal.Feasible || cal.MinimumAttainable == nil || !*cal.MinimumAttainable {
		t.Fatalf("even smallest-normal tie should be attainable: %+v", cal)
	}
	want := big.NewRat(2047, 1<<25)
	if b := parseFrac(t, cal.Bias); b.Cmp(want) != 0 {
		t.Fatalf("bias = %v, want %s", b, ratString(want))
	}
	if !cal.Interval.LowInclusive {
		t.Fatalf("2047/2^25 is an even-F tie and included")
	}
	if len(cal.Results) != 1 || *cal.Results[0].Hex != "0400" {
		t.Fatalf("recalculation = %+v", cal.Results)
	}
	// Rounding error: result 2^-14 minus tie 2047/2^25 is +2^-25.
	if e := parseFrac(t, cal.Results[0].RoundingError); e.Cmp(big.NewRat(1, 1<<25)) != 0 {
		t.Fatalf("rounding error = %v, want 2^-25", e)
	}
}

func TestCalibrationOverflowTail(t *testing.T) {
	body := `{"values":["60000"],"targets":["7c00"]}`
	resp, code, raw := parseCalibration(t, body)
	if code != 0 {
		t.Fatalf("code=%d raw=%s", code, raw)
	}
	cal := resp.Calibration
	if !cal.Feasible {
		t.Fatalf("tail should be feasible")
	}
	if b := parseFrac(t, cal.Bias); b.Cmp(big.NewRat(5520, 1)) != 0 {
		t.Fatalf("bias = %v, want 5520", b)
	}
	if string(cal.Interval.Low) != `"5520/1"` || !cal.Interval.LowInclusive ||
		string(cal.Interval.High) != "null" {
		t.Fatalf("interval = %+v", cal.Interval)
	}
	if len(cal.Results) != 1 || *cal.Results[0].Hex != "7C00" || *cal.Results[0].Target != "7C00" {
		t.Fatalf("lowercase target should canonicalize and recompute: %+v", cal.Results)
	}
	if string(cal.Results[0].RoundingError) != "null" {
		t.Fatalf("infinity rounding error must be null")
	}
}
